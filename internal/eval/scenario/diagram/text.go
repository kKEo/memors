package diagram

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kKEo/memors/internal/eval/scenario"
)

const (
	charW     = 7.2 // estimated width of a 12 px monospace glyph
	chipCharW = 6.6 // 11 px
	sansCharW = 6.4 // 12 px proportional, an estimate for labels
)

// wrap breaks s into lines of at most cols runes, at spaces where it can and
// inside a word when it must. Paragraph breaks are kept; trailing blank
// lines are dropped.
func wrap(s string, cols int) []string {
	if cols < 4 {
		cols = 4
	}
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		para = strings.TrimRight(para, " \t")
		if para == "" {
			out = append(out, "")
			continue
		}
		line := ""
		for _, w := range strings.Fields(para) {
			for utf8.RuneCountInString(w) > cols {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				r := []rune(w)
				out = append(out, string(r[:cols]))
				w = string(r[cols:])
			}
			switch {
			case line == "":
				line = w
			case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) <= cols:
				line += " " + w
			default:
				out = append(out, line)
				line = w
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func textW(s string, cw float64) int { return int(float64(utf8.RuneCountInString(s))*cw + 0.5) }

// clip shortens s to at most cols runes with an ellipsis.
func clip(s string, cols int) string {
	if utf8.RuneCountInString(s) <= cols {
		return s
	}
	r := []rune(s)
	if cols < 2 {
		return "…"
	}
	return string(r[:cols-1]) + "…"
}

var unsafeID = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// ids hands out unique, XML-safe element ids.
type ids struct{ seen map[string]bool }

func (x *ids) get(parts ...string) string {
	id := unsafeID.ReplaceAllString(strings.Join(parts, "-"), "-")
	id = strings.Trim(id, "-")
	if id == "" {
		id = "x"
	}
	if x.seen == nil {
		x.seen = map[string]bool{}
	}
	base := id
	for n := 2; x.seen[id]; n++ {
		id = base + "-" + strconv.Itoa(n)
	}
	x.seen[id] = true
	return id
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sourceOrder sorts the keys of a map by their position in the file when
// known, then by name.
func sourceOrder[V any](m map[string]V, lines map[string]scenario.Pos, path string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		pi, pj := lines[path+"."+keys[i]], lines[path+"."+keys[j]]
		if pi.Line != 0 && pj.Line != 0 && pi != pj {
			if pi.Line != pj.Line {
				return pi.Line < pj.Line
			}
			return pi.Col < pj.Col
		}
		return keys[i] < keys[j]
	})
	return keys
}

// formatValue prints an argument value in YAML flow style, nested keys in
// source order.
func formatValue(v any, lines map[string]scenario.Pos, path string) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = formatValue(e, lines, path+"."+strconv.Itoa(i))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		return "[" + strings.Join(x, ", ") + "]"
	case map[string]any:
		var parts []string
		for _, k := range sourceOrder(x, lines, path) {
			parts = append(parts, k+": "+formatValue(x[k], lines, path+"."+k))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]string:
		var parts []string
		for _, k := range sourceOrder(x, lines, path) {
			parts = append(parts, k+": "+x[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

// chipsFor turns an expectation into chips, in a fixed order.
func chipsFor(e *scenario.Expect, lines map[string]scenario.Pos, path string) []Chip {
	if e == nil {
		return nil
	}
	var out []Chip
	add := func(kind, text string) { out = append(out, Chip{Kind: kind, Text: text}) }
	switch {
	case e.ErrorContains != "":
		add("error", "error ∋ "+strconv.Quote(e.ErrorContains))
	case e.Error != nil && *e.Error:
		add("error", "error")
	case e.Error != nil:
		add("ok", "no error")
	}
	if len(e.Top) > 0 {
		add("top", "top "+strings.Join(e.Top, ", "))
	}
	if len(e.Contains) > 0 {
		add("contains", "contains "+strings.Join(e.Contains, ", "))
	}
	if len(e.Excludes) > 0 {
		add("excludes", "excludes "+strings.Join(e.Excludes, ", "))
	}
	if e.Abstains != nil {
		if *e.Abstains {
			add("abstains", "abstains")
		} else {
			add("ok", "answers")
		}
	}
	if e.Count != nil {
		add("count", "count "+strconv.Itoa(*e.Count))
	}
	for _, k := range sourceOrder(e.Out, lines, path+".out") {
		add("out", "out "+k+" = "+formatValue(e.Out[k], lines, path+".out."+k))
	}
	for _, k := range sourceOrder(e.OutContains, lines, path+".out_contains") {
		add("out", "out "+k+" ∋ "+strconv.Quote(e.OutContains[k]))
	}
	if e.Lint != nil {
		ns := ""
		if e.Lint.Namespace != "" {
			ns = "[" + e.Lint.Namespace + "]"
		}
		for _, k := range e.Lint.Has {
			add("lint", "lint"+ns+" has "+k)
		}
		for _, k := range e.Lint.HasNot {
			add("lint", "lint"+ns+" has_not "+k)
		}
	}
	for _, a := range e.Audit {
		t := "audit " + a.Op
		if a.Actor != "" {
			t += " by " + a.Actor
		}
		if a.Channel != "" {
			t += "/" + a.Channel
		}
		if a.Target != "" {
			t += " → " + a.Target
		}
		add("audit", t)
	}
	if e.AuditNone {
		add("audit_none", "no audit")
	}
	for _, k := range sourceOrder(e.Status, lines, path+".status") {
		add("status", "status "+k+" = "+formatValue(e.Status[k], lines, path+".status."+k))
	}
	return out
}

// fmtDelta prints a clock move as the scenario would (+1d, +3h, +90m, +10s).
func fmtDelta(d time.Duration) string {
	switch {
	case d == 0:
		return ""
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("+%dd", int(d/(24*time.Hour)))
	case d%time.Hour == 0:
		return fmt.Sprintf("+%dh", int(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("+%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("+%ds", int(d/time.Second))
}

// fmtSpan prints a scenario's length in words.
func fmtSpan(d time.Duration) string {
	switch {
	case d == 0:
		return "one moment"
	case d%(24*time.Hour) == 0:
		n := int(d / (24 * time.Hour))
		if n == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", n)
	case d >= time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	}
	return d.String()
}

// Gist is the one-line gist of a step (its statement, query, uri, …) for
// the narration and the tester guide's step tables; path is the step's
// Lines path.
func Gist(st scenario.Step, lines map[string]scenario.Pos, path string) string {
	return stepSummary(st, lines, path)
}

// Span is a scenario's length in words ("2 days") from its first clock to
// its last step.
func Span(sc *scenario.Scenario) string {
	var span time.Duration
	for _, st := range sc.Steps {
		if st.At != "" {
			if d, err := scenario.ParseAt(st.At); err == nil && d > span {
				span = d
			}
		}
	}
	return fmtSpan(span)
}

// ActorOrder lists actor keys the way the picture lays out lanes: agents,
// then humans, then anything else, each sorted.
func ActorOrder(sc *scenario.Scenario) []string { return sortedActorKeys(sc) }

// stepSummary is the one-line gist of a step for the narration.
func stepSummary(st scenario.Step, lines map[string]scenario.Pos, path string) string {
	for _, k := range []string{"statement", "query", "uri", "item_id", "content", "namespace"} {
		if v, ok := st.Args[k]; ok {
			s := formatValue(v, lines, path+".args."+k)
			if k == "content" {
				s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
			}
			return clip(s, 60)
		}
	}
	return ""
}

// Narrate tells the scenario in plain text: the <desc> of the picture, and
// what `memors-scenario describe` prints.
func Narrate(sc *scenario.Scenario) string {
	var sb strings.Builder
	start, _ := sc.ClockStart()
	var span time.Duration
	for _, st := range sc.Steps {
		if st.At != "" {
			if d, err := scenario.ParseAt(st.At); err == nil && d > span {
				span = d
			}
		}
	}
	fmt.Fprintf(&sb, "%s: %d steps over %s from %s", sc.Name, len(sc.Steps), fmtSpan(span), start.Format("2006-01-02"))
	if sc.Decision == "open" {
		sb.WriteString(" (decision open: pins current behaviour)")
	}
	sb.WriteString(". Actors: ")
	var actors []string
	for _, k := range sortedActorKeys(sc) {
		a := sc.Actors[k]
		s := k + " (" + a.Kind
		if a.Client != "" && a.Client != k {
			s += ", " + a.Client
		}
		if a.Elicitation != "" && a.Elicitation != "none" {
			s += ", answers dialogs: " + a.Elicitation
		}
		actors = append(actors, s+")")
	}
	sb.WriteString(strings.Join(actors, ", "))
	sb.WriteString(".")
	if sc.Description != "" {
		sb.WriteString(" " + strings.TrimSpace(sc.Description) + ".")
	}
	var cur time.Duration = -1
	for i, st := range sc.Steps {
		at := ""
		if st.At != "" {
			if d, err := scenario.ParseAt(st.At); err == nil && d != cur {
				cur = d
				at = "[" + st.At + "] "
			}
		}
		if len(st.Parallel) > 0 {
			var subs []string
			for j, sub := range st.Parallel {
				subs = append(subs, strings.TrimSpace(sub.Actor+" "+sub.Op+" "+stepSummary(sub, sc.Lines, scenario.StepPath(i, j))))
			}
			fmt.Fprintf(&sb, " %d. %sin parallel: %s.", i+1, at, strings.Join(subs, "; "))
			continue
		}
		fmt.Fprintf(&sb, " %d. %s%s %s", i+1, at, st.Actor, st.Op)
		if s := stepSummary(st, sc.Lines, scenario.StepPath(i, -1)); s != "" {
			sb.WriteString(" " + s)
		}
		if st.Bind != "" {
			sb.WriteString(" → $" + st.Bind)
		}
		if st.Elicit != "" {
			sb.WriteString(" (dialog: " + st.Elicit + ")")
		}
		switch n := len(chipsFor(st.Expect, sc.Lines, scenario.StepPath(i, -1)+".expect")); {
		case n == 1:
			sb.WriteString("; expects 1 thing")
		case n > 1:
			fmt.Fprintf(&sb, "; expects %d things", n)
		}
		sb.WriteString(".")
	}
	return sb.String()
}

func sortedActorKeys(sc *scenario.Scenario) []string {
	var agents, humans, others []string
	for k, a := range sc.Actors {
		switch a.Kind {
		case "agent":
			agents = append(agents, k)
		case "human":
			humans = append(humans, k)
		default:
			others = append(others, k)
		}
	}
	sort.Strings(agents)
	sort.Strings(humans)
	sort.Strings(others)
	return append(append(agents, humans...), others...)
}
