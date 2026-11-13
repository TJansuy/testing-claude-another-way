// Command castdeck turns a SteamOS (or any Linux) machine into a cast target:
// phones on the network can cast YouTube to it, AirPlay media to it, or send
// it any link from a web remote. Add it to Steam as a non-Steam game and
// launch it from gaming mode.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"castdeck/internal/airplay"
	"castdeck/internal/app"
	"castdeck/internal/browser"
	"castdeck/internal/config"
	"castdeck/internal/dial"
	"castdeck/internal/inhibit"
	"castdeck/internal/netutil"
	"castdeck/internal/player"
	"castdeck/internal/web"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dir         = flag.String("config", config.DefaultDir(), "configuration directory")
		name        = flag.String("name", "", "name shown to phones (default from config)")
		port        = flag.Int("port", 0, "HTTP port for DIAL, the web remote and the screen UI")
		airplayPort = flag.Int("airplay-port", 0, "AirPlay port")
		airplayMode = flag.String("airplay", "", `AirPlay mode: "builtin", "uxplay" or "off"`)
		noDIAL      = flag.Bool("no-dial", false, "disable YouTube (DIAL) casting")
		browserCmd  = flag.String("browser", "", `browser command line, or "auto"`)
		noBrowser   = flag.Bool("no-browser", false, "run without a screen (network services only)")
		install     = flag.Bool("install", false, "add a desktop entry so Steam's \"Add a Non-Steam Game\" lists CastDeck, then exit")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println("castdeck", version)
		return nil
	}
	if *install {
		return installDesktopEntry()
	}

	cfg, err := config.Load(*dir)
	if err != nil {
		return err
	}
	if *name != "" {
		cfg.Name = *name
	}
	if *port != 0 {
		cfg.HTTPPort = *port
	}
	if *airplayPort != 0 {
		cfg.AirPlayPort = *airplayPort
	}
	if *airplayMode != "" {
		cfg.AirPlayMode = *airplayMode
	}
	if *noDIAL {
		cfg.DIAL = false
	}
	if *browserCmd != "" {
		cfg.Browser = *browserCmd
	}
	cfg.NoBrowser = *noBrowser

	logFile, err := os.OpenFile(filepath.Join(cfg.Dir, "castdeck.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err == nil {
		defer logFile.Close()
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	}
	log.Printf("CastDeck %s starting as %q", version, cfg.Name)

	id, err := config.LoadIdentity(cfg.Dir)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}

	hub := player.NewHub()
	var remotes []string
	for _, ip := range netutil.LocalIPv4s() {
		remotes = append(remotes, fmt.Sprintf("http://%s:%d/remote", ip, cfg.HTTPPort))
	}
	hub.SetInfo(cfg.Name, remotes)

	var br *browser.Controller
	if !cfg.NoBrowser {
		spec, err := browser.Detect(cfg.Browser)
		if err != nil {
			return err
		}
		blog, _ := os.OpenFile(filepath.Join(cfg.Dir, "browser.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if blog != nil {
			defer blog.Close()
		}
		br, err = browser.NewController(spec, cfg.UserAgent, cfg.BrowserExtraArgs, writerOrDiscard(blog))
		if err != nil {
			return err
		}
		log.Printf("screen: using %s", spec.Name)
	}
	a := app.New(cfg, hub, br)

	mux := http.NewServeMux()
	(&web.Server{App: a, Hub: hub}).Register(mux)
	if cfg.DIAL {
		(&dial.Server{Name: cfg.Name, UUID: id.UUID, App: a}).Register(mux)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.HTTPPort))
	if err != nil {
		return fmt.Errorf("port %d is busy (is CastDeck already running?): %w", cfg.HTTPPort, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http: %v", err)
			a.Quit()
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if cfg.DIAL {
		go func() {
			if err := (&dial.SSDP{UUID: id.UUID, HTTPPort: cfg.HTTPPort}).Run(ctx); err != nil {
				log.Printf("YouTube discovery (SSDP) unavailable: %v", err)
			}
		}()
	}
	switch cfg.AirPlayMode {
	case "builtin", "":
		go func() {
			rc := &airplay.Receiver{Name: cfg.Name, DeviceID: id.DeviceID, Port: cfg.AirPlayPort, Screen: a, Hub: hub}
			if err := rc.Run(ctx); err != nil {
				log.Printf("AirPlay unavailable: %v", err)
			}
		}()
	case "uxplay":
		go func() {
			ux := &airplay.UxPlay{Name: cfg.Name, Extra: cfg.UxPlayExtraArgs, Log: log.Writer()}
			if err := ux.Run(ctx); err != nil {
				log.Printf("AirPlay unavailable: %v", err)
			}
		}()
	case "off":
	default:
		log.Printf("unknown airplay_mode %q; AirPlay disabled", cfg.AirPlayMode)
	}

	// Keep SteamOS from dimming/sleeping while something is being watched.
	inh := &inhibit.Inhibitor{}
	defer inh.Close()
	go keepAwake(ctx, hub, inh)

	if err := a.Start(); err != nil {
		return err
	}
	for _, r := range remotes {
		log.Printf("web remote: %s", r)
	}
	log.Printf("ready: cast to %q from YouTube or AirPlay", cfg.Name)

	select {
	case <-ctx.Done():
		log.Printf("shutting down (signal)")
	case <-a.Done():
		log.Printf("shutting down")
	}
	stop()
	a.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	time.Sleep(200 * time.Millisecond) // let SSDP send its goodbye
	return nil
}

func keepAwake(ctx context.Context, hub *player.Hub, inh *inhibit.Inhibitor) {
	ch, cancel := hub.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-ch:
			if !ok {
				return
			}
			var st player.State
			if json.Unmarshal(b, &st) == nil {
				inh.Set(st.Watching())
			}
		}
	}
}

func writerOrDiscard(f *os.File) io.Writer {
	if f == nil {
		return io.Discard
	}
	return f
}

func installDesktopEntry() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".local", "share", "applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "castdeck.desktop")
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=CastDeck
Comment=Cast YouTube and AirPlay media to this device
Exec=%q
Icon=video-display
Terminal=false
Categories=AudioVideo;Video;Player;
`, exe)
	if err := os.WriteFile(path, []byte(entry), 0o755); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n\nNext, in Desktop Mode: Steam → Games → Add a Non-Steam Game to My Library → tick CastDeck → Add Selected Programs.\nThen launch CastDeck from your library in Gaming Mode.\n", path)
	return nil
}
