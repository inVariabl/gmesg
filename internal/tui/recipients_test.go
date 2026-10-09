package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/inVariabl/gmesg/internal/contacts"
)

func newRecipientTestModel(t *testing.T) Model {
	t.Helper()
	m := New(nil, "", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	m.loadingConversations = false
	m.contactList = []contacts.Contact{
		{Name: "Ada Lovelace", Numbers: []contacts.Number{{Value: "5550100001", Label: "mobile"}, {Value: "5550100002", Label: "phone"}}},
		{Name: "Charles Babbage", Numbers: []contacts.Number{{Value: "5550100003", Label: "mobile"}}},
	}
	m.beginNewConversation()
	return m
}

func typeKeys(m Model, keys ...tea.KeyMsg) Model {
	for _, k := range keys {
		updated, _ := m.handleNewConvKey(k)
		m = updated.(Model)
	}
	return m
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestRecipientPromptListsOneCandidatePerNumber(t *testing.T) {
	m := typeKeys(newRecipientTestModel(t), runes("ada"))
	if len(m.recipientMatches) != 2 {
		t.Fatalf("expected one candidate for each of Ada's two numbers, got %+v", m.recipientMatches)
	}
	view := m.View()
	if !strings.Contains(view, "mobile 5550100001") || !strings.Contains(view, "phone 5550100002") {
		t.Fatalf("expected both numbers to be listed with their labels, got %q", view)
	}
	if lines := strings.Count(strings.TrimRight(view, "\n"), "\n") + 1; lines > 24 {
		t.Fatalf("view is %d rows, exceeding the 24-row terminal", lines)
	}
}

func TestRecipientPromptBuildsGroupAndStarts(t *testing.T) {
	m := newRecipientTestModel(t)
	m = typeKeys(m, runes("ada"), tea.KeyMsg{Type: tea.KeyCtrlN}, tea.KeyMsg{Type: tea.KeyTab})
	m = typeKeys(m, runes("babb"), tea.KeyMsg{Type: tea.KeyEnter})
	m = typeKeys(m, runes("555 010 0009"), runes(","))

	var got []string
	for _, r := range m.recipients {
		got = append(got, r.Number.Value)
	}
	if want := "5550100002,5550100003,555 010 0009"; strings.Join(got, ",") != want {
		t.Fatalf("recipients = %v, want %s", got, want)
	}

	m = typeKeys(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if len(m.recipients) != 2 {
		t.Fatalf("backspace on an empty prompt must remove the last recipient, got %+v", m.recipients)
	}

	updated, cmd := m.handleNewConvKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.startingConv || cmd == nil {
		t.Fatalf("enter on an empty prompt must start the conversation (startingConv=%v, cmd=%v)", m.startingConv, cmd)
	}
}

func TestRecipientPromptRejectsUnknownName(t *testing.T) {
	m := typeKeys(newRecipientTestModel(t), runes("zelda"), tea.KeyMsg{Type: tea.KeyEnter})
	if m.err == nil || len(m.recipients) != 0 || !m.startingConv {
		t.Fatalf("an unmatched name must report an error and keep the prompt open (err=%v, recipients=%+v)", m.err, m.recipients)
	}
}
