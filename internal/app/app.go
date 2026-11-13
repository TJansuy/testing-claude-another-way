// Package app decides what is on screen: the CastDeck home/player page or the
// YouTube TV app, and switches between them when phones cast.
package app

import (
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"castdeck/internal/browser"
	"castdeck/internal/config"
	"castdeck/internal/player"
)

const (
	ModeHome    = "home"
	ModeYouTube = "youtube"
)

// App is the screen controller.
type App struct {
	cfg *config.Config
	hub *player.Hub
	br  *browser.Controller // nil when running without a screen

	mu       sync.Mutex
	mode     string
	dialData url.Values
	failures int

	quitOnce sync.Once
	quit     chan struct{}
}

// New creates the controller. br may be nil (headless).
func New(cfg *config.Config, hub *player.Hub, br *browser.Controller) *App {
	return &App{cfg: cfg, hub: hub, br: br, mode: ModeHome, quit: make(chan struct{})}
}

// Done is closed when the app should exit.
func (a *App) Done() <-chan struct{} { return a.quit }

// Quit asks the program to exit.
func (a *App) Quit() { a.quitOnce.Do(func() { close(a.quit) }) }

func (a *App) homeURL() string { return fmt.Sprintf("http://127.0.0.1:%d/", a.cfg.HTTPPort) }

// DialDataURL is where the YouTube TV app posts its pairing data.
func (a *App) DialDataURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/apps/YouTube/dial_data", a.cfg.HTTPPort)
}

// Start shows the home screen and watches the browser.
func (a *App) Start() error {
	if a.br == nil {
		return nil
	}
	if _, err := a.br.Open(a.homeURL()); err != nil {
		return err
	}
	go a.watch()
	return nil
}

// Close stops the browser.
func (a *App) Close() {
	if a.br != nil {
		a.br.Close()
	}
}

func (a *App) watch() {
	for {
		select {
		case <-a.quit:
			return
		case ex := <-a.br.Exits:
			if a.br.Current() != 0 {
				continue // an old window we replaced on purpose
			}
			a.mu.Lock()
			mode := a.mode
			a.mu.Unlock()
			log.Printf("browser: exited after %s (mode %s, err %v)", ex.Elapsed.Round(time.Second), mode, ex.Err)
			if mode == ModeYouTube {
				// The user left YouTube (it closes its window on exit).
				a.GoHome()
				continue
			}
			if ex.Elapsed < 10*time.Second {
				a.failures++
				if a.failures >= 3 {
					log.Printf("browser keeps exiting right after start; see browser.log in %s", a.cfg.Dir)
					a.Quit()
					return
				}
				time.Sleep(time.Second)
				a.open(a.homeURL())
				continue
			}
			a.failures = 0
			// The home screen was closed by the user: exit back to Steam.
			a.Quit()
			return
		}
	}
}

func (a *App) open(u string) {
	if a.br == nil {
		log.Printf("screen (headless): would open %s", u)
		return
	}
	if _, err := a.br.Open(u); err != nil {
		log.Printf("browser: %v", err)
	}
}

func (a *App) setMode(m string) {
	a.mu.Lock()
	a.mode = m
	a.mu.Unlock()
	a.hub.SetMode(m)
}

// Mode returns what is on screen.
func (a *App) Mode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mode
}

// LaunchYouTube opens the YouTube TV app. params are the DIAL launch
// parameters (pairingCode, v, t, ...), passed through as the TV app expects.
func (a *App) LaunchYouTube(params url.Values) {
	// Switch mode before clearing media so "someone is watching" never
	// flickers off (that would briefly release the keep-awake inhibitor).
	a.setMode(ModeYouTube)
	a.hub.Stop("")
	q := url.Values{}
	for k, v := range params {
		q[k] = v
	}
	q.Set("additionalDataUrl", a.DialDataURL())
	a.mu.Lock()
	a.dialData = nil
	a.mu.Unlock()
	a.open(a.cfg.YouTubeURL + "?" + q.Encode())
}

// GoHome returns to the CastDeck screen.
func (a *App) GoHome() {
	a.mu.Lock()
	wasHome := a.mode == ModeHome
	a.mu.Unlock()
	a.setMode(ModeHome)
	if !wasHome || (a.br != nil && a.br.Current() == 0) {
		a.open(a.homeURL())
	}
}

// PlayMedia shows media on the CastDeck player, leaving YouTube if needed.
func (a *App) PlayMedia(m player.Media) int64 {
	if a.Mode() != ModeHome {
		a.GoHome()
	}
	return a.hub.Play(m)
}

// ShowPhoto shows an image on the CastDeck player.
func (a *App) ShowPhoto(data []byte, contentType, source string) {
	if a.Mode() != ModeHome {
		a.GoHome()
	}
	a.hub.SetPhoto(data, contentType, source)
}

// SetDialData stores what the YouTube TV app reported (screenId, ...).
func (a *App) SetDialData(v url.Values) {
	a.mu.Lock()
	a.dialData = v
	a.mu.Unlock()
}

// DialData returns the stored YouTube TV data.
func (a *App) DialData() url.Values {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.dialData
}
