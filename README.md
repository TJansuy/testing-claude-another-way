# CastDeck

CastDeck is a cast receiver for **SteamOS gaming mode**, packaged as one
portable executable. Start it on your Steam Deck (or any SteamOS/Linux PC on
your TV), and anyone on the same network can:

- **Cast from YouTube.** Open the YouTube app on a phone, tap **Cast**, pick
  the device. Works from Android and iPhone.
- **Send AirPlay video and photos** from apps that support AirPlay video URLs.
  If you install UxPlay, CastDeck can run it for iPhone screen mirroring.
- **Play any link** from the web remote at `http://<deck-ip>:8090/remote`.
  YouTube links open in YouTube, and direct media links (MP4, WebM, HLS, MP3,
  images) play in CastDeck's own player.

```
 phone ──SSDP/DIAL──▶ CastDeck ──launches──▶ youtube.com/tv (full screen)
   ▲                                                 │
   └────────── YouTube's pairing servers ◀───────────┘
 phone ──AirPlay────▶ CastDeck ──▶ built-in player (full screen)
 browser ──/remote──▶ CastDeck ──▶ YouTube or built-in player
```

## Install on a Steam Deck

1. Switch to **Desktop Mode**.
2. Install **Google Chrome** from the Discover store. CastDeck finds the Chrome
   Flatpak automatically. Chromium, Edge and Brave also work, and Firefox is
   used as a fallback.
3. Download `castdeck-…-linux-amd64.tar.gz` from the Releases page, or build it
   yourself (see below). Extract it somewhere permanent, for example
   `~/Applications/castdeck`.
4. Run it once with `--install` from a terminal (Konsole):
   ```sh
   chmod +x ~/Applications/castdeck
   ~/Applications/castdeck --install
   ```
   This writes a desktop entry.
5. In Steam: **Games → Add a Non-Steam Game to My Library…**, tick
   **CastDeck**, then click **Add Selected Programs**.
6. Go back to **Gaming Mode** and launch CastDeck from your library.

### Controller layout

In gaming mode, open CastDeck's **controller settings** and pick the
**Web Browser** template, or any template that maps the D-pad to the arrow
keys, A to Enter and B to Escape. YouTube's TV interface and CastDeck's home
screen are both driven by the arrow keys, Enter and Escape. CastDeck's own
screen also reads the gamepad directly.

| On CastDeck's player | Action                |
| -------------------- | --------------------- |
| A / Enter            | Pause / resume        |
| B / Esc              | Stop, back to home    |
| ◀ / ▶                | Seek −10 s / +30 s    |
| ▲ / ▼                | Volume                |

To quit, use **Exit** on the home screen or the Steam button → **Exit game**.

## How casting works

### YouTube (the "Cast" button)

The YouTube app casts to smart TVs and consoles over **DIAL**, not the
Chromecast protocol:

1. The phone sends an SSDP search on UDP 1900. CastDeck answers, so it shows
   up in YouTube's Cast list.
2. When you pick it, the phone asks CastDeck over HTTP to launch "YouTube"
   with a pairing code.
3. CastDeck opens the YouTube TV web app (`youtube.com/tv`) full screen with a
   TV user agent and passes the pairing code along.
4. The TV app pairs with your phone through YouTube's servers. From then on
   the phone acts as the remote: queue videos, pause, seek, and change volume.
   Several people can join the same session.

CastDeck goes back to its home screen when you leave YouTube with B / Esc, or
when the phone sends a stop request.

If the device doesn't show up in the Cast list (some networks block
multicast), choose **Open YouTube** on the home screen. Then use **Settings →
Link with TV code** in YouTube and enter the code in your phone's YouTube app.
That link is remembered across restarts.

### AirPlay

- **Built-in (default):** CastDeck advertises itself over Bonjour and
  implements the AirPlay *video* protocol. The sender hands over a video URL
  or a photo, and the Deck streams and plays it while the sender controls
  playback (play, pause, seek, stop, volume, progress).
