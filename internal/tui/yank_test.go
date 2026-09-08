package tui

import (
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// stubNativeClipboard makes clipboardWriteAll succeed without shelling out
// to a real clipboard tool, capturing whatever text it was given.
func stubNativeClipboard(t *testing.T) *string {
	t.Helper()
	var got string
	old := clipboardWriteAll
	clipboardWriteAll = func(s string) error {
		got = s
		return nil
	}
	t.Cleanup(func() { clipboardWriteAll = old })
	return &got
}

// failNativeClipboard makes clipboardWriteAll fail, as it does when no
// clipboard utility (xclip/xsel/wl-copy/...) is installed, so copyToClipboard
// falls back to its OSC52 path.
func failNativeClipboard(t *testing.T) {
	t.Helper()
	old := clipboardWriteAll
	clipboardWriteAll = func(string) error { return errors.New("no clipboard utility") }
	t.Cleanup(func() { clipboardWriteAll = old })
}

// osc52Payload extracts and base64-decodes the copied text out of an OSC52
// escape sequence (however it's wrapped for tmux/screen), so tests can
// assert on the text that was actually sent to the clipboard.
func osc52Payload(t *testing.T, seq string) string {
	t.Helper()
	m := regexp.MustCompile(`52;c;([A-Za-z0-9+/=]+)`).FindStringSubmatch(seq)
	if m == nil {
		t.Fatalf("no OSC52 payload found in %q", seq)
	}
	decoded, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("decode OSC52 payload: %v", err)
	}
	return string(decoded)
}

func pressKeys(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		updated, cmd := m.handleThreadNormalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		m = updated.(Model)
		if cmd != nil {
			updated, _ := m.Update(cmd())
			m = updated.(Model)
		}
	}
	return m
}

func TestYcYanksJustTheOneTimeCode(t *testing.T) {
	got := stubNativeClipboard(t)

	msg := textMessage("m1", "them", "Use code 47154471 to complete Extra Security at Sign In.", 1)
	m := newTestThreadModel(msg)

	m = pressKeys(t, m, "y", "c")

	if *got != "47154471" {
		t.Fatalf("expected the yanked text to be %q, got %q", "47154471", *got)
	}
	if m.status != "Yanked code" {
		t.Fatalf("expected status %q, got %q", "Yanked code", m.status)
	}
}

func TestYyYanksTheFullMessageText(t *testing.T) {
	got := stubNativeClipboard(t)

	text := "Use code 47154471 to complete Extra Security at Sign In."
	msg := textMessage("m1", "them", text, 1)
	m := newTestThreadModel(msg)

	m = pressKeys(t, m, "y", "y")

	if *got != text {
		t.Fatalf("expected the yanked text to be %q, got %q", text, *got)
	}
	if m.status != "Yanked message" {
		t.Fatalf("expected status %q, got %q", "Yanked message", m.status)
	}
}

func TestYlYanksTheFirstLink(t *testing.T) {
	got := stubNativeClipboard(t)

	text := "Check this out: https://example.com/foo and also https://example.com/bar"
	msg := textMessage("m1", "them", text, 1)
	m := newTestThreadModel(msg)

	m = pressKeys(t, m, "y", "l")

	if *got != "https://example.com/foo" {
		t.Fatalf("expected the yanked text to be %q, got %q", "https://example.com/foo", *got)
	}
	if m.status != "Yanked link" {
		t.Fatalf("expected status %q, got %q", "Yanked link", m.status)
	}
}

func TestYlWithNoLinkSetsAnError(t *testing.T) {
	stubNativeClipboard(t)

	msg := textMessage("m1", "them", "Running a bit late, be there in ten.", 1)
	m := newTestThreadModel(msg)

	m = pressKeys(t, m, "y", "l")

	if m.err == nil {
		t.Fatal("expected an error to be set when the message has no link")
	}
}

func TestYcWithNoCodeSetsAnError(t *testing.T) {
	stubNativeClipboard(t)

	msg := textMessage("m1", "them", "Running a bit late, be there in ten.", 1)
	m := newTestThreadModel(msg)

	m = pressKeys(t, m, "y", "c")

	if m.err == nil {
		t.Fatal("expected an error to be set when the message has no code")
	}
}

// When no native clipboard utility is available, copyToClipboard must still
// get the text to the terminal via an OSC52 escape sequence -- the only
// option left when gmesg is SSH'd into a headless box.
func TestYankFallsBackToOSC52WhenNoClipboardUtilityIsAvailable(t *testing.T) {
	failNativeClipboard(t)

	var out strings.Builder
	old := clipboardWriter
	clipboardWriter = &out
	defer func() { clipboardWriter = old }()

	msg := textMessage("m1", "them", "Use code 47154471 to complete Extra Security at Sign In.", 1)
	m := newTestThreadModel(msg)

	m = pressKeys(t, m, "y", "c")

	if got := osc52Payload(t, out.String()); got != "47154471" {
		t.Fatalf("expected the yanked text to be %q, got %q", "47154471", got)
	}
	if m.status != "Yanked code" {
		t.Fatalf("expected status %q, got %q", "Yanked code", m.status)
	}
}
