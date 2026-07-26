package tui

import (
	"fmt"
	"os"
	"path/filepath"
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
		m.attachInput.Width = msg.Width
		m.newConvInput.Width = msg.Width
		if m.previewShowing {
			m.showPreview() // reposition/resize the overlay to match
		}
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
		// Always land on the most recent message, not just the first time
		// this conversation loads -- reacting should default to "the last
		// thing said" without having to navigate there first.
		m.msgCursor[msg.convID] = len(msg.msgs) - 1
		if msg.convID == m.activeConvID {
			m.renderThread(msg.convID)
			m.thread.GotoBottom()
		}
		return m, nil

	case sendResultMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		m.status = ""
		return m, nil

	case reactionResultMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		m.status = ""
		return m, nil

	case openResultMsg:
		m.status = ""
		if msg.err != nil {
			m.err = msg.err
		} else if msg.info != "" {
			m.err = nil
			m.status = msg.info
		}
		return m, nil

	case previewReadyMsg:
		m.status = ""
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// The user may have moved the selection on before this landed --
		// in that case just drop it silently rather than popping up a
		// preview for a message that's no longer selected.
		msgs := m.messages[m.activeConvID]
		cur := m.msgCursor[m.activeConvID]
		if cur < 0 || cur >= len(msgs) || msgs[cur].GetMessageID() != msg.msgID {
			return m, nil
		}
		if msg.info != "" {
			m.err = nil
			m.status = msg.info
			return m, nil
		}
		m.previewShowing = true
		m.previewMsgID = msg.msgID
		m.previewPath = msg.path
		m.showPreview()
		return m, nil

	case conversationResolvedMsg:
		m.status = ""
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		convID := msg.conv.GetConversationID()
		m.conversations[convID] = msg.conv
		m.refreshListItems()
		m.activeConvID = convID
		m.focus = focusThread
		// Straight into insert mode -- the whole point of "n" is to say
		// something to a number/contact you haven't texted in this session,
		// so there's nothing to read yet and no reason to land in normal
		// mode first.
		m.threadMode = modeInsert
		m.compose.Focus()
		m.msgCursor[convID] = len(m.messages[convID]) - 1
		m.renderThread(convID)
		m.thread.GotoBottom()
		return m, fetchThread(m.client, convID)

	case gmEventMsg:
		cmd := m.handleGMEvent(msg.evt)
		return m, tea.Batch(cmd, waitForEvent(m.events))

	default:
		// bubbles/list schedules its own tea.Cmds internally -- notably
		// list.FilterMatchesMsg, which is how a filter query typed via "/"
		// actually narrows the visible items -- and expects the resulting
		// messages to be routed back into list.Update. Without this, the
		// filter input would accept keystrokes but the list would never
		// re-narrow.
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.client.Disconnect()
		return m, tea.Quit
	}

	switch m.focus {
	case focusList:
		// While the user is typing a filter query (bubbles/list's builtin
		// "/" search), every keystroke -- including "q" and "enter" -- has
		// to reach the filter input verbatim instead of being intercepted
		// as quit/open below.
		if m.list.FilterState() == list.Filtering {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}
		if m.startingConv {
			return m.handleNewConvKey(msg)
		}
		switch msg.String() {
		case "q":
			m.client.Disconnect()
			return m, tea.Quit
		case "n":
			m.startingConv = true
			return m, m.newConvInput.Focus()
		case "enter":
			item, ok := m.list.SelectedItem().(conversationItem)
			if !ok {
				return m, nil
			}
			m.activeConvID = item.conv.GetConversationID()
			m.focus = focusThread
			m.threadMode = modeNormal
			m.compose.Blur()
			// Provisional -- corrected to the true most-recent index once
			// fetchThread's threadLoadedMsg arrives, since messages may not
			// be loaded yet on first visit to this conversation.
			m.msgCursor[m.activeConvID] = len(m.messages[m.activeConvID]) - 1
			m.renderThread(m.activeConvID)
			m.thread.GotoBottom()
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
			if m.attaching {
				return m.handleAttachKey(msg)
			}
			return m.handleThreadInsertKey(msg)
		}
	}
	return m, nil
}

// handleThreadNormalKey handles vim-style navigation while viewing a
// thread: j/k to scroll a line, ctrl+d/ctrl+u for a half page, gg/G to
// jump to the top/bottom, J/K to move the message selection, r to react,
// i/a to enter insert mode and compose.
func (m Model) handleThreadNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.reacting {
		return m.handleReactionKey(msg)
	}

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
		m.hidePreview()
		m.renderThread(m.activeConvID) // drop selection highlight
		return m, nil
	case "i", "a":
		m.threadMode = modeInsert
		m.compose.Focus()
		m.hidePreview()
		m.renderThread(m.activeConvID) // drop selection highlight
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
	case "J":
		m.moveMsgCursor(1)
	case "K":
		m.moveMsgCursor(-1)
	case "r":
		msgs := m.messages[m.activeConvID]
		cur := m.msgCursor[m.activeConvID]
		if cur >= 0 && cur < len(msgs) {
			m.reacting = true
			m.reactionCursor = 0
		}
	case "p":
		msgs := m.messages[m.activeConvID]
		cur := m.msgCursor[m.activeConvID]
		if cur < 0 || cur >= len(msgs) {
			return m, nil
		}
		target := msgs[cur]
		if m.previewShowing && m.previewMsgID == target.GetMessageID() {
			m.hidePreview() // "p" again on the same message dismisses it
			return m, nil
		}
		if m.ueberzug == nil {
			m.status = "image preview unavailable: install ueberzug to enable it"
			return m, nil
		}
		m.hidePreview() // drop any previous preview before loading the new one
		m.status = "Loading preview..."
		return m, previewImage(m.client, m.cacheDir, target)
	case "o":
		msgs := m.messages[m.activeConvID]
		cur := m.msgCursor[m.activeConvID]
		if cur < 0 || cur >= len(msgs) {
			return m, nil
		}
		target := msgs[cur]
		if gmclient.AttachmentSummary(target) == "" && len(gmclient.ExtractLinks(target)) == 0 {
			return m, nil
		}
		m.status = "Opening..."
		return m, openMessage(m.client, m.cacheDir, target)
	}
	return m, nil
}

