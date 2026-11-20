package kb

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ExportOptions filters Export.
type ExportOptions struct {
	Namespace string // empty = all
}

// ExportResult says what was written.
type ExportResult struct {
	Files      int // documents and pages; the index and book files are not counted
	Namespaces []string
}

// exportEntry is one written file, for the indexes.
type exportEntry struct {
	Title, Rel, Kind, Trust string // Rel is slash-separated, relative to the export root
	Stale                   bool
	Page                    bool
	ID                      string // page id or document id
}

// Export writes every live document as a markdown file with YAML front
// matter carrying its provenance, laid out as <dir>/<namespace>/<kind>/
// <slug>-<shortid>.md, plus an _index.md per namespace. Curated pages go to
// <namespace>/pages/. The files open in any editor or Obsidian; importing the
// folder back yields zero new revisions because the front matter names each
// document. The root also gets README.md, SUMMARY.md, book.toml and
// .gitbook.yaml, so `mdbook build <dir>` (or GitBook) renders the export as a
// book with one chapter per namespace; `ingest <dir>` skips those files.
func (s *Store) Export(ctx context.Context, dir string, opts ExportOptions) (*ExportResult, error) {
	entries, err := s.List(ctx, ListOptions{Namespace: opts.Namespace, Limit: 1 << 30})
	if err != nil {
		return nil, err
	}
	res := &ExportResult{}
	byNS := map[string][]ListEntry{}
	for _, e := range entries {
		byNS[e.Namespace] = append(byNS[e.Namespace], e)
	}
	// A namespace with pages but no live documents still gets its chapter.
	wanted := []string{opts.Namespace}
	if opts.Namespace == "" {
		all, err := s.Namespaces(ctx)
		if err != nil {
			return nil, err
		}
		wanted = all
	}
	for _, ns := range wanted {
		if _, ok := byNS[ns]; !ok {
			byNS[ns] = nil
		}
	}
	for ns := range byNS {
		res.Namespaces = append(res.Namespaces, ns)
	}
	sort.Strings(res.Namespaces)
	today := s.now().UTC().Format("2006-01-02")

	// First pass: decide every file's path, so a page can link to any other
	// exported file whatever order they are written in.
	type nsFiles struct {
		docs  []*Document
		pages []Page
		book  *Book
	}
	files := map[string]*nsFiles{}
	links := &exportLinks{store: s, ctx: ctx, doc: map[string]string{}, source: map[string]string{}, page: map[string]string{}, entity: map[string]string{}}
	for _, ns := range res.Namespaces {
		f := &nsFiles{}
		files[ns] = f
		list := byNS[ns]
		sort.Slice(list, func(i, j int) bool { return list[i].Title < list[j].Title })
		for _, e := range list {
			d, err := s.ReadDocument(ctx, e.DocumentID)
			if err != nil {
				return nil, err
			}
			f.docs = append(f.docs, d)
			rel := docRel(ns, d)
			links.doc[d.ID], links.source[d.SourceID] = rel, rel
		}
		if f.pages, err = s.ListPages(ctx, PageFilter{Namespace: ns}); err != nil {
			return nil, err
		}
		sort.Slice(f.pages, func(i, j int) bool {
			if f.pages[i].Title != f.pages[j].Title {
				return f.pages[i].Title < f.pages[j].Title
			}
			return f.pages[i].ID < f.pages[j].ID
		})
		for _, pg := range f.pages {
			links.page[pg.ID] = pageRel(ns, &pg)
			if pg.Kind == PageKindEntity && pg.SubjectID != "" {
				links.entity[pg.SubjectID] = links.page[pg.ID]
			}
		}
		if f.book, err = s.Book(ctx, ns); err != nil {
			return nil, err
		}
	}

	written := map[string][]exportEntry{}
	books := map[string]*Book{}
	for _, ns := range res.Namespaces {
		f := files[ns]
		var index strings.Builder
		fmt.Fprintf(&index, "# %s\n\nExported from memors-mcp on %s. One line per document; open the file for the text and its provenance.\n\n", ns, today)
		if len(f.book.Chapters) > 0 {
			books[ns] = f.book
			index.WriteString(renderBookContents(f.book, links, path.Join(ns, "_index.md")))
		}
		for _, d := range f.docs {
			rel := docRel(ns, d)
			if err := writeFile(filepath.Join(dir, filepath.FromSlash(rel)), renderMarkdown(d)); err != nil {
				return nil, err
			}
			fmt.Fprintf(&index, "- [%s](%s) — %s, %s%s\n", orTitle(d.Prov.Title), path.Join(d.Prov.Kind, path.Base(rel)), d.Prov.Kind, d.Prov.Trust, versionSuffix(d.Prov.Version))
			written[ns] = append(written[ns], exportEntry{Title: orTitle(d.Prov.Title), Rel: rel, Kind: d.Prov.Kind, Trust: d.Prov.Trust, ID: d.ID})
			res.Files++
		}
		if len(f.pages) > 0 {
			index.WriteString("\n## Pages (derived by agents; see built_from)\n\n")
		}
		chapterOf, footers := bookPageExtras(f.book, links)
		for _, pg := range f.pages {
			rel := pageRel(ns, &pg)
			pg.Content = strings.TrimRight(links.rewrite(pg.Content, rel), "\n") + footers.get(pg.ID)(rel)
			if err := writeFile(filepath.Join(dir, filepath.FromSlash(rel)), renderPageMarkdown(&pg, chapterOf[pg.ID])); err != nil {
				return nil, err
			}
			stale := ""
			if pg.Stale {
				stale = " (stale)"
			}
			fmt.Fprintf(&index, "- [%s](%s) — %s page, %s%s\n", pg.Title, path.Join("pages", path.Base(rel)), pg.Kind, pg.Trust, stale)
			written[ns] = append(written[ns], exportEntry{Title: orTitle(pg.Title), Rel: rel, Kind: pg.Kind + " page", Trust: pg.Trust, Stale: pg.Stale, Page: true, ID: pg.ID})
			res.Files++
		}
		if err := writeFile(filepath.Join(dir, ns, "_index.md"), index.String()); err != nil {
			return nil, err
		}
	}
	for name, text := range map[string]string{
		"README.md":     renderExportReadme(res.Namespaces, written, today),
		"SUMMARY.md":    renderExportSummary(res.Namespaces, written, books),
		"book.toml":     exportBookTOML,
		".gitbook.yaml": exportGitbookYAML,
	} {
		if err := writeFile(filepath.Join(dir, name), text); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func writeFile(full, text string) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(text), 0o644)
}