- **UxPlay (`"airplay_mode": "uxplay"`):** CastDeck runs
  [UxPlay](https://github.com/FDH2/UxPlay) for full AirPlay 2 **screen
  mirroring**, which is what current iPhones need for most apps. UxPlay must
  be installed separately. Its window shows in gaming mode like CastDeck's.

### Web remote

The home screen shows the address, for example
`http://192.168.1.50:8090/remote`. Open it in any browser on the network to
paste a link, then pause, seek, stop, change volume, open YouTube, or go home.

## Limitations

- **This is not a Chromecast.** Google only lets certified devices be
  Chromecast receivers. Apps that cast *only* over the Chromecast protocol
  (Netflix, Spotify, Chrome "Cast tab") won't see CastDeck. YouTube is
  supported because it also casts over DIAL.
- **Built-in AirPlay is the original video protocol.** Recent iOS versions
  mostly show only AirPlay 2 receivers, and Apple's encrypted screen-mirroring
  protocol isn't built in. iPhone users should use the YouTube app's **Cast**
  button for YouTube, the web remote for links, or UxPlay mode for mirroring.
- **The YouTube TV web app is a Google service.** If Google changes how
  `youtube.com/tv` decides which browsers get the TV interface, change
  `user_agent` in the config file.
- Phones must be on the **same network and subnet** as the Deck. Guest Wi-Fi
  networks often block devices from seeing each other.

## Configuration

On first run CastDeck writes `~/.config/castdeck/config.json`:

```json
{
  "name": "CastDeck (steamdeck)",
  "http_port": 8090,
  "airplay_port": 7000,
  "airplay_mode": "builtin",
  "dial_enabled": true,
  "browser": "auto",
  "browser_extra_args": null,
  "user_agent": "Mozilla/5.0 (X11; Linux x86_64) … Cobalt/25.lts…",
  "youtube_url": "https://www.youtube.com/tv",
  "uxplay_extra_args": null
}
```

- **`name`**: the name phones see.
- **`browser`**: `"auto"`, or a command such as
  `"flatpak run com.google.Chrome"` or `"/usr/bin/chromium"`.
- **`browser_extra_args`**: extra browser flags, for example
  `["--force-device-scale-factor=1.25"]` for bigger text on a TV.
- **`airplay_mode`**: `"builtin"`, `"uxplay"` or `"off"`.

Command-line flags override the file:

```
castdeck [--name NAME] [--port 8090] [--airplay builtin|uxplay|off]
         [--airplay-port 7000] [--no-dial] [--browser CMD] [--no-browser]
         [--config DIR] [--install] [--version]
```

`--no-browser` runs only the network services, without a screen. It's useful
for testing.

Logs are in `~/.config/castdeck/castdeck.log`. The browser's own output is in
`browser.log` in the same folder.

### Network ports

| Port      | Protocol | Used for                                       |
| --------- | -------- | ---------------------------------------------- |
| 1900/udp  | SSDP     | YouTube discovery                              |
| 5353/udp  | mDNS     | AirPlay discovery                              |
| 8090/tcp  | HTTP     | DIAL, web remote, on-screen UI                 |
| 7000/tcp  | HTTP     | AirPlay (built-in mode)                        |

SteamOS doesn't run a firewall by default. If you added one, open these ports.

## Building

You need Go (the version in `go.mod`; the `go` command downloads it if needed).

```sh
make build   # → build/castdeck (static x86_64 Linux binary, no dependencies)
make dist    # → dist/castdeck-<version>-linux-{amd64,arm64}.tar.gz
make test
```

The binary is fully static (`CGO_ENABLED=0`). The web UI is embedded, so the
single file is the whole program. GitHub Actions builds every push and
attaches release archives to `v*` tags.

## Project layout

```
cmd/castdeck/        entry point, flags, wiring
internal/app/        screen controller: home ⇄ YouTube, browser lifecycle
internal/browser/    browser detection (Flatpak/PATH) and kiosk launching
internal/dial/       DIAL server + SSDP discovery (YouTube casting)
internal/airplay/    AirPlay video/photo receiver, Bonjour, UxPlay supervisor
internal/player/     shared playback state, pushed to pages over SSE
internal/web/        HTTP API, on-screen page, web remote (embedded assets)
internal/config/     config file and persistent device identity
```
