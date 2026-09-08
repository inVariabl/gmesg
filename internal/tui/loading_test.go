package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestLoadingScreenIsTheOnlyThingShownUntilConversationsLoad verifies the
// startup loading screen replaces the whole view (no list, no status line)
// while conversations are still being fetched, and that it's centered on
// the terminal.
func TestLoadingScreenIsTheOnlyThingShownUntilConversationsLoad(t *testing.T) {
	m := New(nil, "", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	view := m.View()
	if !strings.Contains(view, "Loading conversations") {
		t.Fatalf("expected the loading screen to mention loading conversations, got %q", view)
	}
	if strings.Contains(view, "Conversations") {
		t.Fatalf("expected the conversation list title to be hidden during loading, got %q", view)
	}

	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) != 24 {
		t.Fatalf("expected the loading screen to fill all 24 rows, got %d", len(lines))
	}
}

// TestLoadingDotsAnimate verifies the "..." after "Loading conversations"
// advances on each loadingTickMsg and wraps back around after three dots.
func TestLoadingDotsAnimate(t *testing.T) {
	m := New(nil, "", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	wantDots := []int{2, 3, 1, 2}
	for _, want := range wantDots {
		updated, _ := m.Update(loadingTickMsg{})
		m = updated.(Model)
		if m.loadingDots != want {
			t.Fatalf("expected loadingDots to be %d, got %d", want, m.loadingDots)
		}
	}
}

// TestLoadingScreenTextDoesNotShiftAsDotsAnimate verifies "Loading
// conversations" starts at the same column on every frame of the dot
// animation -- a growing "..." changes the string's length, which shifts
// where lipgloss.Place centers it unless the dots are padded to a fixed
// width.
func TestLoadingScreenTextDoesNotShiftAsDotsAnimate(t *testing.T) {
	m := New(nil, "", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	col := func(view string) int {
		for _, line := range strings.Split(view, "\n") {
			if i := strings.Index(line, "Loading conversations"); i >= 0 {
				return i
			}
		}
		t.Fatalf("expected a line containing %q, got %q", "Loading conversations", view)
		return -1
	}

	first := col(m.View())
	for i := 0; i < 3; i++ {
		updated, _ := m.Update(loadingTickMsg{})
		m = updated.(Model)
		if got := col(m.View()); got != first {
			t.Fatalf("expected the text to stay at column %d, got %d on frame %d", first, got, i+1)
		}
	}
}

// TestLoadingTickStopsOnceConversationsLoad verifies the animation stops
// rescheduling itself once conversations have finished loading, rather than
// ticking forever in the background.
func TestLoadingTickStopsOnceConversationsLoad(t *testing.T) {
	m := New(nil, "", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, cmd := m.Update(conversationsLoadedMsg{})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no command from conversationsLoadedMsg, got one")
	}

	updated, cmd = m.Update(loadingTickMsg{})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("expected loadingTickMsg to stop rescheduling once loaded")
	}

	view := m.View()
	if strings.Contains(view, "Loading conversations") {
		t.Fatalf("expected the loading screen to be gone once conversations load, got %q", view)
	}
}