// ExportBookFiles are the root files that make an export an mdBook/GitBook
// source. `ingest <dir>` skips README.md and SUMMARY.md when book.toml is
// beside them, so re-importing an export does not turn its table of contents
// into documents.
var ExportBookFiles = []string{"README.md", "SUMMARY.md", "book.toml", ".gitbook.yaml"}

const exportBookTOML = `# Written by memors-mcp export; render with ` + "`mdbook build`" + `. GitBook ignores this file.
[book]
title = "Knowledge base export"
description = "Documents and the pages agents derived from them, exported by memors-mcp with their provenance."
language = "en"
src = "."

[build]
build-dir = "book"
create-missing = false

[output.html.search]
enable = true
`

const exportGitbookYAML = `root: ./

structure:
  readme: README.md
  summary: SUMMARY.md
`

// renderExportReadme is the book's landing page.
func renderExportReadme(namespaces []string, written map[string][]exportEntry, today string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Knowledge base export\n\nExported from memors-mcp on %s. Each namespace is a chapter. Documents are the stored sources with their provenance in the front matter. Files under *Pages* were written by agents from those documents: every page says so (`is_inference: true`) and lists the passages it was built from (`built_from`). Read those before relying on a page.\n\n", today)
	b.WriteString("Render this folder with `mdbook build`, open it in Obsidian, or re-ingest it with `memors-mcp ingest <dir>` (zero new revisions).\n\n| Namespace | Documents | Pages |\n|---|---|---|\n")
	for _, ns := range namespaces {
		docs, pages := 0, 0
		for _, e := range written[ns] {
			if e.Page {
				pages++
			} else {
				docs++
			}
		}
		fmt.Fprintf(&b, "| [%s](%s/_index.md) | %d | %d |\n", ns, ns, docs, pages)
	}
	return b.String()
}

