package setup

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kKEo/memors/tray/internal/config"
	"github.com/kKEo/memors/tray/internal/home"
)

// newServerData fills the "Add server" form.
type newServerData struct {
	KB, Addr, Auth string
	Autostart      bool
	KBs            []string
	MemorsOK       bool
	Err            string
}

func (s *Server) newServerPage(w http.ResponseWriter, r *http.Request) {
	cfg, _, _ := s.loadConfig()
	d := newServerData{Auth: "none", Autostart: true}
	s.fillNewServer(r, cfg, &d)
	s.render(w, "server_new.html", page{Title: "Add server", Data: d})
}

func (s *Server) fillNewServer(r *http.Request, cfg *config.Config, d *newServerData) {
	d.KBs, _ = home.KBs(s.d.Home)
	_, _, err := s.memors(r.Context(), cfg)
	d.MemorsOK = err == nil
}

// addServer saves a new server to tray.json and starts it ("Save & Run").
func (s *Server) addServer(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	kb, addr := strings.TrimSpace(r.PostForm.Get("kb")), strings.TrimSpace(r.PostForm.Get("addr"))
	auth, autostart := authOf(r.PostForm.Get("auth")), r.PostForm.Get("autostart") == "on"
	fail := func(msg string) {
		cfg, _, _ := s.loadConfig()
		d := newServerData{KB: kb, Addr: addr, Auth: authName(auth), Autostart: autostart, Err: msg}
		s.fillNewServer(r, cfg, &d)
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.render(w, "server_new.html", page{Title: "Add server", Data: d})
	}
	if !home.ValidName(kb) {
		fail(fmt.Sprintf("%q is not a valid knowledge-base name (letters, digits, '.', '_', '-'; at most 64).", kb))
		return
	}
	_, err := config.Update(config.Path(s.d.Home), func(c *config.Config) error {
		if c.Find(kb) != nil {
			return fmt.Errorf("%s is already listed", kb)
		}
		if addr != "" {
			c.Servers = append(c.Servers, config.Server{KB: kb, Addr: addr})
		} else if _, _, err := c.AddrFor(kb, s.d.PortFree); err != nil {
			return err
		}
		srv := c.Find(kb)
		srv.Auth, srv.Autostart = auth, autostart
		return c.Validate()
	})
	if err != nil {
		fail(err.Error())
		return
	}
	s.d.Notify(Event{Kind: ConfigSaved})
	s.d.Notify(Event{Kind: StartServer, KB: kb})
	http.Redirect(w, r, "/settings/server/added?"+url.Values{"kb": {kb}}.Encode(), http.StatusSeeOther)
}

// serverAdded confirms a new server and shows how to connect Claude Code
// to it.
func (s *Server) serverAdded(w http.ResponseWriter, r *http.Request) {
	d, ok := s.doneFor(r.URL.Query().Get("kb"))
	if !ok {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	d.Copied = r.URL.Query().Get("copied") == "1"
	if base := strings.TrimSuffix(d.URL, "/mcp"); s.d.Connection != nil {
		if _, ok := s.d.Connection(base); ok {
			d.Connect = connectLink("", base, nil)
		}
	}
	s.render(w, "server_added.html", page{Title: "Server added", Data: d})
}

func (s *Server) copyServerCommand(w http.ResponseWriter, r *http.Request) {
	kb := r.FormValue("kb")
	d, ok := s.doneFor(kb)
	if !ok {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := s.d.Copy(d.Command); err != nil {
		s.d.Logf("copy: %v", err)
	}
	http.Redirect(w, r, "/settings/server/added?"+url.Values{"kb": {kb}, "copied": {"1"}}.Encode(), http.StatusSeeOther)
}
