package compact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kKEo/memors/internal/kb"
)

// The book: an outline, a page per chapter, and an overview, so a person can
// read a namespace from the top down. The agent writes all three; the server
// proposes them children first (entity pages, then chapters, then the
// overview) and checks what comes back at string level.

// Kinds of book work item.
const (
	KindOutline  = "outline"
	KindTopic    = "topic"
	KindOverview = "overview"
)

// Thresholds for the book.
const (
	// OutlineMinPages: a namespace gets an outline item once this many
	// entity pages are in no chapter. Pages come first: an outline groups
	// what has been written up, and sources ride along as candidates.
	OutlineMinPages = 3
	// TopicMaxExamples caps the example passages of a chapter payload.
	TopicMaxExamples = 6
	// TopicPayloadBudget caps the text of a chapter or overview payload, in
	// bytes. What does not fit is listed by address for the agent to read.
	TopicPayloadBudget = 24 << 10
	// BookItemsPerCall is how many chapter and overview items one compact
	// call hands out: their payloads are large.
	BookItemsPerCall = 2
)

// OutlinePayload asks for the outline of a namespace: the chapters as they
// are, and what no chapter covers yet.
type OutlinePayload struct {
	Namespace  string               `json:"namespace"`
	Reason     string               `json:"reason"`
	Current    []kb.OutlineChapter  `json:"current"`
	Candidates []OutlineCandidateIn `json:"candidates"`
	Guide      string               `json:"guide"`
}

// OutlineCandidateIn is something to place in a chapter.
type OutlineCandidateIn struct {
	URI   string `json:"uri"`
	Title string `json:"title"`
	Gist  string `json:"gist,omitempty"` // the opening of an entity's page
}

// ChapterRef names a chapter.
type ChapterRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Scope string `json:"scope,omitempty"`
}

// TopicPayload is everything needed to write a chapter page.
type TopicPayload struct {
	Namespace string         `json:"namespace"`
	Chapter   ChapterRef     `json:"chapter"`
	PageID    string         `json:"page_id,omitempty"`
	Reason    string         `json:"reason"`
	Members   []TopicMember  `json:"members"`
	Examples  []PayloadChunk `json:"examples,omitempty"` // passages to quote, code first
	Facts     []kb.Fact      `json:"facts,omitempty"`
	MustCover []string       `json:"must_cover"`     // member names the chapter must mention
	More      []string       `json:"more,omitempty"` // addresses left out for size; read them if needed
	Previous  string         `json:"previous,omitempty"`
	Guide     string         `json:"guide"`
}

// TopicMember is one thing the chapter covers: an entity with its page, or
// a source with its opening passages.
type TopicMember struct {
	URI      string         `json:"uri"`
	Title    string         `json:"title"`
	PageURI  string         `json:"page_uri,omitempty"`
	PageID   string         `json:"-"`
	Page     string         `json:"page,omitempty"` // the entity's page, in full
	Passages []PayloadChunk `json:"passages,omitempty"`
}

// OverviewPayload is everything needed to write the overview page.
type OverviewPayload struct {
	Namespace string            `json:"namespace"`
	PageID    string            `json:"page_id,omitempty"`
	Reason    string            `json:"reason"`
	Chapters  []OverviewChapter `json:"chapters"`
	MustCover []string          `json:"must_cover"` // chapter titles the overview must mention
	More      []string          `json:"more,omitempty"`
	Previous  string            `json:"previous,omitempty"`
	Guide     string            `json:"guide"`
}

// OverviewChapter is one chapter with its page.
type OverviewChapter struct {
	ChapterRef
	PageURI string `json:"page_uri,omitempty"`
	PageID  string `json:"-"`
	Page    string `json:"page,omitempty"`
}

const (
	outlineGuide = "Group the candidates into chapters a reader would follow from first to last. Submit the whole outline: every chapter with its title, a one or two sentence scope, and its members by address. Keep the id of a chapter you keep. A member belongs to one chapter."
	topicGuide   = "Write the chapter for a reader who starts here: what this part is about, then each member in turn, from the general to the detail. Name every member. Quote examples word for word from the passages, in fenced blocks, and cite the passage by its address, e.g. (memo://chunk/12). Link a member's page by its address for the detail. Add nothing the material does not say."
	overGuide    = "Write the opening page of the book: what this knowledge base covers and how the chapters fit together, one short paragraph per chapter, in order. Name every chapter and link its page by its address."
)