// renderExportSummary is the mdBook/GitBook table of contents: namespace,
// then document kind, then the files; pages last under their own heading.
//
// A namespace with an outline is laid out as its book instead: the overview,
// the chapters with their member pages, the pages in no chapter, and the
// documents as an appendix. Every file still appears exactly once.
func renderExportSummary(namespaces []string, written map[string][]exportEntry, books map[string]*Book) string {
	var b strings.Builder
	b.WriteString("# Summary\n\n[Overview](README.md)\n\n")
	for _, ns := range namespaces {
		fmt.Fprintf(&b, "- [%s](%s/_index.md)\n", ns, ns)
		if book := books[ns]; book != nil {
			renderBookSummary(&b, book, written[ns])
			continue
		}
		byKind := map[string][]exportEntry{}
		var kinds []string
		var pages []exportEntry
		for _, e := range written[ns] {
			if e.Page {
				pages = append(pages, e)
				continue
			}
			if _, ok := byKind[e.Kind]; !ok {
				kinds = append(kinds, e.Kind)
			}
			byKind[e.Kind] = append(byKind[e.Kind], e)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			fmt.Fprintf(&b, "  - [%s]()\n", k) // a draft chapter: a heading without a page
			for _, e := range byKind[k] {
				fmt.Fprintf(&b, "    - [%s](%s)\n", summaryTitle(e), e.Rel)
			}
		}
		if len(pages) > 0 {
			b.WriteString("  - [Pages (derived by agents)]()\n")
			for _, e := range pages {
				fmt.Fprintf(&b, "    - [%s](%s)\n", summaryTitle(e), e.Rel)
			}
		}
	}
	return b.String()
}

// renderBookSummary writes an outlined namespace's part of SUMMARY.md.
func renderBookSummary(b *strings.Builder, book *Book, written []exportEntry) {
	byID := map[string]exportEntry{}
	for _, e := range written {
		byID[e.ID] = e
	}
	line := func(indent string, e exportEntry) {
		fmt.Fprintf(b, "%s- [%s](%s)\n", indent, summaryTitle(e), e.Rel)
	}
	if book.Overview != nil {
		line("  ", byID[book.Overview.PageID])
	}
	for _, c := range book.Chapters {
		if c.Page != nil {
			line("  ", byID[c.Page.PageID])
		} else {
			fmt.Fprintf(b, "  - [%s]()\n", summaryTitle(exportEntry{Title: c.Title})) // a draft: the chapter is not written yet
		}
		for _, m := range c.Members {
			if m.PageID != "" {
				line("    ", byID[m.PageID])
			}
		}
	}
	if len(book.Loose) > 0 {
		b.WriteString("  - [Not in a chapter]()\n")
		for _, e := range book.Loose {
			line("    ", byID[e.PageID])
		}
	}
	byKind := map[string][]exportEntry{}
	var kinds []string
	for _, e := range written {
		if e.Page {
			continue
		}
		if _, ok := byKind[e.Kind]; !ok {
			kinds = append(kinds, e.Kind)
		}
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}
	if len(kinds) == 0 {
		return
	}
	sort.Strings(kinds)
	b.WriteString("  - [Sources]()\n")
	for _, k := range kinds {
		fmt.Fprintf(b, "    - [%s]()\n", k)
		for _, e := range byKind[k] {
			line("      ", e)
		}
	}
}

// renderBookContents is the "Contents" list at the top of an outlined
// namespace's _index.md: the book's reading order, with what is unwritten
// or behind marked.
func renderBookContents(book *Book, links *exportLinks, from string) string {
	var b strings.Builder
	b.WriteString("## Contents\n\n")
	entry := func(indent string, e BookEntry) {
		note := ""
		if e.Stale {
			note = " (stale)"
		}
		if e.Kind == MemberSource {
			note = " (source)"
		}
		if rel := links.entry(e); rel != "" {
			fmt.Fprintf(&b, "%s- [%s](%s)%s\n", indent, mdText(e.Title), relLink(from, rel), note)
		} else {
			fmt.Fprintf(&b, "%s- %s%s\n", indent, mdText(e.Title), note)
		}
	}
	if book.Overview != nil {
		entry("", *book.Overview)
	}
	for _, c := range book.Chapters {
		switch {
		case c.Page == nil:
			fmt.Fprintf(&b, "- %s (not written yet)\n", mdText(c.Title))
		default:
			entry("", *c.Page)
		}
		for _, m := range c.Members {
			entry("  ", m)
		}
	}
	for i, e := range book.Loose {
		if i == 0 {
			b.WriteString("- Not in a chapter\n")
		}
		entry("  ", e)
	}
	b.WriteString("\n## Documents\n\n")
	return b.String()
}

