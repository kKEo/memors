package diagram

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kKEo/memors/internal/eval/scenario"
)

const (
	margin     = 12
	laneW      = 250
	laneGap    = 18
	leftW      = 118
	slotW      = 16
	pad        = 8
	lineH      = 16
	opH        = 22
	chipH      = 18
	chipGap    = 4
	chipPadX   = 6
	rowGap     = 14
	jumpH      = 10
	stripH     = 18
	bracketH   = 18
	laneHdrH   = 50
	legendH    = 20
	shortLines = 2
	maxLines   = 80
	popCols    = 72
	popMax     = 60
)

var cols = colsFor(laneW - 2*pad)

// colsFor is how many monospace glyphs fit in px pixels.
func colsFor(px int) int { return int(float64(px) / charW) }

type builder struct {
	sc      *scenario.Scenario
	opts    Options
	d       *Diagram
	ids     ids
	laneIdx map[string]int
	pathID  map[string]string // step path → box id
	clock0  time.Time
	cur     time.Duration
	span    time.Duration
}

// Build lays the scenario out. It never fails: an invalid scenario (an
// unknown actor, a missing op) still gets a picture, with the issues from
// opts.Issues pinned where they belong.
func Build(sc *scenario.Scenario, opts Options) *Diagram {
	b := &builder{sc: sc, opts: opts, d: &Diagram{Title: sc.Name}, laneIdx: map[string]int{}, pathID: map[string]string{}}
	if b.d.Title == "" {
		b.d.Title = "(unnamed scenario)"
	}
	b.clock0, _ = sc.ClockStart()
	for _, st := range sc.Steps {
		if st.At != "" {
			if d, err := scenario.ParseAt(st.At); err == nil && d > b.span {
				b.span = d
			}
		}
	}
	b.lanes()
	b.header()
	for i, st := range sc.Steps {
		b.row(i, st)
	}
	b.place()
	if !opts.NoEdges {
		b.edges()
	} else {
		b.d.Width = b.d.rightX + margin
	}
	b.popovers()
	b.d.Desc = Narrate(sc)
	b.d.Legend = "one box = one step · $name = what the step binds, dashed arrows lead to the steps that use it · chips = expectations · dialog = the client's answer to a trust question · amber = decision open (pins current behaviour) · ⚠ = validation issue · ✗ = failed in the last run"
	return b.d
}

func (b *builder) lanes() {
	keys := sortedActorKeys(b.sc)
	declared := map[string]bool{}
	for _, k := range keys {
		declared[k] = true
	}
	var unknown []string
	seen := map[string]bool{}
	for _, st := range b.sc.Steps {
		subs := []scenario.Step{st}
		if len(st.Parallel) > 0 {
			subs = st.Parallel
		}
		for _, s := range subs {
			if !declared[s.Actor] && !seen[s.Actor] {
				seen[s.Actor] = true
				unknown = append(unknown, s.Actor)
			}
		}
	}
	all := append(keys, unknown...)
	if len(all) == 0 {
		all = []string{"?"}
	}
	for i, k := range all {
		a := b.sc.Actors[k]
		ln := Lane{ID: b.ids.get("lane", k), Key: k, Kind: a.Kind, Client: a.Client, Elicitation: a.Elicitation, Unknown: !declared[k], X: leftW + i*(laneW+laneGap), W: laneW}
		if ln.Key == "" {
			ln.Key = "?"
		}
		if ln.Client == "" {
			ln.Client = ln.Key
		}
		if ln.Kind == "" {
			ln.Kind = "unknown actor"
		}
		if ln.Elicitation == "none" {
			ln.Elicitation = ""
		}
		b.laneIdx[k] = i
		b.d.Lanes = append(b.d.Lanes, ln)
	}
}

func (b *builder) lanesWidth() int {
	n := len(b.d.Lanes)
	return n*laneW + (n-1)*laneGap
}

