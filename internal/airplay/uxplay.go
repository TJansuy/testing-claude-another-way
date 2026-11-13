package airplay

import (
	"context"
	"errors"
	"io"
	"log"
	"os/exec"
	"time"
)

// UxPlay supervises an installed UxPlay (https://github.com/FDH2/UxPlay),
// an open-source AirPlay 2 mirroring server. Its video window appears on the
// gaming-mode screen just like CastDeck's own browser window.
type UxPlay struct {
	Name  string
	Extra []string
	Log   io.Writer
}

// Available reports whether uxplay is on PATH.
func UxPlayAvailable() bool {
	_, err := exec.LookPath("uxplay")
	return err == nil
}

// Run keeps uxplay running until ctx is done.
func (u *UxPlay) Run(ctx context.Context) error {
	bin, err := exec.LookPath("uxplay")
	if err != nil {
		return errors.New("airplay_mode is \"uxplay\" but uxplay is not installed")
	}
	backoff := time.Second
	for ctx.Err() == nil {
		args := append([]string{"-n", u.Name, "-nh", "-fs"}, u.Extra...)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdout, cmd.Stderr = u.Log, u.Log
		start := time.Now()
		log.Printf("uxplay: starting AirPlay mirroring receiver")
		err := cmd.Run()
		if ctx.Err() != nil {
			return nil
		}
		log.Printf("uxplay: exited: %v", err)
		if time.Since(start) > time.Minute {
			backoff = time.Second
		} else if backoff < time.Minute {
			backoff *= 2
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
	}
	return nil
}
