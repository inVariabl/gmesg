package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/ipc"
)

const composeHeight = 2

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.list.SetSize(msg.Width, msg.Height-1)
		m.thread.Width = msg.Width
		m.thread.Height = msg.Height - composeHeight - 3
		m.compose.SetWidth(msg.Width)
		m.compose.SetHeight(composeHeight)
		m.attachInput.Width = msg.Width
		m.newConvInput.Width = msg.Width
		m.commandInput.Width = msg.Width
		if m.previewShowing {
			m.showPreview()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case conversationsLoadedMsg:
		m.loadingConversations = false
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
		return m, loadContacts(m.ipcClient)

	case contactsLoadedMsg:
		// A failure leaves completion without candidates; a telephone
		// number can still be typed directly, so it is not reported.
		if msg.err == nil {
			m.contactList = msg.contacts
			if m.startingConv {
				m.refreshRecipientMatches()
			}
		}
		return m, nil

	case loadingTickMsg:
		if !m.loadingConversations {
			return m, nil
		}
		m.loadingDots = m.loadingDots%3 + 1
		return m, loadingTick()

	case threadLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.messages[msg.convID] = msg.msgs
		m.msgCursor[msg.convID] = len(msg.msgs) - 1
		if msg.convID == m.activeConvID {
			m.renderThread(msg.convID)
			m.thread.GotoBottom()
		}
		return m, nil

	case sendResultMsg:
		if msg.err != nil {
			m.err = msg.err
			m.removePendingMessage(msg.convID, msg.pendingID)
		}
		m.status = ""
		return m, nil

	case reactionResultMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		m.status = ""
		return m, nil

	case yankResultMsg:
		if msg.err != nil {
			m.err = msg.err
			m.status = ""
			return m, nil
		}
		m.err = nil
		m.status = "Yanked " + msg.label
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
		m.threadMode = modeInsert
		m.compose.Focus()
		m.msgCursor[convID] = len(m.messages[convID]) - 1
		m.renderThread(convID)
		m.thread.GotoBottom()
		return m, tea.Batch(tea.ClearScreen, fetchThread(m.ipcClient, convID))

	case gmEventMsg:
		cmd := m.handleGMEvent(msg.evt)
		return m, tea.Batch(cmd, waitForEvent(m.ipcClient))

	default:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.ipcClient.Close()
		return m, tea.Quit
	}

	if msg.String() == ":" && !m.commanding && !m.attaching && !m.reacting &&
		!(m.focus == focusList && m.list.FilterState() == list.Filtering) &&
		!(m.focus == focusList && m.startingConv) &&
		!(m.focus == focusThread && m.threadMode == modeInsert) {
		m.commanding = true
		m.commandInput.Reset()
		m.compose.Blur()
		m.attachInput.Blur()
		return m, m.commandInput.Focus()
	}

	if m.commanding {
		return m.handleCommandKey(msg)
	}

	switch m.focus {
	case focusList:
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
			m.ipcClient.Close()
			return m, tea.Quit
		case "n":
			return m, m.beginNewConversation()
		case "enter":
			return m.openSelectedConversation()
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

	wasPendingY := m.pendingY
	m.pendingY = false
	if wasPendingY {
		switch key {
		case "y":
			return m.yankMessage(yankFullText)
		case "c":
			return m.yankMessage(yankCode)
		case "l":
			return m.yankMessage(yankLink)
		}
	}

	switch key {
	case "q", "esc":
		m.focus = focusList
		m.hidePreview()
		m.renderThread(m.activeConvID)
		return m, tea.ClearScreen
	case "i", "a":
		m.threadMode = modeInsert
		m.compose.Focus()
		m.hidePreview()
		m.renderThread(m.activeConvID)
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
		wasPreviewShowing := m.previewShowing
		m.moveMsgCursor(1)
		if wasPreviewShowing {
			return m, tea.ClearScreen
		}
	case "K":
		wasPreviewShowing := m.previewShowing
		m.moveMsgCursor(-1)
		if wasPreviewShowing {
			return m, tea.ClearScreen
		}
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
		return m.togglePreview(msgs[cur])
	case "o":
		msgs := m.messages[m.activeConvID]
		cur := m.msgCursor[m.activeConvID]
		if cur < 0 || cur >= len(msgs) {
			return m, nil
		}
		return m.openTarget(msgs[cur])
	case "y":
		m.pendingY = true
	}
	return m, nil
}

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
	return m, sendReaction(m.ipcClient, target, emoji)
}

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
		pending := newPendingMessage(conv, text, attachments)
		m.applyIncomingMessage(pending)
		m.thread.GotoBottom()
		return m, sendMessage(m.ipcClient, conv, text, attachments, pending.GetMessageID())
	}
	var cmd tea.Cmd
	m.compose, cmd = m.compose.Update(msg)
	return m, cmd
}

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
	case "tab":
		partial := expandPath(strings.TrimSpace(m.attachInput.Value()))
		if partial == "" {
			partial = "."
		}
		matches := completePath(partial)
		if len(matches) == 0 {
			return m, nil
		}
		if len(matches) == 1 {
			completed := matches[0]
			info, err := os.Stat(completed)
			if err == nil && info.IsDir() {
				completed += "/"
			}
			m.attachInput.SetValue(completed)
			m.attachInput.CursorEnd()
			return m, nil
		}
		var names []string
		for _, p := range matches {
			base := filepath.Base(p)
			info, err := os.Stat(p)
			if err == nil && info.IsDir() {
				base += "/"
			}
			names = append(names, base)
		}
		m.status = strings.Join(names, "  ")
		return m, nil
	}
	var cmd tea.Cmd
	m.attachInput, cmd = m.attachInput.Update(msg)
	return m, cmd
}

