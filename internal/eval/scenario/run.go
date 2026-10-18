package scenario

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memors/internal/compact"
	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/eval"
	"github.com/kKEo/memors/internal/kb"
	"github.com/kKEo/memors/internal/retrieve"
	"github.com/kKEo/memors/internal/server"
)

// Options tunes a run.
type Options struct {
	// Embedder replaces the deterministic hash embedder (384 dimensions),
	// e.g. a real model for the nightly tier.
	Embedder embedding.Embedder
	// Dir is where the temporary knowledge base is created; default a fresh
	// temporary directory removed when the run ends.
	Dir string
	// Logf receives one line per step when set.
	Logf func(format string, args ...any)
	// Trace records each step's output (capped) and the status counters
	// after it on the report, for tools that show a run on the picture.
	Trace bool
}

// Runner executes one scenario against one fresh knowledge base.
type Runner struct {
	sc    *Scenario
	opts  Options
	dir   string
	owned bool
	db    *sql.DB
	store *kb.Store
	svc   *retrieve.Service
	srv   *server.Server

	clock0 time.Time
	now    time.Time

	agents    map[string]*agent
	binds     map[string]map[string]string
	report    *Report
	lastAudit int64
}

type agent struct {
	name   string
	client string
	cs     *mcp.ClientSession
	mu     sync.Mutex
	answer string
}

// outcome is what one executed step produced.
type outcome struct {
	out    map[string]any
	err    error
	audit  []auditRow
	before string // raw-row checksum before compact/submit
	after  string
	args   map[string]any
}

type auditRow struct {
	ID      int64
	Actor   string
	Channel string
	Op      string
	Target  string
}

// Run executes the scenario and returns its report; the error is for setup
// failures only, step failures are in the report.
func Run(ctx context.Context, sc *Scenario, opts Options) (*Report, error) {
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	r := &Runner{sc: sc, opts: opts, agents: map[string]*agent{}, binds: map[string]map[string]string{},
		report: &Report{Name: sc.Name, Path: sc.Path, Decision: sc.Decision, Tiers: sc.Tiers}}
	if err := r.open(ctx); err != nil {
		r.close()
		return nil, err
	}
	defer r.close()
	for i := range sc.Steps {
		r.runStep(ctx, i, sc.Steps[i])
	}
	return r.report, nil
}

func (r *Runner) open(ctx context.Context) error {
	r.dir = r.opts.Dir
	if r.dir == "" {
		d, err := os.MkdirTemp("", "memors-scenario-")
		if err != nil {
			return err
		}
		r.dir, r.owned = d, true
	}
	db, err := kb.Open(ctx, r.dir, "scenario", kb.Options{})
	if err != nil {
		return err
	}
	r.db = db
	emb := r.opts.Embedder
	if emb == nil {
		emb = embedding.NewHashEmbedder(384)
	}
	r.store = kb.NewStore(db, emb)
	r.clock0, _ = r.sc.ClockStart()
	r.now = r.clock0
	nowFn := func() time.Time { return r.now }
	r.store.SetNow(nowFn)
	r.svc = retrieve.New(r.store, retrieve.Default, false)
	r.svc.SetNow(nowFn)
	r.srv = server.New(r.store, r.svc, "scenario")
	names := make([]string, 0, len(r.sc.Actors))
	for n := range r.sc.Actors {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if a := r.sc.Actors[n]; a.Kind == "agent" {
			if err := r.connectAgent(n, a); err != nil {
				return err
			}
		}
	}
	r.lastAudit, err = r.maxAuditID(ctx)
	return err
}

func (r *Runner) close() {
	for _, a := range r.agents {
		if a.cs != nil {
			_ = a.cs.Close()
		}
	}
	if r.db != nil {
		_ = r.db.Close()
	}
	if r.owned {
		_ = os.RemoveAll(r.dir)
	}
}

func (r *Runner) connectAgent(name string, a Actor) error {
	ag := &agent{name: name, client: a.Client, answer: a.Elicitation}
	if ag.client == "" {
		ag.client = name
	}
	ct, st := mcp.NewInMemoryTransports()
	if _, err := r.srv.Connect(context.Background(), st); err != nil {
		return fmt.Errorf("actor %s: server connect: %w", name, err)
	}
	var opts *mcp.ClientOptions
	if a.Elicitation != "" && a.Elicitation != "none" {
		opts = &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, _ *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			ag.mu.Lock()
			ans := ag.answer
			ag.mu.Unlock()
			switch ans {
			case "accept":
				return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
			case "decline", "cancel":
				return &mcp.ElicitResult{Action: ans}, nil
			}
			return nil, fmt.Errorf("actor %s has nobody to answer the dialog", name)
		}}
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: ag.client, Version: "scenario"}, opts).Connect(context.Background(), ct, nil)
	if err != nil {
		return fmt.Errorf("actor %s: client connect: %w", name, err)
	}
	ag.cs = cs
	r.agents[name] = ag
	return nil
}