// bookPageExtras says, for the pages of an outlined namespace, which chapter
// an entity page belongs to (for its front matter) and what footer a
// chapter or overview page gets: the way down to the next level of detail.
// The footer is written at export time and never stored.
func bookPageExtras(book *Book, links *exportLinks) (chapterOf map[string]string, footers footerMap) {
	chapterOf, footers = map[string]string{}, footerMap{}
	list := func(heading string, entries []BookEntry) func(from string) string {
		return func(from string) string {
			var b strings.Builder
			for _, e := range entries {
				rel := links.entry(e)
				if rel == "" {
					continue
				}
				note := ""
				switch {
				case e.Kind == MemberSource:
					note = " (source)"
				case e.Stale:
					note = " (stale)"
				}
				fmt.Fprintf(&b, "- [%s](%s)%s\n", mdText(e.Title), relLink(from, rel), note)
			}
			if b.Len() == 0 {
				return ""
			}
			return "\n\n---\n\n**" + heading + "**\n\n" + b.String()
		}
	}
	var chapters []BookEntry
	for _, c := range book.Chapters {
		for _, m := range c.Members {
			if m.PageID != "" {
				chapterOf[m.PageID] = c.Title
			}
		}
		if c.Page != nil {
			chapters = append(chapters, *c.Page)
			footers[c.Page.PageID] = list("In this chapter", c.Members)
		}
	}
	if book.Overview != nil {
		footers[book.Overview.PageID] = list("Chapters", chapters)
	}
	return chapterOf, footers
}

// footerMap gives a page's footer; a page without one gets nothing.
type footerMap map[string]func(from string) string

func (m footerMap) get(id string) func(string) string {
	if f := m[id]; f != nil {
		return f
	}
	return func(string) string { return "" }
}

func docRel(ns string, d *Document) string {
	return path.Join(ns, d.Prov.Kind, slug(d.Prov.Title)+"-"+shortID(d.ID)+".md")
}

func pageRel(ns string, p *Page) string {
	return path.Join(ns, "pages", slug(p.Title)+"-"+shortID(p.ID)+".md")
}

// relLink is the link from one exported file to another.
func relLink(from, to string) string {
	rel, err := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(to))
	if err != nil {
		return to
	}
	return filepath.ToSlash(rel)
}

// mdText keeps a title from breaking the link it is the text of.
func mdText(t string) string {
	return strings.NewReplacer("[", "(", "]", ")").Replace(orTitle(t))
}

// exportLinks knows where each exported record's file is.
type exportLinks struct {
	store                     *Store
	ctx                       context.Context
	doc, source, page, entity map[string]string
}

func (l *exportLinks) entry(e BookEntry) string {
	if e.PageID != "" {
		return l.page[e.PageID]
	}
	return l.doc[e.DocID]
}

// resolve returns the exported file of a memo:// address, or "".
func (l *exportLinks) resolve(kind, id string) string {
	switch kind {
	case "doc":
		if rel := l.doc[id]; rel != "" {
			return rel
		}
		// An older revision: the file of the live one.
		var live string
		_ = l.store.db.QueryRowContext(l.ctx, `SELECT d2.id FROM documents d JOIN documents d2 ON d2.source_id = d.source_id AND d2.deleted_at IS NULL AND d2.superseded_by IS NULL WHERE d.id = ?`, id).Scan(&live)
		return l.doc[live]
	case "source":
		return l.source[id]
	case "page":
		return l.page[id]
	case "entity":
		return l.entity[id]
	case "chunk":
		// A passage is read in its document; one from a superseded revision
		// leads to the live revision.
		var live string
		_ = l.store.db.QueryRowContext(l.ctx, `SELECT d2.id FROM chunks c JOIN documents d ON d.id = c.document_id JOIN documents d2 ON d2.source_id = d.source_id AND d2.deleted_at IS NULL AND d2.superseded_by IS NULL WHERE c.id = ?`, id).Scan(&live)
		return l.doc[live]
	}
	return ""
}

var exportMemoRef = regexp.MustCompile(`(\]\()?memo://(chunk|doc|source|page|entity)/([0-9A-Za-z-]+)`)