func completePath(partial string) []string {
	dir := filepath.Dir(partial)
	base := filepath.Base(partial)
	if strings.HasSuffix(partial, "/") {
		dir = partial
		base = ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var matches []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), base) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		matches = append(matches, full)
	}
	return matches
}

func (m Model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.commanding = false
		m.commandInput.Reset()
		m.commandInput.Blur()
		m.commandMatches = nil
		m.commandMatchIdx = 0
		return m, nil
	case "enter":
		cmd := strings.TrimSpace(m.commandInput.Value())
		m.commanding = false
		m.commandInput.Reset()
		m.commandInput.Blur()
		m.commandMatches = nil
		m.commandMatchIdx = 0
		if cmd == "" {
			return m, nil
		}
		return m.executeCommand(cmd)
	case "tab":
		prefix := m.commandInput.Value()
		if prefix == "" {
			return m, nil
		}
		if m.commandMatches == nil {
			for _, c := range commandNames {
				if strings.HasPrefix(c, prefix) {
					m.commandMatches = append(m.commandMatches, c)
				}
			}
			m.commandMatchIdx = 0
		}
		if len(m.commandMatches) == 0 {
			return m, nil
		}
		m.commandInput.SetValue(m.commandMatches[m.commandMatchIdx])
		m.commandMatchIdx = (m.commandMatchIdx + 1) % len(m.commandMatches)
		return m, nil
	case "shift+tab":
		prefix := m.commandInput.Value()
		if prefix == "" {
			return m, nil
		}
		if m.commandMatches == nil {
			for _, c := range commandNames {
				if strings.HasPrefix(c, prefix) {
					m.commandMatches = append(m.commandMatches, c)
				}
			}
			m.commandMatchIdx = 0
		}
		if len(m.commandMatches) == 0 {
			return m, nil
		}
		m.commandMatchIdx = (m.commandMatchIdx - 1 + len(m.commandMatches)) % len(m.commandMatches)
		m.commandInput.SetValue(m.commandMatches[m.commandMatchIdx])
		return m, nil
	}
	m.commandMatches = nil
	m.commandMatchIdx = 0
	var cmd tea.Cmd
	m.commandInput, cmd = m.commandInput.Update(msg)
	return m, cmd
}

var commandNames = []string{
	"q", "quit",
	"back", "list", "close",
	"open",
	"react",
	"preview", "p",
	"attach",
	"new", "n",
	"search", "s",
	"send", "i",
	"help",
}