// --- steps ---

func (r *Runner) runStep(ctx context.Context, idx int, st Step) {
	sr := StepReport{Index: idx + 1, Path: StepPath(idx, -1), At: st.At, Actor: st.Actor, Op: st.Op, Note: st.Note}
	if st.At != "" {
		d, _ := ParseAt(st.At)
		r.now = r.clock0.Add(d)
	}
	sr.Clock = r.now
	var last *outcome
	if len(st.Parallel) > 0 {
		sr.Op = fmt.Sprintf("parallel(%d)", len(st.Parallel))
		outs := make([]outcome, len(st.Parallel))
		// Resolve references before anything runs, then run at once.
		for i, sub := range st.Parallel {
			args, err := r.resolveArgs(sub.Args)
			if err != nil {
				outs[i].err = err
				continue
			}
			outs[i].args = args
		}
		var wg sync.WaitGroup
		for i, sub := range st.Parallel {
			if outs[i].err != nil {
				continue
			}
			wg.Add(1)
			go func(i int, sub Step) {
				defer wg.Done()
				o := r.execute(ctx, sub, outs[i].args)
				outs[i] = o
			}(i, sub)
		}
		wg.Wait()
		// Audit rows for the group as a whole: each sub-step sees them all.
		rows, err := r.auditSince(ctx)
		if err != nil {
			sr.Failures = append(sr.Failures, "audit: "+err.Error())
		}
		for i, sub := range st.Parallel {
			outs[i].audit = rows
			label := fmt.Sprintf("%s/%s", sub.Actor, sub.Op)
			fails := r.finish(ctx, sub, outs[i])
			for _, f := range fails {
				sr.Failures = append(sr.Failures, label+": "+f)
			}
			ss := StepReport{Index: i + 1, Path: StepPath(idx, i), Clock: r.now, Actor: sub.Actor, Op: sub.Op, Note: sub.Note, Summary: summarize(sub.Op, outs[i]), Failures: fails}
			r.trace(&ss, sub, outs[i])
			sr.Sub = append(sr.Sub, ss)
		}
	} else {
		args, err := r.resolveArgs(st.Args)
		var o outcome
		if err != nil {
			o.err = err
		} else {
			o = r.execute(ctx, st, args)
			o.audit, err = r.auditSince(ctx)
			if err != nil {
				sr.Failures = append(sr.Failures, "audit: "+err.Error())
			}
		}
		sr.Failures = append(sr.Failures, r.finish(ctx, st, o)...)
		sr.Summary = summarize(st.Op, o)
		r.trace(&sr, st, o)
		last = &o
	}
	sr.Invariants = r.checkInvariants(ctx, st, last)
	if r.opts.Trace {
		sr.Status, _ = r.statusMap(ctx)
	}
	if r.opts.Logf != nil {
		r.opts.Logf("%s", sr.Line())
	}
	r.report.Steps = append(r.report.Steps, sr)
}

// trace records what the step bound and, under Options.Trace, what it
// returned.
func (r *Runner) trace(sr *StepReport, st Step, o outcome) {
	if st.Bind != "" {
		if b, ok := r.binds[st.Bind]; ok {
			sr.Binds = make(map[string]string, len(b))
			for k, v := range b {
				sr.Binds[k] = v
			}
		}
	}
	if r.opts.Trace && o.out != nil {
		sr.Out, _ = capValue(o.out, 2048, 20).(map[string]any)
	}
}

// capValue copies a value tree with strings cut at maxStr bytes (on a rune
// boundary) and lists at maxList items.
func capValue(v any, maxStr, maxList int) any {
	switch x := v.(type) {
	case string:
		if len(x) <= maxStr {
			return x
		}
		i := maxStr
		for i > 0 && !utf8.RuneStart(x[i]) {
			i--
		}
		return x[:i] + "…"
	case []any:
		out := make([]any, 0, min(len(x), maxList))
		for i, e := range x {
			if i >= maxList {
				break
			}
			out = append(out, capValue(e, maxStr, maxList))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = capValue(e, maxStr, maxList)
		}
		return out
	}
	return v
}

