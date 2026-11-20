package kb

import (
	"context"
	"sort"
)

// Book is a namespace read as a book: the overview, the chapters of the
// outline with what each covers, and the pages no chapter covers. It is a
// view, built on every call; the export and the web UI both read it, so
// they show the same order.
type Book struct {
	Namespace string
	Overview  *BookEntry // nil until the overview page is written
	Chapters  []BookChapter
	Loose     []BookEntry // pages in no chapter, by title
}

// BookChapter is one chapter of the outline.
type BookChapter struct {
	ID      string
	Title   string
	Scope   string
	Page    *BookEntry  // nil while the chapter has no page (a draft)
	Members []BookEntry // entity pages and source documents, in outline order
	// Changed counts the pages the chapter page was written from that were
	// rewritten, added or retired since; StaleMembers counts member pages
	// that are stale now. Both are shown next to a chapter that is not
	// stale itself yet.
	Changed      int
	StaleMembers int
}

// BookEntry is one thing to read: a page, or a member's source document.
type BookEntry struct {
	Kind     string // overview | topic | entity | source
	Title    string
	PageID   string // overview, topic and entity entries
	DocID    string // source entries: the live document
	SourceID string // source entries
	EntityID string // entity entries
	Stale    bool
}

// Book reads a namespace's book. A namespace with no outline has no
// chapters; callers treat that as "no book".
func (s *Store) Book(ctx context.Context, ns string) (*Book, error) {
	o, err := s.ReadOutline(ctx, ns)
	if err != nil {
		return nil, err
	}
	b := &Book{Namespace: ns}
	if len(o.Chapters) == 0 {
		return b, nil
	}
	over, err := s.PageBySubject(ctx, ns, PageKindOverview, OverviewSubject)
	if err != nil {
		return nil, err
	}
	placed := map[string]bool{}
	if over != nil {
		b.Overview = &BookEntry{Kind: PageKindOverview, Title: over.Title, PageID: over.ID, Stale: over.Stale}
		placed[over.ID] = true
	}
	for _, c := range o.Chapters {
		ch := BookChapter{ID: c.ID, Title: c.Title, Scope: c.Scope}
		if c.PageID != "" {
			ch.Page = &BookEntry{Kind: PageKindTopic, Title: c.Title, PageID: c.PageID, Stale: c.PageStale}
			placed[c.PageID] = true
			if ch.Changed, err = s.PageDrift(ctx, c.PageID); err != nil {
				return nil, err
			}
		}
		for _, m := range c.Members {
			switch {
			case m.Missing:
			case m.Kind == MemberEntity && m.PageID != "":
				ch.Members = append(ch.Members, BookEntry{Kind: PageKindEntity, Title: m.Title, PageID: m.PageID, EntityID: m.ID, Stale: m.PageStale})
				placed[m.PageID] = true
				if m.PageStale {
					ch.StaleMembers++
				}
			case m.Kind == MemberSource:
				ch.Members = append(ch.Members, BookEntry{Kind: MemberSource, Title: m.Title, DocID: m.DocID, SourceID: m.ID})
			}
		}
		b.Chapters = append(b.Chapters, ch)
	}
	pages, err := s.ListPages(ctx, PageFilter{Namespace: ns})
	if err != nil {
		return nil, err
	}
	for _, p := range pages {
		if !placed[p.ID] {
			b.Loose = append(b.Loose, BookEntry{Kind: p.Kind, Title: p.Title, PageID: p.ID, EntityID: p.SubjectID, Stale: p.Stale})
		}
	}
	sort.Slice(b.Loose, func(i, j int) bool {
		if b.Loose[i].Title != b.Loose[j].Title {
			return b.Loose[i].Title < b.Loose[j].Title
		}
		return b.Loose[i].PageID < b.Loose[j].PageID
	})
	return b, nil
}

// Order is the reading order of the book's pages: the overview, then each
// chapter's page followed by its member pages, then the pages in no
// chapter. Source documents are reached from their chapter, not paged
// through.
func (b *Book) Order() []BookEntry {
	var out []BookEntry
	if b.Overview != nil {
		out = append(out, *b.Overview)
	}
	for _, c := range b.Chapters {
		if c.Page != nil {
			out = append(out, *c.Page)
		}
		for _, m := range c.Members {
			if m.PageID != "" {
				out = append(out, m)
			}
		}
	}
	return append(out, b.Loose...)
}
