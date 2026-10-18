package scenario

import (
	"fmt"
	"strings"
	"time"
)

// Report is what a run produced: one row per step with its failures, plus
// the invariants that broke after it.
type Report struct {
	Name     string
	Path     string
	Decision string
	Tiers    []string
	Steps    []StepReport
}

// StepReport is one step's outcome.
type StepReport struct {
	Index int
	// Path is the step's Lines path (steps.2; steps.4.parallel.1 for a
	// sub-step), so a tool can map the outcome back to the source.
	Path       string
	At         string
	Clock      time.Time
	Actor      string
	Op         string
	Note       string
	Summary    string
	Failures   []string
	Invariants []string
	// Binds is what the step bound: "" is the address, then chunk, id, a,
	// b, kind as the op provides them.
	Binds map[string]string
	// Status holds the status counters after the step (Options.Trace only).
	Status map[string]any
	// Out is the step's output, strings capped at 2 KB and lists at 20
	// items (Options.Trace only).
	Out map[string]any
	// Sub holds one report per sub-step of a parallel group; Failures on
	// the group itself carry the aggregate with an actor/op prefix.
	Sub []StepReport
}

// OK reports whether every step met its expectations and every invariant held.
func (r *Report) OK() bool {
	for _, s := range r.Steps {
		if len(s.Failures) > 0 || len(s.Invariants) > 0 {
			return false
		}
	}
	return true
}

// Failures flattens every failure with its step number.
func (r *Report) Failures() []string {
	var out []string
	for _, s := range r.Steps {
		for _, f := range s.Failures {
			out = append(out, fmt.Sprintf("step %d (%s %s): %s", s.Index, s.Actor, s.Op, f))
		}
		for _, f := range s.Invariants {
			out = append(out, fmt.Sprintf("step %d (%s %s): invariant %s", s.Index, s.Actor, s.Op, f))
		}
	}
	return out
}

// OK reports whether the step met its expectations and the invariants held.
func (s StepReport) OK() bool { return len(s.Failures) == 0 && len(s.Invariants) == 0 }

// Line is the one-line log form of a step.
func (s StepReport) Line() string {
	state := "ok"
	if !s.OK() {
		state = "FAIL"
	}
	line := fmt.Sprintf("%2d %-4s %-6s %-10s %-8s %s", s.Index, s.At, s.Actor, s.Op, state, s.Summary)
	for _, f := range s.Failures {
		line += "\n     - " + f
	}
	for _, f := range s.Invariants {
		line += "\n     - invariant " + f
	}
	return line
}

// Markdown renders the report as a table for docs/eval.
func (r *Report) Markdown() string {
	var sb strings.Builder
	state := "pass"
	if !r.OK() {
		state = "FAIL"
	}
	decision := r.Decision
	if decision == "" {
		decision = "decided"
	}
	fmt.Fprintf(&sb, "### %s — %s (decision: %s)\n\n", r.Name, state, decision)
	sb.WriteString("| # | at | actor | op | result | failures |\n|---|---|---|---|---|---|\n")
	for _, s := range r.Steps {
		var fails []string
		fails = append(fails, s.Failures...)
		for _, f := range s.Invariants {
			fails = append(fails, "invariant "+f)
		}
		cell := strings.ReplaceAll(strings.Join(fails, "; "), "|", "\\|")
		fmt.Fprintf(&sb, "| %d | %s | %s | %s | %s | %s |\n", s.Index, s.At, s.Actor, s.Op, strings.ReplaceAll(s.Summary, "|", "\\|"), cell)
	}
	return sb.String()
}