// execute performs one step's operation and returns what it produced.
func (r *Runner) execute(ctx context.Context, st Step, args map[string]any) outcome {
	o := outcome{args: args}
	a := r.sc.Actors[st.Actor]
	if st.Op == "compact" || st.Op == "submit" {
		o.before, _ = eval.RawChecksum(ctx, r.db, false)
	}
	switch a.Kind {
	case "agent":
		o.out, o.err = r.callTool(ctx, r.agents[st.Actor], st, args)
	case "human":
		o.out, o.err = r.humanOp(ctx, st.Actor, a, st.Op, args)
	}
	if st.Op == "compact" || st.Op == "submit" {
		o.after, _ = eval.RawChecksum(ctx, r.db, false)
	}
	return o
}

func (r *Runner) callTool(ctx context.Context, ag *agent, st Step, args map[string]any) (map[string]any, error) {
	if st.Elicit != "" {
		ag.mu.Lock()
		prev := ag.answer
		ag.answer = st.Elicit
		ag.mu.Unlock()
		defer func() {
			ag.mu.Lock()
			ag.answer = prev
			ag.mu.Unlock()
		}()
	}
	if args == nil {
		args = map[string]any{}
	}
	res, err := ag.cs.CallTool(ctx, &mcp.CallToolParams{Name: st.Op, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("protocol: %w", err)
	}
	if res.IsError {
		return nil, errors.New(strings.TrimSpace(textOf(res)))
	}
	out, err := toMap(res.StructuredContent)
	if err != nil {
		return nil, fmt.Errorf("structured content: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	out["text"] = textOf(res)
	return out, nil
}

func textOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// finish applies binds and expectations to an executed step.
func (r *Runner) finish(ctx context.Context, st Step, o outcome) []string {
	var fails []string
	if st.Bind != "" && o.err == nil {
		b, err := r.bindOf(ctx, st, o.out)
		if err != nil {
			fails = append(fails, "bind: "+err.Error())
		} else {
			r.binds[st.Bind] = b
		}
	}
	e := st.Expect
	wantErr := e != nil && ((e.Error != nil && *e.Error) || e.ErrorContains != "")
	if o.err != nil {
		if !wantErr {
			return append(fails, "failed: "+o.err.Error())
		}
		if e.ErrorContains != "" && !strings.Contains(o.err.Error(), e.ErrorContains) {
			fails = append(fails, fmt.Sprintf("error %q does not contain %q", o.err.Error(), e.ErrorContains))
		}
	} else if wantErr {
		fails = append(fails, "expected an error, the op succeeded")
	}
	if e == nil {
		return fails
	}
	if o.err == nil {
		fails = append(fails, r.checkResults(e, o)...)
	}
	// Lint, audit and status are about the store, so they apply to a
	// refused operation too (a refusal must leave no audit row).
	fails = append(fails, r.checkStore(ctx, e, o)...)
	return fails
}

// checkResults compares the step's own output with the expectations.
func (r *Runner) checkResults(e *Expect, o outcome) []string {
	var fails []string
	fail := func(format string, args ...any) { fails = append(fails, fmt.Sprintf(format, args...)) }
	uris := resultURIs(o.out)
	if len(e.Top) > 0 {
		for i, want := range e.Top {
			w, err := r.resolveString(want)
			if err != nil {
				fail("%v", err)
				continue
			}
			if i >= len(uris) {
				fail("top[%d]: want %s, only %d results", i, w, len(uris))
				continue
			}
			if !uris[i][w] {
				fail("top[%d]: want %s, got %s", i, w, keys(uris[i]))
			}
		}
	}
	for _, want := range e.Contains {
		w, err := r.resolveString(want)
		if err != nil {
			fail("%v", err)
			continue
		}
		if !anyHas(uris, w) {
			fail("results lack %s (got %s)", w, allKeys(uris))
		}
	}
	for _, want := range e.Excludes {
		w, err := r.resolveString(want)
		if err != nil {
			fail("%v", err)
			continue
		}
		if anyHas(uris, w) {
			fail("results must not contain %s", w)
		}
	}
	if e.Abstains != nil {
		results, _ := lookup(o.out, "results").([]any)
		reason, _ := lookup(o.out, "reason").(string)
		if *e.Abstains && (len(results) != 0 || reason == "") {
			fail("expected abstention (zero results with a reason); got %d results, reason %q", len(results), reason)
		}
		if !*e.Abstains && len(results) == 0 {
			fail("expected results, got an abstention (%s)", reason)
		}
	}
	if e.Count != nil {
		if n := len(uris); n != *e.Count {
			fail("want %d results, got %d", *e.Count, n)
		}
	}
	for _, path := range sortedKeysOf(e.Out) {
		want, err := r.resolveAny(e.Out[path])
		if err != nil {
			fail("%v", err)
			continue
		}
		got := lookup(o.out, path)
		if !sameValue(want, got) {
			fail("out.%s: want %v, got %v", path, want, got)
		}
	}
	for _, path := range sortedKeysOfS(e.OutContains) {
		want, err := r.resolveString(e.OutContains[path])
		if err != nil {
			fail("%v", err)
			continue
		}
		got, _ := lookup(o.out, path).(string)
		if !strings.Contains(got, want) {
			fail("out.%s: %q does not contain %q", path, got, want)
		}
	}
	return fails
}

// checkStore checks lint, audit and status expectations against the store.
func (r *Runner) checkStore(ctx context.Context, e *Expect, o outcome) []string {
	var fails []string
	fail := func(format string, args ...any) { fails = append(fails, fmt.Sprintf(format, args...)) }
	if e.Lint != nil {
		ns := e.Lint.Namespace
		if ns == "" {
			ns, _ = o.args["namespace"].(string)
		}
		findings, err := compact.Lint(ctx, r.store, ns, r.now)
		if err != nil {
			fail("lint: %v", err)
		} else {
			for _, h := range e.Lint.Has {
				if ok, err := r.lintHas(findings, h); err != nil {
					fail("%v", err)
				} else if !ok {
					fail("lint lacks %s (findings: %s)", h, lintKinds(findings))
				}
			}
			for _, h := range e.Lint.HasNot {
				if ok, err := r.lintHas(findings, h); err != nil {
					fail("%v", err)
				} else if ok {
					fail("lint must not report %s", h)
				}
			}
		}
	}
	if e.AuditNone && len(o.audit) > 0 {
		fail("expected no audit rows, got %s", auditLines(o.audit))
	}
	for _, want := range e.Audit {
		target, err := r.resolveString(want.Target)
		if err != nil {
			fail("%v", err)
			continue
		}
		found := false
		for _, row := range o.audit {
			if row.Op == want.Op && (want.Actor == "" || row.Actor == want.Actor) && (want.Channel == "" || row.Channel == want.Channel) && (target == "" || row.Target == target) {
				found = true
				break
			}
		}
		if !found {
			fail("audit lacks {op %s actor %q channel %q target %q}; rows: %s", want.Op, want.Actor, want.Channel, target, auditLines(o.audit))
		}
	}
	if len(e.Status) > 0 {
		m, err := r.statusMap(ctx)
		if err != nil {
			fail("status: %v", err)
		} else {
			for _, k := range sortedKeysOf(e.Status) {
				if got := lookup(m, k); !sameValue(e.Status[k], got) {
					fail("status.%s: want %v, got %v", k, e.Status[k], got)
				}
			}
		}
	}
	return fails
}

func (r *Runner) lintHas(findings []compact.Finding, spec string) (bool, error) {
	kind, ref, _ := strings.Cut(spec, ":")
	uri := ""
	if ref != "" {
		u, err := r.resolveString(ref)
		if err != nil {
			return false, err
		}
		uri = u
	}
	for _, f := range findings {
		if f.Kind != kind {
			continue
		}
		if uri == "" || f.URI == uri || strings.Contains(f.Message, uri) {
			return true, nil
		}
	}
	return false, nil
}

// --- binds and references ---

func (r *Runner) bindOf(ctx context.Context, st Step, out map[string]any) (map[string]string, error) {
	b := map[string]string{}
	switch st.Op {
	case "ingest":
		uri, _ := out["uri"].(string)
		b[""] = uri
		_, id, err := kb.ParseURI(uri)
		if err != nil {
			return nil, err
		}
		b["id"] = id
		var chunkID int64
		if err := r.db.QueryRowContext(ctx, `SELECT id FROM chunks WHERE document_id = ? ORDER BY ord LIMIT 1`, id).Scan(&chunkID); err == nil {
			b["chunk"] = fmt.Sprintf("memo://chunk/%d", chunkID)
		}
	case "remember":
		b[""], _ = out["uri"].(string)
	case "submit":
		b[""], _ = out["page_uri"].(string)
	case "compact":
		items, _ := out["items"].([]any)
		var chosen map[string]any
		for _, it := range items {
			m, _ := it.(map[string]any)
			kind, _ := m["kind"].(string)
			if k := st.Select["kind"]; k != "" && k != kind {
				continue
			}
			if s := st.Select["subject"]; s != "" && !itemIsAbout(m, s) {
				continue
			}
			chosen = m
			break
		}
		if chosen == nil {
			return nil, fmt.Errorf("no work item matches select %v among %d items", st.Select, len(items))
		}
		b[""], _ = chosen["id"].(string)
		b["kind"], _ = chosen["kind"].(string)
		if p, ok := chosen["payload"].(map[string]any); ok {
			if a, ok := lookup(p, "a.uri").(string); ok {
				b["a"] = a
			}
			if bb, ok := lookup(p, "b.uri").(string); ok {
				b["b"] = bb
			}
		}
	default:
		if uri, ok := out["uri"].(string); ok {
			b[""] = uri
		} else {
			return nil, fmt.Errorf("op %s produces nothing to bind", st.Op)
		}
	}
	if b[""] == "" {
		return nil, fmt.Errorf("op %s produced no address to bind", st.Op)
	}
	return b, nil
}

// itemIsAbout reports whether a work item's subject, or the names in its
// payload (the entity of a page item, the subject of a conflict, the two
// names of a merge), contains s (case-insensitive). Subjects themselves are
// ids, so the names are what a scenario author selects by.
func itemIsAbout(item map[string]any, s string) bool {
	s = strings.ToLower(s)
	candidates := []string{str(item, "subject")}
	for _, path := range []string{"payload.entity.canonical", "payload.subject.canonical", "payload.candidate.a.canonical", "payload.candidate.b.canonical", "payload.a.statement", "payload.b.statement"} {
		if v, ok := lookup(item, path).(string); ok {
			candidates = append(candidates, v)
		}
	}
	for _, c := range candidates {
		if c != "" && strings.Contains(strings.ToLower(c), s) {
			return true
		}
	}
	return false
}

func (r *Runner) resolveArgs(args map[string]any) (map[string]any, error) {
	if args == nil {
		return map[string]any{}, nil
	}
	v, err := r.resolveAny(args)
	if err != nil {
		return nil, err
	}
	return v.(map[string]any), nil
}

func (r *Runner) resolveAny(v any) (any, error) {
	switch x := v.(type) {
	case string:
		return r.resolveString(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			rv, err := r.resolveAny(e)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			rv, err := r.resolveAny(e)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	}
	return v, nil
}

func (r *Runner) resolveString(s string) (string, error) {
	var firstErr error
	out := refRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := refRe.FindStringSubmatch(m)
		b, ok := r.binds[sub[1]]
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("$%s is not bound", sub[1])
			}
			return m
		}
		v, ok := b[sub[2]]
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("$%s has no field %q (has %s)", sub[1], sub[2], keys(toSet(b)))
			}
			return m
		}
		return v
	})
	return out, firstErr
}