func (b *builder) header() {
	sc := b.sc
	h := &b.d.Header
	h.Name = b.d.Title
	h.Decision = sc.Decision
	if h.Decision == "" {
		h.Decision = "decided"
	}
	h.Tiers = sc.Tiers
	if len(h.Tiers) == 0 {
		h.Tiers = []string{"ci"}
	}
	h.Covers = sc.Covers
	if sc.Invariants != nil {
		h.Skips = sc.Invariants.Skip
	}
	h.Clock = b.clock0
	h.Steps = len(sc.Steps)
	h.Span = b.span
	descCols := int(float64(b.lanesWidth()+leftW-margin) / sansCharW)
	h.Description = b.block("hdr-description", "description", sc.Description, descCols)
	for _, is := range b.opts.Issues {
		if !strings.HasPrefix(is.Path, "steps.") {
			h.Issues = append(h.Issues, is)
		}
	}
	kind := "meta"
	if h.Decision == "open" {
		kind = "open"
	}
	h.Meta = append(h.Meta, Chip{Kind: kind, Text: "decision: " + h.Decision})
	h.Meta = append(h.Meta, Chip{Kind: "meta", Text: "tiers: " + strings.Join(h.Tiers, ", ")})
	h.Meta = append(h.Meta, Chip{Kind: "meta", Text: "clock: " + h.Clock.Format("2006-01-02")})
	h.Meta = append(h.Meta, Chip{Kind: "meta", Text: fmt.Sprintf("%d steps over %s", h.Steps, fmtSpan(h.Span))})
	if len(h.Covers) > 0 {
		h.Meta = append(h.Meta, Chip{Kind: "meta", Text: "covers: " + strings.Join(h.Covers, ", ")})
	}
	for _, sk := range h.Skips {
		h.Meta = append(h.Meta, Chip{Kind: "excludes", Text: "skips " + sk})
	}
	metaH := flow(h.Meta, b.lanesWidth()+leftW-margin)
	h.H = pad + 20 + len(h.Description.Lines)*lineH + 6 + metaH + len(h.Issues)*lineH + pad
	if b.affordance(&h.Description) != "" {
		h.H += lineH
	}
}

// block prepares a text for display: wrapped, short unless expanded.
func (b *builder) block(id, path, full string, cols int) TextBlock {
	tb := TextBlock{ID: id, Path: path, Full: full}
	all := wrap(full, cols)
	if len(all) > maxLines {
		all = all[:maxLines]
	}
	tb.Truncated = len(all) > shortLines
	tb.Expanded = b.opts.Expand.Has(id)
	if tb.Truncated && !tb.Expanded {
		// The short form shows the first lines that say something.
		for _, l := range all {
			if l != "" {
				tb.Lines = append(tb.Lines, l)
			}
			if len(tb.Lines) == shortLines {
				break
			}
		}
	} else {
		tb.Lines = all
	}
	return tb
}

// affordance says which link a block shows: more, less or nothing.
func (b *builder) affordance(tb *TextBlock) string {
	if !tb.Truncated {
		return ""
	}
	if !tb.Expanded && (b.opts.Mode == Standalone || b.opts.ExpandHref != nil) {
		return "more"
	}
	if tb.Expanded && b.opts.Mode == Inline && b.opts.ExpandHref != nil {
		return "less"
	}
	return ""
}

func (b *builder) row(i int, st scenario.Step) {
	path := scenario.StepPath(i, -1)
	r := Row{ID: b.ids.get("row", strconv.Itoa(i+1)), Path: path, Index: i + 1, At: st.At}
	if st.At != "" {
		if d, err := scenario.ParseAt(st.At); err == nil {
			if i == 0 || d != b.cur {
				r.ClockJump = true
				r.Delta = fmtDelta(d - b.cur)
			}
			b.cur = d
		}
	} else if i == 0 {
		r.ClockJump = true
	}
	r.Clock = b.clock0.Add(b.cur)
	if st.Invariants != nil {
		r.Skips = st.Invariants.Skip
	}
	if len(st.Parallel) > 0 {
		r.Parallel = true
		if st.Note != "" {
			r.Note = b.block(b.ids.get("step", strconv.Itoa(i+1), "note"), path+".note", st.Note, int(float64(b.lanesWidth())/charW))
		}
		for _, is := range b.opts.Issues {
			if is.Path == path || (strings.HasPrefix(is.Path, path+".") && !strings.HasPrefix(is.Path, path+".parallel.")) {
				r.Issues = append(r.Issues, is)
			}
		}
		for j, sub := range st.Parallel {
			r.Boxes = append(r.Boxes, b.box(i, j, sub))
		}
	} else {
		r.Boxes = []Box{b.box(i, -1, st)}
	}
	if b.opts.Overlay != nil {
		if so, ok := b.opts.Overlay.Steps["step-"+strconv.Itoa(i+1)]; ok {
			r.Strip = so.Counters
		}
	}
	b.d.Rows = append(b.d.Rows, r)
}

