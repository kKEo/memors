package diagram

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"

	"github.com/kKEo/memors/internal/eval/scenario"
)

const (
	sans = "Inter, -apple-system, BlinkMacSystemFont, Segoe UI, Roboto, Helvetica, Arial, sans-serif"
	mono = "JetBrains Mono, ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"

	cBg, cBox, cStroke, cText, cMuted = "#faf8f4", "#ffffff", "#d3cec4", "#171a1f", "#6d727a"
	cAgent, cAgentLine                = "#eef3f8", "#5b7ea3"
	cHuman, cHumanLine                = "#eef5f2", "#3b8572"
	cUnknown, cUnknownLine            = "#f3f0ea", "#8a8f96"
	cBind, cEdge                      = "#1f5c4f", "#8a8f96"
	cOpen, cOpenLine                  = "#fdf3d7", "#b8860b"
	cFail, cFailLine, cPass           = "#fdf1ee", "#b4412e", "#3b8572"
	cLink                             = "#36577a"
)

// chip colours by kind: fill, stroke, text.
var chipColours = map[string][3]string{
	"top":        {"#eef5f2", "#3b8572", "#1f5c4f"},
	"contains":   {"#eef5f2", "#3b8572", "#1f5c4f"},
	"ok":         {"#eef5f2", "#3b8572", "#1f5c4f"},
	"excludes":   {"#fbf1ea", "#a85a2c", "#8a4a24"},
	"error":      {"#fbf1ea", "#a85a2c", "#8a4a24"},
	"audit_none": {"#fbf1ea", "#a85a2c", "#8a4a24"},
	"abstains":   {"#fbf1ea", "#a85a2c", "#8a4a24"},
	"out":        {"#eef3f8", "#5b7ea3", "#36577a"},
	"status":     {"#eef3f8", "#5b7ea3", "#36577a"},
	"count":      {"#eef3f8", "#5b7ea3", "#36577a"},
	"open":       {cOpen, cOpenLine, "#7a5a0a"},
	"meta":       {"#f3f0ea", cStroke, "#4a4f57"},
}

// Render builds and writes the picture.
func Render(sc *scenario.Scenario, opts Options) ([]byte, error) {
	d := Build(sc, opts)
	var buf bytes.Buffer
	if err := d.WriteSVG(&buf, opts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type writer struct {
	sb   strings.Builder
	opts Options
}

func (w *writer) p(format string, args ...any) { fmt.Fprintf(&w.sb, format, args...) }

// esc makes s safe as XML text or attribute value.
func esc(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		return r
	}, s)
	return html.EscapeString(s)
}

func (w *writer) text(x, y int, attrs, s string) {
	w.p(`<text x="%d" y="%d" %s>%s</text>`+"\n", x, y, attrs, esc(s))
}

// chip draws one pill at an absolute position.
func (w *writer) chip(c Chip, id string) {
	col, ok := chipColours[c.Kind]
	if !ok {
		col = [3]string{"#f3f0ea", cStroke, "#4a4f57"}
	}
	attrs := ""
	if id != "" {
		attrs = ` id="` + esc(id) + `"`
	}
	h := c.H
	if h == 0 {
		h = chipH
	}
	lines := c.Lines
	if len(lines) == 0 {
		lines = []string{c.Text}
	}
	w.p(`<g class="chip chip-%s"%s><rect x="%d" y="%d" width="%d" height="%d" rx="9" fill="%s" stroke="%s"/>`, esc(c.Kind), attrs, c.X, c.Y, c.W, h, col[0], col[1])
	for i, l := range lines {
		w.p(`<text x="%d" y="%d" font-family="%s" font-size="11" fill="%s">%s</text>`, c.X+chipPadX, c.Y+13+i*13, mono, col[2], esc(l))
	}
	w.p("</g>\n")
}

