package scenario

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/kKEo/memors/internal/retrieve"
)

// An invariant is a property of the store that must hold after every step,
// whatever the scenario did. A scenario or a step may skip one by name when
// it documents a known gap (decision: open).
type invariant struct {
	name  string
	check func(ctx context.Context, r *Runner, st Step, o *outcome) error
}

// Invariants is the pack, in the order it runs. Names are stable: they are
// what `invariants: {skip: [...]}` refers to.
var Invariants = []invariant{
	{"one-live-revision", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		return countIsZero(ctx, r.db, `SELECT COUNT(*) FROM (SELECT source_id FROM documents WHERE deleted_at IS NULL AND superseded_by IS NULL GROUP BY source_id HAVING COUNT(*) > 1)`, "sources with more than one live revision")
	}},
	{"contiguous-revisions", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		return countIsZero(ctx, r.db, `SELECT COUNT(*) FROM (SELECT source_id FROM documents GROUP BY source_id HAVING MAX(revision) != COUNT(*) OR MIN(revision) != 1)`, "sources whose revision numbers are not 1..n")
	}},
	{"acyclic-supersession", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		rows, err := r.db.QueryContext(ctx, `SELECT id, superseded_by FROM facts WHERE superseded_by IS NOT NULL`)
		if err != nil {
			return err
		}
		defer rows.Close()
		next := map[string]string{}
		for rows.Next() {
			var id, by string
			if err := rows.Scan(&id, &by); err != nil {
				return err
			}
			next[id] = by
		}
		for start := range next {
			seen := map[string]bool{start: true}
			for cur := next[start]; cur != ""; cur = next[cur] {
				if seen[cur] {
					return fmt.Errorf("fact supersession cycle through memo://fact/%s", cur)
				}
				seen[cur] = true
			}
		}
		return rows.Err()
	}},
	{"invalidated-has-successor", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		return countIsZero(ctx, r.db, `SELECT COUNT(*) FROM facts WHERE invalidated_at IS NOT NULL AND superseded_by IS NULL`, "invalidated facts with no successor recorded")
	}},
	{"no-chunks-for-forgotten", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		return countIsZero(ctx, r.db, `SELECT COUNT(*) FROM chunks c JOIN documents d ON d.id = c.document_id WHERE d.deleted_at IS NOT NULL`, "chunks of forgotten documents still indexed")
	}},
	{"forgotten-never-served", forgottenNeverServed},
	{"tool-channel-cap", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		// A tool call never raises or lowers trust, and never retires or
		// supersedes a record above its cap (agent).
		return countIsZero(ctx, r.db, `SELECT COUNT(*) FROM audit a WHERE a.channel = 'tool' AND (
			a.op IN ('promote', 'demote')
			OR (a.op IN ('forget', 'redact', 'supersede', 'resolve_conflict') AND (
				EXISTS (SELECT 1 FROM facts f WHERE 'memo://fact/' || f.id = a.target_uri AND f.trust IN ('user', 'curated'))
				OR EXISTS (SELECT 1 FROM documents d JOIN sources s ON s.id = d.source_id WHERE 'memo://doc/' || d.id = a.target_uri AND s.trust IN ('user', 'curated'))
				OR EXISTS (SELECT 1 FROM pages p WHERE 'memo://page/' || p.id = a.target_uri AND p.trust IN ('user', 'curated')))))`,
			"audit rows where a tool call acted above the agent trust cap")
	}},
	{"audit-per-mutation", func(_ context.Context, _ *Runner, st Step, o *outcome) error {
		if o == nil || o.err != nil || len(o.audit) > 0 {
			return nil
		}
		switch st.Op {
		case "ingest":
			if unchanged, _ := o.out["unchanged"].(bool); !unchanged {
				return fmt.Errorf("ingest wrote a revision and no audit row")
			}
		case "remember", "forget", "demote", "merge", "reject":
			return fmt.Errorf("%s succeeded and wrote no audit row", st.Op)
		case "promote":
			if applied, _ := o.out["applied"].(bool); applied || o.out["applied"] == nil {
				return fmt.Errorf("promote applied and wrote no audit row")
			}
		case "submit":
			if applied, _ := o.out["applied"].(bool); applied && !boolArg(o.args, "skip") && !boolArg(o.args, "dry_run") {
				return fmt.Errorf("submit applied and wrote no audit row")
			}
		}
		return nil
	}},
	{"raw-unchanged-by-compaction", func(_ context.Context, _ *Runner, st Step, o *outcome) error {
		if o == nil || (st.Op != "compact" && st.Op != "submit") {
			return nil
		}
		if o.before != "" && o.after != "" && o.before != o.after {
			return fmt.Errorf("%s changed a raw source, document or chunk row", st.Op)
		}
		return nil
	}},
	{"page-sources-exist", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		return countIsZero(ctx, r.db, `SELECT COUNT(*) FROM page_sources ps LEFT JOIN chunks c ON c.id = ps.chunk_id WHERE c.id IS NULL`, "page sources pointing at missing chunks")
	}},
	{"stale-consistent", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		return countIsZero(ctx, r.db, `SELECT COUNT(*) FROM pages p WHERE p.deleted_at IS NULL AND p.stale = 0 AND EXISTS (
			SELECT 1 FROM page_sources ps JOIN chunks c ON c.id = ps.chunk_id JOIN documents d ON d.id = c.document_id
			WHERE ps.page_id = p.id AND (d.superseded_by IS NOT NULL OR d.deleted_at IS NOT NULL))`, "pages not marked stale although a source revision is gone")
	}},
	{"one-open-item-per-subject", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		return countIsZero(ctx, r.db, `SELECT COUNT(*) FROM (SELECT kind, subject FROM work_items WHERE state = 'open' GROUP BY kind, subject HAVING COUNT(*) > 1)`, "subjects with more than one open work item of a kind")
	}},
	{"mentions-live", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		return countIsZero(ctx, r.db, `SELECT COUNT(*) FROM mentions m LEFT JOIN chunks c ON c.id = m.chunk_id WHERE c.id IS NULL`, "mentions pointing at missing chunks")
	}},
	{"status-matches-sql", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		st, err := r.store.Status(ctx)
		if err != nil {
			return err
		}
		var live, chunks int
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents WHERE deleted_at IS NULL AND superseded_by IS NULL`).Scan(&live); err != nil {
			return err
		}
		if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&chunks); err != nil {
			return err
		}
		if st.LiveDocuments != live || st.Chunks != chunks {
			return fmt.Errorf("status says %d live documents and %d chunks; SQL says %d and %d", st.LiveDocuments, st.Chunks, live, chunks)
		}
		return nil
	}},
	{"verify-clean", func(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
		rep, err := r.store.Verify(ctx, false)
		if err != nil {
			return err
		}
		if !rep.Clean() {
			return fmt.Errorf("verify: %s", strings.Join(rep.Problems, "; "))
		}
		return nil
	}},
}

// InvariantNames lists the pack for documentation and validation.
func InvariantNames() []string {
	out := make([]string, 0, len(Invariants))
	for _, i := range Invariants {
		out = append(out, i.name)
	}
	return out
}

func countIsZero(ctx context.Context, db *sql.DB, q, what string) error {
	var n int
	if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("%d %s", n, what)
	}
	return nil
}

// forgottenNeverServed searches for every forgotten fact and document by its
// own text and title, live and under as_of, and fails if any comes back.
func forgottenNeverServed(ctx context.Context, r *Runner, _ Step, _ *outcome) error {
	type rec struct{ uri, query, gran string }
	var recs []rec
	facts, err := pairs(ctx, r.db, `SELECT id, statement FROM facts WHERE deleted_at IS NOT NULL AND statement != '[redacted]'`)
	if err != nil {
		return err
	}
	for _, p := range facts {
		recs = append(recs, rec{"memo://fact/" + p[0], p[1], retrieve.GranularityFact})
	}
	docs, err := pairs(ctx, r.db, `SELECT d.id, s.title FROM documents d JOIN sources s ON s.id = d.source_id WHERE d.deleted_at IS NOT NULL`)
	if err != nil {
		return err
	}
	for _, p := range docs {
		recs = append(recs, rec{"memo://doc/" + p[0], p[1], retrieve.GranularityDocument})
	}
	now := r.now
	for _, rc := range recs {
		if strings.TrimSpace(rc.query) == "" {
			continue
		}
		for _, asOf := range []*time.Time{nil, &now} {
			resp, err := r.svc.Search(ctx, retrieve.Request{Query: rc.query, Granularity: rc.gran, Limit: 50, MaxTokens: 1 << 20, AsOf: asOf})
			if err != nil {
				return err
			}
			for _, res := range resp.Results {
				if res.URI == rc.uri || res.DocumentURI == rc.uri {
					return fmt.Errorf("forgotten %s is still served (as_of=%v)", rc.uri, asOf != nil)
				}
			}
		}
	}
	return nil
}

// pairs runs a two-column query and returns its rows.
func pairs(ctx context.Context, db *sql.DB, q string) ([][2]string, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// checkInvariants runs the pack after a step, honouring scenario- and
// step-level skips, and returns one line per failure.
func (r *Runner) checkInvariants(ctx context.Context, st Step, o *outcome) []string {
	skip := map[string]bool{}
	if r.sc.Invariants != nil {
		for _, n := range r.sc.Invariants.Skip {
			skip[n] = true
		}
	}
	if st.Invariants != nil {
		for _, n := range st.Invariants.Skip {
			skip[n] = true
		}
	}
	var fails []string
	for _, inv := range Invariants {
		if skip[inv.name] {
			continue
		}
		if err := inv.check(ctx, r, st, o); err != nil {
			fails = append(fails, inv.name+": "+err.Error())
		}
	}
	return fails
}
