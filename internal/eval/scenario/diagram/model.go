// Package diagram draws a scenario as an SVG swimlane picture: one lane per
// actor, time flowing down, one box per step with its arguments and
// expectation chips, bind badges with edges to the steps that use them, and
// an optional overlay of a run's outcome. Build lays the picture out;
// WriteSVG renders it. Output is deterministic for a given scenario and
// options, so it can be golden-tested.
package diagram

import (
	"strings"
	"time"

	"github.com/kKEo/memors/internal/eval/scenario"
)

// Mode says where the SVG will live.
type Mode int

const (
	// Standalone is a file opened on its own: it carries its own <style>, a
	// tooltip per truncated block, popovers with the full text and a small
	// script that shows them on click.
	Standalone Mode = iota
	// Inline is an SVG embedded in an HTML page under a strict content
	// security policy: no <style>, no script, no popovers; "… more" and
	// "less" are links the page's server answers with a re-layout.
	Inline
)

// Expand says which truncated blocks are shown in full.
type Expand struct {
	All bool
	IDs map[string]bool
}

// ParseExpand reads an expand parameter: "all", or ids separated by commas.
func ParseExpand(q string) Expand {
	q = strings.TrimSpace(q)
	if q == "all" {
		return Expand{All: true}
	}
	e := Expand{IDs: map[string]bool{}}
	for _, id := range strings.Split(q, ",") {
		if id = strings.TrimSpace(id); id != "" {
			e.IDs[id] = true
		}
	}
	return e
}

// Has reports whether block id is expanded.
func (e Expand) Has(id string) bool { return e.All || e.IDs[id] }

// Toggle returns the id list with id added or removed, sorted.
func (e Expand) Toggle(id string) []string {
	set := map[string]bool{}
	for k := range e.IDs {
		set[k] = true
	}
	if set[id] {
		delete(set, id)
	} else {
		set[id] = true
	}
	return sortedKeys(set)
}

// Options control one rendering.
type Options struct {
	Mode   Mode
	Expand Expand
	// ExpandHref builds the link target for "… more" / "less" in Inline
	// mode from the ids that should be expanded after the click; nil means
	// no links (the short form only).
	ExpandHref func(ids []string) string
	// Overlay paints a run's outcome onto the steps.
	Overlay *Overlay
	// Issues pins validation problems on the step (or the header) they are
	// about, so an invalid file still renders.
	Issues []scenario.Issue
	// NoEdges leaves the bind edges out.
	NoEdges bool
}

// Diagram is the laid-out picture.
type Diagram struct {
	Title    string
	Desc     string
	Width    int
	Height   int
	Header   Header
	Lanes    []Lane
	Rows     []Row
	Edges    []Edge
	Legend   string
	Popovers []Popover

	laneTop, laneBottom int // the lane band
	rightX              int // where the edge gutter starts
}

// Header is the title block.
type Header struct {
	Name        string
	Description TextBlock
	Decision    string
	Tiers       []string
	Covers      []string
	Skips       []string
	Clock       time.Time
	Steps       int
	Span        time.Duration
	Issues      []scenario.Issue
	Meta        []Chip // decision, tiers, clock, span, covers, skips
	Y, H        int
}

// Lane is one actor's column.
type Lane struct {
	ID, Key, Kind, Client, Elicitation string
	Unknown                            bool // referenced by a step but not declared
	X, W                               int
}

// Row is one top-level step (or a parallel group).
type Row struct {
	ID, Path  string
	Index     int
	At        string
	Clock     time.Time
	ClockJump bool
	Delta     string
	Y, H      int
	Parallel  bool
	Bracket   *Rect
	Note      TextBlock // a parallel group's note
	Skips     []string
	Issues    []scenario.Issue // problems with the group itself
	Boxes     []Box
	Strip     []Counter
}

// Box is one step's card.
type Box struct {
	ID, Path      string
	Lane          int
	Rect          Rect
	Op            string
	Elicit        string // this step's dialog answer, when overridden
	DefaultAnswer string // the actor's default answer, when the op may ask
	Bind          string
	Select        string
	Args          []Field
	Chips         []Chip
	Note          TextBlock
	Issues        []scenario.Issue
	Overlay       *StepOverlay
	lines         []line // laid-out text lines, top to bottom
	chipAt        int    // how many lines precede the chips
	chipsH        int    // height of the chip band
}

// Field is one argument.
type Field struct {
	Key   string
	Value TextBlock
}

// TextBlock is a piece of text that may be shown short or in full.
type TextBlock struct {
	ID, Path  string
	Full      string
	Lines     []string // the lines shown
	Truncated bool     // the full text has more lines than the short form shows
	Expanded  bool
	anchor    Point // where a popover opens
}

// Chip is one expectation.
type Chip struct {
	Kind       string
	Text       string
	Lines      []string // Text wrapped to the chip's width
	X, Y, W, H int
}

// Edge joins a bind badge to the steps that use the bind.
type Edge struct {
	ID      string
	Bind    string
	FromBox string
	Uses    []string
	FromRow int
	ToRow   int
	Slot    int
	From    Point
	X       int // the trunk
	Stubs   []Point
}

// Rect is a box in diagram coordinates.
type Rect struct{ X, Y, W, H int }

// Point is a position in diagram coordinates.
type Point struct{ X, Y int }

// Overlay is a run's outcome keyed by box id (step-3, step-4-2).
type Overlay struct {
	OK    bool
	Steps map[string]StepOverlay
}

// StepOverlay is one step's outcome.
type StepOverlay struct {
	State      string // pass | fail
	Summary    string
	Failures   []string
	Invariants []string
	Binds      map[string]string
	Counters   []Counter
}

// Counter is one status number after a step, with its change.
type Counter struct {
	Key   string
	Value int
	Delta int
}

// Popover is the full text of a truncated block (Standalone mode).
type Popover struct {
	For        string
	Title      string
	Lines      []string
	X, Y, W, H int
}

// line is one laid-out text line inside a box.
type line struct {
	kind  string // op | resolved | field | more | less | select | note | issue | fail
	text  string
	block *TextBlock
	id    string // the block id for more/less
	field int    // index of the field, for ids
}