// WriteSVG renders the laid-out diagram.
func (d *Diagram) WriteSVG(out io.Writer, opts Options) error {
	w := &writer{opts: opts}
	w.p(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="t d" font-family="%s" font-size="12" fill="%s">`+"\n", d.Width, d.Height, d.Width, d.Height, sans, cText)
	w.p("<title id=\"t\">%s</title>\n<desc id=\"d\">%s</desc>\n", esc(d.Title), esc(d.Desc))
	if opts.Mode == Standalone && len(d.Popovers) > 0 {
		// Only a file with something to expand needs the hover rules and
		// the script; a fully expanded file stays plain SVG.
		w.p("<style>%s</style>\n", CSS)
	}
	w.p(`<defs><marker id="arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L8,4 L0,8 z" fill="%s"/></marker></defs>`+"\n", cEdge)
	w.p(`<rect class="bg" x="0" y="0" width="%d" height="%d" fill="%s"/>`+"\n", d.Width, d.Height, cBg)
	w.header(d)
	w.lanes(d)
	for ri := range d.Rows {
		w.row(d, &d.Rows[ri])
	}
	w.edges(d)
	w.text(margin, d.laneBottom+pad+14, `class="legend" font-size="11" fill="`+cMuted+`"`, clip(d.Legend, int(float64(d.Width-2*margin)/5.9)))
	if opts.Mode == Standalone && len(d.Popovers) > 0 {
		w.popovers(d)
	}
	w.p("</svg>\n")
	_, err := io.WriteString(out, w.sb.String())
	return err
}

func (w *writer) header(d *Diagram) {
	h := &d.Header
	w.p(`<g class="header" id="header">` + "\n")
	y := h.Y + pad + 16
	w.text(margin, y, `id="hdr-name" data-path="name" font-size="16" font-weight="600"`, h.Name)
	y += 20
	if h.Description.Full != "" {
		w.block(&h.Description, margin, y, `font-family="`+sans+`" fill="`+cMuted+`"`, "desc")
		y += len(h.Description.Lines) * lineH
		if w.affordance(&h.Description) != "" {
			y += lineH
		}
	}
	metaH := 0
	for _, c := range h.Meta {
		c.X += margin
		c.Y += y - 10
		if c.Y+c.H-(y-10) > metaH {
			metaH = c.Y + c.H - (y - 10)
		}
		w.chip(c, "")
	}
	y += metaH + 10
	for _, is := range h.Issues {
		w.text(margin, y, `class="issue" fill="`+cFailLine+`" data-path="`+esc(is.Path)+`"`, "⚠ "+is.Msg)
		y += lineH
	}
	w.p("</g>\n")
}

func (w *writer) lanes(d *Diagram) {
	w.p(`<g class="lanes">` + "\n")
	for _, ln := range d.Lanes {
		fill, stroke := cUnknown, cUnknownLine
		switch ln.Kind {
		case "agent":
			fill, stroke = cAgent, cAgentLine
		case "human":
			fill, stroke = cHuman, cHumanLine
		}
		w.p(`<g class="lane lane-%s" id="%s" data-path="actors.%s">`+"\n", esc(ln.Kind), esc(ln.ID), esc(ln.Key))
		w.p(`<rect x="%d" y="%d" width="%d" height="%d" fill="%s" fill-opacity="0.45"/>`+"\n", ln.X, d.laneTop, ln.W, d.laneBottom-d.laneTop, fill)
		w.p(`<rect x="%d" y="%d" width="%d" height="%d" rx="6" fill="%s" stroke="%s"/>`+"\n", ln.X, d.laneTop, ln.W, laneHdrH, fill, stroke)
		w.text(ln.X+pad, d.laneTop+18, `font-weight="600" font-size="13"`, ln.Key)
		kind := ln.Kind
		if ln.Unknown {
			kind = "⚠ not declared"
		}
		second := kind
		if ln.Client != ln.Key {
			second += " · " + ln.Client
		}
		w.text(ln.X+pad, d.laneTop+33, `font-size="11" fill="`+cMuted+`"`, clip(second, colsFor(ln.W-2*pad)+4))
		if ln.Elicitation != "" {
			w.text(ln.X+pad, d.laneTop+45, `font-size="11" fill="`+cMuted+`"`, "answers dialogs: "+ln.Elicitation)
		}
		w.p("</g>\n")
	}
	w.p("</g>\n")
}

func (w *writer) row(d *Diagram, r *Row) {
	w.p(`<g class="row" id="%s" data-index="%d" data-path="%s">`+"\n", esc(r.ID), r.Index, esc(r.Path))
	if r.ClockJump && r.Index > 1 {
		w.p(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-dasharray="2 4"/>`+"\n", margin, r.Y-jumpH/2-rowGap/2, d.rightX-pad, r.Y-jumpH/2-rowGap/2, cStroke)
	}
	label := strconv.Itoa(r.Index)
	if r.At != "" {
		label += " · " + r.At
	}
	w.text(margin, r.Y+14, `font-weight="600"`, label)
	if r.ClockJump {
		date := r.Clock.Format("2006-01-02")
		if r.Delta != "" {
			date += " " + r.Delta
		}
		w.text(margin, r.Y+29, `font-size="11" fill="`+cMuted+`"`, date)
	}
	gy := r.Y + 44
	for _, s := range r.Skips {
		w.text(margin, gy, `font-size="10" fill="#8a4a24"`, clip("skips "+s, 16))
		gy += 12
	}
	if r.Parallel && r.Bracket != nil {
		b := r.Bracket
		w.p(`<rect x="%d" y="%d" width="%d" height="%d" rx="8" fill="none" stroke="%s" stroke-dasharray="6 4"/>`+"\n", b.X, b.Y, b.W, b.H, cUnknownLine)
		w.text(b.X+pad, b.Y+13, `font-size="11" font-weight="600" fill="`+cMuted+`"`, "parallel")
		y := b.Y + b.H
		if r.Note.Full != "" {
			w.block(&r.Note, b.X, y+12, `font-style="italic" fill="`+cMuted+`"`, "note")
			y += len(r.Note.Lines) * lineH
			if w.affordance(&r.Note) != "" {
				y += lineH
			}
		}
		for _, is := range r.Issues {
			w.text(b.X, y+12, `class="issue" fill="`+cFailLine+`" data-path="`+esc(is.Path)+`"`, "⚠ "+is.Msg)
			y += lineH
		}
	}
	for bi := range r.Boxes {
		w.box(&r.Boxes[bi])
	}
	if len(r.Strip) > 0 {
		var parts []string
		for _, c := range r.Strip {
			s := fmt.Sprintf("%s %d", c.Key, c.Value)
			if c.Delta != 0 {
				s += fmt.Sprintf(" (%+d)", c.Delta)
			}
			parts = append(parts, s)
		}
		w.text(d.Lanes[0].X, r.Y+r.H-rowGap-4, `class="strip" font-family="`+mono+`" font-size="11" fill="`+cMuted+`"`, "state: "+strings.Join(parts, " · "))
	}
	w.p("</g>\n")
}