// generateBook proposes the book's work for a namespace: the outline when
// there is enough to group, a chapter page once its member pages are in
// place, the overview once the chapters are. It closes book items that no
// longer apply, so a list of open work stays a list of work.
func generateBook(ctx context.Context, store *kb.Store, ns string, wanted func(kind string) bool, add func(kind, subject string, payload any) error) error {
	outline, err := store.ReadOutline(ctx, ns)
	if err != nil {
		return err
	}
	cands, err := store.OutlineCandidates(ctx, ns)
	if err != nil {
		return err
	}
	live := map[string]bool{} // work-item subjects that still apply, by kind+subject
	keep := func(kind, subject string, payload any) error {
		live[kind+"\x00"+subject] = true
		if !wanted(kind) {
			return nil
		}
		return add(kind, subject, payload)
	}

	// The outline.
	pages := 0
	for _, c := range cands {
		if c.Kind == kb.MemberEntity {
			pages++
		}
	}
	reason := ""
	switch {
	case pages < OutlineMinPages:
	case len(outline.Chapters) == 0:
		reason = fmt.Sprintf("%q has %d pages and no outline", ns, pages)
	default:
		reason = fmt.Sprintf("%d pages of %q are in no chapter", pages, ns)
	}
	if reason != "" {
		p := OutlinePayload{Namespace: ns, Reason: reason, Current: outline.Chapters, Candidates: []OutlineCandidateIn{}, Guide: outlineGuide}
		for _, c := range cands {
			in := OutlineCandidateIn{URI: c.URI(), Title: c.Title}
			if c.PageID != "" {
				if page, err := store.ReadPage(ctx, c.PageID); err == nil {
					in.Gist = gist(page.Content)
				}
			}
			p.Candidates = append(p.Candidates, in)
		}
		if err := keep(KindOutline, outlineSubject(ns), p); err != nil {
			return err
		}
	}

	// Chapters wait for their members' pages: an entity that is about to get
	// a page, or a new one, would send the chapter straight back.
	rows, err := store.EntitiesWithMentions(ctx, ns, MinMentionsForPage)
	if err != nil {
		return err
	}
	unsettled := map[string]bool{}
	for _, r := range rows {
		if r.PageID == "" || r.PageStale {
			unsettled[r.Entity.ID] = true
		}
	}
	topicsOpen := 0
	var chunks map[string][]kb.ChunkText
	for _, c := range outline.Chapters {
		members := 0
		waiting := false
		for _, m := range c.Members {
			if m.Missing {
				continue
			}
			members++
			waiting = waiting || (m.Kind == kb.MemberEntity && unsettled[m.ID])
		}
		if members == 0 || (c.PageID != "" && !c.PageStale) {
			continue
		}
		topicsOpen++
		if waiting {
			continue
		}
		if chunks == nil {
			if chunks, err = chunksByDocument(ctx, store, ns); err != nil {
				return err
			}
		}
		p, err := topicPayload(ctx, store, ns, c, chunks)
		if err != nil {
			return err
		}
		if err := keep(KindTopic, c.ID, p); err != nil {
			return err
		}
	}

	// The overview waits for the chapters.
	if topicsOpen == 0 {
		over, err := store.PageBySubject(ctx, ns, kb.PageKindOverview, kb.OverviewSubject)
		if err != nil {
			return err
		}
		written := 0
		for _, c := range outline.Chapters {
			if c.PageID != "" {
				written++
			}
		}
		if written > 0 && (over == nil || over.Stale) {
			p, err := overviewPayload(ctx, store, ns, outline, over)
			if err != nil {
				return err
			}
			if err := keep(KindOverview, overviewSubject(ns), p); err != nil {
				return err
			}
		}
	}

	// Retire the book items that no longer apply.
	for _, kind := range []string{KindOutline, KindTopic, KindOverview} {
		open, err := store.ListWorkItems(ctx, ns, kind, "open", 0)
		if err != nil {
			return err
		}
		for _, w := range open {
			if live[kind+"\x00"+w.Subject] {
				continue
			}
			if err := store.CloseWorkItem(ctx, w.ID, "skipped", map[string]any{"reason": "no longer needed: the outline or the pages changed"}, "compact"); err != nil {
				return err
			}
		}
	}
	return nil
}

