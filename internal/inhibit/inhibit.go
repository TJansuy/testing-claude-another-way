// Package inhibit keeps the screen awake while something is being watched.
//
// SteamOS gaming mode dims the screen and eventually suspends after a period
// without controller input, which is exactly what happens while watching a
// video. Video players avoid this by asking the session to inhibit idle over
// D-Bus; Steam's gaming-mode session and KDE desktop mode both implement the
// freedesktop ScreenSaver interface for this. We also take a logind "idle"
// inhibitor, which covers sessions that only consult logind.
package inhibit

import (
	"log"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	appName = "CastDeck"
	reason  = "Playing media"
)

// Inhibitor holds or releases the idle inhibitors on request.
type Inhibitor struct {
	mu      sync.Mutex
	active  bool
	session *dbus.Conn
	cookies []screensaverCookie
	logind  *os.File // logind inhibitor: held for as long as the fd is open
	warned  bool
}

type screensaverCookie struct {
	dest, path, iface string
	cookie            uint32
}

// screensaver interfaces to try, in order; several may exist at once.
var screensavers = []struct{ dest, path, iface string }{
	{"org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver", "org.freedesktop.ScreenSaver"},
	{"org.freedesktop.ScreenSaver", "/ScreenSaver", "org.freedesktop.ScreenSaver"},
	{"org.freedesktop.PowerManagement", "/org/freedesktop/PowerManagement/Inhibit", "org.freedesktop.PowerManagement.Inhibit"},
}

// Set turns inhibition on or off. It is cheap to call repeatedly.
func (in *Inhibitor) Set(on bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if on == in.active {
		return
	}
	in.active = on
	if on {
		in.acquire()
	} else {
		in.release()
	}
}

// Close releases everything.
func (in *Inhibitor) Close() {
	in.Set(false)
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.session != nil {
		in.session.Close()
		in.session = nil
	}
}

func (in *Inhibitor) acquire() {
	var errs []error
	if in.session == nil {
		// A private connection: inhibitors are dropped automatically if
		// CastDeck exits or crashes, because the connection closes.
		if c, err := dbus.SessionBusPrivate(); err == nil {
			if err = c.Auth(nil); err == nil {
				err = c.Hello()
			}
			if err != nil {
				c.Close()
				errs = append(errs, err)
			} else {
				in.session = c
			}
		} else {
			errs = append(errs, err)
		}
	}
	if in.session != nil {
		for _, s := range screensavers {
			var cookie uint32
			err := in.session.Object(s.dest, dbus.ObjectPath(s.path)).
				Call(s.iface+".Inhibit", 0, appName, reason).Store(&cookie)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			in.cookies = append(in.cookies, screensaverCookie{s.dest, s.path, s.iface, cookie})
		}
	}
	if sys, err := dbus.SystemBus(); err == nil {
		var fd dbus.UnixFD
		err := sys.Object("org.freedesktop.login1", "/org/freedesktop/login1").
			Call("org.freedesktop.login1.Manager.Inhibit", 0, "idle", appName, reason, "block").Store(&fd)
		if err == nil {
			in.logind = os.NewFile(uintptr(fd), "logind-inhibit")
		} else {
			errs = append(errs, err)
		}
	} else {
		errs = append(errs, err)
	}
	if len(in.cookies) == 0 && in.logind == nil {
		if !in.warned {
			in.warned = true
			log.Printf("inhibit: could not keep the screen awake: %v", errs)
		}
		return
	}
	log.Printf("inhibit: screen kept awake (%d screensaver, logind %v)", len(in.cookies), in.logind != nil)
}

func (in *Inhibitor) release() {
	for _, c := range in.cookies {
		if in.session != nil {
			_ = in.session.Object(c.dest, dbus.ObjectPath(c.path)).Call(c.iface+".UnInhibit", 0, c.cookie).Err
		}
	}
	in.cookies = nil
	if in.logind != nil {
		in.logind.Close()
		in.logind = nil
	}
	log.Printf("inhibit: released")
}