// rewrite turns the memo:// addresses in a page into links to the exported
// files, so a reader of the book can follow a citation. An address that is
// already a link target keeps its text; a bare one becomes its own text.
// Addresses of records that are not in the export, and anything inside a
// fenced block, are left as they are. Only pages are rewritten: a document
// is exported byte for byte.
func (l *exportLinks) rewrite(content, from string) string {
	lines := strings.Split(content, "\n")
	inFence := false
	for i, line := range lines {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.Contains(line, "memo://") {
			continue
		}
		lines[i] = exportMemoRef.ReplaceAllStringFunc(line, func(m string) string {
			sub := exportMemoRef.FindStringSubmatch(m)
			rel := l.resolve(sub[2], sub[3])
			if rel == "" {
				return m
			}
			if sub[1] != "" {
				return "](" + relLink(from, rel)
			}
			return "[" + strings.TrimPrefix(m, "memo://") + "](" + relLink(from, rel) + ")"
		})
	}
	return strings.Join(lines, "\n")
}

func summaryTitle(e exportEntry) string {
	t := strings.NewReplacer("[", "(", "]", ")").Replace(e.Title)
	if e.Stale {
		t += " (stale)"
	}
	return t
}

func versionSuffix(v string) string {
	if v == "" {
		return ""
	}
	return ", " + v
}

func orTitle(t string) string {
	if t == "" {
		return "(untitled)"
	}
	return t
}

// renderMarkdown is the exported file: front matter, then the content. The
// blank line before the closing --- keeps mdBook from reading the YAML as a
// heading; YAML and Obsidian do not mind it.
func renderMarkdown(d *Document) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "memo_uri: %s\n", d.URI)
	if d.Prov.SourceURI != "" {
		fmt.Fprintf(&sb, "source_uri: %s\n", d.Prov.SourceURI)
	}
	fmt.Fprintf(&sb, "title: %s\n", yamlStr(d.Prov.Title))
	fmt.Fprintf(&sb, "kind: %s\n", d.Prov.Kind)
	fmt.Fprintf(&sb, "namespace: %s\n", d.Prov.Namespace)
	if d.Prov.Library != "" {
		fmt.Fprintf(&sb, "library: %s\n", d.Prov.Library)
	}
	if d.Prov.Version != "" {
		fmt.Fprintf(&sb, "version: %s\n", d.Prov.Version)
	}
	fmt.Fprintf(&sb, "revision: %d\n", d.Prov.Revision)
	fmt.Fprintf(&sb, "content_hash: %s\n", d.Prov.Hash)
	fmt.Fprintf(&sb, "fetched_at: %s\n", d.Prov.FetchedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&sb, "trust: %s\n", d.Prov.Trust)
	fmt.Fprintf(&sb, "origin: %s\n", d.Prov.Origin)
	if d.Context != "" {
		fmt.Fprintf(&sb, "context: %s\n", yamlStr(d.Context))
	}
	sb.WriteString("\n---\n\n")
	sb.WriteString(d.Content)
	return sb.String()
}

// FrontMatter is what ParseExported recovers from an exported file.
type FrontMatter struct {
	MemoURI   string
	SourceURI string
	Title     string
	Kind      string
	Namespace string
	Library   string
	Version   string
	Trust     string
	Origin    string
	Context   string
}

// ParseExported splits an exported file into front matter and body. Files
// without front matter return an empty FrontMatter and the whole text.
func ParseExported(text string) (FrontMatter, string) {
	var fm FrontMatter
	if !strings.HasPrefix(text, "---\n") {
		return fm, text
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return fm, text
	}
	head, body := text[4:4+end], text[4+end+5:]
	for _, line := range strings.Split(head, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = unquote(strings.TrimSpace(v))
		switch strings.TrimSpace(k) {
		case "memo_uri":
			fm.MemoURI = v
		case "source_uri":
			fm.SourceURI = v
		case "title":
			fm.Title = v
		case "kind":
			fm.Kind = v
		case "namespace":
			fm.Namespace = v
		case "library":
			fm.Library = v
		case "version":
			fm.Version = v
		case "trust":
			fm.Trust = v
		case "origin":
			fm.Origin = v
		case "context":
			fm.Context = v
		}
	}
	return fm, strings.TrimPrefix(body, "\n")
}