func (b *builder) box(i, j int, st scenario.Step) Box {
	path := scenario.StepPath(i, j)
	var id string
	if j < 0 {
		id = b.ids.get("step", strconv.Itoa(i+1))
	} else {
		id = b.ids.get("step", strconv.Itoa(i+1), strconv.Itoa(j+1))
	}
	b.pathID[path] = id
	bx := Box{ID: id, Path: path, Op: st.Op, Elicit: st.Elicit, Bind: st.Bind}
	if bx.Op == "" {
		bx.Op = "(no op)"
	}
	lane, ok := b.laneIdx[st.Actor]
	if !ok {
		lane = len(b.d.Lanes) - 1
	}
	bx.Lane = lane
	if a, ok := b.sc.Actors[st.Actor]; ok && a.Kind == "agent" && st.Op == "promote" && st.Elicit == "" && a.Elicitation != "" && a.Elicitation != "none" {
		bx.DefaultAnswer = a.Elicitation
	}
	if len(st.Select) > 0 {
		bx.Select = "select " + formatValue(st.Select, b.sc.Lines, path+".select")
	}
	for _, k := range sourceOrder(st.Args, b.sc.Lines, path+".args") {
		v := formatValue(st.Args[k], b.sc.Lines, path+".args."+k)
		bx.Args = append(bx.Args, Field{Key: k, Value: b.block(b.ids.get(id, "arg", k), path+".args."+k, k+": "+v, cols)})
	}
	bx.Chips = chipsFor(st.Expect, b.sc.Lines, path+".expect")
	if st.Note != "" {
		bx.Note = b.block(b.ids.get(id, "note"), path+".note", st.Note, cols)
	}
	for _, is := range b.opts.Issues {
		if is.Path == path || strings.HasPrefix(is.Path, path+".") {
			if j >= 0 || !strings.HasPrefix(strings.TrimPrefix(is.Path, path+"."), "parallel.") {
				bx.Issues = append(bx.Issues, is)
			}
		}
	}
	if b.opts.Overlay != nil {
		if so, ok := b.opts.Overlay.Steps[id]; ok {
			bx.Overlay = &so
		}
	}
	b.layoutBox(&bx)
	return bx
}

func (b *builder) blockLines(tb *TextBlock, kind string, fi int) []line {
	var ls []line
	for _, t := range tb.Lines {
		ls = append(ls, line{kind: kind, text: t, block: tb, field: fi})
	}
	if a := b.affordance(tb); a != "" {
		ls = append(ls, line{kind: a, block: tb, id: tb.ID, field: fi})
	}
	return ls
}

func (b *builder) layoutBox(bx *Box) {
	ls := []line{{kind: "op"}}
	if bx.Overlay != nil && bx.Bind != "" {
		if addr := bx.Overlay.Binds[""]; addr != "" {
			ls = append(ls, line{kind: "resolved", text: "$" + bx.Bind + " → " + addr})
		}
	}
	if bx.Select != "" {
		ls = append(ls, line{kind: "select", text: bx.Select})
	}
	for fi := range bx.Args {
		ls = append(ls, b.blockLines(&bx.Args[fi].Value, "field", fi)...)
	}
	bx.chipAt = len(ls)
	if bx.Note.Full != "" {
		ls = append(ls, b.blockLines(&bx.Note, "note", -1)...)
	}
	for _, is := range bx.Issues {
		for _, l := range wrap("⚠ "+is.Msg, cols) {
			ls = append(ls, line{kind: "issue", text: l})
		}
	}
	if bx.Overlay != nil {
		for _, f := range bx.Overlay.Failures {
			for _, l := range wrap("✗ "+f, cols) {
				ls = append(ls, line{kind: "fail", text: l})
			}
		}
		for _, f := range bx.Overlay.Invariants {
			for _, l := range wrap("✗ invariant "+f, cols) {
				ls = append(ls, line{kind: "fail", text: l})
			}
		}
	}
	bx.lines = ls
	bx.chipsH = flow(bx.Chips, laneW-2*pad)
	bx.Rect = Rect{W: laneW, H: pad + opH + (len(ls)-1)*lineH + bx.chipsH + pad}
}

