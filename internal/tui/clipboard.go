package tui

import (
	"io"
	"os"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
)

// clipboardWriter is where the OSC52 fallback (see copyToClipboard) writes
// its escape sequence. Tests swap it out so they don't spray control codes
// at the test runner's stdout.
var clipboardWriter io.Writer = os.Stdout

// clipboardWriteAll sets the system clipboard via a native utility
// (xclip/xsel/wl-copy/pbcopy/clip.exe, whichever is found). Tests swap it
// out so they don't shell out to a real clipboard tool.
var clipboardWriteAll = clipboard.WriteAll

type yankResultMsg struct {
	label string
	err   error
}

// copyToClipboard sends s to the system clipboard, preferring a native
// clipboard utility since that's the only method that actually reaches the
// clipboard when gmesg is running inside tmux without `allow-passthrough`
// set (the default). If no such utility is available -- SSH'd into a
// headless box, say -- it falls back to an OSC52 escape sequence, which
// still works over SSH or inside tmux/screen with passthrough enabled.
func copyToClipboard(s, label string) tea.Cmd {
	return func() tea.Msg {
		if err := clipboardWriteAll(s); err == nil {
			return yankResultMsg{label: label}
		}
		seq := osc52.New(s)
		switch {
		case os.Getenv("TMUX") != "":
			seq = seq.Tmux()
		case strings.HasPrefix(os.Getenv("TERM"), "screen"):
			seq = seq.Screen()
		}
		_, err := io.WriteString(clipboardWriter, seq.String())
		return yankResultMsg{label: label, err: err}
	}
}