func (w *writer) affordance(tb *TextBlock) string {
	if !tb.Truncated {
		return ""
	}
	if !tb.Expanded && (w.opts.Mode == Standalone || w.opts.ExpandHref != nil) {
		return "more"
	}
	if tb.Expanded && w.opts.Mode == Inline && w.opts.ExpandHref != nil {
		return "less"
	}
	return ""
}

// block writes a text block's lines from baseline y, plus its affordance.
func (w *writer) block(tb *TextBlock, x, y int, attrs, cls string) {
	w.p(`<g class="%s" id="%s" data-path="%s">`+"\n", cls, esc(tb.ID), esc(tb.Path))
	if tb.Truncated {
		w.p("<title>%s</title>\n", esc(tb.Full))
	}
	for i, l := range tb.Lines {
		w.text(x, y+i*lineH, attrs, l)
	}
	w.more(tb, x, y+len(tb.Lines)*lineH)
	w.p("</g>\n")
}

func (w *writer) more(tb *TextBlock, x, y int) {
	switch w.affordance(tb) {
	case "more":
		if w.opts.Mode == Inline {
			w.p(`<a href="%s"><text class="more" x="%d" y="%d" font-size="11" fill="%s">… more</text></a>`+"\n", esc(w.opts.ExpandHref(w.opts.Expand.Toggle(tb.ID))), x, y, cLink)
		} else {
			w.p(`<text class="more" data-expand="%s" x="%d" y="%d" font-size="11" fill="%s" tabindex="0">… more</text>`+"\n", esc(tb.ID), x, y, cLink)
		}
	case "less":
		w.p(`<a href="%s"><text class="less" x="%d" y="%d" font-size="11" fill="%s">less</text></a>`+"\n", esc(w.opts.ExpandHref(w.opts.Expand.Toggle(tb.ID))), x, y, cLink)
	}
}