// --- audit ---

func (r *Runner) maxAuditID(ctx context.Context) (int64, error) {
	var n sql.NullInt64
	if err := r.db.QueryRowContext(ctx, `SELECT MAX(id) FROM audit`).Scan(&n); err != nil {
		return 0, err
	}
	return n.Int64, nil
}

// auditSince returns the rows written since the last call and advances the
// cursor.
func (r *Runner) auditSince(ctx context.Context) ([]auditRow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, actor, channel, op, target_uri FROM audit WHERE id > ? ORDER BY id`, r.lastAudit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var a auditRow
		if err := rows.Scan(&a.ID, &a.Actor, &a.Channel, &a.Op, &a.Target); err != nil {
			return nil, err
		}
		out = append(out, a)
		r.lastAudit = a.ID
	}
	return out, rows.Err()
}

func auditLines(rows []auditRow) string {
	if len(rows) == 0 {
		return "(none)"
	}
	var parts []string
	for _, a := range rows {
		parts = append(parts, fmt.Sprintf("{%s %s %s %s}", a.Op, a.Actor, a.Channel, a.Target))
	}
	return strings.Join(parts, " ")
}

// --- human operations (the CLI channel) ---

func (r *Runner) humanOp(ctx context.Context, name string, a Actor, op string, args map[string]any) (map[string]any, error) {
	actor := a.Client
	if actor == "" {
		actor = name
	}
	ch := kb.ChannelCLI
	switch op {
	case "ingest":
		in := kb.IngestInput{Namespace: str(args, "namespace"), Content: str(args, "content"), Context: str(args, "context"), DocumentID: str(args, "document"), Trust: str(args, "trust"), Actor: actor, Channel: ch}
		if in.Namespace == "" {
			in.Namespace = "default"
		}
		if in.Trust == "" {
			in.Trust = kb.TrustUser
		}
		src, _ := args["source"].(map[string]any)
		in.Source = kb.SourceInput{URI: str(src, "uri"), Title: str(src, "title"), Kind: str(src, "kind"), Library: str(src, "library"), Version: str(src, "version"), Origin: str(src, "origin"), Tags: strs(src, "tags")}
		if in.Source.Kind == "" {
			in.Source.Kind = kb.KindDoc
		}
		if in.Source.Origin == "" {
			in.Source.Origin = kb.OriginUserSaid
			if in.Source.URI != "" {
				in.Source.Origin = kb.OriginWeb
			}
		}
		for _, e := range strs(args, "entities") {
			in.Entities = append(in.Entities, kb.EntityInput{Name: e, Type: "name"})
		}
		res, err := r.store.Ingest(ctx, in)
		if err != nil {
			return nil, err
		}
		return toMap(map[string]any{"uri": res.URI, "revision": res.Revision, "unchanged": res.Dedup, "chunks": res.Chunks, "embedded": res.Embedded, "pending": res.Pending, "trust": in.Trust})
	case "remember":
		in := kb.RememberInput{Namespace: str(args, "namespace"), Statement: str(args, "statement"), About: strs(args, "about"), Supersedes: str(args, "supersedes"), EvidenceURI: str(args, "evidence_uri"), Origin: str(args, "origin"), Trust: str(args, "trust"), Actor: actor, Channel: ch}
		if in.Namespace == "" {
			in.Namespace = "default"
		}
		if in.Trust == "" {
			in.Trust = kb.TrustUser
		}
		if in.Origin == "" {
			in.Origin = kb.OriginUserSaid
		}
		var err error
		if in.ValidFrom, err = timeArg(args, "valid_from"); err != nil {
			return nil, err
		}
		if in.ValidTo, err = timeArg(args, "valid_to"); err != nil {
			return nil, err
		}
		f, err := r.store.Remember(ctx, in)
		if err != nil {
			return nil, err
		}
		return toMap(map[string]any{"uri": f.URI, "superseded": in.Supersedes, "trust": f.Trust})
	case "forget":
		in := kb.ForgetInput{URI: str(args, "uri"), Reason: str(args, "reason"), Redact: boolArg(args, "redact"), Actor: actor, Channel: ch}
		if err := r.store.Forget(ctx, in); err != nil {
			return nil, err
		}
		return map[string]any{"uri": in.URI, "forgotten": true, "redacted": in.Redact}, nil
	case "promote", "demote":
		uri, to := str(args, "uri"), str(args, "to")
		if err := r.store.SetTrust(ctx, uri, to, actor, ch); err != nil {
			return nil, err
		}
		return map[string]any{"uri": uri, "trust": to}, nil
	case "merge", "reject":
		id, err := r.mergeCandidate(ctx, args)
		if err != nil {
			return nil, err
		}
		if err := r.store.DecideMerge(ctx, id, op == "merge", actor, ch); err != nil {
			return nil, err
		}
		return map[string]any{"id": strconv.FormatInt(id, 10), "accepted": op == "merge"}, nil
	case "compact":
		items, err := compact.Generate(ctx, r.store, str(args, "namespace"), strs(args, "kinds"))
		if err != nil {
			return nil, err
		}
		var list []any
		for _, w := range items {
			var payload any
			_ = json.Unmarshal(w.Payload, &payload)
			list = append(list, map[string]any{"id": w.ID, "namespace": w.Namespace, "kind": w.Kind, "subject": w.Subject, "state": w.State, "payload": payload})
		}
		var open int
		_ = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_items WHERE state = 'open'`).Scan(&open)
		return toMap(map[string]any{"items": list, "open": open})
	case "submit":
		res := compact.Result{Title: str(args, "title"), Content: str(args, "content"), Keep: str(args, "keep"), Reason: str(args, "reason"), Skip: boolArg(args, "skip")}
		if v, ok := args["accept"].(bool); ok {
			res.Accept = &v
		}
		rep, err := compact.Submit(ctx, r.store, str(args, "item_id"), res, boolArg(args, "dry_run"), actor, ch)
		if err != nil {
			return nil, err
		}
		return toMap(rep)
	case "lint":
		findings, err := compact.Lint(ctx, r.store, str(args, "namespace"), r.now)
		if err != nil {
			return nil, err
		}
		return toMap(map[string]any{"findings": findings})
	case "verify":
		rep, err := r.store.Verify(ctx, boolArg(args, "repair"))
		if err != nil {
			return nil, err
		}
		return toMap(map[string]any{"problems": rep.Problems, "repaired": rep.Repaired, "clean": rep.Clean()})
	case "backfill":
		n, err := r.store.Backfill(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"embedded": float64(n)}, nil
	case "status":
		return r.statusMap(ctx)
	case "search":
		req := retrieve.Request{Query: str(args, "query"), Granularity: str(args, "granularity"), Mode: str(args, "mode"), ResponseFormat: retrieve.FormatExplain, Limit: 10, MaxTokens: 1 << 20, ExcludeIDs: strs(args, "exclude_ids")}
		if req.Granularity == "" {
			req.Granularity = retrieve.GranularityDocument
		}
		if n, ok := numArg(args, "limit"); ok {
			req.Limit = n
		}
		scope, _ := args["scope"].(map[string]any)
		req.Scope = retrieve.Scope{Namespaces: strs(scope, "namespaces"), Kinds: strs(scope, "kinds"), Library: str(scope, "library"), Version: str(scope, "version"), Tags: strs(scope, "tags"), MinTrust: str(scope, "min_trust")}
		if t, err := timeArg(args, "as_of"); err != nil {
			return nil, err
		} else if t != nil {
			req.AsOf = t
		}
		resp, err := r.svc.Search(ctx, req)
		if err != nil {
			return nil, err
		}
		return toMap(resp)
	case "read":
		uri := str(args, "uri")
		kind, id, err := kb.ParseURI(uri)
		if err != nil {
			return nil, err
		}
		if kind == "fact" {
			f, err := r.store.ReadFact(ctx, id)
			if fg := (*kb.Forgotten)(nil); errors.As(err, &fg) {
				return map[string]any{"uri": uri, "tombstone": true, "reason": fg.Reason, "forgotten_at": fg.At.Format(time.RFC3339)}, nil
			}
			if err != nil {
				return nil, err
			}
			m, err := toMap(f)
			if err != nil {
				return nil, err
			}
			m["tombstone"] = false
			return m, nil
		}
		text, prov, err := r.store.Read(ctx, uri)
		if fg := (*kb.Forgotten)(nil); errors.As(err, &fg) {
			return map[string]any{"uri": uri, "tombstone": true, "reason": fg.Reason, "forgotten_at": fg.At.Format(time.RFC3339)}, nil
		}
		if err != nil {
			return nil, err
		}
		return toMap(map[string]any{"uri": uri, "content": text, "provenance": prov, "tombstone": false})
	case "history":
		entries, err := r.store.History(ctx, str(args, "uri"))
		if err != nil {
			return nil, err
		}
		return toMap(map[string]any{"entries": entries})
	case "facts":
		f := kb.FactFilter{Namespace: str(args, "namespace"), IncludeHistory: boolArg(args, "history"), Limit: 1 << 20}
		if t, err := timeArg(args, "as_of"); err != nil {
			return nil, err
		} else if t != nil {
			f.AsOf = t
		}
		facts, err := r.store.ListFacts(ctx, f)
		if err != nil {
			return nil, err
		}
		return toMap(map[string]any{"facts": facts})
	}
	return nil, fmt.Errorf("unknown human op %q", op)
}