// flow lays chips out left to right in rows of width innerW; X and Y are
// relative to the band's top-left corner. A chip wider than the row wraps
// its text (up to three lines). It returns the band's height.
func flow(chips []Chip, innerW int) int {
	maxCols := int(float64(innerW-2*chipPadX) / chipCharW)
	x, y, rowH := 0, 0, 0
	for ci := range chips {
		c := &chips[ci]
		c.Lines = wrap(c.Text, maxCols)
		if len(c.Lines) > 3 {
			c.Lines = c.Lines[:3]
			c.Lines[2] = clip(c.Lines[2]+"…", maxCols)
		}
		if len(c.Lines) == 0 {
			c.Lines = []string{""}
		}
		w := 0
		for _, l := range c.Lines {
			if tw := textW(l, chipCharW); tw > w {
				w = tw
			}
		}
		c.W = w + 2*chipPadX
		c.H = chipH + (len(c.Lines)-1)*13
		if x > 0 && x+c.W > innerW {
			x = 0
			y += rowH + chipGap
			rowH = 0
		}
		c.X, c.Y = x, y
		if c.H > rowH {
			rowH = c.H
		}
		x += c.W + chipGap
	}
	if len(chips) == 0 {
		return 0
	}
	return y + rowH + chipGap
}

func (b *builder) place() {
	d := b.d
	y := margin
	d.Header.Y = y
	y += d.Header.H + pad
	d.laneTop = y
	y += laneHdrH + pad
	for ri := range d.Rows {
		r := &d.Rows[ri]
		if r.ClockJump && ri > 0 {
			y += jumpH
		}
		r.Y = y
		top := y
		if r.Parallel {
			top += bracketH
		}
		laneY := map[int]int{}
		bottom := top
		minX, maxX := -1, -1
		for bi := range r.Boxes {
			bx := &r.Boxes[bi]
			by := top + laneY[bx.Lane]
			bx.Rect.X = d.Lanes[bx.Lane].X
			bx.Rect.Y = by
			laneY[bx.Lane] += bx.Rect.H + pad
			if by+bx.Rect.H > bottom {
				bottom = by + bx.Rect.H
			}
			if minX < 0 || bx.Rect.X < minX {
				minX = bx.Rect.X
			}
			if bx.Rect.X+bx.Rect.W > maxX {
				maxX = bx.Rect.X + bx.Rect.W
			}
			cy := by + pad + opH + (bx.chipAt-1)*lineH
			for ci := range bx.Chips {
				c := &bx.Chips[ci]
				c.X = bx.Rect.X + pad + c.X
				c.Y = cy + c.Y
			}
			for fi := range bx.Args {
				bx.Args[fi].Value.anchor = Point{bx.Rect.X, by + pad + opH}
			}
			bx.Note.anchor = Point{bx.Rect.X, by + pad + opH}
		}
		if r.Parallel {
			r.Bracket = &Rect{X: minX - 4, Y: r.Y, W: maxX - minX + 8, H: bottom + pad - r.Y}
			bottom += pad
			if r.Note.Full != "" {
				r.Note.anchor = Point{minX, bottom}
				bottom += len(r.Note.Lines) * lineH
				if b.affordance(&r.Note) != "" {
					bottom += lineH
				}
			}
			bottom += len(r.Issues) * lineH
		}
		if len(r.Strip) > 0 {
			bottom += stripH
		}
		r.H = bottom - r.Y + rowGap
		y = r.Y + r.H
	}
	d.laneBottom = y
	d.Height = y + pad + legendH + margin
	d.rightX = leftW + b.lanesWidth() + pad
	d.Width = d.rightX + margin
}

func (b *builder) boxRect(id string) (Rect, bool) {
	for _, r := range b.d.Rows {
		for _, bx := range r.Boxes {
			if bx.ID == id {
				return bx.Rect, true
			}
		}
	}
	return Rect{}, false
}