func outlineSubject(ns string) string  { return "outline:" + ns }
func overviewSubject(ns string) string { return "overview:" + ns }

// BookWaiting says how many chapters of a namespace need a page and are
// waiting for a member's entity page first.
func BookWaiting(ctx context.Context, store *kb.Store, ns string) (int, error) {
	outline, err := store.ReadOutline(ctx, ns)
	if err != nil || len(outline.Chapters) == 0 {
		return 0, err
	}
	rows, err := store.EntitiesWithMentions(ctx, ns, MinMentionsForPage)
	if err != nil {
		return 0, err
	}
	unsettled := map[string]bool{}
	for _, r := range rows {
		if r.PageID == "" || r.PageStale {
			unsettled[r.Entity.ID] = true
		}
	}
	n := 0
	for _, c := range outline.Chapters {
		if c.PageID != "" && !c.PageStale {
			continue
		}
		for _, m := range c.Members {
			if !m.Missing && m.Kind == kb.MemberEntity && unsettled[m.ID] {
				n++
				break
			}
		}
	}
	return n, nil
}

func chunksByDocument(ctx context.Context, store *kb.Store, ns string) (map[string][]kb.ChunkText, error) {
	all, err := store.LiveChunks(ctx, ns)
	if err != nil {
		return nil, err
	}
	out := map[string][]kb.ChunkText{}
	for _, c := range all {
		out[c.DocID] = append(out[c.DocID], c)
	}
	return out, nil
}

// budget hands out the payload's byte allowance.
type budget struct{ left int }

func (b *budget) take(text string) bool {
	if len(text) > b.left {
		return false
	}
	b.left -= len(text)
	return true
}