func yamlStr(s string) string {
	if s == "" || strings.ContainsAny(s, ":#\"'\n") || strings.TrimSpace(s) != s {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		var out string
		if _, err := fmt.Sscanf(v, "%q", &out); err == nil {
			return out
		}
	}
	return v
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(title string) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "untitled"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// IndexOptions filters ExportIndex.
type IndexOptions struct {
	Namespace string
	Library   string
	Version   string
	MaxBytes  int // default 8192: the size that scored 100% in Vercel's AGENTS.md eval
}

// ExportIndex writes a compact markdown index of the knowledge base, sized
// for AGENTS.md or CLAUDE.md: one line per document (title, address, kind,
// version) grouped by namespace, with facts summarised, cut to MaxBytes with
// a note saying how many lines were left out. Agents read it passively, so
// it must be short and must say how to get the rest.
func (s *Store) ExportIndex(ctx context.Context, opts IndexOptions) (string, error) {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 8192
	}
	entries, err := s.List(ctx, ListOptions{Namespace: opts.Namespace, Limit: 1 << 20})
	if err != nil {
		return "", err
	}
	var kept []ListEntry
	for _, e := range entries {
		if opts.Library != "" && e.Library != opts.Library {
			continue
		}
		if opts.Version != "" && e.Version != opts.Version {
			continue
		}
		kept = append(kept, e)
	}
	facts, err := s.ListFacts(ctx, FactFilter{Namespace: opts.Namespace, Limit: 1 << 20})
	if err != nil {
		return "", err
	}
	var header strings.Builder
	fmt.Fprintf(&header, "# memors-mcp knowledge base index (%d documents, %d facts)\n\n", len(kept), len(facts))
	header.WriteString("Search with the `search` tool (scope by namespace/library/version), read an address with `read`. Lines: title · address · kind · version · trust.\n")
	var lines []string
	byNS := map[string][]ListEntry{}
	var nsOrder []string
	for _, e := range kept {
		if _, ok := byNS[e.Namespace]; !ok {
			nsOrder = append(nsOrder, e.Namespace)
		}
		byNS[e.Namespace] = append(byNS[e.Namespace], e)
	}
	sort.Strings(nsOrder)
	for _, ns := range nsOrder {
		lines = append(lines, "", "## "+ns)
		list := byNS[ns]
		sort.Slice(list, func(i, j int) bool { return list[i].Title < list[j].Title })
		for _, e := range list {
			v := ""
			if e.Version != "" {
				v = " · " + e.Version
			}
			lines = append(lines, fmt.Sprintf("- %s · %s · %s%s · %s", orTitle(e.Title), e.URI, e.Kind, v, e.Trust))
		}
	}
	if len(facts) > 0 {
		lines = append(lines, "", "## facts")
		for _, f := range facts {
			lines = append(lines, fmt.Sprintf("- %s · %s · %s", oneLineIndex(f.Statement, 100), f.URI, f.Trust))
		}
	}
	out := header.String()
	budget := opts.MaxBytes - len(out) - 80 // room for the footer
	used := 0
	written := 0
	for _, l := range lines {
		if used+len(l)+1 > budget {
			break
		}
		out += l + "\n"
		used += len(l) + 1
		written++
	}
	if written < len(lines) {
		out += fmt.Sprintf("\n_%d more line(s) omitted to fit %d bytes; use `search` or `memors-mcp ls`._\n", len(lines)-written, opts.MaxBytes)
	}
	return out, nil
}

func oneLineIndex(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// renderPageMarkdown writes a derived page with front matter that says so.
// chapter is the title of the chapter an entity page belongs to, if any.
func renderPageMarkdown(p *Page, chapter string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "memo_uri: %s\ntitle: %s\nkind: page\npage_kind: %s\nnamespace: %s\nis_inference: true\ntrust: %s\nbuilt_at: %s\nbuilt_from_rev: %d\nstale: %t\n", p.URI, yamlStr(p.Title), p.Kind, p.Namespace, p.Trust, p.BuiltAt.UTC().Format(time.RFC3339), p.BuiltFromRev, p.Stale)
	if p.StaleReason != "" {
		fmt.Fprintf(&sb, "stale_reason: %s\n", yamlStr(p.StaleReason))
	}
	if chapter != "" {
		fmt.Fprintf(&sb, "chapter: %s\n", yamlStr(chapter))
	}
	sb.WriteString("built_from:\n")
	for _, c := range p.Sources {
		sb.WriteString("  - " + c + "\n")
	}
	if len(p.Deps) > 0 {
		sb.WriteString("built_from_pages:\n")
		for _, d := range p.Deps {
			sb.WriteString("  - " + d + "\n")
		}
	}
	sb.WriteString("\n---\n\n" + strings.TrimRight(p.Content, "\n") + "\n")
	return sb.String()
}