func (m Model) executeCommand(cmd string) (tea.Model, tea.Cmd) {
	switch cmd {
	case "q", "quit":
		m.ipcClient.Close()
		return m, tea.Quit

	case "back", "list", "close":
		m.focus = focusList
		m.hidePreview()
		m.threadMode = modeNormal
		m.compose.Blur()
		m.renderThread(m.activeConvID)
		return m, tea.ClearScreen

	case "open":
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
		return m, openMessage(m.ipcClient, m.cacheDir, target)

	case "react":
		if m.focus != focusThread {
			return m, nil
		}
		msgs := m.messages[m.activeConvID]
		cur := m.msgCursor[m.activeConvID]
		if cur >= 0 && cur < len(msgs) {
			m.reacting = true
			m.reactionCursor = 0
		}
		return m, nil

	case "preview", "p":
		if m.focus != focusThread {
			return m, nil
		}
		msgs := m.messages[m.activeConvID]
		cur := m.msgCursor[m.activeConvID]
		if cur < 0 || cur >= len(msgs) {
			return m, nil
		}
		target := msgs[cur]
		if m.previewShowing && m.previewMsgID == target.GetMessageID() {
			m.hidePreview()
			return m, nil
		}
		if m.ueberzug == nil {
			m.status = "image preview unavailable: install ueberzug to enable it"
			return m, nil
		}
		m.hidePreview()
		m.status = "Loading preview..."
		return m, previewImage(m.ipcClient, m.cacheDir, target)

	case "attach":
		if m.focus != focusThread {
			return m, nil
		}
		m.attaching = true
		m.compose.Blur()
		return m, m.attachInput.Focus()

	case "new", "n":
		return m, m.beginNewConversation()

	case "search", "s":
		if m.focus == focusList {
			m.list.SetFilterState(list.Filtering)
		}
		return m, nil

	case "send", "i":
		if m.focus != focusThread {
			return m, nil
		}
		m.threadMode = modeInsert
		m.compose.Focus()
		m.hidePreview()
		m.renderThread(m.activeConvID)
		return m, nil

	case "help":
		m.status = ":q quit · :back list · :open · :react · :preview · :attach · :new · :search · :send · :help"
		return m, nil

	default:
		m.status = fmt.Sprintf("unknown command: %s (try :help)", cmd)
		return m, nil
	}
}

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

// The default delegate renders each item as a 2-line title+description
// block with 1 blank line of spacing between items (see
// list.DefaultDelegate.Render/populatedView), and newListStyles keeps the
// title bar and status bar at their default 2-row heights each. Together
// that's a fixed 4-row header above the item rows, 3 rows per item. These
// stay in sync with the list configuration in New() and newListStyles().
const (
	listRowsAboveItems = 4
	listRowsPerItem    = 3
)

// listItemIndexAt maps a terminal row to the conversation list item
// rendered there, accounting for the header rows and the current page.
// bubbles' list.Model doesn't process mouse events itself, so this is our
// own hit-test.
func listItemIndexAt(m Model, y int) (int, bool) {
	if m.list.FilterState() == list.Filtering {
		return 0, false
	}
	row := y - listRowsAboveItems
	if row < 0 {
		return 0, false
	}
	perPage := m.list.Paginator.PerPage
	if perPage <= 0 {
		return 0, false
	}
	rel := row / listRowsPerItem
	if rel >= perPage {
		return 0, false
	}
	idx := m.list.Paginator.Page*perPage + rel
	if idx < 0 || idx >= len(m.list.VisibleItems()) {
		return 0, false
	}
	return idx, true
}

func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.commanding {
		return m, nil
	}

	// Use Button/Action rather than the deprecated Type: under the SGR mouse
	// protocol (and always under the older X10 protocol) a button release
	// is reported with no button number, so Type normalizes every release
	// to MouseRelease and a left-click release can never match MouseLeft.
	// Button stays reliable on press in both protocols, so that's what we
	// key clicks off of.
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		up := msg.Button == tea.MouseButtonWheelUp
		switch m.focus {
		case focusList:
			if m.startingConv {
				return m, nil
			}
			if up {
				m.list.CursorUp()
			} else {
				m.list.CursorDown()
			}
			return m, nil
		case focusThread:
			if up {
				m.thread.LineUp(3)
			} else {
				m.thread.LineDown(3)
			}
			return m, nil
		}

	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		switch m.focus {
		case focusList:
			if m.startingConv {
				return m, nil
			}
			if idx, ok := listItemIndexAt(m, msg.Y); ok {
				m.list.Select(idx)
				return m.openSelectedConversation()
			}
			return m, nil
		case focusThread:
			if m.threadMode != modeNormal || m.reacting {
				return m, nil
			}
			composeY := m.height - composeHeight - 1
			if msg.Y >= composeY {
				m.threadMode = modeInsert
				m.compose.Focus()
				m.hidePreview()
				m.renderThread(m.activeConvID)
				return m, nil
			}
			if target, ok := m.senderAt(msg.Y); ok {
				conv := m.conversations[m.activeConvID]
				if !gmclient.SenderIsSelf(conv, target) {
					participantID := gmclient.SenderParticipantID(target)
					if convID, ok := m.findDirectConversation(participantID); ok && convID != m.activeConvID {
						return m.openConversationByID(convID)
					}
				}
			}
			if idx, ok := m.messageIndexAt(msg.Y); ok {
				target := m.messages[m.activeConvID][idx]
				m.setMsgCursor(idx)
				if _, ok := gmclient.FirstImageAttachment(target); ok {
					return m.togglePreview(target)
				}
				return m.openTarget(target)
			}
			return m, nil
		}

	case tea.MouseButtonRight:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		if m.focus != focusThread || m.threadMode != modeNormal || m.reacting {
			return m, nil
		}
		composeY := m.height - composeHeight - 1
		if msg.Y >= composeY {
			return m, nil
		}
		if idx, ok := m.messageIndexAt(msg.Y); ok {
			m.setMsgCursor(idx)
			m.reacting = true
			m.reactionCursor = 0
		}
		return m, nil
	}
	return m, nil
}