func topicPayload(ctx context.Context, store *kb.Store, ns string, c kb.OutlineChapter, chunks map[string][]kb.ChunkText) (*TopicPayload, error) {
	p := &TopicPayload{Namespace: ns, Chapter: ChapterRef{ID: c.ID, Title: c.Title, Scope: c.Scope}, PageID: c.PageID,
		Members: []TopicMember{}, MustCover: []string{}, Guide: topicGuide}
	switch {
	case c.PageID == "":
		p.Reason = fmt.Sprintf("chapter %q has no page", c.Title)
	default:
		p.Reason = fmt.Sprintf("the page of chapter %q is stale", c.Title)
		if prev, err := store.ReadPage(ctx, c.PageID); err == nil {
			p.Previous = prev.Content
			if prev.StaleReason != "" {
				p.Reason += ": " + prev.StaleReason
			}
		}
	}
	b := &budget{left: TopicPayloadBudget - len(p.Previous)}
	var pool []int64 // chunks an example may come from, in member order
	seen := map[int64]bool{}
	pooled := func(id int64) {
		if !seen[id] {
			seen[id] = true
			pool = append(pool, id)
		}
	}
	passage := func(id int64, title, text string) (PayloadChunk, bool) {
		uri := fmt.Sprintf("memo://chunk/%d", id)
		if !b.take(text) {
			p.More = append(p.More, uri)
			return PayloadChunk{}, false
		}
		return PayloadChunk{URI: uri, ID: id, Title: title, Text: text}, true
	}
	for _, m := range c.Members {
		if m.Missing {
			continue
		}
		tm := TopicMember{URI: m.URI(), Title: m.Title}
		p.MustCover = append(p.MustCover, m.Title)
		switch m.Kind {
		case kb.MemberEntity:
			ex, err := store.Explore(ctx, m.ID, ns, 1, nil)
			if err != nil {
				return nil, err
			}
			var ids []int64
			for _, uri := range ex.Chunks {
				var id int64
				if _, err := fmt.Sscanf(uri, "memo://chunk/%d", &id); err == nil {
					ids = append(ids, id)
					pooled(id)
				}
			}
			if m.PageID != "" {
				tm.PageURI, tm.PageID = "memo://page/"+m.PageID, m.PageID
				if page, err := store.ReadPage(ctx, m.PageID); err == nil {
					if b.take(page.Content) {
						tm.Page = page.Content
					} else {
						p.More = append(p.More, tm.PageURI)
					}
				}
			} else {
				// No page: the first passages stand in for it.
				for i, id := range ids {
					ch, err := store.ReadChunk(ctx, id)
					if err != nil {
						continue
					}
					if i >= 2 {
						p.More = append(p.More, ch.URI)
						continue
					}
					if pc, ok := passage(id, ch.Prov.Title, ch.Text); ok {
						tm.Passages = append(tm.Passages, pc)
					}
				}
			}
			facts, err := store.FactsAbout(ctx, m.ID)
			if err != nil {
				return nil, err
			}
			p.Facts = append(p.Facts, facts...)
		case kb.MemberSource:
			for i, ch := range chunks[m.DocID] {
				pooled(ch.ID)
				if i >= 2 {
					p.More = append(p.More, fmt.Sprintf("memo://chunk/%d", ch.ID))
					continue
				}
				if pc, ok := passage(ch.ID, ch.Title, ch.Text); ok {
					tm.Passages = append(tm.Passages, pc)
				}
			}
		}
		p.Members = append(p.Members, tm)
	}

	// Examples: code sources first, then any passage with a fenced block.
	var code, fenced []*kb.ChunkRead
	for _, id := range pool {
		ch, err := store.ReadChunk(ctx, id)
		if err != nil {
			continue
		}
		switch {
		case ch.Prov.Kind == kb.KindCode:
			code = append(code, ch)
		case strings.Contains(ch.Text, "```"):
			fenced = append(fenced, ch)
		}
	}
	for _, ch := range append(code, fenced...) {
		if len(p.Examples) == TopicMaxExamples {
			break
		}
		if pc, ok := passage(ch.ID, ch.Prov.Title, ch.Text); ok {
			p.Examples = append(p.Examples, pc)
		}
	}
	p.More = dedupeStr(p.More, examplesURIs(p))
	return p, nil
}

// examplesURIs lists the addresses the payload carries in full, so More does
// not name them again.
func examplesURIs(p *TopicPayload) map[string]bool {
	have := map[string]bool{}
	for _, e := range p.Examples {
		have[e.URI] = true
	}
	for _, m := range p.Members {
		for _, c := range m.Passages {
			have[c.URI] = true
		}
	}
	return have
}

func dedupeStr(in []string, drop map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] && !drop[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func overviewPayload(ctx context.Context, store *kb.Store, ns string, outline *kb.Outline, over *kb.Page) (*OverviewPayload, error) {
	p := &OverviewPayload{Namespace: ns, Chapters: []OverviewChapter{}, MustCover: []string{}, Guide: overGuide, Reason: fmt.Sprintf("%q has chapters and no overview", ns)}
	if over != nil {
		p.PageID, p.Previous = over.ID, over.Content
		p.Reason = "the overview is stale: " + over.StaleReason
	}
	b := &budget{left: TopicPayloadBudget - len(p.Previous)}
	for _, c := range outline.Chapters {
		oc := OverviewChapter{ChapterRef: ChapterRef{ID: c.ID, Title: c.Title, Scope: c.Scope}}
		if c.PageID != "" {
			oc.PageURI, oc.PageID = "memo://page/"+c.PageID, c.PageID
			p.MustCover = append(p.MustCover, c.Title)
			if page, err := store.ReadPage(ctx, c.PageID); err == nil {
				if b.take(page.Content) {
					oc.Page = page.Content
				} else {
					p.More = append(p.More, oc.PageURI)
				}
			}
		}
		p.Chapters = append(p.Chapters, oc)
	}
	return p, nil
}

// gist is the first prose line of a page, shortened.
func gist(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			continue
		}
		if r := []rune(line); len(r) > 200 {
			return string(r[:200]) + "…"
		}
		return line
	}
	return ""
}

