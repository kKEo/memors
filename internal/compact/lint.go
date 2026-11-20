package compact

import (
	"context"
	"fmt"
	"time"

	"github.com/kKEo/memors/internal/kb"
)

// Finding is one lint result with the address to look at.
type Finding struct {
	// contradiction | orphan-entity | missing-page | stale-page | expired-fact |
	// chapter-without-page | empty-chapter | missing-member | unassigned-page
	Kind    string `json:"kind"`
	URI     string `json:"uri"`
	Message string `json:"message"`
}

// Lint reads the knowledge base and reports what a librarian would flag:
// two live facts that disagree, entities no live passage mentions, entities
// with many mentions and no page, stale pages, and facts past their
// valid_to that are still live. For a namespace with an outline it also
// reports where the book has holes: a chapter with no page or nothing in
// it, a member that is gone, a page no chapter covers. It changes nothing.
func Lint(ctx context.Context, store *kb.Store, ns string, now time.Time) ([]Finding, error) {
	var out []Finding
	conflicts, err := Conflicts(ctx, store, ns)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		out = append(out, Finding{Kind: "contradiction", URI: c.A.URI, Message: fmt.Sprintf("disagrees with %s about %s; rule: %s", c.B.URI, c.Subject.Canonical, c.Rule)})
	}
	orphans, err := store.OrphanEntities(ctx, ns)
	if err != nil {
		return nil, err
	}
	for _, e := range orphans {
		out = append(out, Finding{Kind: "orphan-entity", URI: e.URI, Message: e.Canonical + " has no live passage mentioning it (its sources were forgotten or revised away)"})
	}
	rows, err := store.EntitiesWithMentions(ctx, ns, MinMentionsForPage)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.PageID == "" {
			out = append(out, Finding{Kind: "missing-page", URI: r.Entity.URI, Message: fmt.Sprintf("%s is mentioned in %d live passages and has no page", r.Entity.Canonical, r.Entity.Mentions)})
		}
	}
	stale, err := store.ListPages(ctx, kb.PageFilter{Namespace: ns, StaleOnly: true})
	if err != nil {
		return nil, err
	}
	for _, p := range stale {
		out = append(out, Finding{Kind: "stale-page", URI: p.URI, Message: p.Title + ": " + p.StaleReason})
	}
	facts, err := store.ListFacts(ctx, kb.FactFilter{Namespace: ns, Limit: 1 << 20})
	if err != nil {
		return nil, err
	}
	for _, f := range facts {
		if f.ValidTo != nil && f.ValidTo.Before(now) && f.InvalidatedAt == nil {
			out = append(out, Finding{Kind: "expired-fact", URI: f.URI, Message: fmt.Sprintf("valid_to %s has passed and nothing supersedes it: %s", f.ValidTo.Format("2006-01-02"), f.Statement)})
		}
	}
	outline, err := store.ReadOutline(ctx, ns)
	if err != nil {
		return nil, err
	}
	if len(outline.Chapters) == 0 {
		return out, nil
	}
	for _, c := range outline.Chapters {
		members := 0
		for _, m := range c.Members {
			if m.Missing {
				out = append(out, Finding{Kind: "missing-member", URI: m.URI(), Message: fmt.Sprintf("chapter %q lists it, and it is gone (forgotten, or its source has no live document)", c.Title)})
				continue
			}
			members++
		}
		switch {
		case members == 0:
			out = append(out, Finding{Kind: "empty-chapter", URI: "chapter:" + c.ID, Message: fmt.Sprintf("chapter %q covers nothing; give it members or remove it", c.Title)})
		case c.PageID == "":
			out = append(out, Finding{Kind: "chapter-without-page", URI: "chapter:" + c.ID, Message: fmt.Sprintf("chapter %q has %d member(s) and no page", c.Title, members)})
		}
	}
	cands, err := store.OutlineCandidates(ctx, ns)
	if err != nil {
		return nil, err
	}
	for _, c := range cands {
		if c.PageID != "" {
			out = append(out, Finding{Kind: "unassigned-page", URI: "memo://page/" + c.PageID, Message: c.Title + " has a page and is in no chapter of the outline"})
		}
	}
	return out, nil
}