func (b *builder) edges() {
	type def struct {
		box string
		row int
	}
	defs := map[string]def{}
	uses := map[string][]string{}
	useRow := map[string]int{}
	for i, st := range b.sc.Steps {
		subs := []scenario.Step{st}
		par := len(st.Parallel) > 0
		if par {
			subs = st.Parallel
		}
		for j, s := range subs {
			jj := -1
			if par {
				jj = j
			}
			id := b.pathID[scenario.StepPath(i, jj)]
			seen := map[string]bool{}
			for _, ref := range append(scenario.RefsInArgs(s.Args), scenario.RefsInExpect(s.Expect)...) {
				if seen[ref] {
					continue
				}
				seen[ref] = true
				if d, ok := defs[ref]; ok && d.box != id {
					uses[ref] = append(uses[ref], id)
					useRow[ref] = i
				}
			}
		}
		for j, s := range subs {
			if s.Bind != "" {
				jj := -1
				if par {
					jj = j
				}
				defs[s.Bind] = def{b.pathID[scenario.StepPath(i, jj)], i}
			}
		}
	}
	var edges []Edge
	for bind, d := range defs {
		if len(uses[bind]) > 0 {
			edges = append(edges, Edge{Bind: bind, FromBox: d.box, Uses: uses[bind], FromRow: d.row, ToRow: useRow[bind]})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].FromRow != edges[j].FromRow {
			return edges[i].FromRow < edges[j].FromRow
		}
		if edges[i].ToRow != edges[j].ToRow {
			return edges[i].ToRow < edges[j].ToRow
		}
		return edges[i].Bind < edges[j].Bind
	})
	var slotEnd []int
	for ei := range edges {
		e := &edges[ei]
		e.ID = b.ids.get("edge", e.Bind)
		slot := -1
		for k, end := range slotEnd {
			if end < e.FromRow {
				slot = k
				break
			}
		}
		if slot < 0 {
			slotEnd = append(slotEnd, 0)
			slot = len(slotEnd) - 1
		}
		slotEnd[slot] = e.ToRow
		e.Slot = slot
		if rc, ok := b.boxRect(e.FromBox); ok {
			e.From = Point{rc.X + rc.W, rc.Y + pad + opH/2}
		}
		e.X = b.d.rightX + slot*slotW + slotW/2
		for _, u := range e.Uses {
			if rc, ok := b.boxRect(u); ok {
				e.Stubs = append(e.Stubs, Point{rc.X + rc.W, rc.Y + pad + opH/2})
			}
		}
	}
	b.d.Edges = edges
	b.d.Width = b.d.rightX + len(slotEnd)*slotW + margin
}

func (b *builder) popovers() {
	if b.opts.Mode != Standalone {
		return
	}
	d := b.d
	add := func(tb *TextBlock, title string) {
		if !tb.Truncated || tb.Expanded {
			return
		}
		lines := wrap(tb.Full, popCols)
		if len(lines) > popMax {
			lines = append(lines[:popMax], "…")
		}
		w := textW(title, sansCharW) + 40
		for _, l := range lines {
			if tw := textW(l, charW); tw > w {
				w = tw
			}
		}
		w += 2 * pad
		h := 2*pad + lineH + len(lines)*lineH
		x, y := tb.anchor.X, tb.anchor.Y
		if x+w > d.Width-margin {
			x = d.Width - margin - w
		}
		if x < margin {
			x = margin
		}
		if y+h > d.Height-margin {
			y = d.Height - margin - h
		}
		if y < margin {
			y = margin
		}
		d.Popovers = append(d.Popovers, Popover{For: tb.ID, Title: title, Lines: lines, X: x, Y: y, W: w, H: h})
	}
	d.Header.Description.anchor = Point{margin, d.Header.Y + 30}
	add(&d.Header.Description, "description")
	for ri := range d.Rows {
		r := &d.Rows[ri]
		for bi := range r.Boxes {
			bx := &r.Boxes[bi]
			for fi := range bx.Args {
				add(&bx.Args[fi].Value, "step "+strconv.Itoa(r.Index)+" · "+bx.Args[fi].Key)
			}
			add(&bx.Note, "step "+strconv.Itoa(r.Index)+" · note")
		}
		add(&r.Note, "step "+strconv.Itoa(r.Index)+" · note")
	}
}
