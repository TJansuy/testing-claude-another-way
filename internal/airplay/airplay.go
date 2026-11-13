// Package airplay implements the AirPlay "video" receiver protocol: a sender
// hands over a media URL (or a photo) and then remote-controls playback while
// the receiver streams it itself. Playback happens on the CastDeck screen.
//
// AirPlay screen mirroring and AirPlay 2 use Apple's encrypted protocols and
// are not implemented here; for those, CastDeck can supervise UxPlay
// (see uxplay.go).
package airplay

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"
	"howett.net/plist"

	"castdeck/internal/player"
)

// Features advertised: video (0x1), photo (0x2), volume control (0x8),
// HTTP live streaming (0x10) and slideshow (0x20).
const Features = 0x3B

// Screen is implemented by the screen controller.
type Screen interface {
	PlayMedia(m player.Media) int64
	ShowPhoto(data []byte, contentType, source string)
}

// Receiver is the AirPlay HTTP server.
type Receiver struct {
	Name     string
	DeviceID string
	Port     int
	Screen   Screen
	Hub      *player.Hub

	mu     sync.Mutex
	photos map[string][]byte
}

const source = "airplay"

// Run serves AirPlay and advertises it over Bonjour until ctx is done.
func (rc *Receiver) Run(ctx context.Context) error {
	rc.photos = map[string][]byte{}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", rc.Port))
	if err != nil {
		return fmt.Errorf("airplay listen: %w", err)
	}
	srv := &http.Server{Handler: rc}
	go func() { <-ctx.Done(); srv.Close() }()

	txt := []string{
		"deviceid=" + rc.DeviceID,
		fmt.Sprintf("features=0x%X", Features),
		"model=AppleTV2,1",
		"srcvers=130.14",
		"vv=2",
		"flags=0x4",
	}
	zc, err := zeroconf.Register(rc.Name, "_airplay._tcp", "local.", rc.Port, txt, nil)
	if err != nil {
		log.Printf("airplay: mDNS advertisement failed (%v); senders will not find this device", err)
	} else {
		defer zc.Shutdown()
	}
	err = srv.Serve(ln)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (rc *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "AirTunes/130.14")
	switch r.URL.Path {
	case "/server-info":
		writePlist(w, map[string]interface{}{
			"deviceid":   rc.DeviceID,
			"features":   Features,
			"model":      "AppleTV2,1",
			"protovers":  "1.0",
			"srcvers":    "130.14",
			"macAddress": rc.DeviceID,
		})
	case "/reverse":
		rc.reverse(w, r)
	case "/play":
		rc.play(w, r)
	case "/scrub":
		if r.Method == http.MethodPost {
			if p, err := strconv.ParseFloat(r.URL.Query().Get("position"), 64); err == nil {
				rc.Hub.Seek(p)
			}
			return
		}
		st := rc.Hub.Snapshot()
		w.Header().Set("Content-Type", "text/parameters")
		fmt.Fprintf(w, "duration: %f\r\nposition: %f\r\n", st.Duration, st.Position)
	case "/rate":
		if v, err := strconv.ParseFloat(r.URL.Query().Get("value"), 64); err == nil {
			rc.Hub.SetPaused(v == 0)
		}
	case "/volume":
		if v, err := strconv.ParseFloat(r.URL.Query().Get("volume"), 64); err == nil {
			rc.Hub.SetVolume(v)
		}
	case "/stop":
		rc.Hub.Stop(source)
	case "/playback-info":
		rc.playbackInfo(w)
	case "/photo":
		rc.photo(w, r)
	case "/slideshow-features":
		writePlist(w, map[string]interface{}{"themes": []interface{}{}})
	case "/setProperty", "/getProperty", "/action", "/authorize":
		writePlist(w, map[string]interface{}{"errorCode": 0})
	default:
		if strings.HasPrefix(r.URL.Path, "/setProperty") || strings.HasPrefix(r.URL.Path, "/getProperty") {
			writePlist(w, map[string]interface{}{"errorCode": 0})
			return
		}
		log.Printf("airplay: unhandled %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

// reverse upgrades to the "PTTH" event channel senders expect to exist.
func (rc *Receiver) reverse(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusSwitchingProtocols)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	fmt.Fprintf(buf, "HTTP/1.1 101 Switching Protocols\r\nDate: %s\r\nUpgrade: PTTH/1.0\r\nConnection: Upgrade\r\n\r\n",
		httpDate())
	_ = buf.Flush()
	// Keep the connection open until the sender goes away.
	go func() {
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()
}

func (rc *Receiver) play(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	loc, startSec, startFrac := parsePlay(r.Header.Get("Content-Type"), body)
	if loc == "" {
		http.Error(w, "missing Content-Location", http.StatusBadRequest)
		return
	}
	log.Printf("airplay: play %s from %s", loc, r.RemoteAddr)
	rc.Screen.PlayMedia(player.Media{
		URL: loc, Kind: player.KindOf(loc), Source: source,
		StartSeconds: startSec, StartFraction: startFrac,
	})
	w.WriteHeader(http.StatusOK)
}

// parsePlay understands both body formats: "text/parameters" (Key: Value
// lines) and binary/XML property lists.
func parsePlay(ctype string, body []byte) (loc string, startSec, startFrac float64) {
	if strings.Contains(ctype, "plist") || bytes.HasPrefix(body, []byte("bplist")) || bytes.HasPrefix(body, []byte("<?xml")) {
		var m map[string]interface{}
		if _, err := plist.Unmarshal(body, &m); err == nil {
			loc, _ = m["Content-Location"].(string)
			if v, ok := toFloat(m["Start-Position-Seconds"]); ok {
				startSec = v
			} else if v, ok := toFloat(m["Start-Position"]); ok {
				startFrac = v
			}
			return
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Content-Location":
			loc = v
		case "Start-Position":
			startFrac, _ = strconv.ParseFloat(v, 64)
		case "Start-Position-Seconds":
			startSec, _ = strconv.ParseFloat(v, 64)
		}
	}
	return
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

func (rc *Receiver) playbackInfo(w http.ResponseWriter) {
	st := rc.Hub.Snapshot()
	if st.Media == nil || st.Media.Source != source {
		writePlist(w, map[string]interface{}{"readyToPlay": false})
		return
	}
	rate := 1.0
	if st.Paused || !st.Ready {
		rate = 0
	}
	ranges := []interface{}{map[string]interface{}{"start": 0.0, "duration": st.Duration}}
	writePlist(w, map[string]interface{}{
		"duration":               st.Duration,
		"position":               st.Position,
		"rate":                   rate,
		"readyToPlay":            st.Ready,
		"playbackBufferEmpty":    !st.Ready,
		"playbackBufferFull":     st.Ready,
		"playbackLikelyToKeepUp": st.Ready,
		"loadedTimeRanges":       ranges,
		"seekableTimeRanges":     ranges,
	})
}

func (rc *Receiver) photo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	key := r.Header.Get("X-Apple-AssetKey")
	switch r.Header.Get("X-Apple-AssetAction") {
	case "cacheOnly":
		rc.mu.Lock()
		if len(rc.photos) > 16 {
			rc.photos = map[string][]byte{}
		}
		rc.photos[key] = data
		rc.mu.Unlock()
		return
	case "displayCached":
		rc.mu.Lock()
		cached, ok := rc.photos[key]
		rc.mu.Unlock()
		if !ok {
			http.Error(w, "not cached", http.StatusPreconditionFailed)
			return
		}
		data = cached
	}
	if len(data) == 0 {
		http.Error(w, "empty photo", http.StatusBadRequest)
		return
	}
	rc.Screen.ShowPhoto(data, http.DetectContentType(data), source)
}

func writePlist(w http.ResponseWriter, v interface{}) {
	out, err := plist.MarshalIndent(v, plist.XMLFormat, "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/x-apple-plist+xml")
	_, _ = w.Write(out)
}
