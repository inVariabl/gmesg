package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Regression test: tab-completing a path in the attach prompt must leave the
// cursor at the end of the completed value. bubbles/textinput's SetValue
// only moves the cursor when the field was previously empty or the old
// position is out of bounds, so completing "~/Dow" -> "~/Downloads" left the
// cursor sitting right after "Dow" instead of at the end.
func TestHandleAttachKeyTabCompletionMovesCursorToEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := New(nil, "", nil)
	m.attaching = true
	m.attachInput.Focus()
	partial := filepath.Join(dir, "Dow")
	m.attachInput.SetValue(partial)
	m.attachInput.SetCursor(len(partial))

	updated, _ := m.handleAttachKey(tea.KeyMsg{Type: tea.KeyTab})
	model := updated.(Model)

	want := filepath.Join(dir, "Downloads") + "/"
	if got := model.attachInput.Value(); got != want {
		t.Fatalf("attachInput value = %q, want %q", got, want)
	}
	if got := model.attachInput.Position(); got != len(want) {
		t.Fatalf("attachInput cursor position = %d, want %d (end of value)", got, len(want))
	}
}