func (w *writer) box(bx *Box) {
	cls := "step"
	stroke, fill, width := cStroke, cBox, 1
	if bx.Overlay != nil {
		cls += " " + bx.Overlay.State
		if bx.Overlay.State == "fail" {
			stroke, fill, width = cFailLine, cFail, 2
		} else {
			stroke, width = cPass, 2
		}
	}
	if len(bx.Issues) > 0 {
		cls += " invalid"
		stroke, width = cFailLine, 2
	}
	w.p(`<g class="%s" id="%s" data-path="%s">`+"\n", cls, esc(bx.ID), esc(bx.Path))
	w.p(`<rect class="box" x="%d" y="%d" width="%d" height="%d" rx="6" fill="%s" stroke="%s" stroke-width="%d"/>`+"\n", bx.Rect.X, bx.Rect.Y, bx.Rect.W, bx.Rect.H, fill, stroke, width)
	x := bx.Rect.X + pad
	y := bx.Rect.Y + pad + 15
	// the op line: op, dialog bubble, bind badge
	w.text(x, y, `class="op" font-weight="600" font-size="13" data-path="`+esc(bx.Path+".op")+`"`, bx.Op)
	ox := x + textW(bx.Op, 7.8) + 8
	if bx.Elicit != "" || bx.DefaultAnswer != "" {
		label, fill := "dialog: "+bx.Elicit, cOpen
		if bx.Elicit == "" {
			label, fill = "dialog: "+bx.DefaultAnswer, cBox
		}
		bw := textW(label, 5.9) + 10
		w.p(`<rect x="%d" y="%d" width="%d" height="15" rx="7" fill="%s" stroke="%s"/>`+"\n", ox, y-11, bw, fill, cOpenLine)
		w.text(ox+5, y, `font-size="10" fill="#7a5a0a" data-path="`+esc(bx.Path+".elicit")+`"`, label)
	}
	if bx.Bind != "" {
		w.p(`<text class="bind" id="bind-%s" data-path="%s" x="%d" y="%d" text-anchor="end" font-family="%s" font-weight="600" fill="%s">$%s</text>`+"\n", esc(bx.Bind), esc(bx.Path+".bind"), bx.Rect.X+bx.Rect.W-pad, y, mono, cBind, esc(bx.Bind))
	}
	y = bx.Rect.Y + pad + opH + 12
	var open *TextBlock
	closeBlock := func() {
		if open != nil {
			w.p("</g>\n")
			open = nil
		}
	}
	for i, l := range bx.lines {
		if i == 0 {
			continue
		}
		if i == bx.chipAt && len(bx.Chips) > 0 {
			closeBlock()
			for ci, c := range bx.Chips {
				w.chip(c, bx.ID+"-chip-"+strconv.Itoa(ci+1))
			}
			y += bx.chipsH
		}
		if l.block != nil && l.block != open {
			closeBlock()
			open = l.block
			w.p(`<g class="%s" id="%s" data-path="%s">`+"\n", l.kind, esc(open.ID), esc(open.Path))
			if open.Truncated {
				w.p("<title>%s</title>\n", esc(open.Full))
			}
		}
		if l.block == nil {
			closeBlock()
		}
		switch l.kind {
		case "resolved":
			w.text(x, y, `class="resolved" font-family="`+mono+`" font-size="11" fill="`+cBind+`"`, l.text)
		case "select":
			w.text(x, y, `class="select" font-family="`+mono+`" fill="`+cMuted+`" data-path="`+esc(bx.Path+".select")+`"`, l.text)
		case "field":
			w.text(x, y, `font-family="`+mono+`"`, l.text)
		case "note":
			w.text(x, y, `font-style="italic" fill="`+cMuted+`"`, l.text)
		case "issue":
			w.text(x, y, `class="issue" fill="`+cFailLine+`"`, l.text)
		case "fail":
			w.text(x, y, `class="fail" font-family="`+mono+`" font-size="11" fill="`+cFailLine+`"`, l.text)
		case "more", "less":
			w.more(l.block, x, y)
		}
		y += lineH
	}
	closeBlock()
	if bx.chipAt == len(bx.lines) && len(bx.Chips) > 0 {
		for ci, c := range bx.Chips {
			w.chip(c, bx.ID+"-chip-"+strconv.Itoa(ci+1))
		}
	}
	w.p("</g>\n")
}

