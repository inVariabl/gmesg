package tui

import "strings"

func (m Model) View() string {
	if !m.ready {
		return "Loading..."
	}

	var body string
	switch m.focus {
	case focusList:
		body = m.list.View()
	case focusThread:
		conv := m.conversations[m.activeConvID]
		title := "Conversation"
		if conv != nil {
			title = conv.GetName()
		}
		body = headerStyle.Render(title) + "\n" +
			m.thread.View() + "\n" +
			m.compose.View()
	}

	status := m.status
	if m.err != nil {
		status = "Error: " + m.err.Error()
	}
	if status == "" {
		switch {
		case m.focus == focusList:
			status = "j/k/gg/G navigate · enter open · q quit"
		case m.threadMode == modeNormal:
			status = "-- NORMAL -- j/k/gg/G/ctrl-d/ctrl-u scroll · i insert · q back"
		default:
			status = "-- INSERT -- enter send · esc normal mode"
		}
	}
	line := statusStyle.Render(status)
	if m.err != nil {
		line = errStyle.Render(status)
	}

	return strings.TrimRight(body, "\n") + "\n" + line
}
