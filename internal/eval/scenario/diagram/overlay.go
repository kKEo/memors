package diagram

import (
	"strconv"
	"strings"

	"github.com/kKEo/memors/internal/eval/scenario"
)

// counterKeys are the status counters the state strip shows, in order.
var counterKeys = [][2]string{
	{"live_documents", "docs"}, {"facts", "facts"}, {"chunks", "chunks"},
	{"pages.pages", "pages"}, {"pages.open_work_items", "open items"}, {"graph.entities", "entities"},
}

// OverlayFrom maps a run report onto box ids.
func OverlayFrom(rep *scenario.Report) *Overlay {
	if rep == nil {
		return nil
	}
	o := &Overlay{OK: rep.OK(), Steps: map[string]StepOverlay{}}
	prev := map[string]int{}
	for _, s := range rep.Steps {
		id := "step-" + strconv.Itoa(s.Index)
		so := stepOverlay(s)
		if s.Status != nil {
			so.Counters = counters(s.Status, prev)
		}
		o.Steps[id] = so
		for _, sub := range s.Sub {
			o.Steps[id+"-"+strconv.Itoa(sub.Index)] = stepOverlay(sub)
		}
	}
	return o
}

func stepOverlay(s scenario.StepReport) StepOverlay {
	so := StepOverlay{State: "pass", Summary: s.Summary, Failures: s.Failures, Invariants: s.Invariants, Binds: s.Binds}
	if !s.OK() {
		so.State = "fail"
	}
	return so
}

func counters(st map[string]any, prev map[string]int) []Counter {
	var out []Counter
	for _, k := range counterKeys {
		v, ok := numberAt(st, k[0])
		if !ok {
			continue
		}
		out = append(out, Counter{Key: k[1], Value: v, Delta: v - prev[k[0]]})
		prev[k[0]] = v
	}
	return out
}

// numberAt reads a dotted path out of a decoded status map.
func numberAt(m map[string]any, path string) (int, bool) {
	var cur any = m
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur, ok = mm[p]
		if !ok {
			return 0, false
		}
	}
	switch x := cur.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	}
	return 0, false
}
