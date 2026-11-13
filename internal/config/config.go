// Package config loads CastDeck settings and the persistent device identity.
package config

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultUserAgent is a "living room device" user agent. youtube.com/tv only
// serves the TV interface (the one that can be cast to) to TV-like browsers.
const DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) " +
	"Chrome/120.0.0.0 Safari/537.36 Cobalt/25.lts.30.1034943-gold (unlike Gecko), " +
	"Unknown_TV_Unknown_0/Unknown (Unknown, Unknown)"

// Config holds user-editable settings. It is stored as JSON in
// ~/.config/castdeck/config.json and every field can be overridden by a flag.
type Config struct {
	Name        string `json:"name"`
	HTTPPort    int    `json:"http_port"`
	AirPlayPort int    `json:"airplay_port"`
	// AirPlayMode is "builtin" (AirPlay video/photo URL casting handled by
	// CastDeck), "uxplay" (supervise an installed UxPlay for screen
	// mirroring) or "off".
	AirPlayMode string `json:"airplay_mode"`
	DIAL        bool   `json:"dial_enabled"`
	// Browser is "auto" or an explicit command line, e.g.
	// "flatpak run com.google.Chrome" or "/usr/bin/chromium".
	Browser          string   `json:"browser"`
	BrowserExtraArgs []string `json:"browser_extra_args"`
	UserAgent        string   `json:"user_agent"`
	YouTubeURL       string   `json:"youtube_url"`
	UxPlayExtraArgs  []string `json:"uxplay_extra_args"`

	// Runtime-only values.
	NoBrowser bool   `json:"-"`
	Dir       string `json:"-"`
}

// Defaults returns the built-in configuration.
func Defaults() *Config {
	host, _ := os.Hostname()
	name := "CastDeck"
	if host != "" {
		name = fmt.Sprintf("CastDeck (%s)", host)
	}
	return &Config{
		Name:        name,
		HTTPPort:    8090,
		AirPlayPort: 7000,
		AirPlayMode: "builtin",
		DIAL:        true,
		Browser:     "auto",
		UserAgent:   DefaultUserAgent,
		YouTubeURL:  "https://www.youtube.com/tv",
	}
}

// DefaultDir returns ~/.config/castdeck (respecting XDG_CONFIG_HOME).
func DefaultDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "castdeck")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".castdeck"
	}
	return filepath.Join(home, ".config", "castdeck")
}

// Load reads dir/config.json over the defaults. When the file does not exist
// a commented-by-example default file is written so it is easy to edit.
func Load(dir string) (*Config, error) {
	cfg := Defaults()
	cfg.Dir = dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	case os.IsNotExist(err):
		if out, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			_ = os.WriteFile(path, append(out, '\n'), 0o644)
		}
	default:
		return nil, err
	}
	cfg.AirPlayMode = strings.ToLower(strings.TrimSpace(cfg.AirPlayMode))
	return cfg, nil
}

// Identity is the stable identity advertised to the network, so phones keep
// recognising the device between runs.
type Identity struct {
	UUID     string `json:"uuid"`
	DeviceID string `json:"device_id"` // MAC-style id used by AirPlay
}

// LoadIdentity reads or creates dir/identity.json.
func LoadIdentity(dir string) (*Identity, error) {
	path := filepath.Join(dir, "identity.json")
	var id Identity
	if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &id) == nil && id.UUID != "" && id.DeviceID != "" {
		return &id, nil
	}
	b := make([]byte, 22)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	u := b[:16]
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	id.UUID = fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	m := b[16:22]
	m[0] = (m[0] | 0x02) & 0xfe // locally administered, unicast
	id.DeviceID = strings.ToUpper(fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", m[0], m[1], m[2], m[3], m[4], m[5]))
	out, _ := json.MarshalIndent(&id, "", "  ")
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return nil, err
	}
	return &id, nil
}
