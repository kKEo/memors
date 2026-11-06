package ui

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// md renders agent-written page markdown. Raw HTML in the source is escaped
// (goldmark's default), so a page cannot inject markup; the CSP blocks
// everything but the page's own stylesheet anyway. memo:// addresses in the
// text become links to the matching route, except inside code.
var md = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))

func renderMarkdown(src string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(linkMemoURIs(src)), &buf); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(src) + "</pre>")
	}
	return template.HTML(buf.String())
}

var memoURI = regexp.MustCompile(`memo://[a-z]+/[A-Za-z0-9_-]+`)

// linkMemoURIs wraps bare memo:// addresses in markdown links. It leaves
// fenced blocks, inline code and addresses that are already a link target or
// text alone.
func linkMemoURIs(src string) string {
	lines := strings.Split(src, "\n")
	fence := ""
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue // indented code
		}
		lines[i] = linkOutsideCode(line)
	}
	return strings.Join(lines, "\n")
}

// linkOutsideCode rewrites one line, skipping backtick spans.
func linkOutsideCode(line string) string {
	var out strings.Builder
	for len(line) > 0 {
		start := strings.IndexByte(line, '`')
		if start < 0 {
			out.WriteString(linkSegment(line))
			break
		}
		end := strings.IndexByte(line[start+1:], '`')
		if end < 0 {
			out.WriteString(linkSegment(line))
			break
		}
		out.WriteString(linkSegment(line[:start]))
		out.WriteString(line[start : start+end+2])
		line = line[start+end+2:]
	}
	return out.String()
}

func linkSegment(s string) string {
	var out strings.Builder
	last := 0
	for _, m := range memoURI.FindAllStringIndex(s, -1) {
		u := s[m[0]:m[1]]
		out.WriteString(s[last:m[0]])
		last = m[1]
		linked := false
		if m[0] > 0 {
			switch s[m[0]-1] {
			case '<', '/', '"', '\'':
				linked = true // autolink, attribute, or part of a longer address
			case '(':
				linked = m[0] > 1 && s[m[0]-2] == ']' // a link target
			case '[':
				linked = strings.HasPrefix(s[m[1]:], "](") // link text
			}
		}
		if h := uriHref(u); !linked && h != "/" {
			out.WriteString("[" + u + "](" + h + ")")
		} else {
			out.WriteString(u)
		}
	}
	out.WriteString(s[last:])
	return out.String()
}