// handleReactionKey handles the reaction-picker overlay: h/l or number keys
// choose an emoji, enter confirms, esc cancels.
func (m Model) handleReactionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.reacting = false
		return m, nil
	case "h", "left":
		if m.reactionCursor > 0 {
			m.reactionCursor--
		}
		return m, nil
	case "l", "right":
		if m.reactionCursor < len(reactionEmojis)-1 {
			m.reactionCursor++
		}
		return m, nil
	case "enter":
		return m.sendSelectedReaction(reactionEmojis[m.reactionCursor])
	case "1", "2", "3", "4", "5", "6", "7", "8":
		idx := int(msg.String()[0] - '1')
		if idx < len(reactionEmojis) {
			return m.sendSelectedReaction(reactionEmojis[idx])
		}
	}
	return m, nil
}

func (m Model) sendSelectedReaction(emoji string) (tea.Model, tea.Cmd) {
	m.reacting = false
	msgs := m.messages[m.activeConvID]
	cur := m.msgCursor[m.activeConvID]
	if cur < 0 || cur >= len(msgs) {
		return m, nil
	}
	target := msgs[cur]
	m.status = "Reacting..."
	return m, sendReaction(m.client, m.outgoingSIM(), target, emoji)
}

// handleThreadInsertKey handles typing into the compose box. Esc returns
// to normal mode (vim-style); ctrl+t opens the attach-file prompt;
// everything else goes to the textarea.
func (m Model) handleThreadInsertKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.threadMode = modeNormal
		m.compose.Blur()
		return m, nil
	case "ctrl+t":
		m.attaching = true
		m.compose.Blur()
		return m, m.attachInput.Focus()
	case "enter":
		text := strings.TrimSpace(m.compose.Value())
		if text == "" && len(m.pendingAttachments) == 0 {
			return m, nil
		}
		conv := m.conversations[m.activeConvID]
		if conv == nil {
			return m, nil
		}
		attachments := m.pendingAttachments
		m.compose.Reset()
		m.pendingAttachments = nil
		m.status = "Sending..."
		return m, sendMessage(m.client, conv, m.outgoingSIM(), text, attachments)
	}
	var cmd tea.Cmd
	m.compose, cmd = m.compose.Update(msg)
	return m, cmd
}

// handleAttachKey handles the attach-file path prompt opened by ctrl+t.
// Enter validates the path and queues it as a pending attachment; esc
// cancels. Either way, control returns to the compose box.
func (m Model) handleAttachKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.attaching = false
		m.attachInput.Reset()
		m.attachInput.Blur()
		m.compose.Focus()
		return m, nil
	case "enter":
		path := expandPath(strings.TrimSpace(m.attachInput.Value()))
		m.attaching = false
		m.attachInput.Reset()
		m.attachInput.Blur()
		m.compose.Focus()
		if path == "" {
			return m, nil
		}
		if _, err := os.Stat(path); err != nil {
			m.err = fmt.Errorf("attach %s: %w", path, err)
			return m, nil
		}
		m.pendingAttachments = append(m.pendingAttachments, path)
		return m, nil
	}
	var cmd tea.Cmd
	m.attachInput, cmd = m.attachInput.Update(msg)
	return m, cmd
}

// handleNewConvKey handles the new-conversation phone number prompt opened
// by "n" from the conversation list. Enter resolves (or creates) the
// conversation and jumps straight into it, ready to type; esc cancels.
func (m Model) handleNewConvKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.startingConv = false
		m.newConvInput.Reset()
		m.newConvInput.Blur()
		return m, nil
	case "enter":
		phones := splitPhones(m.newConvInput.Value())
		m.startingConv = false
		m.newConvInput.Reset()
		m.newConvInput.Blur()
		if len(phones) == 0 {
			return m, nil
		}
		m.status = "Starting conversation..."
		return m, resolveConversation(m.client, phones)
	}
	var cmd tea.Cmd
	m.newConvInput, cmd = m.newConvInput.Update(msg)
	return m, cmd
}

// splitPhones parses a comma-separated phone number list into its trimmed,
// non-empty parts. A single number starts a 1:1 conversation; more than one
// starts a group, mirroring gmclient.ResolveConversation.
func splitPhones(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// expandPath expands a leading ~ to the user's home directory.
func expandPath(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
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

// applyIncomingMessage merges a message from the event stream into the
// active conversation's message list. A message ID already present gets
// updated in place -- this is how reaction changes and edits to earlier
// messages arrive, not just new sends -- otherwise it's appended.
func (m *Model) applyIncomingMessage(msg *gmproto.Message) {
	convID := msg.GetConversationID()
	existing := m.messages[convID]

	replaced := false
	for i, e := range existing {
		if e.GetMessageID() == msg.GetMessageID() {
			existing[i] = msg
			replaced = true
			break
		}
	}
	wasAtBottom := len(existing) == 0 || m.msgCursor[convID] >= len(existing)-1
	if !replaced {
		existing = append(existing, msg)
	}
	m.messages[convID] = existing
	if wasAtBottom {
		m.msgCursor[convID] = len(existing) - 1
	}

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