// threadHeaderRows is the number of rows the conversation title takes up
// above the message viewport (see the focusThread branch of View() in
// view.go).
const threadHeaderRows = 1

// senderAt returns the message whose "name · time" header line is rendered
// at terminal row y in the thread view, so a click there can jump to that
// sender's own conversation. It relies on msgLineOffsets, which renderThread
// keeps in sync with the message list's line positions.
func (m Model) senderAt(y int) (*gmproto.Message, bool) {
	row := y - threadHeaderRows
	if row < 0 || row >= m.thread.Height {
		return nil, false
	}
	line := m.thread.YOffset + row
	msgs := m.messages[m.activeConvID]
	for i, off := range m.msgLineOffsets {
		if off == line && i < len(msgs) {
			return msgs[i], true
		}
	}
	return nil, false
}

// messageIndexAt returns the index of the message rendered anywhere in its
// block (header, text, attachment, or reaction lines) at terminal row y in
// the thread view -- unlike senderAt, which only matches the header line.
func (m Model) messageIndexAt(y int) (int, bool) {
	row := y - threadHeaderRows
	if row < 0 || row >= m.thread.Height {
		return 0, false
	}
	line := m.thread.YOffset + row
	msgs := m.messages[m.activeConvID]
	idx := -1
	for i, off := range m.msgLineOffsets {
		if off > line {
			break
		}
		idx = i
	}
	if idx < 0 || idx >= len(msgs) {
		return 0, false
	}
	return idx, true
}

// setMsgCursor moves the message cursor straight to idx and re-renders.
// Unlike moveMsgCursor (used for J/K keyboard navigation), it doesn't touch
// the viewport scroll position or any open preview -- for a mouse click the
// target is already on screen, and callers that care about the preview
// (togglePreview) need to read its prior state themselves first.
func (m *Model) setMsgCursor(idx int) {
	m.msgCursor[m.activeConvID] = idx
	m.renderThread(m.activeConvID)
}

// togglePreview shows target's image in the ueberzug overlay, or hides it
// if it's the one already showing. Shared by the "p" key and clicking an
// image message.
func (m Model) togglePreview(target *gmproto.Message) (tea.Model, tea.Cmd) {
	if m.previewShowing && m.previewMsgID == target.GetMessageID() {
		m.hidePreview()
		return m, nil
	}
	if m.ueberzug == nil {
		m.status = "image preview unavailable: install ueberzug to enable it"
		return m, nil
	}
	m.hidePreview()
	m.status = "Loading preview..."
	return m, previewImage(m.ipcClient, m.cacheDir, target)
}

// openTarget opens target's first link or attachment with the system
// default handler. Shared by the "o" key and clicking a message with a
// link or attachment.
func (m Model) openTarget(target *gmproto.Message) (tea.Model, tea.Cmd) {
	if gmclient.AttachmentSummary(target) == "" && len(gmclient.ExtractLinks(target)) == 0 {
		return m, nil
	}
	m.status = "Opening..."
	return m, openMessage(m.ipcClient, m.cacheDir, target)
}

// yankKind selects what part of the selected message yankMessage copies to
// the clipboard.
type yankKind int

const (
	yankFullText yankKind = iota // "yy": the message's full text
	yankCode                     // "yc": its one-time passcode
	yankLink                     // "yl": its first link
)

// yankMessage copies part of the selected message to the clipboard: its
// one-time passcode for "yc" (see gmclient.ExtractCode), its first link for
// "yl" (see gmclient.ExtractLinks), or its full text for "yy".
func (m Model) yankMessage(kind yankKind) (tea.Model, tea.Cmd) {
	msgs := m.messages[m.activeConvID]
	cur := m.msgCursor[m.activeConvID]
	if cur < 0 || cur >= len(msgs) {
		return m, nil
	}
	target := msgs[cur]
	switch kind {
	case yankCode:
		code, ok := gmclient.ExtractCode(target)
		if !ok {
			m.err = fmt.Errorf("no code found in selected message")
			m.status = ""
			return m, nil
		}
		return m, copyToClipboard(code, "code")
	case yankLink:
		links := gmclient.ExtractLinks(target)
		if len(links) == 0 {
			m.err = fmt.Errorf("no link found in selected message")
			m.status = ""
			return m, nil
		}
		return m, copyToClipboard(links[0], "link")
	default:
		text := gmclient.MessageText(target)
		if text == "" {
			m.err = fmt.Errorf("selected message has no text to yank")
			m.status = ""
			return m, nil
		}
		return m, copyToClipboard(text, "message")
	}
}

