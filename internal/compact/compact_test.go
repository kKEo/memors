package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/kb"
)

func openStore(t *testing.T) *kb.Store {
	t.Helper()
	db, err := kb.Open(context.Background(), t.TempDir(), "c", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return kb.NewStore(db, embedding.NewHashEmbedder(64))
}

func ingest(t *testing.T, s *kb.Store, ns, uri, title, body string) *kb.IngestResult {
	t.Helper()
	r, err := s.Ingest(context.Background(), kb.IngestInput{Namespace: ns, Content: "# " + title + "\n\n" + body + "\n",
		Source: kb.SourceInput{URI: uri, Title: title, Kind: kb.KindDoc, Origin: kb.OriginWeb}, Trust: kb.TrustUser, Actor: "t", Channel: kb.ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The whole loop: three passages about one entity → a page item → submit
// (dry run shows the diff and the omission) → page stored with sources and
// is_inference → revising a source marks it stale → lint lists it → a stale
// item rebuilds it. Raw rows are never touched.
func TestPageLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	ingest(t, s, "plat", "https://p/billing", "Billing Service", "The Billing Service issues invoices daily and writes to the Ledger Store.")
	ingest(t, s, "plat", "https://p/oncall", "Billing on-call", "The Billing Service pages finance when the invoice run is late.")
	ingest(t, s, "plat", "https://p/mailer", "Mailer", "The Mailer sends invoice emails for the Billing Service.")
	if _, err := s.Remember(ctx, kb.RememberInput{Namespace: "plat", Statement: "The Billing Service runs the invoice job at 02:00 UTC.", About: []string{"Billing Service"}, Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Actor: "t", Channel: kb.ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	items, err := Generate(ctx, s, "plat", nil)
	if err != nil {
		t.Fatal(err)
	}
	var item *kb.WorkItem
	for i := range items {
		if items[i].Kind == KindPage && strings.Contains(string(items[i].Payload), "Billing Service") {
			item = &items[i]
		}
	}
	if item == nil {
		t.Fatalf("no page item for Billing Service: %+v", items)
	}
	// Dry run: an incomplete page is reported, nothing written.
	rep, err := Submit(ctx, s, item.ID, Result{Content: "# Billing Service\n\nIssues invoices daily; writes to the Ledger Store (memo://chunk/1).\n"}, true, "agent", kb.ChannelTool)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Applied || len(rep.Omitted) != 1 || !strings.Contains(rep.Diff, "+ # Billing Service") {
		t.Fatalf("dry run: %+v", rep)
	}
	if pages, _ := s.ListPages(ctx, kb.PageFilter{Namespace: "plat"}); len(pages) != 0 {
		t.Fatal("dry run wrote a page")
	}
	// Real submit with the fact covered.
	content := "# Billing Service\n\nIssues invoices daily and writes to the Ledger Store (memo://chunk/1). Pages finance when the run is late (memo://chunk/2). The invoice job runs at 02:00 UTC.\n"
	rep, err = Submit(ctx, s, item.ID, Result{Content: content}, false, "agent", kb.ChannelTool)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || len(rep.Omitted) != 0 || rep.PageURI == "" {
		t.Fatalf("submit: %+v", rep)
	}
	page, err := s.ReadPage(ctx, strings.TrimPrefix(rep.PageURI, "memo://page/"))
	if err != nil {
		t.Fatal(err)
	}
	if !page.IsInference || page.Trust != kb.TrustAgent || len(page.Sources) < 3 || page.Stale {
		t.Fatalf("page: %+v", page)
	}
	if w, _ := s.ReadWorkItem(ctx, item.ID); w.State != "done" {
		t.Fatalf("item state %s", w.State)
	}
	// Nothing proposed again while nothing grew (recurrence trigger).
	again, _ := Generate(ctx, s, "plat", []string{KindPage, KindStale})
	for _, w := range again {
		if strings.Contains(string(w.Payload), "Billing Service") {
			t.Fatalf("page proposed again without growth: %+v", w)
		}
	}
	// Revise a source → stale → lint → stale item → rebuild.
	ingest(t, s, "plat", "https://p/billing", "Billing Service", "The Billing Service issues invoices hourly now and writes to the Ledger Store.")
	page, _ = s.ReadPage(ctx, page.ID)
	if !page.Stale || !strings.Contains(page.StaleReason, "source revised") {
		t.Fatalf("page should be stale after revision: %+v", page)
	}
	findings, err := Lint(ctx, s, "plat", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var sawStale bool
	for _, f := range findings {
		if f.Kind == "stale-page" && f.URI == page.URI {
			sawStale = true
		}
	}
	if !sawStale {
		t.Fatalf("lint missed the stale page: %+v", findings)
	}
	items, _ = Generate(ctx, s, "plat", []string{KindStale})
	if len(items) != 1 || items[0].Kind != KindStale {
		t.Fatalf("stale item: %+v", items)
	}
	rep, err = Submit(ctx, s, items[0].ID, Result{Content: strings.Replace(content, "daily", "hourly", 1)}, false, "agent", kb.ChannelTool)
	if err != nil || !rep.Applied || !strings.Contains(rep.Diff, "- Issues invoices daily") {
		t.Fatalf("rebuild: %+v %v", rep, err)
	}
	page, _ = s.ReadPage(ctx, page.ID)
	if page.Stale || page.BuiltFromRev != 2 {
		t.Fatalf("rebuilt page: %+v", page)
	}
	st, _ := s.Status(ctx)
	if st.Pages.Pages != 1 || st.Pages.StalePages != 0 {
		t.Fatalf("status: %+v", st.Pages)
	}
}

// Conflicts: two live facts about one subject disagree; the rule names the
// winner; submit invalidates the loser (kept as history), never deletes.
func TestConflictResolution(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a, err := s.Remember(ctx, kb.RememberInput{Namespace: "plat", Statement: "Quorum Replication needs three nodes.", About: []string{"Quorum Replication"}, Origin: kb.OriginAgentDerived, Trust: kb.TrustAgent, Actor: "t", Channel: kb.ChannelTool})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Remember(ctx, kb.RememberInput{Namespace: "plat", Statement: "Quorum Replication needs five nodes.", About: []string{"Quorum Replication"}, Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Actor: "t", Channel: kb.ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	items, err := Generate(ctx, s, "plat", []string{KindConflict})
	if err != nil || len(items) != 1 {
		t.Fatalf("conflict items: %+v %v", items, err)
	}
	if !strings.Contains(string(items[0].Payload), "keep "+b.URI) {
		t.Fatalf("rule should keep the user fact: %s", items[0].Payload)
	}
	// Against the rule: a tool may not invalidate a user fact.
	if _, err := Submit(ctx, s, items[0].ID, Result{Keep: a.URI, Reason: "prefer the old one"}, false, "agent", kb.ChannelTool); err == nil {
		t.Fatal("a tool invalidated a more-trusted fact")
	}
	rep, err := Submit(ctx, s, items[0].ID, Result{Keep: b.URI, Reason: "user said five"}, false, "agent", kb.ChannelTool)
	if err != nil || !rep.Applied {
		t.Fatalf("resolve: %+v %v", rep, err)
	}
	fa, _ := s.ReadFact(ctx, a.ID)
	if fa.InvalidatedAt == nil || fa.SupersededBy != b.ID {
		t.Fatalf("loser not invalidated: %+v", fa)
	}
	if f, _ := Lint(ctx, s, "plat", time.Now()); len(f) != 0 {
		t.Fatalf("lint after resolution: %+v", f)
	}
}

func TestDuplicatesAndOmissions(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	body := "Retries use exponential backoff with full jitter and a cap of thirty seconds; the policy is configured per method in the service config and applies to idempotent calls only."
	ingest(t, s, "d", "https://d/1", "Retry A", body)
	ingest(t, s, "d", "https://d/2", "Retry B", body+" See also the deadline section.")
	ingest(t, s, "d", "https://d/3", "Other", "Deadlines propagate through the context and are enforced by the server before the handler runs.")
	dups, err := Duplicates(ctx, s, "d")
	if err != nil || len(dups) != 1 || dups[0].Jaccard < DuplicateJaccard {
		t.Fatalf("duplicates: %+v %v", dups, err)
	}
	if om := Omissions("The Billing Service runs its invoice job at 02:00 UTC every day.", []string{"The Billing Service runs the invoice job at 02:00 UTC.", "Unrelated statement about kimchi fermentation."}); len(om) != 1 || !strings.Contains(om[0], "kimchi") {
		t.Fatalf("omissions: %v", om)
	}
	if d := Diff("a\nb\nc", "a\nx\nc"); d != "  a\n- b\n+ x\n  c\n" {
		t.Fatalf("diff:\n%s", d)
	}
}

// bookFixture ingests a small platform and writes every entity page the
// first scan proposes. It returns the page id of each entity by name.
func bookFixture(t *testing.T, s *kb.Store) map[string]string {
	t.Helper()
	ctx := context.Background()
	ingest(t, s, "plat", "https://p/billing", "Billing Service", "The Billing Service issues invoices daily and writes to the Ledger Store.")
	ingest(t, s, "plat", "https://p/oncall", "Billing on-call", "The Billing Service pages finance when the invoice run is late. The Ledger Store is read-only during the run.")
	ingest(t, s, "plat", "https://p/relay", "Mail Relay", "The Mail Relay sends invoice emails for the Billing Service. The Mail Relay reads the Ledger Store.")
	ingest(t, s, "plat", "https://p/queue", "Mail Relay queue", "The Mail Relay drains the Outbox Queue every minute.")
	ingest(t, s, "plat", "https://p/retries", "Mail Relay retries", "The Mail Relay retries a failed send three times.\n\n```\nrelay retry --max 3\n```")
	items, err := Generate(ctx, s, "plat", []string{KindPage})
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]string{}
	for _, it := range items {
		var p PagePayload
		if err := json.Unmarshal(it.Payload, &p); err != nil || len(p.Chunks) == 0 {
			t.Fatalf("page payload: %v %+v", err, p)
		}
		rep, err := Submit(ctx, s, it.ID, Result{Content: "# " + p.Entity.Canonical + "\n\n" + p.Chunks[0].Text + " (" + p.Chunks[0].URI + ")\n"}, false, "agent", kb.ChannelTool)
		if err != nil {
			t.Fatal(err)
		}
		pages[p.Entity.Canonical] = strings.TrimPrefix(rep.PageURI, "memo://page/")
	}
	for _, name := range []string{"Billing Service", "Ledger Store", "Mail Relay"} {
		if pages[name] == "" {
			t.Fatalf("no page for %s: %v", name, pages)
		}
	}
	return pages
}

func itemOf(t *testing.T, items []kb.WorkItem, kind, contains string) *kb.WorkItem {
	t.Helper()
	for i := range items {
		if items[i].Kind == kind && strings.Contains(string(items[i].Payload), contains) {
			return &items[i]
		}
	}
	return nil
}

// The book, children first: entity pages → an outline item → chapter items →
// the overview item. A chapter records the pages it was written from and
// only the passages it quotes; a made-up example and an unnamed member are
// reported; one rewritten member leaves a chapter alone and a second sends
// it back; a chapter waits while a member's page is being rebuilt.
func TestBookLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	pages := bookFixture(t, s)
	scan := func(kinds ...string) []kb.WorkItem {
		t.Helper()
		items, err := Generate(ctx, s, "plat", kinds)
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	chapterPage := func(title string) *kb.Page {
		t.Helper()
		o, err := s.ReadOutline(ctx, "plat")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range o.Chapters {
			if c.Title == title && c.PageID != "" {
				p, err := s.ReadPage(ctx, c.PageID)
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
		}
		t.Fatalf("chapter %q has no page", title)
		return nil
	}

	// The outline item lists what to place; nothing else of the book exists yet.
	items := scan()
	outline := itemOf(t, items, KindOutline, "no outline")
	if outline == nil || itemOf(t, items, KindTopic, "") != nil || itemOf(t, items, KindOverview, "") != nil {
		t.Fatalf("expected an outline item and no chapter or overview item: %+v", items)
	}
	var op OutlinePayload
	if err := json.Unmarshal(outline.Payload, &op); err != nil {
		t.Fatal(err)
	}
	uri := map[string]string{}
	for _, c := range op.Candidates {
		if uri[c.Title] == "" { // entities come first; a source may share the name
			uri[c.Title] = c.URI
		}
		if strings.HasPrefix(c.URI, "memo://entity/") && c.Gist == "" {
			t.Errorf("candidate %s has no gist", c.Title)
		}
	}
	for _, name := range []string{"Billing Service", "Ledger Store", "Mail Relay", "Mail Relay retries"} {
		if uri[name] == "" {
			t.Fatalf("%s is not a candidate: %+v", name, op.Candidates)
		}
	}
	chapters := []OutlineChapterIn{
		{Title: "Money", Members: []string{uri["Billing Service"], uri["Ledger Store"]}},
		{Title: "Mail", Scope: "How invoices leave the building.", Members: []string{uri["Mail Relay"], uri["Mail Relay retries"]}},
	}
	rep, err := Submit(ctx, s, outline.ID, Result{Outline: chapters}, true, "agent", kb.ChannelTool)
	if err != nil || rep.Applied || !strings.Contains(rep.Diff, "+ ## Money") {
		t.Fatalf("outline dry run: %+v %v", rep, err)
	}
	if o, _ := s.ReadOutline(ctx, "plat"); len(o.Chapters) != 0 {
		t.Fatal("the dry run stored the outline")
	}
	if rep, err = Submit(ctx, s, outline.ID, Result{Outline: chapters}, false, "agent", kb.ChannelTool); err != nil || !rep.Applied {
		t.Fatalf("outline submit: %+v %v", rep, err)
	}
	findings, err := Lint(ctx, s, "plat", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	noPage := 0
	for _, f := range findings {
		if f.Kind == "chapter-without-page" {
			noPage++
		}
	}
	if noPage != 2 {
		t.Fatalf("lint should flag two chapters without a page: %+v", findings)
	}

	// Chapter items: the material, and an example to quote.
	items = scan()
	mail, money := itemOf(t, items, KindTopic, `"title":"Mail"`), itemOf(t, items, KindTopic, `"title":"Money"`)
	if mail == nil || money == nil || itemOf(t, items, KindOverview, "") != nil || itemOf(t, items, KindOutline, "") != nil {
		t.Fatalf("expected two chapter items, no overview yet and the outline item gone: %+v", items)
	}
	var tp TopicPayload
	if err := json.Unmarshal(mail.Payload, &tp); err != nil {
		t.Fatal(err)
	}
	if len(tp.Members) != 2 || tp.Members[0].Page == "" || len(tp.Members[1].Passages) == 0 || len(tp.Examples) == 0 ||
		!strings.Contains(tp.Examples[0].Text, "relay retry --max 3") || strings.Join(tp.MustCover, ",") != "Mail Relay,Mail Relay retries" {
		t.Fatalf("chapter payload: %+v", tp)
	}
	example := tp.Examples[0]

	// A made-up example and an unnamed member are reported.
	bad := "# Mail\n\nThe Mail Relay sends invoices out of the building.\n\n```\nrelay retry --max 5\n```\n"
	rep, err = Submit(ctx, s, mail.ID, Result{Content: bad}, true, "agent", kb.ChannelTool)
	if err != nil || len(rep.Misquoted) != 1 || len(rep.Omitted) != 1 || rep.Omitted[0] != "Mail Relay retries" {
		t.Fatalf("chapter dry run: %+v %v", rep, err)
	}
	good := "# Mail\n\nThe Mail Relay sends invoice emails (memo://page/" + pages["Mail Relay"] + "). Mail Relay retries are bounded (" + example.URI + "):\n\n```\nrelay   retry --max 3\n```\n"
	rep, err = Submit(ctx, s, mail.ID, Result{Content: good}, false, "agent", kb.ChannelTool)
	if err != nil || !rep.Applied || len(rep.Misquoted) != 0 || len(rep.Omitted) != 0 {
		t.Fatalf("chapter submit: %+v %v", rep, err)
	}
	mailPage := chapterPage("Mail")
	if mailPage.Kind != kb.PageKindTopic || len(mailPage.Deps) != 1 || mailPage.Deps[0] != "memo://page/"+pages["Mail Relay"] ||
		len(mailPage.Sources) != 1 || mailPage.Sources[0] != example.URI {
		t.Fatalf("chapter page should depend on the Mail Relay page and cite only what it quotes: %+v", mailPage)
	}

	// The overview waits for the last chapter.
	if items = scan(); itemOf(t, items, KindOverview, "") != nil {
		t.Fatal("the overview was proposed while a chapter has no page")
	}
	moneyText := "# Money\n\nThe Billing Service issues invoices and writes to the Ledger Store.\n"
	if rep, err = Submit(ctx, s, money.ID, Result{Content: moneyText}, false, "agent", kb.ChannelTool); err != nil || !rep.Applied {
		t.Fatalf("second chapter: %+v %v", rep, err)
	}
	items = scan()
	over := itemOf(t, items, KindOverview, "no overview")
	if over == nil || itemOf(t, items, KindTopic, "") != nil {
		t.Fatalf("expected the overview item and no chapter item: %+v", items)
	}
	rep, err = Submit(ctx, s, over.ID, Result{Content: "# Overview\n\nThe platform in two parts: Money.\n"}, true, "agent", kb.ChannelTool)
	if err != nil || len(rep.Omitted) != 1 || rep.Omitted[0] != "Mail" {
		t.Fatalf("overview dry run should miss the Mail chapter: %+v %v", rep, err)
	}
	overText := "# Overview\n\nThe platform in two parts: Money, then Mail (memo://page/" + mailPage.ID + ").\n"
	if rep, err = Submit(ctx, s, over.ID, Result{Content: overText}, false, "agent", kb.ChannelTool); err != nil || !rep.Applied {
		t.Fatalf("overview submit: %+v %v", rep, err)
	}
	overview, err := s.PageBySubject(ctx, "plat", kb.PageKindOverview, kb.OverviewSubject)
	if err != nil || overview == nil || len(overview.Deps) != 2 || len(overview.Sources) != 0 {
		t.Fatalf("overview page: %+v %v", overview, err)
	}
	if items = scan(KindOutline, KindTopic, KindOverview); len(items) != 0 {
		t.Fatalf("the book is complete, nothing should be proposed: %+v", items)
	}

	// One rewritten member leaves the chapter alone; the second sends it
	// back. The overview is untouched until chapters are rewritten.
	rewrite := func(name string) {
		t.Helper()
		p, err := s.ReadPage(ctx, pages[name])
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if _, err := fmt.Sscanf(p.Sources[0], "memo://chunk/%d", &id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.WritePage(ctx, kb.PageInput{Namespace: "plat", Kind: kb.PageKindEntity, SubjectID: p.SubjectID, Title: p.Title, Content: p.Content + "\nRevised.\n", Sources: []int64{id}, Actor: "agent"}); err != nil {
			t.Fatal(err)
		}
	}
	rewrite("Billing Service")
	if p := chapterPage("Money"); p.Stale {
		t.Fatalf("one rewritten member staled the chapter: %s", p.StaleReason)
	}
	if n, _ := s.PageDrift(ctx, chapterPage("Money").ID); n != 1 {
		t.Fatalf("drift = %d, want 1", n)
	}
	rewrite("Ledger Store")
	if p := chapterPage("Money"); !p.Stale || !strings.Contains(p.StaleReason, "2 of the pages") {
		t.Fatalf("two rewritten members should stale the chapter: %+v", p)
	}
	if o, _ := s.PageBySubject(ctx, "plat", kb.PageKindOverview, kb.OverviewSubject); o.Stale {
		t.Fatal("the overview went stale before any chapter was rewritten")
	}
	if p := chapterPage("Mail"); p.Stale {
		t.Fatal("the other chapter went stale")
	}

	// An outline edit stales the chapter it touches, and the overview.
	o, _ := s.ReadOutline(ctx, "plat")
	o.Chapters[1].Scope = "How invoices and reminders leave the building."
	if _, err := s.SetOutline(ctx, "plat", o.Chapters, "me", kb.ChannelCLI); err != nil {
		t.Fatal(err)
	}
	if p := chapterPage("Mail"); !p.Stale {
		t.Fatal("re-scoping a chapter should stale its page")
	}
	if o, _ := s.PageBySubject(ctx, "plat", kb.PageKindOverview, kb.OverviewSubject); !o.Stale {
		t.Fatal("re-scoping a chapter should stale the overview")
	}

	// Revising a document the Mail Relay page was built from stales that page;
	// its chapter then waits for it, while the other chapter is offered.
	ingest(t, s, "plat", "https://p/relay", "Mail Relay", "The Mail Relay sends invoice and reminder emails for the Billing Service. The Mail Relay reads the Ledger Store.")
	if p, _ := s.ReadPage(ctx, pages["Mail Relay"]); !p.Stale {
		t.Skip("the Mail Relay page was not built from the revised document")
	}
	items = scan(KindTopic, KindOverview)
	if itemOf(t, items, KindTopic, `"title":"Mail"`) != nil || itemOf(t, items, KindTopic, `"title":"Money"`) == nil || itemOf(t, items, KindOverview, "") != nil {
		t.Fatalf("Mail should wait for the Mail Relay page, Money be offered, the overview wait: %+v", items)
	}
	if n, err := BookWaiting(ctx, s, "plat"); err != nil || n != 1 {
		t.Fatalf("waiting chapters = %d (%v), want 1", n, err)
	}
}

func TestQuotesAndMisquoted(t *testing.T) {
	page := "Intro.\n\n```go\nfunc A() {}\n```\n\n> first line\n> second line\n\nText.\n\n~~~\nraw\n~~~\n"
	q := Quotes(page)
	if len(q) != 3 || q[0] != "func A() {}" || q[1] != "first line\nsecond line" || q[2] != "raw" {
		t.Fatalf("quotes: %q", q)
	}
	bad, from := Misquoted(page, []source{{chunk: 7, text: "x\nfunc A()   {}\ny"}, {text: "first line second line"}})
	if len(bad) != 1 || bad[0] != "raw" || len(from) != 1 || from[0] != 7 {
		t.Fatalf("misquoted: %q from %v", bad, from)
	}
	if got := stripQuotes(page); strings.Contains(got, "func A") || strings.Contains(got, "first line") || !strings.Contains(got, "Intro.") {
		t.Fatalf("stripQuotes: %q", got)
	}
}