// OutlineChapterIn is one chapter of a submitted outline.
type OutlineChapterIn struct {
	ID      string   `json:"id,omitempty"` // keep the id of a chapter that stays
	Title   string   `json:"title"`
	Scope   string   `json:"scope,omitempty"`
	Members []string `json:"members"` // memo://entity/<id> and memo://source/<id> addresses
}

func toChapters(in []OutlineChapterIn) ([]kb.OutlineChapter, error) {
	out := make([]kb.OutlineChapter, len(in))
	for i, c := range in {
		out[i] = kb.OutlineChapter{ID: c.ID, Title: c.Title, Scope: c.Scope, Members: []kb.OutlineMember{}}
		for _, uri := range c.Members {
			kind, id, err := kb.ParseURI(strings.TrimSpace(uri))
			if err != nil || (kind != kb.MemberEntity && kind != kb.MemberSource) {
				return nil, fmt.Errorf("chapter %q: a member is a memo://entity/ or memo://source/ address, got %q", c.Title, uri)
			}
			out[i].Members = append(out[i].Members, kb.OutlineMember{Kind: kind, ID: id})
		}
	}
	return out, nil
}

// submitOutline stores (or previews) a submitted outline.
func submitOutline(ctx context.Context, store *kb.Store, w *kb.WorkItem, res Result, rep *Report, dryRun bool, actor, channel string) error {
	var p OutlinePayload
	if err := json.Unmarshal(w.Payload, &p); err != nil {
		return err
	}
	if len(res.Outline) == 0 {
		return errors.New("an outline item needs outline: the chapters, each with title, scope and members")
	}
	chapters, err := toChapters(res.Outline)
	if err != nil {
		return err
	}
	before, err := store.ReadOutline(ctx, w.Namespace)
	if err != nil {
		return err
	}
	set := store.SetOutline
	if dryRun {
		set = store.PreviewOutline
	}
	after, err := set(ctx, w.Namespace, chapters, actor, channel)
	if err != nil {
		return err
	}
	rep.Diff = Diff(kb.RenderOutline(before, nil), kb.RenderOutline(after, nil))
	placed := map[string]bool{}
	for _, c := range after.Chapters {
		if len(c.Members) == 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("chapter %q has no members and will get no page", c.Title))
		}
		for _, m := range c.Members {
			placed[m.URI()] = true
		}
	}
	for _, c := range p.Candidates {
		if !placed[c.URI] && strings.HasPrefix(c.URI, "memo://entity/") {
			rep.Omitted = append(rep.Omitted, c.Title)
		}
	}
	if len(rep.Omitted) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d page(s) are still in no chapter", len(rep.Omitted)))
	}
	if dryRun {
		rep.Note = "dry run: outline not stored"
		return nil
	}
	rep.Applied = true
	rep.Note = fmt.Sprintf("outline stored: %d chapters; compact again for the chapter pages", len(after.Chapters))
	return store.CloseWorkItem(ctx, w.ID, "done", map[string]any{"chapters": len(after.Chapters), "unplaced": rep.Omitted}, actor)
}

// source is a text a page may draw on, with the chunk it is, if it is one.
type source struct {
	chunk int64
	text  string
}

