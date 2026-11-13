// Package browser finds a web browser on the system and runs it full-screen
// (kiosk mode). In SteamOS gaming mode, any window started by the game's
// process tree is shown by gamescope, so the browser becomes "the screen".
package browser

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Kind is the browser engine, which decides the command-line flags we use.
type Kind int

const (
	Chromium Kind = iota
	Firefox
)

// Spec describes how to start a particular browser.
type Spec struct {
	Name       string
	Argv       []string // command prefix, e.g. ["flatpak", "run", "com.google.Chrome"]
	Kind       Kind
	ProfileDir string
}

type candidate struct {
	name    string
	flatpak string // flatpak app id, or "" for a binary on PATH
	bin     string
	kind    Kind
}

var candidates = []candidate{
	{name: "Google Chrome (Flatpak)", flatpak: "com.google.Chrome", kind: Chromium},
	{name: "Chromium (Flatpak)", flatpak: "org.chromium.Chromium", kind: Chromium},
	{name: "Microsoft Edge (Flatpak)", flatpak: "com.microsoft.Edge", kind: Chromium},
	{name: "Brave (Flatpak)", flatpak: "com.brave.Browser", kind: Chromium},
	{name: "Google Chrome", bin: "google-chrome-stable", kind: Chromium},
	{name: "Google Chrome", bin: "google-chrome", kind: Chromium},
	{name: "Chromium", bin: "chromium", kind: Chromium},
	{name: "Chromium", bin: "chromium-browser", kind: Chromium},
	{name: "Brave", bin: "brave-browser", kind: Chromium},
	{name: "Microsoft Edge", bin: "microsoft-edge-stable", kind: Chromium},
	{name: "Firefox (Flatpak)", flatpak: "org.mozilla.firefox", kind: Firefox},
	{name: "Firefox", bin: "firefox", kind: Firefox},
}

// Detect picks a browser. pref is "auto" or an explicit command line.
func Detect(pref string) (*Spec, error) {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(xdgData(home), "castdeck")
	pref = strings.TrimSpace(pref)
	if pref != "" && pref != "auto" {
		argv := strings.Fields(pref)
		kind := Chromium
		if strings.Contains(strings.ToLower(pref), "firefox") {
			kind = Firefox
		}
		s := &Spec{Name: pref, Argv: argv, Kind: kind, ProfileDir: filepath.Join(dataDir, "profile-custom")}
		if len(argv) >= 3 && filepath.Base(argv[0]) == "flatpak" && argv[1] == "run" {
			s.ProfileDir = flatpakProfile(home, argv[len(argv)-1])
		}
		return s, nil
	}
	_, flatpakErr := exec.LookPath("flatpak")
	for _, c := range candidates {
		if c.flatpak != "" {
			if flatpakErr != nil || exec.Command("flatpak", "info", c.flatpak).Run() != nil {
				continue
			}
			return &Spec{Name: c.name, Argv: []string{"flatpak", "run", c.flatpak}, Kind: c.kind,
				ProfileDir: flatpakProfile(home, c.flatpak)}, nil
		}
		if p, err := exec.LookPath(c.bin); err == nil {
			return &Spec{Name: c.name, Argv: []string{p}, Kind: c.kind,
				ProfileDir: filepath.Join(dataDir, "profile-"+c.bin)}, nil
		}
	}
	return nil, errors.New("no supported browser found: install Google Chrome from the Discover store " +
		"(Flatpak com.google.Chrome) or set \"browser\" in the config file")
}

// A Flatpak browser can always write inside its own ~/.var/app directory.
func flatpakProfile(home, appID string) string {
	return filepath.Join(home, ".var", "app", appID, "data", "castdeck-profile")
}

func xdgData(home string) string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	return filepath.Join(home, ".local", "share")
}

// Command builds the full argv to open url.
func (s *Spec) Command(url, userAgent string, extra []string) []string {
	argv := append([]string{}, s.Argv...)
	switch s.Kind {
	case Firefox:
		argv = append(argv, "--no-remote", "--kiosk", "--profile", s.ProfileDir)
	default:
		argv = append(argv,
			"--user-data-dir="+s.ProfileDir,
			"--kiosk",
			"--start-fullscreen",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-session-crashed-bubble",
			"--hide-crash-restore-bubble",
			"--noerrdialogs",
			"--disable-infobars",
			"--password-store=basic", // never pop a keyring dialog in gaming mode
			"--autoplay-policy=no-user-gesture-required",
			// youtube.com/tv reports its pairing data to CastDeck on
			// 127.0.0.1; don't let local-network-access checks block that.
			"--disable-features=PrivateNetworkAccessSendPreflights,PrivateNetworkAccessRespectPreflightResults,"+
				"BlockInsecurePrivateNetworkRequests,LocalNetworkAccessChecks,Translate,MediaRouter",
		)
		if userAgent != "" {
			argv = append(argv, "--user-agent="+userAgent)
		}
	}
	argv = append(argv, extra...)
	return append(argv, url)
}

