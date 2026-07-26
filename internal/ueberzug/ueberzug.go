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
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
)

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
// environment (ueberzug not installed) -- that's a feature-detection
// result, not a failure worth surfacing to the user.
func Start() (*Client, error) {
	path, err := exec.LookPath("ueberzug")
	if err != nil {
		return nil, nil
	}
	cmd := exec.Command(path, "layer", "--parser", "json", "--silent")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open ueberzug stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ueberzug: %w", err)
	}
	return &Client{cmd: cmd, stdin: stdin}, nil
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

// Close hides any showing images and stops the ueberzug process. Safe to
// call on a nil Client.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.stdin.Close()
	return c.cmd.Wait()
}
