package ui

import (
	"html/template"

	"github.com/kKEo/memors/internal/memomd"
)

// renderMarkdown renders agent-written page markdown (see memomd). The CSP
// blocks everything but the page's own stylesheet anyway. memo:// addresses
// in the text become links to the matching route, except inside code.
func renderMarkdown(src string) template.HTML { return memomd.Render(src, memoHref) }

// linkMemoURIs wraps bare memo:// addresses in markdown links.
func linkMemoURIs(src string) string { return memomd.Link(src, memoHref) }

// memoHref is the route for a memo:// address; "" when there is none.
func memoHref(uri string) string {
	if h := uriHref(uri); h != "/" {
		return h
	}
	return ""
}