// Prepare creates the profile directory and, for Firefox, writes preferences
// (Firefox has no command-line flag for the user agent or autoplay).
func (s *Spec) Prepare(userAgent string) error {
	if err := os.MkdirAll(s.ProfileDir, 0o755); err != nil {
		return err
	}
	if s.Kind != Firefox {
		return nil
	}
	prefs := []string{
		`user_pref("media.autoplay.default", 0);`,
		`user_pref("media.autoplay.blocking_policy", 0);`,
		`user_pref("browser.shell.checkDefaultBrowser", false);`,
		`user_pref("browser.sessionstore.resume_from_crash", false);`,
		`user_pref("browser.aboutwelcome.enabled", false);`,
		`user_pref("datareporting.policy.dataSubmissionPolicyBypassNotification", true);`,
		`user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);`,
		`user_pref("media.ffmpeg.vaapi.enabled", true);`,
	}
	if userAgent != "" {
		prefs = append(prefs, fmt.Sprintf(`user_pref("general.useragent.override", %q);`, userAgent))
	}
	return os.WriteFile(filepath.Join(s.ProfileDir, "user.js"), []byte(strings.Join(prefs, "\n")+"\n"), 0o644)
}

// resetSession stops Chromium from restoring the previous window's tabs.
// We switch screens by restarting the browser, which Chromium records as a
// crash and would otherwise "helpfully" undo on the next start.
func (s *Spec) resetSession() {
	if s.Kind != Chromium {
		return
	}
	def := filepath.Join(s.ProfileDir, "Default")
	for _, d := range []string{"Sessions", "Sessions_Encrypted", "Current Session", "Current Tabs", "Last Session", "Last Tabs"} {
		_ = os.RemoveAll(filepath.Join(def, d))
	}
	prefs := filepath.Join(def, "Preferences")
	if data, err := os.ReadFile(prefs); err == nil {
		fixed := strings.ReplaceAll(string(data), `"exit_type":"Crashed"`, `"exit_type":"Normal"`)
		fixed = strings.ReplaceAll(fixed, `"exited_cleanly":false`, `"exited_cleanly":true`)
		if fixed != string(data) {
			_ = os.WriteFile(prefs, []byte(fixed), 0o600)
		}
	}
}

// Exit reports that a browser process ended.
type Exit struct {
	Gen     int
	Err     error
	Elapsed time.Duration
}

// Controller runs at most one browser window at a time.
type Controller struct {
	spec  *Spec
	ua    string
	extra []string
	logW  io.Writer

	mu   sync.Mutex
	gen  int
	cur  *exec.Cmd
	done chan struct{}

	// Exits receives an event whenever a browser process ends.
	Exits chan Exit
}

// NewController creates a controller. Browser output goes to logW.
func NewController(spec *Spec, userAgent string, extra []string, logW io.Writer) (*Controller, error) {
	if err := spec.Prepare(userAgent); err != nil {
		return nil, err
	}
	if logW == nil {
		logW = io.Discard
	}
	return &Controller{spec: spec, ua: userAgent, extra: extra, logW: logW, Exits: make(chan Exit, 4)}, nil
}

// Name returns the human readable browser name.
func (c *Controller) Name() string { return c.spec.Name }

// Open shows url, replacing whatever the browser currently shows. It returns
// the generation number of the new process.
func (c *Controller) Open(url string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
	c.spec.resetSession()
	argv := c.spec.Command(url, c.ua, c.extra)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = c.logW, c.logW
	cmd.SysProcAttr = procAttr()
	log.Printf("browser: opening %s", redact(url))
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", c.spec.Name, err)
	}
	c.gen++
	gen, done, start := c.gen, make(chan struct{}), time.Now()
	c.cur, c.done = cmd, done
	go func() {
		err := cmd.Wait()
		close(done)
		select {
		case c.Exits <- Exit{Gen: gen, Err: err, Elapsed: time.Since(start)}:
		default:
		}
	}()
	return gen, nil
}

// Current returns the generation of the running browser (0 if none).
func (c *Controller) Current() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur == nil {
		return 0
	}
	select {
	case <-c.done:
		return 0
	default:
		return c.gen
	}
}

// Close stops the browser.
func (c *Controller) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

func (c *Controller) stopLocked() {
	if c.cur == nil {
		return
	}
	cmd, done := c.cur, c.done
	c.cur = nil
	select {
	case <-done:
		return
	default:
	}
	terminate(cmd)
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		kill(cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			log.Printf("browser: process %d did not exit", cmd.Process.Pid)
		}
	}
	// Give Chromium a moment to drop its profile lock before relaunching.
	time.Sleep(300 * time.Millisecond)
}

// redact hides pairing codes from logs.
func redact(u string) string {
	if i := strings.Index(u, "pairingCode="); i >= 0 {
		end := strings.IndexByte(u[i:], '&')
		if end < 0 {
			return u[:i] + "pairingCode=…"
		}
		return u[:i] + "pairingCode=…" + u[i+end:]
	}
	return u
}
