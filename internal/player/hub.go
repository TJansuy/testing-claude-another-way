// Package player holds the shared playback state. The on-screen page renders
// whatever this state says; network receivers (AirPlay, web remote) change it.
package player

import (
	"encoding/json"
	"path"
	"strings"
	"sync"
)

// Media is something to show on the screen.
type Media struct {
	ID    int64  `json:"id"`
	URL   string `json:"url"`
	Kind  string `json:"kind"` // video | audio | image
	Title string `json:"title,omitempty"`
	// Source is the receiver that started it: "airplay" or "remote".
	Source string `json:"source"`
	// Start position: absolute seconds, or a fraction (0..1) of the duration,
	// which is what legacy AirPlay senders provide.
	StartSeconds  float64 `json:"startSeconds,omitempty"`
	StartFraction float64 `json:"startFraction,omitempty"`
}

// State is broadcast to every connected page as a JSON snapshot.
type State struct {
	Name    string   `json:"name"`
	Mode    string   `json:"mode"` // home | youtube
	Remotes []string `json:"remotes"`

	Media *Media `json:"media"`
	// Desired state, set by commands.
	Paused  bool    `json:"paused"`
	Volume  float64 `json:"volume"`
	SeekSeq int64   `json:"seekSeq"`
	SeekTo  float64 `json:"seekTo"`
	// Reported by the on-screen player.
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	Ready    bool    `json:"ready"`
}

// Hub owns the State and fans out updates.
type Hub struct {
	mu        sync.Mutex
	st        State
	nextID    int64
	subs      map[chan []byte]struct{}
	photo     []byte
	photoType string
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{st: State{Mode: "home", Volume: 1}, subs: map[chan []byte]struct{}{}}
}

// Snapshot returns a copy of the current state.
func (h *Hub) Snapshot() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.st
	if s.Media != nil {
		m := *s.Media
		s.Media = &m
	}
	return s
}

// Subscribe returns a channel of JSON-encoded snapshots, starting with the
// current one, and a cancel function.
func (h *Hub) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	b, _ := json.Marshal(h.st)
	h.mu.Unlock()
	ch <- b
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

func (h *Hub) update(fn func(s *State)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	fn(&h.st)
	b, _ := json.Marshal(h.st)
	for ch := range h.subs {
		select {
		case ch <- b:
		default: // slow subscriber; it will catch up on the next snapshot
		}
	}
}

// SetInfo sets the device name and web remote URLs shown on screen.
func (h *Hub) SetInfo(name string, remotes []string) {
	h.update(func(s *State) { s.Name, s.Remotes = name, remotes })
}

// SetMode records whether the screen shows CastDeck or YouTube.
func (h *Hub) SetMode(mode string) { h.update(func(s *State) { s.Mode = mode }) }

// Play replaces the current media and returns its id.
func (h *Hub) Play(m Media) int64 {
	var id int64
	h.update(func(s *State) {
		h.nextID++
		id = h.nextID
		m.ID = id
		s.Media = &m
		s.Paused = false
		s.Position, s.Duration, s.Ready = m.StartSeconds, 0, false
	})
	return id
}

// SetPhoto shows an image (AirPlay photo) and returns its media id.
func (h *Hub) SetPhoto(data []byte, contentType, source string) int64 {
	h.mu.Lock()
	h.photo, h.photoType = data, contentType
	h.mu.Unlock()
	var id int64
	h.update(func(s *State) {
		h.nextID++
		id = h.nextID
		s.Media = &Media{ID: id, Kind: "image", Source: source, URL: "/api/photo?id=" + itoa(id)}
		s.Paused, s.Position, s.Duration, s.Ready = false, 0, 0, true
	})
	return id
}

// Photo returns the most recent photo.
func (h *Hub) Photo() ([]byte, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.photo, h.photoType
}

// Stop clears the media. If source is non-empty only media from that source
// is stopped. It reports whether anything was stopped.
func (h *Hub) Stop(source string) bool {
	stopped := false
	h.update(func(s *State) {
		if s.Media != nil && (source == "" || s.Media.Source == source) {
			s.Media, s.Position, s.Duration, s.Ready, s.Paused = nil, 0, 0, false, false
			stopped = true
		}
	})
	return stopped
}

// SetPaused pauses or resumes.
func (h *Hub) SetPaused(p bool) { h.update(func(s *State) { s.Paused = p }) }

// Seek asks the player to jump to an absolute position in seconds.
func (h *Hub) Seek(sec float64) {
	if sec < 0 {
		sec = 0
	}
	h.update(func(s *State) {
		s.SeekSeq++
		s.SeekTo = sec
		s.Position = sec
	})
}

// SetVolume sets volume in 0..1.
func (h *Hub) SetVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	h.update(func(s *State) { s.Volume = v })
}

// Report is called by the on-screen player with its progress.
func (h *Hub) Report(id int64, pos, dur float64, ready, ended bool) {
	h.mu.Lock()
	cur := h.st.Media
	h.mu.Unlock()
	if cur == nil || cur.ID != id {
		return
	}
	if ended {
		h.update(func(s *State) {
			if s.Media != nil && s.Media.ID == id {
				s.Media, s.Position, s.Duration, s.Ready = nil, 0, 0, false
			}
		})
		return
	}
	h.update(func(s *State) {
		if s.Media != nil && s.Media.ID == id {
			s.Position, s.Duration, s.Ready = pos, dur, ready
		}
	})
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// KindOf guesses whether a URL is video, audio or an image from its extension.
// Anything unknown is treated as video (the <video> element also plays audio).
func KindOf(u string) string {
	l := strings.ToLower(u)
	if i := strings.IndexAny(l, "?#"); i >= 0 {
		l = l[:i]
	}
	switch path.Ext(l) {
	case ".mp3", ".m4a", ".aac", ".flac", ".wav", ".ogg", ".oga", ".opus":
		return "audio"
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".bmp":
		return "image"
	}
	return "video"
}