func (r *Runner) mergeCandidate(ctx context.Context, args map[string]any) (int64, error) {
	if id := str(args, "id"); id != "" {
		return strconv.ParseInt(id, 10, 64)
	}
	a, b := strings.ToLower(str(args, "a")), strings.ToLower(str(args, "b"))
	cands, err := r.store.MergeCandidates(ctx, "open")
	if err != nil {
		return 0, err
	}
	for _, c := range cands {
		ca, cb := strings.ToLower(c.A.Canonical), strings.ToLower(c.B.Canonical)
		if (ca == a && cb == b) || (ca == b && cb == a) {
			return c.ID, nil
		}
	}
	return 0, fmt.Errorf("no open merge candidate for %q and %q", a, b)
}

func (r *Runner) statusMap(ctx context.Context) (map[string]any, error) {
	st, err := r.store.Status(ctx)
	if err != nil {
		return nil, err
	}
	return toMap(map[string]any{"schema_version": st.SchemaVersion, "sources": st.Sources, "live_documents": st.LiveDocuments, "revisions": st.Revisions, "chunks": st.Chunks, "facts": st.Facts,
		"default_model": st.DefaultModel, "pending_embeddings": st.PendingEmbeddings, "jobs_queued": st.JobsQueued, "jobs_failed": st.JobsFailed, "graph": st.Graph, "pages": st.Pages, "namespaces": st.Namespaces})
}

