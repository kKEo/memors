package kb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// outlineFixture is a namespace with two documents, their entities, and the
// near-duplicate name that opens a merge candidate.
func outlineFixture(t *testing.T) (*Store, *sql.DB, map[string]string) {
	t.Helper()
	s, db := openTestStore(t, nil)
	ctx := context.Background()
	docs := []IngestInput{
		{Namespace: "widgets", Content: "# Client.Connect\n\n`Connect(ctx, addr)` dials the Widgets Server. It is used by the Pipeline Scheduler.\n",
			Source: SourceInput{URI: "https://w.example/connect", Title: "Client.Connect", Kind: KindDoc, Origin: OriginWeb}},
		{Namespace: "widgets", Content: "# Pipeline Scheduler\n\nThe Pipeline Scheduler retries client.connect on ERR_CONN_RESET.\n",
			Source: SourceInput{URI: "https://w.example/scheduler", Title: "Pipeline Scheduler", Kind: KindDoc, Origin: OriginWeb}},
		{Namespace: "widgets", Content: "# Notes\n\nThe Pipeline Schedulers team owns rollout.\n",
			Source: SourceInput{Title: "Notes", Kind: KindNote, Origin: OriginUserSaid}},
	}
	for _, in := range docs {
		in.Trust, in.Actor, in.Channel = TrustUser, "t", ChannelCLI
		if _, err := s.Ingest(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]string{}
	for _, name := range []string{"Client.Connect", "Pipeline Scheduler", "Pipeline Schedulers"} {
		var id string
		if err := db.QueryRow(`SELECT id FROM entities WHERE namespace = 'widgets' AND canonical = ?`, name).Scan(&id); err != nil {
			t.Fatalf("entity %s: %v", name, err)
		}
		ids[name] = id
	}
	for _, title := range []string{"Client.Connect", "Notes"} {
		var id string
		if err := db.QueryRow(`SELECT id FROM sources WHERE namespace = 'widgets' AND title = ?`, title).Scan(&id); err != nil {
			t.Fatalf("source %s: %v", title, err)
		}
		ids["source:"+title] = id
	}
	return s, db, ids
}

func TestOutlineSetReadAndFileRoundTrip(t *testing.T) {
	s, db, ids := outlineFixture(t)
	ctx := context.Background()
	chapters := []OutlineChapter{
		{Title: "Connecting", Scope: "How a client\nreaches the server.", Members: []OutlineMember{
			{Kind: MemberEntity, ID: ids["Client.Connect"]}, {Kind: MemberSource, ID: ids["source:Client.Connect"]}}},
		{Title: "Scheduling", Members: []OutlineMember{{Kind: MemberEntity, ID: ids["Pipeline Scheduler"]}}},
	}
	// A preview checks and stores nothing.
	if o, err := s.PreviewOutline(ctx, "widgets", chapters, "t", ChannelCLI); err != nil || len(o.Chapters) != 2 {
		t.Fatalf("preview: %+v %v", o, err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM outline_chapters`); n != 0 {
		t.Fatalf("a preview stored %d chapters", n)
	}
	o, err := s.SetOutline(ctx, "widgets", chapters, "t", ChannelCLI)
	if err != nil {
		t.Fatal(err)
	}
	first := o.Chapters[0]
	if first.ID == "" || first.Trust != TrustCurated || first.Scope != "How a client reaches the server." ||
		len(first.Members) != 2 || first.Members[0].Title != "Client.Connect" || first.Members[1].DocID == "" || first.Members[1].Missing {
		t.Fatalf("chapter as stored: %+v", first)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM audit WHERE op = 'outline_set'`); n != 1 {
		t.Fatalf("audit rows: %d", n)
	}

	// The file form reads back to the same outline, and names what is left over.
	cands, err := s.OutlineCandidates(ctx, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	text := RenderOutline(o, cands)
	if !strings.Contains(text, "## Connecting <!-- chapter: "+first.ID+" -->") || !strings.Contains(text, "> How a client reaches the server.") ||
		!strings.Contains(text, "<!-- Not in a chapter.") || !strings.Contains(text, "memo://source/"+ids["source:Notes"]+"  Notes") {
		t.Fatalf("rendered outline:\n%s", text)
	}
	ns, parsed, err := ParseOutline(text)
	if err != nil || ns != "widgets" || len(parsed) != 2 {
		t.Fatalf("parse: %q %+v %v", ns, parsed, err)
	}
	for i, c := range parsed {
		if c.ID != o.Chapters[i].ID || c.Title != o.Chapters[i].Title || c.Scope != o.Chapters[i].Scope || !sameMembers(c.Members, o.Chapters[i].Members) {
			t.Errorf("chapter %d did not survive the file: %+v", i, c)
		}
	}

	// Without ids, a chapter is matched to the stored one by its title.
	if again, err := s.SetOutline(ctx, "widgets", chapters, "t", ChannelCLI); err != nil || again.Chapters[0].ID != first.ID || again.Chapters[1].ID != o.Chapters[1].ID {
		t.Fatalf("the same outline without ids should keep the chapters: %+v %v", again, err)
	}

	// A chapter page follows its chapter: retitling keeps the id and stales
	// the page, reordering does not, removing the chapter retires the page.
	page, err := s.WritePage(ctx, PageInput{Namespace: "widgets", Kind: PageKindTopic, SubjectID: first.ID, Title: "Connecting", Content: "# Connecting\n\nClient.Connect dials (memo://chunk/1).\n", Sources: []int64{1}, Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	parsed[0], parsed[1] = parsed[1], parsed[0]
	if o, err = s.SetOutline(ctx, "widgets", parsed, "t", ChannelCLI); err != nil || o.Chapters[1].ID != first.ID || o.Chapters[1].PageID != page.ID || o.Chapters[1].PageStale {
		t.Fatalf("reorder: %+v %v", o, err)
	}
	parsed[1].Title = "Connecting to the server"
	if o, err = s.SetOutline(ctx, "widgets", parsed, "t", ChannelCLI); err != nil || o.Chapters[1].ID != first.ID || !o.Chapters[1].PageStale {
		t.Fatalf("retitle should keep the id and stale the page: %+v %v", o, err)
	}
	if o, err = s.SetOutline(ctx, "widgets", parsed[:1], "t", ChannelCLI); err != nil || len(o.Chapters) != 1 {
		t.Fatalf("remove: %+v %v", o, err)
	}
	var gone *Forgotten
	if _, err := s.ReadPage(ctx, page.ID); !errors.As(err, &gone) || !strings.Contains(gone.Reason, "removed from the outline") {
		t.Fatalf("the removed chapter's page should be retired: %v", err)
	}
	if got, _ := s.OutlinedNamespaces(ctx); len(got) != 1 || got[0] != "widgets" {
		t.Fatalf("outlined namespaces: %v", got)
	}
}

func TestOutlineRefusesWhatDoesNotFit(t *testing.T) {
	s, _, ids := outlineFixture(t)
	ctx := context.Background()
	connect := OutlineMember{Kind: MemberEntity, ID: ids["Client.Connect"]}
	cases := []struct {
		name     string
		chapters []OutlineChapter
		want     string
	}{
		{"no title", []OutlineChapter{{Title: "  "}}, "one-line title"},
		{"same title twice", []OutlineChapter{{Title: "A"}, {Title: "a"}}, "two chapters are titled"},
		{"member in two chapters", []OutlineChapter{{Title: "A", Members: []OutlineMember{connect}}, {Title: "B", Members: []OutlineMember{connect}}}, "in two chapters"},
		{"unknown member", []OutlineChapter{{Title: "A", Members: []OutlineMember{{Kind: MemberEntity, ID: "nope"}}}}, "not found"},
		{"member kind", []OutlineChapter{{Title: "A", Members: []OutlineMember{{Kind: "page", ID: "x"}}}}, "an entity or a source"},
		{"unknown chapter id", []OutlineChapter{{ID: "0192-nope", Title: "A"}}, "is not a chapter"},
	}
	for _, c := range cases {
		if _, err := s.SetOutline(ctx, "widgets", c.chapters, "t", ChannelCLI); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", c.name, err, c.want)
		}
	}
	// A member of another namespace is not found here.
	if _, err := s.SetOutline(ctx, "elsewhere", []OutlineChapter{{Title: "A", Members: []OutlineMember{connect}}}, "t", ChannelCLI); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-namespace member: %v", err)
	}
	for _, bad := range []string{"## A\nplain text\n", "- memo://entity/x  before any chapter\n", "## A\n- not an address\n", "> scope first\n"} {
		if _, _, err := ParseOutline(bad); err == nil {
			t.Errorf("ParseOutline accepted %q", bad)
		}
	}
}

func TestOutlineToolCallsOnlyAddToWhatAPersonWrote(t *testing.T) {
	s, _, ids := outlineFixture(t)
	ctx := context.Background()
	connect := OutlineMember{Kind: MemberEntity, ID: ids["Client.Connect"]}
	sched := OutlineMember{Kind: MemberEntity, ID: ids["Pipeline Scheduler"]}
	o, err := s.SetOutline(ctx, "widgets", []OutlineChapter{{Title: "Connecting", Members: []OutlineMember{connect}}}, "me", ChannelCLI)
	if err != nil {
		t.Fatal(err)
	}
	mine := o.Chapters[0]
	var needs *ErrNeedsHuman
	refused := map[string][]OutlineChapter{
		"remove":      {},
		"retitle":     {{ID: mine.ID, Title: "Dialling", Members: []OutlineMember{connect}}},
		"take member": {{ID: mine.ID, Title: "Connecting"}},
		"move member": {{ID: mine.ID, Title: "Connecting"}, {Title: "Other", Members: []OutlineMember{connect}}},
	}
	for name, chapters := range refused {
		if _, err := s.SetOutline(ctx, "widgets", chapters, "agent", ChannelTool); !errors.As(err, &needs) || !strings.Contains(err.Error(), "memors-mcp outline apply --ns widgets") {
			t.Errorf("%s: a tool call should need a human, got %v", name, err)
		}
	}
	// Adding a member and a chapter of its own is allowed; the person's
	// chapter stays theirs.
	o, err = s.SetOutline(ctx, "widgets", []OutlineChapter{
		{ID: mine.ID, Title: "Connecting", Members: []OutlineMember{connect, {Kind: MemberSource, ID: ids["source:Client.Connect"]}}},
		{Title: "Scheduling", Members: []OutlineMember{sched}},
	}, "agent", ChannelTool)
	if err != nil {
		t.Fatal(err)
	}
	if o.Chapters[0].Trust != TrustCurated || o.Chapters[0].Actor != "me" || len(o.Chapters[0].Members) != 2 || o.Chapters[1].Trust != TrustAgent {
		t.Fatalf("after the tool call: %+v", o.Chapters)
	}
	// Its own chapter it may drop again.
	if _, err := s.SetOutline(ctx, "widgets", o.Chapters[:1], "agent", ChannelTool); err != nil {
		t.Fatalf("a tool call should be able to remove an agent chapter: %v", err)
	}
}

func TestOutlineMemberFollowsAnEntityMerge(t *testing.T) {
	s, _, ids := outlineFixture(t)
	ctx := context.Background()
	cands, err := s.MergeCandidates(ctx, "open")
	if err != nil || len(cands) == 0 {
		t.Fatalf("expected a merge candidate: %v %v", cands, err)
	}
	loser, winner := cands[0].B.ID, cands[0].A.ID
	if _, err := s.SetOutline(ctx, "widgets", []OutlineChapter{{Title: "Scheduling", Members: []OutlineMember{{Kind: MemberEntity, ID: loser}}}}, "t", ChannelCLI); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideMerge(ctx, cands[0].ID, true, "t", ChannelCLI); err != nil {
		t.Fatal(err)
	}
	o, err := s.ReadOutline(ctx, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	if m := o.Chapters[0].Members; len(m) != 1 || m[0].ID != winner || m[0].Missing {
		t.Fatalf("the member should now be the surviving entity %s (was %s, ids %v): %+v", winner, loser, ids, m)
	}
}

// A file at schema version 5 gains the outline tables and keeps its pages.
func TestMigrateV5ToV6(t *testing.T) {
	ctx := context.Background()
	s, db, _ := outlineFixture(t)
	page, err := s.WritePage(ctx, PageInput{Namespace: "widgets", Kind: PageKindEntity, SubjectID: "e1", Title: "Kept", Content: "kept (memo://chunk/1)", Sources: []int64{1}, Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE page_deps`, `DROP TABLE outline_members`, `DROP TABLE outline_chapters`, `PRAGMA user_version = 5`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if v, err := SchemaVersion(ctx, db); err != nil || v != 6 {
		t.Fatalf("version = %d (%v)", v, err)
	}
	if o, err := s.ReadOutline(ctx, "widgets"); err != nil || len(o.Chapters) != 0 {
		t.Fatalf("outline after migration: %+v %v", o, err)
	}
	if p, err := s.ReadPage(ctx, page.ID); err != nil || p.Title != "Kept" {
		t.Fatalf("page after migration: %+v %v", p, err)
	}
}

// readTree returns every file under dir by its slash-separated path.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// An outlined namespace is exported as its book: overview, chapters with
// their member pages, loose pages, then the documents as an appendix. A
// namespace without an outline keeps the old layout, documents are exported
// byte for byte either way, and citations in pages become links.
func TestExportFollowsOutline(t *testing.T) {
	s, _, ids := outlineFixture(t)
	ctx := context.Background()
	s.SetNow(func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) })
	if _, err := s.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc())); err != nil {
		t.Fatal(err)
	}
	entityPage := func(name, content string) *Page {
		t.Helper()
		p, err := s.WritePage(ctx, PageInput{Namespace: "widgets", Kind: PageKindEntity, SubjectID: ids[name], Title: name, Content: content, Sources: []int64{1}, Actor: "t"})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	connect := entityPage("Client.Connect", "# Client.Connect\n\nDials the server (memo://chunk/1); used by [the scheduler](memo://entity/"+ids["Pipeline Scheduler"]+"). Gone: memo://chunk/999.\n")
	sched := entityPage("Pipeline Scheduler", "# Pipeline Scheduler\n\nRetries connects (memo://chunk/1).\n")
	loose := entityPage("Pipeline Schedulers", "# Pipeline Schedulers\n\nA team (memo://chunk/1).\n")

	plain := t.TempDir()
	if _, err := s.Export(ctx, plain, ExportOptions{}); err != nil {
		t.Fatal(err)
	}

	o, err := s.SetOutline(ctx, "widgets", []OutlineChapter{
		{Title: "Connecting", Scope: "Reaching the server.", Members: []OutlineMember{{Kind: MemberEntity, ID: ids["Client.Connect"]}, {Kind: MemberSource, ID: ids["source:Client.Connect"]}}},
		{Title: "Scheduling", Members: []OutlineMember{{Kind: MemberEntity, ID: ids["Pipeline Scheduler"]}}},
	}, "t", ChannelCLI)
	if err != nil {
		t.Fatal(err)
	}
	chapter, err := s.WritePage(ctx, PageInput{Namespace: "widgets", Kind: PageKindTopic, SubjectID: o.Chapters[0].ID, Title: "Connecting",
		Content: "# Connecting\n\nStart with Client.Connect (memo://page/" + connect.ID + ").\n\n```\nsee memo://chunk/1\n```\n", Sources: []int64{1}, Deps: []string{connect.ID}, Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	overview, err := s.WritePage(ctx, PageInput{Namespace: "widgets", Kind: PageKindOverview, SubjectID: OverviewSubject, Title: "Overview",
		Content: "# Overview\n\nOne chapter so far: Connecting.\n", Deps: []string{chapter.ID}, Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if _, err := s.Export(ctx, dir, ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	tree := readTree(t, dir)
	again := t.TempDir()
	if _, err := s.Export(ctx, again, ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	for name, text := range readTree(t, again) {
		if tree[name] != text {
			t.Errorf("%s differs between two exports", name)
		}
	}

	// Reading order in the table of contents.
	summary := tree["SUMMARY.md"]
	rel := func(p *Page) string { return pageRel("widgets", p) }
	order := []string{
		"- [widgets](widgets/_index.md)",
		"  - [Overview](" + rel(overview) + ")",
		"  - [Connecting](" + rel(chapter) + ")",
		"    - [Client.Connect](" + rel(connect) + ")",
		"  - [Scheduling]()",
		"    - [Pipeline Scheduler](" + rel(sched) + ")",
		"  - [Not in a chapter]()",
		"    - [Pipeline Schedulers](" + rel(loose) + ")",
		"  - [Sources]()",
		"    - [doc]()",
		"      - [Client.Connect](widgets/doc/",
		"    - [note]()",
	}
	at := 0
	for _, want := range order {
		i := strings.Index(summary[at:], want+"")
		if i < 0 {
			t.Fatalf("SUMMARY.md lacks %q after position %d:\n%s", want, at, summary)
		}
		at += i + len(want)
	}
	// The namespace without an outline keeps its layout.
	if !strings.Contains(summary, "- [grpc](grpc/_index.md)\n  - [doc]()\n    - [gRPC Interceptors](grpc/doc/") {
		t.Errorf("the plain namespace changed:\n%s", summary)
	}
	// Every file is in the table of contents once, and every link in every
	// file leads to a file.
	link := regexp.MustCompile(`\]\(([^)#]+)\)`)
	for name, text := range tree {
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		if name != "SUMMARY.md" {
			if n := strings.Count(summary, "]("+name+")"); n != 1 {
				t.Errorf("%s appears %d times in SUMMARY.md", name, n)
			}
		}
		for _, m := range link.FindAllStringSubmatch(text, -1) {
			if strings.Contains(m[1], "://") {
				continue
			}
			if _, ok := tree[path.Join(path.Dir(name), m[1])]; !ok {
				t.Errorf("%s links to %s, which is not in the export", name, m[1])
			}
		}
	}

	// Citations became links; a fenced block and an unknown address did not.
	page := tree[rel(connect)]
	for _, want := range []string{"([chunk/1](../doc/client-connect-", "[the scheduler](" + path.Base(rel(sched)) + ")", "Gone: memo://chunk/999.", "\nchapter: Connecting\n"} {
		if !strings.Contains(page, want) {
			t.Errorf("entity page lacks %q:\n%s", want, page)
		}
	}
	ch := tree[rel(chapter)]
	for _, want := range []string{"```\nsee memo://chunk/1\n```", "**In this chapter**", "- [Client.Connect](" + path.Base(rel(connect)) + ")", "(source)", "built_from_pages:\n  - memo://page/" + connect.ID} {
		if !strings.Contains(ch, want) {
			t.Errorf("chapter page lacks %q:\n%s", want, ch)
		}
	}
	if ov := tree[rel(overview)]; !strings.Contains(ov, "**Chapters**\n\n- [Connecting]("+path.Base(rel(chapter))+")") {
		t.Errorf("overview footer:\n%s", ov)
	}
	if stored, _ := s.ReadPage(ctx, chapter.ID); strings.Contains(stored.Content, "In this chapter") {
		t.Error("the footer was stored")
	}
	if idx := tree["widgets/_index.md"]; !strings.Contains(idx, "## Contents") || !strings.Contains(idx, "- Scheduling (not written yet)") || !strings.Contains(idx, "## Documents") {
		t.Errorf("_index.md:\n%s", idx)
	}
	if strings.Contains(tree["grpc/_index.md"], "## Contents") {
		t.Error("the plain namespace got a contents list")
	}

	// Documents are the same bytes with and without an outline.
	docs := 0
	for name, text := range readTree(t, plain) {
		if strings.Contains(name, "/pages/") || !strings.Contains(name, "/") || strings.HasSuffix(name, "_index.md") {
			continue
		}
		docs++
		if tree[name] != text {
			t.Errorf("document %s changed when the outline was added", name)
		}
	}
	if docs != 4 {
		t.Fatalf("compared %d documents, want 4", docs)
	}

	// The same order, for the web UI.
	book, err := s.Book(ctx, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range book.Order() {
		got = append(got, e.Title)
	}
	if strings.Join(got, " > ") != "Overview > Connecting > Client.Connect > Pipeline Scheduler > Pipeline Schedulers" {
		t.Fatalf("reading order: %v", got)
	}
	if plainBook, _ := s.Book(ctx, "grpc"); len(plainBook.Chapters) != 0 || len(plainBook.Order()) != 0 {
		t.Fatalf("a namespace without an outline has no book: %+v", plainBook)
	}
}