// submitTopic stores (or previews) a chapter page.
func submitTopic(ctx context.Context, store *kb.Store, w *kb.WorkItem, res Result, rep *Report, dryRun bool, actor, channel string) error {
	var p TopicPayload
	if err := json.Unmarshal(w.Payload, &p); err != nil {
		return err
	}
	if strings.TrimSpace(res.Content) == "" {
		return errors.New("a chapter item needs content")
	}
	outline, err := store.ReadOutline(ctx, w.Namespace)
	if err != nil {
		return err
	}
	var chapter *kb.OutlineChapter
	for i := range outline.Chapters {
		if outline.Chapters[i].ID == p.Chapter.ID {
			chapter = &outline.Chapters[i]
		}
	}
	if chapter == nil {
		return fmt.Errorf("chapter %q is no longer in the outline; skip this item", p.Chapter.Title)
	}
	title := res.Title
	if title == "" {
		title = chapter.Title
	}
	var srcs []source
	var names []named
	var deps []string
	for _, m := range chapter.Members {
		if m.Missing {
			continue
		}
		n := named{name: m.Title, uris: []string{m.URI()}}
		if m.PageID != "" {
			n.uris = append(n.uris, "memo://page/"+m.PageID)
			deps = append(deps, m.PageID)
		}
		names = append(names, n)
	}
	for _, m := range p.Members {
		if m.Page != "" {
			srcs = append(srcs, source{text: m.Page})
		}
		for _, c := range m.Passages {
			srcs = append(srcs, source{chunk: c.ID, text: c.Text})
		}
	}
	for _, e := range p.Examples {
		srcs = append(srcs, source{chunk: e.ID, text: e.Text})
	}
	cited := citedChunks(res.Content)
	for _, id := range cited {
		if ch, err := store.ReadChunk(ctx, id); err == nil {
			srcs = append(srcs, source{chunk: id, text: ch.Text})
		}
	}
	support := []string{chapter.Scope}
	for _, f := range p.Facts {
		support = append(support, f.Statement)
	}
	quoted := checkPage(rep, res.Content, p.Previous, names, srcs, support, "member")
	if len(p.Examples) > 0 && len(Quotes(res.Content)) == 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("the payload has %d example passage(s) and the chapter quotes none", len(p.Examples)))
	}
	if len(cited) == 0 {
		rep.Warnings = append(rep.Warnings, "the chapter cites no memo://chunk address")
	}
	if dryRun {
		rep.Note = "dry run: nothing written"
		return nil
	}
	page, err := store.WritePage(ctx, kb.PageInput{Namespace: w.Namespace, Kind: kb.PageKindTopic, SubjectID: chapter.ID, Title: title, Content: res.Content,
		Sources: dedupe(append(cited, quoted...)), Deps: deps, Actor: actor, Channel: channel})
	if err != nil {
		return err
	}
	rep.PageURI, rep.Applied = page.URI, true
	return store.CloseWorkItem(ctx, w.ID, "done", map[string]any{"page": page.URI, "omitted": rep.Omitted, "misquoted": len(rep.Misquoted)}, actor)
}

// submitOverview stores (or previews) the overview page.
func submitOverview(ctx context.Context, store *kb.Store, w *kb.WorkItem, res Result, rep *Report, dryRun bool, actor, channel string) error {
	var p OverviewPayload
	if err := json.Unmarshal(w.Payload, &p); err != nil {
		return err
	}
	if strings.TrimSpace(res.Content) == "" {
		return errors.New("an overview item needs content")
	}
	outline, err := store.ReadOutline(ctx, w.Namespace)
	if err != nil {
		return err
	}
	title := res.Title
	if title == "" {
		title = "Overview"
	}
	var srcs []source
	var names []named
	var deps []string
	var support []string
	for _, c := range outline.Chapters {
		support = append(support, c.Scope)
		if c.PageID == "" {
			continue
		}
		names = append(names, named{name: c.Title, uris: []string{"memo://page/" + c.PageID}})
		deps = append(deps, c.PageID)
	}
	if len(deps) == 0 {
		return errors.New("no chapter has a page any more; skip this item")
	}
	for _, c := range p.Chapters {
		if c.Page != "" {
			srcs = append(srcs, source{text: c.Page})
		}
	}
	cited := citedChunks(res.Content)
	for _, id := range cited {
		if ch, err := store.ReadChunk(ctx, id); err == nil {
			srcs = append(srcs, source{chunk: id, text: ch.Text})
		}
	}
	quoted := checkPage(rep, res.Content, p.Previous, names, srcs, support, "chapter")
	if dryRun {
		rep.Note = "dry run: nothing written"
		return nil
	}
	page, err := store.WritePage(ctx, kb.PageInput{Namespace: w.Namespace, Kind: kb.PageKindOverview, SubjectID: kb.OverviewSubject, Title: title, Content: res.Content,
		Sources: dedupe(append(cited, quoted...)), Deps: deps, Actor: actor, Channel: channel})
	if err != nil {
		return err
	}
	rep.PageURI, rep.Applied = page.URI, true
	return store.CloseWorkItem(ctx, w.ID, "done", map[string]any{"page": page.URI, "omitted": rep.Omitted, "misquoted": len(rep.Misquoted)}, actor)
}

