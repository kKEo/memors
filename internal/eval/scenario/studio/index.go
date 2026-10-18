package studio

import (
	"flag"
	"io"
	"net/http"

	"github.com/kKEo/memors/internal/eval/scenario/coverage"
)

type indexData struct {
	*coverage.Index
	CatalogueErr string
	HasCatalogue bool
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	data := indexData{}
	var cat *coverage.Catalogue
	if s.catalogue != "" {
		c, err := coverage.LoadCatalogue(s.catalogue)
		if err != nil {
			data.CatalogueErr = err.Error()
		} else {
			cat, data.HasCatalogue = c, true
		}
	}
	ix, err := coverage.Build(s.dir, cat)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	data.Index = ix
	s.render(w, "index.html", "Scenarios", data)
}

func newFlagSet(stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}