// --- small helpers ---

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	switch v := m[k].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func strs(m map[string]any, k string) []string {
	if m == nil {
		return nil
	}
	switch v := m[k].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case []string:
		return v
	case string:
		return []string{v}
	}
	return nil
}

func boolArg(m map[string]any, k string) bool {
	if m == nil {
		return false
	}
	b, _ := m[k].(bool)
	return b
}

func numArg(m map[string]any, k string) (int, bool) {
	if m == nil {
		return 0, false
	}
	switch v := m[k].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

func timeArg(m map[string]any, k string) (*time.Time, error) {
	s := str(m, k)
	if s == "" {
		return nil, nil
	}
	t, err := parseTime(s)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", k, err)
	}
	return &t, nil
}

// toMap round-trips a value through JSON so every output has one shape
// (string keys, float64 numbers) whatever produced it.
func toMap(v any) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// lookup follows a dotted path through maps and lists (results.0.uri).
func lookup(v any, path string) any {
	if path == "" {
		return v
	}
	for _, seg := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			v = x[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

// resultURIs lists, per result in order, the addresses that identify it:
// uri, document_uri and chunk_uri of search results, the uri of facts and
// history entries, work item ids, passage addresses from explore.
func resultURIs(out map[string]any) []map[string]bool {
	var list []any
	for _, k := range []string{"results", "facts", "entries", "items", "findings"} {
		if l, ok := out[k].([]any); ok {
			list = l
			break
		}
	}
	if l, ok := out["chunks"].([]any); ok && list == nil {
		list = l
	}
	var uris []map[string]bool
	for _, it := range list {
		m := map[string]bool{}
		switch x := it.(type) {
		case string:
			m[x] = true
		case map[string]any:
			for _, k := range []string{"uri", "document_uri", "chunk_uri", "id"} {
				if s, ok := x[k].(string); ok && s != "" {
					m[s] = true
				}
			}
		}
		uris = append(uris, m)
	}
	return uris
}

func anyHas(uris []map[string]bool, want string) bool {
	for _, m := range uris {
		if m[want] {
			return true
		}
	}
	return false
}

func keys(m map[string]bool) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, "|")
}