// checkPage runs the string-level checks of a chapter or overview page and
// fills the report. It returns the chunks the page quotes from.
func checkPage(rep *Report, content, previous string, names []named, srcs []source, support []string, what string) []int64 {
	rep.Omitted = MissingMembers(content, names)
	if len(rep.Omitted) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d %s(s) are not named in the page", len(rep.Omitted), what))
	}
	texts := append([]string(nil), support...)
	for _, s := range srcs {
		texts = append(texts, s.text)
	}
	rep.Unsupported = Unsupported(stripQuotes(content), texts)
	if len(rep.Unsupported) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d sentence(s) are not backed by any member page, passage or fact", len(rep.Unsupported)))
	}
	var quoted []int64
	rep.Misquoted, quoted = Misquoted(content, srcs)
	if len(rep.Misquoted) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d quoted block(s) are not found word for word in the material or in a cited passage", len(rep.Misquoted)))
	}
	rep.Diff = Diff(previous, content)
	return quoted
}

// named is something a page must mention: by its name or by an address.
type named struct {
	name string
	uris []string
}

// MissingMembers returns the names a page neither spells out nor links.
// A chapter is a summary, so the check asks for the member, not for every
// fact about it.
func MissingMembers(page string, members []named) []string {
	lower := strings.ToLower(page)
	var out []string
next:
	for _, m := range members {
		if m.name != "" && strings.Contains(lower, strings.ToLower(m.name)) {
			continue
		}
		for _, u := range m.uris {
			if strings.Contains(page, u) {
				continue next
			}
		}
		out = append(out, m.name)
	}
	return out
}

// Quotes returns what a page presents as quoted material: the bodies of
// fenced blocks, and blockquotes (consecutive `>` lines joined).
func Quotes(page string) []string {
	var out []string
	var block []string
	inFence, inQuote := false, false
	flush := func() {
		if text := strings.TrimSpace(strings.Join(block, "\n")); text != "" {
			out = append(out, text)
		}
		block = nil
	}
	for _, line := range strings.Split(page, "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			if inFence {
				flush()
			}
			inFence = !inFence
		case inFence:
			block = append(block, line)
		case strings.HasPrefix(trim, ">"):
			inQuote = true
			block = append(block, strings.TrimSpace(trim[1:]))
		default:
			if inQuote {
				flush()
				inQuote = false
			}
		}
	}
	flush()
	return out
}

// stripQuotes removes the quoted material, so the prose check does not
// judge code as sentences.
func stripQuotes(page string) string {
	var sb strings.Builder
	inFence := false
	for _, line := range strings.Split(page, "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~"):
			inFence = !inFence
		case inFence || strings.HasPrefix(trim, ">"):
		default:
			sb.WriteString(line + "\n")
		}
	}
	return sb.String()
}

// Misquoted returns the quoted blocks of a page that are not found, word
// for word, in any source text; whitespace is ignored, since chunking and
// markdown both reflow it. It also returns the chunks the good quotes came
// from. The check shows an example was copied; it cannot show it works.
func Misquoted(page string, srcs []source) (bad []string, from []int64) {
	squeezed := make([]string, len(srcs))
	for i, s := range srcs {
		squeezed[i] = squeeze(s.text)
	}
	for _, q := range Quotes(page) {
		want := squeeze(q)
		if want == "" {
			continue
		}
		found := false
		for i, text := range squeezed {
			if strings.Contains(text, want) {
				found = true
				if srcs[i].chunk != 0 {
					from = append(from, srcs[i].chunk)
				}
			}
		}
		if !found {
			if r := []rune(q); len(r) > 160 {
				q = string(r[:160]) + "…"
			}
			bad = append(bad, q)
		}
	}
	return bad, from
}

// squeeze drops all whitespace.
func squeeze(s string) string {
	return strings.Join(strings.Fields(s), "")
}
