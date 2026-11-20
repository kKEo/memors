package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// The outline (migration 6) is what turns a namespace's pages into a book:
// an ordered list of chapters, each with the entities and sources it covers.
// A person or the calling agent writes it; the server never groups anything
// on its own. Chapter pages (kind topic) and the overview page are written
// from it through compaction, and the export and the web UI follow its order.

const (
	// MemberEntity and MemberSource are the two kinds of chapter member.
	MemberEntity = "entity"
	MemberSource = "source"

	// OverviewSubject is the subject of a namespace's one overview page.
	OverviewSubject = "namespace"
)

// Outline is a namespace's chapters in reading order.
type Outline struct {
	Namespace string           `json:"namespace"`
	Chapters  []OutlineChapter `json:"chapters"`
}

// OutlineChapter is one chapter. ID is empty for a chapter that is new in a
// SetOutline call. The page fields are filled on read.
type OutlineChapter struct {
	ID        string          `json:"id,omitempty"`
	Title     string          `json:"title"`
	Scope     string          `json:"scope,omitempty"` // one or two sentences: what the chapter is about
	Trust     string          `json:"trust,omitempty"`
	Actor     string          `json:"actor,omitempty"`
	Members   []OutlineMember `json:"members"`
	PageID    string          `json:"page_id,omitempty"` // the chapter's topic page, once written
	PageStale bool            `json:"page_stale,omitempty"`
}

// OutlineMember is an entity or a source a chapter covers. Only Kind and ID
// are read by SetOutline; the rest is filled on read.
type OutlineMember struct {
	Kind      string `json:"kind"` // entity | source
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Missing   bool   `json:"missing,omitempty"`    // the entity is gone, or the source has no live document
	PageID    string `json:"page_id,omitempty"`    // an entity's page
	PageStale bool   `json:"page_stale,omitempty"` //
	DocID     string `json:"doc_id,omitempty"`     // a source's live document
}

// URI is the member's address.
func (m OutlineMember) URI() string { return "memo://" + m.Kind + "/" + m.ID }

// ReadOutline returns a namespace's outline; one with no chapters when none
// was written.
func (s *Store) ReadOutline(ctx context.Context, ns string) (*Outline, error) {
	return readOutline(ctx, s.db, ns)
}

