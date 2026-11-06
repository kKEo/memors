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
	written := map[string][]exportEntry{}
	for _, ns := range res.Namespaces {
		list := byNS[ns]
		var index strings.Builder
		fmt.Fprintf(&index, "# %s\n\nExported from memors-mcp on %s. One line per document; open the file for the text and its provenance.\n\n", ns, today)
		sort.Slice(list, func(i, j int) bool { return list[i].Title < list[j].Title })
		for _, e := range list {
			d, err := s.ReadDocument(ctx, e.DocumentID)
			if err != nil {
				return nil, err
			}
			rel := path.Join(ns, d.Prov.Kind, slug(d.Prov.Title)+"-"+shortID(d.ID)+".md")
			if err := writeFile(filepath.Join(dir, filepath.FromSlash(rel)), renderMarkdown(d)); err != nil {
				return nil, err
			}
			fmt.Fprintf(&index, "- [%s](%s) — %s, %s%s\n", orTitle(d.Prov.Title), path.Join(d.Prov.Kind, path.Base(rel)), d.Prov.Kind, d.Prov.Trust, versionSuffix(d.Prov.Version))
			written[ns] = append(written[ns], exportEntry{Title: orTitle(d.Prov.Title), Rel: rel, Kind: d.Prov.Kind, Trust: d.Prov.Trust})
			res.Files++
		}
		pages, err := s.ListPages(ctx, PageFilter{Namespace: ns})
		if err != nil {
			return nil, err
		}
		sort.Slice(pages, func(i, j int) bool {
			if pages[i].Title != pages[j].Title {
				return pages[i].Title < pages[j].Title
			}
			return pages[i].ID < pages[j].ID
		})
		if len(pages) > 0 {
			index.WriteString("\n## Pages (derived by agents; see built_from)\n\n")
		}
		for _, pg := range pages {
			rel := path.Join(ns, "pages", slug(pg.Title)+"-"+shortID(pg.ID)+".md")
			if err := writeFile(filepath.Join(dir, filepath.FromSlash(rel)), renderPageMarkdown(&pg)); err != nil {
				return nil, err
			}
			stale := ""
			if pg.Stale {
				stale = " (stale)"
			}
			fmt.Fprintf(&index, "- [%s](%s) — %s page, %s%s\n", pg.Title, path.Join("pages", path.Base(rel)), pg.Kind, pg.Trust, stale)
			written[ns] = append(written[ns], exportEntry{Title: orTitle(pg.Title), Rel: rel, Kind: pg.Kind + " page", Trust: pg.Trust, Stale: pg.Stale, Page: true})
			res.Files++
		}
		if err := writeFile(filepath.Join(dir, ns, "_index.md"), index.String()); err != nil {
			return nil, err
		}
	}
	for name, text := range map[string]string{
		"README.md":     renderExportReadme(res.Namespaces, written, today),
		"SUMMARY.md":    renderExportSummary(res.Namespaces, written),
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
func renderExportSummary(namespaces []string, written map[string][]exportEntry) string {
	var b strings.Builder
	b.WriteString("# Summary\n\n[Overview](README.md)\n\n")
	for _, ns := range namespaces {
		fmt.Fprintf(&b, "- [%s](%s/_index.md)\n", ns, ns)
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
func renderPageMarkdown(p *Page) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "memo_uri: %s\ntitle: %s\nkind: page\npage_kind: %s\nnamespace: %s\nis_inference: true\ntrust: %s\nbuilt_at: %s\nbuilt_from_rev: %d\nstale: %t\n", p.URI, yamlStr(p.Title), p.Kind, p.Namespace, p.Trust, p.BuiltAt.UTC().Format(time.RFC3339), p.BuiltFromRev, p.Stale)
	if p.StaleReason != "" {
		fmt.Fprintf(&sb, "stale_reason: %s\n", yamlStr(p.StaleReason))
	}
	sb.WriteString("built_from:\n")
	for _, c := range p.Sources {
		sb.WriteString("  - " + c + "\n")
	}
	sb.WriteString("\n---\n\n" + strings.TrimRight(p.Content, "\n") + "\n")
	return sb.String()
}