func allKeys(uris []map[string]bool) string {
	var parts []string
	for _, m := range uris {
		parts = append(parts, keys(m))
	}
	if len(parts) == 0 {
		return "(no results)"
	}
	return strings.Join(parts, ", ")
}

func toSet(m map[string]string) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func sortedKeysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func sortedKeysOfS(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// sameValue compares an expected YAML value with a JSON-decoded one.
func sameValue(want, got any) bool {
	if want == nil {
		return got == nil
	}
	if wf, ok := asFloat(want); ok {
		gf, ok := asFloat(got)
		return ok && wf == gf
	}
	switch w := want.(type) {
	case bool:
		g, ok := got.(bool)
		return ok && g == w
	case string:
		g, ok := got.(string)
		return ok && g == w
	}
	return fmt.Sprint(want) == fmt.Sprint(got)
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func lintKinds(f []compact.Finding) string {
	if len(f) == 0 {
		return "(none)"
	}
	var parts []string
	for _, x := range f {
		parts = append(parts, x.Kind+" "+x.URI)
	}
	return strings.Join(parts, ", ")
}

func summarize(op string, o outcome) string {
	if o.err != nil {
		return "error: " + truncate(o.err.Error(), 80)
	}
	switch op {
	case "search", "facts", "history":
		return fmt.Sprintf("%d results", len(resultURIs(o.out)))
	case "ingest", "remember", "submit", "promote", "demote", "forget":
		if uri, ok := o.out["uri"].(string); ok {
			return uri
		}
		if uri, ok := o.out["page_uri"].(string); ok {
			return uri
		}
		if applied, ok := o.out["applied"].(bool); ok {
			return fmt.Sprintf("applied=%v", applied)
		}
	case "compact":
		return fmt.Sprintf("%d items", len(resultURIs(o.out)))
	case "lint":
		return fmt.Sprintf("%d findings", len(resultURIs(o.out)))
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