// findDirectConversation returns the ID of the cached non-group conversation
// whose only other participant is participantID, if one has been loaded.
func (m Model) findDirectConversation(participantID string) (string, bool) {
	for id, conv := range m.conversations {
		if conv.GetIsGroupChat() {
			continue
		}
		others := conv.GetOtherParticipants()
		if len(others) == 1 && others[0] == participantID {
			return id, true
		}
	}
	return "", false
}

func (m Model) openSelectedConversation() (tea.Model, tea.Cmd) {
	item, ok := m.list.SelectedItem().(conversationItem)
	if !ok {
		return m, nil
	}
	return m.openConversationByID(item.conv.GetConversationID())
}

func (m Model) openConversationByID(convID string) (tea.Model, tea.Cmd) {
	m.activeConvID = convID
	m.focus = focusThread
	m.threadMode = modeNormal
	m.compose.Blur()
	m.msgCursor[convID] = len(m.messages[convID]) - 1
	m.renderThread(convID)
	m.thread.GotoBottom()
	// Switching from the list layout to the thread layout changes the
	// screen's total line count out from under bubbletea's line-diffing
	// renderer, which can leave its cache of "what's on screen" pointing
	// at the wrong row for the rest of the session. Force a full repaint
	// so it re-syncs against reality instead of drifting.
	return m, tea.Batch(tea.ClearScreen, fetchThread(m.ipcClient, convID))
}

func (m *Model) handleGMEvent(evt any) tea.Cmd {
	switch v := evt.(type) {
	case ipc.Event:
		switch v.Event {
		case "message":
			var data struct {
				Message json.RawMessage `json:"message"`
				IsOld   bool            `json:"is_old"`
			}
			if err := json.Unmarshal(v.Data, &data); err != nil {
				return nil
			}
			var msg gmproto.Message
			if err := protojson.Unmarshal(data.Message, &msg); err != nil {
				return nil
			}
			m.applyIncomingMessage(&msg)
		case "conversation":
			var conv gmproto.Conversation
			if err := protojson.Unmarshal(v.Data, &conv); err != nil {
				return nil
			}
			m.conversations[conv.GetConversationID()] = &conv
			m.refreshListItems()
		}
	}
	return nil
}

func (m *Model) applyIncomingMessage(msg *gmproto.Message) {
	convID := msg.GetConversationID()
	existing := m.messages[convID]
	conv := m.conversations[convID]

	replaced := false
	for i, e := range existing {
		if e.GetMessageID() == msg.GetMessageID() {
			existing[i] = msg
			replaced = true
			break
		}
	}
	// The daemon's confirmation of a message we sent ourselves: swap it in
	// for the optimistic placeholder newPendingMessage added, rather than
	// appending a second copy.
	if !replaced && !isPendingMessageID(msg.GetMessageID()) && conv != nil && msg.GetParticipantID() == conv.GetDefaultOutgoingID() {
		for i, e := range existing {
			if isPendingMessageID(e.GetMessageID()) {
				existing[i] = msg
				replaced = true
				break
			}
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
		// SetContent (inside renderThread) leaves YOffset exactly where it
		// was, so appending a message the user was already scrolled to the
		// bottom of otherwise leaves the view stuck one message behind.
		viewAtBottom := m.thread.AtBottom()
		m.renderThread(convID)
		if viewAtBottom {
			m.thread.GotoBottom()
		}
	}
	if conv != nil {
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

// removePendingMessage drops the optimistic placeholder newPendingMessage
// added for a send that then failed, so the thread doesn't keep showing a
// message as sent when it wasn't.
func (m *Model) removePendingMessage(convID, pendingID string) {
	if pendingID == "" {
		return
	}
	existing := m.messages[convID]
	for i, e := range existing {
		if e.GetMessageID() != pendingID {
			continue
		}
		existing = append(existing[:i], existing[i+1:]...)
		m.messages[convID] = existing
		if cur := m.msgCursor[convID]; cur >= len(existing) {
			m.msgCursor[convID] = len(existing) - 1
		}
		if convID == m.activeConvID {
			m.renderThread(convID)
		}
		return
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
