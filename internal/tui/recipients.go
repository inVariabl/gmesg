package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/inVariabl/gmesg/internal/contacts"
	"github.com/inVariabl/gmesg/internal/ipc"
)

// recipientMatchLimit is the number of completion candidates listed below
// the new-conversation prompt.
const recipientMatchLimit = 6

var recipientStyle = lipgloss.NewStyle().Foreground(accentColor).Bold(true)
var candidateStyle = lipgloss.NewStyle().Padding(0, 0, 0, 2)
var selectedCandidateStyle = lipgloss.NewStyle().Reverse(true).Padding(0, 0, 0, 2)
var candidateDetailStyle = lipgloss.NewStyle().Faint(true)

type contactsLoadedMsg struct {
	contacts []contacts.Contact
	err      error
}

func loadContacts(ipcClient *ipc.Client) tea.Cmd {
	if ipcClient == nil {
		return nil
	}
	return func() tea.Msg {
		var result struct {
			Contacts []contacts.Contact `json:"contacts"`
		}
		if err := ipcClient.Call("list_contacts", nil, &result); err != nil {
			return contactsLoadedMsg{err: err}
		}
		return contactsLoadedMsg{contacts: result.Contacts}
	}
}

// beginNewConversation opens the new-conversation prompt with no
// recipients and refreshes the contact list, so that an address book edit
// made while the TUI is running is offered for completion.
func (m *Model) beginNewConversation() tea.Cmd {
	m.startingConv = true
	m.focus = focusList
	m.recipients = nil
	m.recipientMatches = nil
	m.recipientMatchIdx = 0
	m.newConvInput.Reset()
	return tea.Batch(m.newConvInput.Focus(), loadContacts(m.ipcClient))
}

func (m *Model) endNewConversation() {
	m.startingConv = false
	m.recipients = nil
	m.recipientMatches = nil
	m.recipientMatchIdx = 0
	m.newConvInput.Reset()
	m.newConvInput.Blur()
}

// handleNewConvKey drives the recipient prompt. Typing a name lists the
// matching contacts; Tab or Enter adds the highlighted one as a recipient.
// A telephone number may be typed directly and is added on Enter or a
// comma. Enter on an empty prompt starts the conversation: a direct
// conversation for one recipient, a group for several.
func (m Model) handleNewConvKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	query := strings.TrimSpace(m.newConvInput.Value())

	switch msg.String() {
	case "esc":
		m.endNewConversation()
		return m, nil

	case "down", "ctrl+n":
		if len(m.recipientMatches) > 0 {
			m.recipientMatchIdx = (m.recipientMatchIdx + 1) % len(m.recipientMatches)
		}
		return m, nil

	case "up", "ctrl+p":
		if len(m.recipientMatches) > 0 {
			m.recipientMatchIdx = (m.recipientMatchIdx - 1 + len(m.recipientMatches)) % len(m.recipientMatches)
		}
		return m, nil

	case "tab":
		if len(m.recipientMatches) > 0 {
			m.addRecipient(m.recipientMatches[m.recipientMatchIdx])
		}
		return m, nil

	case ",":
		if contacts.IsNumberLike(query) {
			m.addRecipient(contacts.Candidate{Number: contacts.Number{Value: query}})
			return m, nil
		}
		if len(m.recipientMatches) > 0 {
			m.addRecipient(m.recipientMatches[m.recipientMatchIdx])
		}
		return m, nil

	case "backspace":
		if m.newConvInput.Value() == "" && len(m.recipients) > 0 {
			m.recipients = m.recipients[:len(m.recipients)-1]
			return m, nil
		}

	case "enter":
		if query != "" {
			// A typed number is taken as written even when it also
			// matches a contact's number, so the prompt never substitutes
			// a different number for the one entered.
			switch {
			case contacts.IsNumberLike(query):
				m.addRecipient(contacts.Candidate{Number: contacts.Number{Value: query}})
			case len(m.recipientMatches) > 0:
				m.addRecipient(m.recipientMatches[m.recipientMatchIdx])
			default:
				m.err = fmt.Errorf("no contact matches %q", query)
			}
			return m, nil
		}
		if len(m.recipients) == 0 {
			return m, nil
		}
		phones := make([]string, len(m.recipients))
		for i, r := range m.recipients {
			phones[i] = r.Number.Value
		}
		m.endNewConversation()
		m.status = "Starting conversation..."
		return m, resolveConversation(m.ipcClient, phones)
	}

	var cmd tea.Cmd
	m.newConvInput, cmd = m.newConvInput.Update(msg)
	m.err = nil
	m.refreshRecipientMatches()
	return m, cmd
}

// addRecipient appends c unless a recipient with the same number is
// already present, then clears the prompt for the next name.
func (m *Model) addRecipient(c contacts.Candidate) {
	key := contacts.NormalizeNumber(c.Number.Value)
	duplicate := false
	for _, r := range m.recipients {
		if contacts.NormalizeNumber(r.Number.Value) == key {
			duplicate = true
			break
		}
	}
	if !duplicate {
		m.recipients = append(m.recipients, c)
	}
	m.newConvInput.Reset()
	m.recipientMatches = nil
	m.recipientMatchIdx = 0
}

func (m *Model) refreshRecipientMatches() {
	m.recipientMatches = contacts.Search(m.contactList, m.newConvInput.Value(), recipientMatchLimit)
	if m.recipientMatchIdx >= len(m.recipientMatches) {
		m.recipientMatchIdx = 0
	}
}

// newConversationView renders the recipient prompt and, beneath it, the
// completion candidates for the name being typed.
func (m Model) newConversationView() string {
	var b strings.Builder
	b.WriteString("To: ")
	for _, r := range m.recipients {
		label := r.Name
		if label == "" {
			label = r.Number.Value
		}
		b.WriteString(recipientStyle.Render(label))
		b.WriteString(", ")
	}
	input := m.newConvInput
	if w := m.width - lipgloss.Width(b.String()) - 1; w > 0 {
		input.Width = w
	}
	b.WriteString(input.View())

	for i, c := range m.recipientMatches {
		detail := c.Number.Value
		if c.Number.Label != "" {
			detail = c.Number.Label + " " + detail
		}
		row := normalizeColumnWidth(c.Name) + "  " + candidateDetailStyle.Render(detail)
		style := candidateStyle
		if i == m.recipientMatchIdx {
			style = selectedCandidateStyle
		}
		b.WriteString("\n")
		b.WriteString(style.Render(row))
	}
	return b.String()
}
