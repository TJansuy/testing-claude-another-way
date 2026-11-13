// Package web serves the on-screen home/player page, the phone web remote and
// the small JSON API both use.
package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"castdeck/internal/app"
	"castdeck/internal/player"
)

//go:embed static
var static embed.FS

// Server is the CastDeck web UI.
type Server struct {
	App *app.App
	Hub *player.Hub
}

// Register adds the UI routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	files, _ := fs.Sub(static, "static")
	fileServer := http.FileServer(http.FS(files))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			serveFile(w, files, "index.html")
		case "/remote", "/remote/":
			serveFile(w, files, "remote.html")
		default:
			http.NotFound(w, r)
		}
	})
	mux.Handle("/static/", http.StripPrefix("/static/", fileServer))
	mux.HandleFunc("/api/events", s.events)
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Hub.Snapshot()) })
	mux.HandleFunc("/api/photo", s.photo)
	mux.HandleFunc("/api/report", s.post(s.report))
	mux.HandleFunc("/api/play", s.post(s.play))
	mux.HandleFunc("/api/control", s.post(s.control))
	mux.HandleFunc("/api/youtube", s.post(func(w http.ResponseWriter, r *http.Request) {
		go s.App.LaunchYouTube(nil)
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/home", s.post(func(w http.ResponseWriter, r *http.Request) {
		s.Hub.Stop("")
		go s.App.GoHome()
		writeJSON(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/quit", s.post(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r) {
			http.Error(w, "only the device itself can quit CastDeck", http.StatusForbidden)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		go s.App.Quit()
	}))
}

func serveFile(w http.ResponseWriter, files fs.FS, name string) {
	data, err := fs.ReadFile(files, name)
	if err != nil {
		http.Error(w, "missing asset", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

// post wraps an API handler: POST with a JSON content type only. Requiring
// JSON means ordinary web pages cannot fire these requests cross-site.
func (s *Server) post(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
			http.Error(w, "expected application/json", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		h(w, r)
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch, cancel := s.Hub.Subscribe()
	defer cancel()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) photo(w http.ResponseWriter, r *http.Request) {
	data, ctype := s.Hub.Photo()
	if data == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       int64   `json:"id"`
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		Ready    bool    `json:"ready"`
		Ended    bool    `json:"ended"`
		Error    string  `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if in.Error != "" {
		log.Printf("player: media %d failed: %s", in.ID, in.Error)
	}
	s.Hub.Report(in.ID, in.Position, in.Duration, in.Ready, in.Ended)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) play(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL   string  `json:"url"`
		Title string  `json:"title"`
		Start float64 `json:"start"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		http.Error(w, "please enter an http(s) link", http.StatusBadRequest)
		return
	}
	if id, t := YouTubeID(u); id != "" {
		log.Printf("remote: YouTube video %s from %s", id, r.RemoteAddr)
		p := url.Values{"v": {id}}
		if t != "" {
			p.Set("t", t)
		}
		go s.App.LaunchYouTube(p)
		writeJSON(w, map[string]string{"opened": "youtube"})
		return
	}
	log.Printf("remote: play %s from %s", u, r.RemoteAddr)
	id := s.App.PlayMedia(player.Media{URL: u.String(), Kind: player.KindOf(u.Path), Title: in.Title, Source: "remote", StartSeconds: in.Start})
	writeJSON(w, map[string]int64{"id": id})
}

func (s *Server) control(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string  `json:"action"`
		Value  float64 `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	switch in.Action {
	case "pause":
		s.Hub.SetPaused(true)
	case "resume":
		s.Hub.SetPaused(false)
	case "toggle":
		s.Hub.SetPaused(!s.Hub.Snapshot().Paused)
	case "seek":
		s.Hub.Seek(in.Value)
	case "skip":
		s.Hub.Seek(s.Hub.Snapshot().Position + in.Value)
	case "volume":
		s.Hub.SetVolume(in.Value)
	case "stop":
		s.Hub.Stop("")
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// YouTubeID extracts a video id (and optional start time) from a YouTube link.
func YouTubeID(u *url.URL) (id, start string) {
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	host = strings.TrimPrefix(host, "m.")
	start = strings.TrimSuffix(u.Query().Get("t"), "s")
	switch host {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	case "youtube.com", "music.youtube.com", "youtube-nocookie.com":
		if v := u.Query().Get("v"); v != "" {
			id = v
		} else {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) == 2 && (parts[0] == "shorts" || parts[0] == "live" || parts[0] == "embed") {
				id = parts[1]
			}
		}
	}
	if len(id) < 6 || len(id) > 20 || strings.ContainsAny(id, "/?&#=") {
		return "", ""
	}
	if _, err := fmt.Sscanf(start, "%d", new(int)); err != nil {
		start = ""
	}
	return id, start
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
