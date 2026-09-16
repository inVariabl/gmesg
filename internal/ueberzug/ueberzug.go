// Package ueberzug drives a "ueberzug layer" subprocess to display images
// as an X11 overlay positioned over specific terminal cells. It's the
// classic way terminal file managers (ranger, lf) and editors show images
// from inside tmux, where escape-code graphics protocols (Kitty, iTerm2)
// don't reliably pass through a multiplexer -- ueberzug computes the
// overlay's pixel position itself (tmux-aware) from cell coordinates, so
// callers only ever deal in terminal columns/rows like everything else in
// a TUI.
package ueberzug

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// terminationGracePeriod is how long Close waits for the ueberzug process to
// exit after SIGTERM before escalating to SIGKILL. The legacy python build
// does not exit on stdin EOF, so a signal is required either way; this bound
// only covers processes that are slow to act on the signal.
const terminationGracePeriod = 500 * time.Millisecond

// Client drives a running `ueberzug layer` process over its JSON-parser
// stdin protocol. The zero value is not usable; construct with Start. A
// nil *Client is valid and makes every method a no-op, so callers don't
// need to branch on whether image preview is available.
type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

// Start launches ueberzug if the binary is on PATH. A nil Client and nil
// error together mean image preview simply isn't available in this
// environment (ueberzug not installed, or no display server detected) --
// that's a feature-detection result, not a failure worth surfacing to the
// user.
func Start() (*Client, error) {
	path, err := exec.LookPath("ueberzug")
	if err != nil {
		return nil, nil
	}

	args := []string{"layer", "--silent"}
	if isUeberzugpp(path) {
		// ueberzugpp supports several image output backends and picks one
		// itself when --output is omitted, but its auto-detection prefers
		// X11 whenever $DISPLAY is set -- which is also true under XWayland.
		// Its X11 backend overlays an override-redirect window on top of
		// the terminal, which doesn't composite reliably through wlroots
		// compositors (Hyprland, Sway, ...). Wayland sessions get the
		// native wlr-layer-shell backend instead, which those compositors
		// handle correctly; only genuine X11 sessions get --output x11.
		backend := outputBackend()
		if backend == "" {
			return nil, nil
		}
		args = append(args, "--output", backend)
	} else {
		// The legacy (unmaintained) python ueberzug: X11-only, has no
		// --output flag, and errors out on unrecognized arguments.
		if os.Getenv("DISPLAY") == "" {
			return nil, nil
		}
		args = append(args, "--parser", "json")
	}

	cmd := exec.Command(path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open ueberzug stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ueberzug: %w", err)
	}
	return &Client{cmd: cmd, stdin: stdin}, nil
}

// isUeberzugpp reports whether path is ueberzugpp's `ueberzug` compat
// binary (which understands --output) rather than the original python
// ueberzug (which doesn't). Feature-detected off `layer --help` rather than
// version strings, since ueberzugpp ships both an `ueberzugpp` binary and a
// same-CLI `ueberzug` compat shim depending on distro packaging.
func isUeberzugpp(path string) bool {
	out, err := exec.Command(path, "layer", "--help").CombinedOutput()
	if err != nil {
		return false
	}
	return bytes.Contains(out, []byte("--output"))
}

// outputBackend picks the ueberzugpp image output method for the current
// display server. Empty means no supported display server was detected.
func outputBackend() string {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return "wayland"
	}
	if os.Getenv("DISPLAY") != "" {
		return "x11"
	}
	return ""
}

// Add displays an image at the given cell position and size, scaled down
// to fit while preserving aspect ratio. Re-adding the same identifier
// moves/resizes/replaces it in place.
func (c *Client) Add(identifier, path string, x, y, width, height int) error {
	if c == nil {
		return nil
	}
	return c.send(map[string]any{
		"action":     "add",
		"identifier": identifier,
		"path":       path,
		"x":          x,
		"y":          y,
		"width":      width,
		"height":     height,
		"scaler":     "fit_contain",
	})
}

// Remove hides a previously added image. Removing an identifier that isn't
// showing is a harmless no-op as far as ueberzug is concerned.
func (c *Client) Remove(identifier string) error {
	if c == nil {
		return nil
	}
	return c.send(map[string]any{
		"action":     "remove",
		"identifier": identifier,
	})
}

func (c *Client) send(cmd map[string]any) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

// Close stops the ueberzug process. Safe to call on a nil Client.
//
// The legacy python ueberzug build does not exit on stdin EOF -- it keeps
// running until signaled -- so Close sends SIGTERM and escalates to SIGKILL
// if the process hasn't exited within terminationGracePeriod.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.stdin.Close()

	c.cmd.Process.Signal(syscall.SIGTERM)

	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-time.After(terminationGracePeriod):
		c.cmd.Process.Kill()
		return <-done
	}
}
