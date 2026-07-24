package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/inVariabl/gmesg/internal/gmclient"
)

const composeHeight = 2

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.list.SetSize(msg.Width, msg.Height-1) // -1 for the status line
		m.thread.Width = msg.Width
		m.thread.Height = msg.Height - composeHeight - 3 // header + status + compose border
		m.compose.SetWidth(msg.Width)
		m.compose.SetHeight(composeHeight)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case conversationsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = ""
			return m, nil
		}
		for _, conv := range msg.convs {
			m.conversations[conv.GetConversationID()] = conv
		}
		m.status = ""
		m.refreshListItems()
		return m, nil

	case threadLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.messages[msg.convID] = msg.msgs
		if msg.convID == m.activeConvID {
			m.renderThread(msg.convID)
		}
		return m, nil

	case sendResultMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = ""
		} else {
			m.status = ""
		}
		return m, nil

	case gmEventMsg:
		cmd := m.handleGMEvent(msg.evt)
		return m, tea.Batch(cmd, waitForEvent(m.events))
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.client.Disconnect()
		return m, tea.Quit
	}

	switch m.focus {
	case focusList:
		switch msg.String() {
		case "q":
			m.client.Disconnect()
			return m, tea.Quit
		case "enter":
			item, ok := m.list.SelectedItem().(conversationItem)
			if !ok {
				return m, nil
			}
			m.activeConvID = item.conv.GetConversationID()
			m.focus = focusThread
			m.threadMode = modeNormal
			m.compose.Blur()
			m.renderThread(m.activeConvID)
			return m, fetchThread(m.client, m.activeConvID)
		}
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd

	case focusThread:
		switch m.threadMode {
		case modeNormal:
			return m.handleThreadNormalKey(msg)
		case modeInsert:
			return m.handleThreadInsertKey(msg)
		}
	}
	return m, nil
}

// handleThreadNormalKey handles vim-style navigation while viewing a
// thread: j/k to scroll a line, ctrl+d/ctrl+u for a half page, gg/G to
// jump to the top/bottom, i/a to enter insert mode and compose.
func (m Model) handleThreadNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	wasPendingG := m.pendingG
	m.pendingG = false
	if wasPendingG && key == "g" {
		m.thread.GotoTop()
		return m, nil
	}

	switch key {
	case "q", "esc":
		m.focus = focusList
		return m, nil
	case "i", "a":
		m.threadMode = modeInsert
		m.compose.Focus()
		return m, nil
	case "j", "down":
		m.thread.LineDown(1)
	case "k", "up":
		m.thread.LineUp(1)
	case "ctrl+d":
		m.thread.HalfViewDown()
	case "ctrl+u":
		m.thread.HalfViewUp()
	case "g":
		m.pendingG = true
	case "G":
		m.thread.GotoBottom()
	}
	return m, nil
}

// handleThreadInsertKey handles typing into the compose box. Esc returns
// to normal mode (vim-style); everything else goes to the textarea.
func (m Model) handleThreadInsertKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.threadMode = modeNormal
		m.compose.Blur()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.compose.Value())
		if text == "" {
			return m, nil
		}
		conv := m.conversations[m.activeConvID]
		if conv == nil {
			return m, nil
		}
		m.compose.Reset()
		m.status = "Sending..."
		return m, sendMessage(m.client, conv, m.outgoingSIM(), text)
	}
	var cmd tea.Cmd
	m.compose, cmd = m.compose.Update(msg)
	return m, cmd
}

// handleGMEvent applies a live libgm event (new message, conversation
// update, etc) to the model. It never returns waitForEvent itself --
// the caller is responsible for re-arming the listener exactly once.
func (m *Model) handleGMEvent(evt any) tea.Cmd {
	switch v := evt.(type) {
	case *libgm.WrappedMessage:
		m.applyIncomingMessage(v.Message)
	case *gmproto.Conversation:
		m.conversations[v.GetConversationID()] = v
		m.refreshListItems()
	}
	return nil
}

func (m *Model) applyIncomingMessage(msg *gmproto.Message) {
	convID := msg.GetConversationID()
	existing := m.messages[convID]
	if n := len(existing); n > 0 && existing[n-1].GetMessageID() == msg.GetMessageID() {
		return // already have this one
	}
	m.messages[convID] = append(existing, msg)
	if convID == m.activeConvID {
		m.renderThread(convID)
	}
	if conv := m.conversations[convID]; conv != nil {
		conv.LatestMessage = &gmproto.LatestMessage{
			DisplayContent: gmclient.MessageText(msg),
		}
		if msg.GetParticipantID() == conv.GetDefaultOutgoingID() {
			conv.LatestMessage.FromMe = 1
		} else {
			conv.Unread = convID != m.activeConvID
		}
		conv.LastMessageTimestamp = msg.GetTimestamp()
		m.refreshListItems()
	}
}

func (m *Model) refreshListItems() {
	convs := make([]*gmproto.Conversation, 0, len(m.conversations))
	for _, c := range m.conversations {
		convs = append(convs, c)
	}
	sort.Slice(convs, func(i, j int) bool {
		return convs[i].GetLastMessageTimestamp() > convs[j].GetLastMessageTimestamp()
	})
	items := make([]list.Item, len(convs))
	for i, c := range convs {
		items[i] = conversationItem{conv: c}
	}
	m.list.SetItems(items)
}