func readOutline(ctx context.Context, q queryer, ns string) (*Outline, error) {
	o := &Outline{Namespace: ns, Chapters: []OutlineChapter{}}
	rows, err := q.QueryContext(ctx, `SELECT c.id, c.title, c.scope, c.trust, c.actor,
		COALESCE((SELECT p.id FROM pages p WHERE p.namespace = c.namespace AND p.kind = 'topic' AND p.subject_id = c.id AND p.deleted_at IS NULL), ''),
		COALESCE((SELECT p.stale FROM pages p WHERE p.namespace = c.namespace AND p.kind = 'topic' AND p.subject_id = c.id AND p.deleted_at IS NULL), 0)
		FROM outline_chapters c WHERE c.namespace = ? ORDER BY c.position, c.id`, ns)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		c := OutlineChapter{Members: []OutlineMember{}}
		var stale int
		if err := rows.Scan(&c.ID, &c.Title, &c.Scope, &c.Trust, &c.Actor, &c.PageID, &stale); err != nil {
			rows.Close()
			return nil, err
		}
		c.PageStale = stale == 1
		o.Chapters = append(o.Chapters, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range o.Chapters {
		c := &o.Chapters[i]
		mrows, err := q.QueryContext(ctx, `SELECT kind, ref_id FROM outline_members WHERE chapter_id = ? ORDER BY position, kind, ref_id`, c.ID)
		if err != nil {
			return nil, err
		}
		for mrows.Next() {
			var m OutlineMember
			if err := mrows.Scan(&m.Kind, &m.ID); err != nil {
				mrows.Close()
				return nil, err
			}
			c.Members = append(c.Members, m)
		}
		mrows.Close()
		if err := mrows.Err(); err != nil {
			return nil, err
		}
		for j := range c.Members {
			if err := fillMember(ctx, q, ns, &c.Members[j]); err != nil {
				return nil, err
			}
		}
	}
	return o, nil
}

// fillMember loads what a member points at.
func fillMember(ctx context.Context, q queryer, ns string, m *OutlineMember) error {
	var err error
	switch m.Kind {
	case MemberEntity:
		var stale int
		err = q.QueryRowContext(ctx, `SELECT e.canonical,
			COALESCE((SELECT p.id FROM pages p WHERE p.namespace = e.namespace AND p.kind = 'entity' AND p.subject_id = e.id AND p.deleted_at IS NULL), ''),
			COALESCE((SELECT p.stale FROM pages p WHERE p.namespace = e.namespace AND p.kind = 'entity' AND p.subject_id = e.id AND p.deleted_at IS NULL), 0)
			FROM entities e WHERE e.id = ? AND e.namespace = ?`, m.ID, ns).Scan(&m.Title, &m.PageID, &stale)
		m.PageStale = stale == 1
	case MemberSource:
		err = q.QueryRowContext(ctx, `SELECT s.title,
			COALESCE((SELECT d.id FROM documents d WHERE d.source_id = s.id AND d.deleted_at IS NULL AND d.superseded_by IS NULL), '')
			FROM sources s WHERE s.id = ? AND s.namespace = ?`, m.ID, ns).Scan(&m.Title, &m.DocID)
		if err == nil && m.DocID == "" {
			m.Missing = true
		}
	default:
		return fmt.Errorf("outline member kind %q", m.Kind)
	}
	if errors.Is(err, sql.ErrNoRows) {
		m.Missing = true
		return nil
	}
	return err
}

// SetOutline replaces a namespace's outline with chapters, in the order
// given. A chapter with an ID keeps it (and its page); one without is new.
//
// A tool call may not delete, retitle, re-scope or take members from a
// chapter a person wrote; it may add members to it. That returns
// ErrNeedsHuman with the CLI command.
//
// Pages follow the outline: a chapter page goes stale when its members,
// title or scope change, the overview goes stale when chapters are added,
// removed, retitled or re-scoped, and the page of a removed chapter is
// retired. Reordering alone stales nothing.
func (s *Store) SetOutline(ctx context.Context, ns string, chapters []OutlineChapter, actor, channel string) (*Outline, error) {
	return s.setOutline(ctx, ns, chapters, actor, channel, false)
}

// PreviewOutline runs SetOutline's checks and returns the outline it would
// store, without storing it.
func (s *Store) PreviewOutline(ctx context.Context, ns string, chapters []OutlineChapter, actor, channel string) (*Outline, error) {
	return s.setOutline(ctx, ns, chapters, actor, channel, true)
}

func (s *Store) setOutline(ctx context.Context, ns string, chapters []OutlineChapter, actor, channel string, preview bool) (*Outline, error) {
	if ns == "" {
		return nil, errors.New("outline needs a namespace")
	}
	if channel == "" {
		channel = ChannelTool
	}
	limit := channelCap(channel)
	nowMs := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	before, err := readOutline(ctx, tx, ns)
	if err != nil {
		return nil, err
	}
	old := map[string]*OutlineChapter{}
	for i := range before.Chapters {
		old[before.Chapters[i].ID] = &before.Chapters[i]
	}

	// A chapter given without an id is the stored chapter of the same title,
	// when there is one: a file or a submission written from scratch then
	// updates the outline instead of replacing every chapter and its page.
	claimed, byTitle := map[string]bool{}, map[string]string{}
	for _, c := range chapters {
		claimed[c.ID] = true
	}
	for id, c := range old {
		if !claimed[id] {
			byTitle[strings.ToLower(c.Title)] = id
		}
	}
	chapters = append([]OutlineChapter(nil), chapters...)
	for i := range chapters {
		if chapters[i].ID == "" {
			chapters[i].ID = byTitle[strings.ToLower(strings.TrimSpace(chapters[i].Title))]
		}
	}

	// Validate the new outline as a whole before touching anything.
	titles, placed, kept := map[string]bool{}, map[string]string{}, map[string]bool{}
	next := make([]OutlineChapter, len(chapters))
	for i, c := range chapters {
		c.Title, c.Scope = strings.TrimSpace(c.Title), strings.Join(strings.Fields(c.Scope), " ")
		if c.Title == "" || strings.ContainsAny(c.Title, "\r\n") || strings.Contains(c.Title, "<!--") {
			return nil, fmt.Errorf("chapter %d needs a one-line title", i+1)
		}
		if key := strings.ToLower(c.Title); titles[key] {
			return nil, fmt.Errorf("two chapters are titled %q", c.Title)
		} else {
			titles[key] = true
		}
		if c.ID != "" {
			if old[c.ID] == nil {
				return nil, fmt.Errorf("chapter %q: id %s is not a chapter of %q (leave the id out for a new chapter)", c.Title, c.ID, ns)
			}
			if kept[c.ID] {
				return nil, fmt.Errorf("chapter id %s appears twice", c.ID)
			}
			kept[c.ID] = true
		}
		for _, m := range c.Members {
			var table string
			switch m.Kind {
			case MemberEntity:
				table = "entities"
			case MemberSource:
				table = "sources"
			default:
				return nil, fmt.Errorf("chapter %q: a member is an entity or a source, not %q", c.Title, m.Kind)
			}
			if other, dup := placed[m.URI()]; dup {
				return nil, fmt.Errorf("%s is in two chapters (%q and %q); a member belongs to one", m.URI(), other, c.Title)
			}
			placed[m.URI()] = c.Title
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id = ? AND namespace = ?`, m.ID, ns).Scan(&n); err != nil {
				return nil, err
			}
			if n == 0 {
				return nil, fmt.Errorf("chapter %q: %s in namespace %q: %w", c.Title, m.URI(), ns, ErrNotFound)
			}
		}
		next[i] = c
	}

	// The trust rule: what a person wrote, a tool call only adds to.
	needsHuman := func(c *OutlineChapter, what string) error {
		return &ErrNeedsHuman{Op: what, URI: fmt.Sprintf("chapter %q", c.Title), Trust: c.Trust, Command: "memors-mcp outline apply --ns " + ns + " <file>"}
	}
	for id, c := range old {
		if trustRank[c.Trust] <= trustRank[limit] {
			continue
		}
		if !kept[id] {
			return nil, needsHuman(c, "removing a chapter")
		}
		for _, n := range next {
			if n.ID != id {
				continue
			}
			if n.Title != c.Title || n.Scope != c.Scope {
				return nil, needsHuman(c, "retitling or re-scoping a chapter")
			}
			have := memberSet(n.Members)
			for _, m := range c.Members {
				if !have[m.URI()] {
					return nil, needsHuman(c, "taking a member from a chapter")
				}
			}
		}
	}

	// Write. Members go first because (kind, ref_id) is unique across chapters.
	if _, err := tx.ExecContext(ctx, `DELETE FROM outline_members WHERE chapter_id IN (SELECT id FROM outline_chapters WHERE namespace = ?)`, ns); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO namespaces(name, created_at) VALUES (?, ?)`, ns, nowMs); err != nil {
		return nil, err
	}
	var added, removed, changed, restructured int
	for id, c := range old {
		if kept[id] {
			continue
		}
		removed++
		if _, err := tx.ExecContext(ctx, `DELETE FROM outline_chapters WHERE id = ?`, id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pages SET deleted_at = ?, deleted_reason = ? WHERE namespace = ? AND kind = 'topic' AND subject_id = ? AND deleted_at IS NULL`,
			nowMs, fmt.Sprintf("chapter %q was removed from the outline", c.Title), ns, id); err != nil {
			return nil, err
		}
	}
	for i := range next {
		c := &next[i]
		prev := old[c.ID]
		if prev == nil {
			added++
			c.ID = uuid.Must(uuid.NewV7()).String()
			if _, err := tx.ExecContext(ctx, `INSERT INTO outline_chapters(id, namespace, position, title, scope, trust, actor, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
				c.ID, ns, i, c.Title, c.Scope, limit, actor, nowMs, nowMs); err != nil {
				return nil, err
			}
		} else {
			renamed := prev.Title != c.Title || prev.Scope != c.Scope
			moved := !sameMembers(prev.Members, c.Members)
			trust, who := prev.Trust, prev.Actor
			if renamed || moved {
				changed++
				if renamed {
					restructured++
				}
				if trustRank[limit] >= trustRank[trust] {
					trust, who = limit, actor
				}
				if _, err := tx.ExecContext(ctx, `UPDATE pages SET stale = 1, stale_reason = ? WHERE namespace = ? AND kind = 'topic' AND subject_id = ? AND deleted_at IS NULL AND stale = 0`,
					"the chapter changed in the outline", ns, c.ID); err != nil {
					return nil, err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE outline_chapters SET position = ?, title = ?, scope = ?, trust = ?, actor = ?, updated_at = CASE WHEN ? THEN ? ELSE updated_at END WHERE id = ?`,
				i, c.Title, c.Scope, trust, who, renamed || moved, nowMs, c.ID); err != nil {
				return nil, err
			}
		}
		for j, m := range c.Members {
			if _, err := tx.ExecContext(ctx, `INSERT INTO outline_members(chapter_id, position, kind, ref_id) VALUES (?,?,?,?)`, c.ID, j, m.Kind, m.ID); err != nil {
				return nil, err
			}
		}
	}
	if added+removed+restructured > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE pages SET stale = 1, stale_reason = ? WHERE namespace = ? AND kind = 'overview' AND deleted_at IS NULL AND stale = 0`,
			"the outline's chapters changed", ns); err != nil {
			return nil, err
		}
	}
	after, err := readOutline(ctx, tx, ns)
	if err != nil || preview {
		return after, err // the deferred rollback undoes a preview
	}
	if err := writeAudit(ctx, tx, s.metrics, nowMs, actor, channel, "outline_set", "outline/"+ns, map[string]any{"chapters": len(next), "added": added, "removed": removed, "changed": changed}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return after, nil
}

func memberSet(ms []OutlineMember) map[string]bool {
	set := make(map[string]bool, len(ms))
	for _, m := range ms {
		set[m.URI()] = true
	}
	return set
}

func sameMembers(a, b []OutlineMember) bool {
	if len(a) != len(b) {
		return false
	}
	set := memberSet(a)
	for _, m := range b {
		if !set[m.URI()] {
			return false
		}
	}
	return true
}

// OutlineCandidate is something a chapter could cover and none does yet.
type OutlineCandidate struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	PageID string `json:"page_id,omitempty"`
}

// URI is the candidate's address.
func (c OutlineCandidate) URI() string { return "memo://" + c.Kind + "/" + c.ID }

// OutlineCandidates lists what is in no chapter: entities that have a page,
// then sources with a live document, each by title.
func (s *Store) OutlineCandidates(ctx context.Context, ns string) ([]OutlineCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT 'entity', e.id, e.canonical, p.id FROM entities e
		JOIN pages p ON p.namespace = e.namespace AND p.kind = 'entity' AND p.subject_id = e.id AND p.deleted_at IS NULL
		WHERE e.namespace = ?1 AND NOT EXISTS (SELECT 1 FROM outline_members m WHERE m.kind = 'entity' AND m.ref_id = e.id)
		UNION ALL
		SELECT 'source', s.id, s.title, '' FROM sources s
		WHERE s.namespace = ?1 AND EXISTS (SELECT 1 FROM documents d WHERE d.source_id = s.id AND d.deleted_at IS NULL AND d.superseded_by IS NULL)
		AND NOT EXISTS (SELECT 1 FROM outline_members m WHERE m.kind = 'source' AND m.ref_id = s.id)
		ORDER BY 1, 3, 2`, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutlineCandidate
	for rows.Next() {
		var c OutlineCandidate
		if err := rows.Scan(&c.Kind, &c.ID, &c.Title, &c.PageID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// OutlinedNamespaces lists the namespaces that have an outline.
func (s *Store) OutlinedNamespaces(ctx context.Context) ([]string, error) {
	return s.idList(ctx, `SELECT DISTINCT namespace FROM outline_chapters ORDER BY namespace`)
}

// RenderOutline writes an outline as the markdown file `outline apply`
// reads back. The file is a way to edit the outline, not a copy of it: the
// chapter ids in the comments are what tie a heading to its stored chapter.
// Candidates, when given, are listed in a comment for moving under a chapter.
func RenderOutline(o *Outline, candidates []OutlineCandidate) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Outline: %s\n", o.Namespace)
	for _, c := range o.Chapters {
		fmt.Fprintf(&sb, "\n## %s", c.Title)
		if c.ID != "" {
			fmt.Fprintf(&sb, " <!-- chapter: %s -->", c.ID)
		}
		sb.WriteString("\n")
		if c.Scope != "" {
			fmt.Fprintf(&sb, "\n> %s\n", c.Scope)
		}
		if len(c.Members) > 0 {
			sb.WriteString("\n")
		}
		for _, m := range c.Members {
			title := m.Title
			if m.Missing {
				title = strings.TrimSpace(title + " (missing)")
			}
			fmt.Fprintf(&sb, "- %s  %s\n", m.URI(), title)
		}
	}
	if len(candidates) > 0 {
		sb.WriteString("\n<!-- Not in a chapter. Move a line under a chapter heading to add it.\n")
		for _, c := range candidates {
			fmt.Fprintf(&sb, "- %s  %s\n", c.URI(), strings.ReplaceAll(c.Title, "-->", "->"))
		}
		sb.WriteString("-->\n")
	}
	return sb.String()
}

var (
	outlineChapterID = regexp.MustCompile(`<!--\s*chapter:\s*([0-9A-Za-z-]+)\s*-->`)
	outlineMember    = regexp.MustCompile(`^[-*]\s+memo://(entity|source)/(\S+)`)
)

// ParseOutline reads the file form: `## Title` starts a chapter, a `>` line
// is its scope, and a list item that starts with a memo://entity/ or
// memo://source/ address is a member (the text after the address is for the
// reader and is ignored). Comment blocks are skipped. The namespace comes
// from the `# Outline: <ns>` line, when there is one.
func ParseOutline(text string) (ns string, chapters []OutlineChapter, err error) {
	inComment := false
	var cur *OutlineChapter
	for i, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if inComment {
			inComment = !strings.Contains(line, "-->")
			continue
		}
		switch {
		case line == "":
		case strings.HasPrefix(line, "<!--"):
			inComment = !strings.Contains(line, "-->")
		case strings.HasPrefix(line, "## "):
			c := OutlineChapter{Members: []OutlineMember{}}
			title := line[3:]
			if m := outlineChapterID.FindStringSubmatch(title); m != nil {
				c.ID = m[1]
				title = strings.Replace(title, m[0], "", 1)
			}
			c.Title = strings.TrimSpace(title)
			chapters = append(chapters, c)
			cur = &chapters[len(chapters)-1]
		case strings.HasPrefix(line, "# "):
			if rest, ok := strings.CutPrefix(line[2:], "Outline:"); ok {
				ns = strings.TrimSpace(rest)
			}
		case strings.HasPrefix(line, ">"):
			if cur == nil {
				return "", nil, fmt.Errorf("line %d: a scope line needs a chapter heading (## Title) above it", i+1)
			}
			cur.Scope = strings.TrimSpace(cur.Scope + " " + strings.TrimSpace(line[1:]))
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			m := outlineMember.FindStringSubmatch(line)
			if m == nil {
				return "", nil, fmt.Errorf("line %d: a member line starts with a memo://entity/ or memo://source/ address", i+1)
			}
			if cur == nil {
				return "", nil, fmt.Errorf("line %d: a member needs a chapter heading (## Title) above it", i+1)
			}
			cur.Members = append(cur.Members, OutlineMember{Kind: m[1], ID: m[2]})
		default:
			return "", nil, fmt.Errorf("line %d: not a heading, a scope line (>) or a member line (-): %q", i+1, line)
		}
	}
	return ns, chapters, nil
}
