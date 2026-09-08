package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}
	if m.loadingConversations {
		// Pad the dots out to a fixed width so the growing "..." doesn't
		// change the string's length and shift it within the centered
		// placement below.
		dots := strings.Repeat(".", m.loadingDots) + strings.Repeat(" ", 3-m.loadingDots)
		text := "Loading conversations" + dots
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, text)
	}

	var body string
	switch m.focus {
	case focusList:
		body = m.list.View()
		if m.startingConv {
			body += "\nNew conversation: " + m.newConvInput.View()
		}
	case focusThread:
		conv := m.conversations[m.activeConvID]
		title := "Conversation"
		if conv != nil {
			title = conv.GetName()
		}
		body = headerStyle.Render(title) + "\n" +
			m.thread.View() + "\n" +
			m.composeArea()
	}

	status := m.status
	if m.err != nil {
		status = "Error: " + m.err.Error()
	}
	if status == "" {
		switch {
		case m.commanding:
			status = m.commandInput.View()
		case m.focus == focusList && m.startingConv:
			status = "-- NEW CONVERSATION -- enter start · esc cancel"
		case m.focus == focusList:
			status = "j/k/gg/G navigate · enter open · / search · n new conversation · q quit"
		case m.reacting:
			status = "-- REACT -- h/l or 1-8 choose · enter send · esc cancel"
		case m.attaching:
			status = "-- ATTACH -- enter confirm path · esc cancel"
		case m.threadMode == modeNormal:
			status = "-- NORMAL -- r react · o open attachment/link · p preview image · i insert · q back"
		default:
			status = "-- INSERT -- enter send · ctrl-t attach · esc normal mode"
		}
	}
	line := statusStyle.Render(status)
	if m.err != nil {
		line = errStyle.Render(status)
	}
	if m.commanding {
		line = m.commandInput.View()
	}

	return strings.TrimRight(body, "\n") + "\n" + line
}

// composeArea renders whatever belongs below the thread viewport: the
// reaction picker, the attach-file prompt, or the compose box (with any
// pending attachment chips above it).
func (m Model) composeArea() string {
	if m.reacting {
		return renderReactionPicker(m.reactionCursor)
	}
	if m.attaching {
		return "Attach: " + m.attachInput.View()
	}
	if len(m.pendingAttachments) > 0 {
		return pendingAttachmentStyle.Render("📎 "+strings.Join(m.pendingAttachments, "  ")) + "\n" + m.compose.View()
	}
	return m.compose.View()
}

// renderReactionPicker draws reactionEmojis as a horizontal strip with the
// selected one highlighted.
func renderReactionPicker(cursor int) string {
	parts := make([]string, len(reactionEmojis))
	for i, e := range reactionEmojis {
		cell := " " + e + " "
		if i == cursor {
			cell = reactionPickerSelectedStyle.Render(cell)
		}
		parts[i] = cell
	}
	return "React: " + strings.Join(parts, "")
}