func (w *writer) edges(d *Diagram) {
	if len(d.Edges) == 0 {
		return
	}
	w.p(`<g class="edges">` + "\n")
	for _, e := range d.Edges {
		if len(e.Stubs) == 0 {
			continue
		}
		last := e.Stubs[len(e.Stubs)-1].Y
		w.p(`<g class="edge" id="%s" data-bind="%s">`+"\n", esc(e.ID), esc(e.Bind))
		w.p(`<title>$%s is used by %d later step(s)</title>`+"\n", esc(e.Bind), len(e.Stubs))
		w.p(`<circle cx="%d" cy="%d" r="3" fill="%s"/>`+"\n", e.From.X, e.From.Y, cBind)
		w.p(`<path d="M%d,%d H%d V%d" fill="none" stroke="%s" stroke-dasharray="4 3"/>`+"\n", e.From.X, e.From.Y, e.X, last, cEdge)
		for _, s := range e.Stubs {
			w.p(`<path d="M%d,%d H%d" fill="none" stroke="%s" stroke-dasharray="4 3" marker-end="url(#arrow)"/>`+"\n", e.X, s.Y, s.X+2, cEdge)
		}
		w.text(e.X+4, e.From.Y-4, `font-family="`+mono+`" font-size="9" fill="`+cBind+`" transform="rotate(90 `+strconv.Itoa(e.X+4)+` `+strconv.Itoa(e.From.Y-4)+`)"`, "$"+e.Bind)
		w.p("</g>\n")
	}
	w.p("</g>\n")
}

func (w *writer) popovers(d *Diagram) {
	w.p(`<g class="popovers">` + "\n")
	for _, po := range d.Popovers {
		w.p(`<g class="popover" id="pop-%s" display="none">`+"\n", esc(po.For))
		w.p(`<rect x="%d" y="%d" width="%d" height="%d" rx="6" fill="%s" stroke="%s" stroke-width="1.5"/>`+"\n", po.X, po.Y, po.W, po.H, cBox, cLink)
		w.text(po.X+pad, po.Y+pad+12, `font-weight="600" font-size="12"`, po.Title)
		w.text(po.X+po.W-pad-8, po.Y+pad+12, `class="popover-close" font-weight="600" fill="`+cMuted+`"`, "✕")
		for i, l := range po.Lines {
			w.text(po.X+pad, po.Y+pad+lineH+12+i*lineH, `font-family="`+mono+`" font-size="12"`, l)
		}
		w.p("</g>\n")
	}
	w.p("</g>\n")
	w.p("<script><![CDATA[%s]]></script>\n", script)
}

// script shows a popover when its "… more" is clicked and hides it on the
// next click anywhere (standalone files only).
const script = `(function () {
  var root = document.documentElement;
  function hide() {
    var ps = root.querySelectorAll('.popover');
    for (var i = 0; i < ps.length; i++) ps[i].setAttribute('display', 'none');
  }
  root.addEventListener('click', function (ev) {
    var t = ev.target;
    while (t && t !== root && !(t.getAttribute && t.getAttribute('data-expand')) && !(t.classList && t.classList.contains('popover'))) t = t.parentNode;
    if (!t || t === root) { hide(); return; }
    if (t.classList.contains('popover')) {
      if (ev.target.classList && ev.target.classList.contains('popover-close')) hide();
      return;
    }
    var id = t.getAttribute('data-expand');
    hide();
    var p = document.getElementById('pop-' + id);
    if (p) p.setAttribute('display', 'inline');
    ev.preventDefault();
  });
  root.addEventListener('keydown', function (ev) { if (ev.key === 'Escape') hide(); });
})();`
