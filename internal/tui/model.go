// Package tui implements gmesg's interactive terminal UI: a conversation
// list and a thread view with a compose box, kept live by libgm's event
// stream.
package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/ueberzug"
)

type focus int

const (
	focusList focus = iota
	focusThread
)

// threadMode mirrors vim's modal split: normal mode scrolls/navigates the
// thread, insert mode types into the compose box.
type threadMode int

const (
	modeNormal threadMode = iota
	modeInsert
)

// reactionEmojis are the picker's quick-react choices. These map to
// gmproto's canonical reaction types via gmproto.MakeReactionData, which
// falls back to a custom reaction for anything outside that set -- the
// phone may not render those, so keep this list to emoji Google recognizes.
var reactionEmojis = []string{"👍", "💀", "😭", "😂", "😐", "😳", "🥀", "🍅"}

// Model is the root bubbletea model for gmesg's TUI.
type Model struct {
	client   *libgm.Client
	settings *gmproto.Settings
	events   chan any
	cacheDir string
	ueberzug *ueberzug.Client // nil-safe: nil means preview isn't available (ueberzug not installed)

	focus      focus
	threadMode threadMode
	pendingG   bool
	list       list.Model
	thread     viewport.Model
	compose    textarea.Model

	conversations map[string]*gmproto.Conversation
	messages      map[string][]*gmproto.Message
	activeConvID  string

	// msgCursor tracks, per conversation, the index into messages[convID]
	// that's currently selected for reacting to. msgLineOffsets is the
	// rendered-line start of each message in the active thread, kept in
	// sync by renderThread so cursor movement can scroll the viewport to
	// keep the selection visible.
	msgCursor      map[string]int
	msgLineOffsets []int

	reacting       bool
	reactionCursor int

	attaching          bool
	attachInput        textinput.Model
	pendingAttachments []string

	startingConv bool
	newConvInput textinput.Model

	commanding       bool
	commandInput     textinput.Model
	commandMatches   []string
	commandMatchIdx  int

	// previewShowing tracks whether an ueberzug overlay is currently on
	// screen; previewMsgID/previewPath identify what it's showing, so a
	// resize can redraw it in place and a cursor move can tell whether the
	// selection has moved away from the previewed message.
	previewShowing bool
	previewMsgID   string
	previewPath    string

	status string
	err    error

	width, height int
	ready         bool
}

var statusStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
var errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Padding(0, 1)
var headerStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1)
var selectedHeaderStyle = lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1)
var reactionStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
var attachmentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Padding(0, 1)
var pendingAttachmentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
var reactionPickerSelectedStyle = lipgloss.NewStyle().Reverse(true)

// New builds the TUI model. client must already be connected (see
// gmclient.Connect); events is the channel that client's event handler
// feeds raw libgm events into. cacheDir is where downloaded attachments are
// written before being handed to an external viewer (see the "o" keybinding).
// uz is the inline image preview overlay (see the "p" keybinding); a nil uz
// is fine and simply disables previews, e.g. when ueberzug isn't installed.
func New(client *libgm.Client, settings *gmproto.Settings, events chan any, cacheDir string, uz *ueberzug.Client) Model {
	convList := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	convList.Title = "Conversations"
	convList.SetShowHelp(false)

	compose := textarea.New()
	compose.Placeholder = "Type a message, Enter to send..."
	compose.ShowLineNumbers = false
	compose.SetHeight(2)

	attachInput := textinput.New()
	attachInput.Placeholder = "path to file (~ expands to $HOME)"

	newConvInput := textinput.New()
	newConvInput.Placeholder = "phone number(s), comma-separated for a group"

	commandInput := textinput.New()
	commandInput.Placeholder = "command (:q :open :react :preview :attach :new :search :send :help)"
	commandInput.Prompt = ":"

	return Model{
		client:        client,
		settings:      settings,
		events:        events,
		cacheDir:      cacheDir,
		ueberzug:      uz,
		list:          convList,
		thread:        viewport.New(0, 0),
		compose:       compose,
		attachInput:   attachInput,
		newConvInput:  newConvInput,
		commandInput:  commandInput,
		conversations: map[string]*gmproto.Conversation{},
		messages:      map[string][]*gmproto.Message{},
		msgCursor:     map[string]int{},
		status:        "Loading conversations...",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchConversations(m.client), waitForEvent(m.events))
}

func (m *Model) outgoingSIM() *gmproto.SIMCard {
	return gmclient.DefaultSIM(m.settings)
}

// renderThread rebuilds the thread viewport's content for convID, one block
// per message (sender/time header, text, attachments, reactions), marking
// the message at msgCursor[convID] as selected when the thread has normal-mode
// focus.
func (m *Model) renderThread(convID string) {
	conv := m.conversations[convID]
	msgs := m.messages[convID]
	if conv == nil || len(msgs) == 0 {
		m.thread.SetContent("(no messages yet)")
		m.msgLineOffsets = nil
		return
	}

	cursor := m.msgCursor[convID]
	showCursor := m.focus == focusThread && m.threadMode == modeNormal

	// Plain message text has no padding of its own, so it wraps to the
	// full viewport width; header/attachment/reaction lines carry the
	// styles' Padding(0, 1), so their content has 2 fewer columns to work
	// with before they'd overflow it.
	textWidth := m.thread.Width
	paddedWidth := m.thread.Width - 2

	var b strings.Builder
	offsets := make([]int, len(msgs))
	lineNo := 0
	for i, msg := range msgs {
		text := wrapText(gmclient.MessageText(msg), textWidth)
		attachments := wrapText(gmclient.AttachmentSummary(msg), paddedWidth)
		reactions := wrapText(gmclient.ReactionSummary(msg), paddedWidth)
		if text == "" && attachments == "" && reactions == "" {
			offsets[i] = lineNo
			continue
		}

		offsets[i] = lineNo
		sender := gmclient.SenderName(conv, msg)
		ts := time.UnixMicro(msg.GetTimestamp()).Local().Format("15:04")
		header := wrapText(sender+" · "+ts, paddedWidth)
		style := headerStyle
		if showCursor && i == cursor {
			header = "▶ " + header
			style = selectedHeaderStyle
		}

		var block strings.Builder
		block.WriteString(style.Render(header))
		block.WriteString("\n")
		if text != "" {
			block.WriteString(text)
			block.WriteString("\n")
		}
		if attachments != "" {
			block.WriteString(attachmentStyle.Render(attachments))
			block.WriteString("\n")
		}
		if reactions != "" {
			block.WriteString(reactionStyle.Render(reactions))
			block.WriteString("\n")
		}
		block.WriteString("\n")

		s := block.String()
		b.WriteString(s)
		lineNo += strings.Count(s, "\n")
	}
	m.thread.SetContent(b.String())
	m.msgLineOffsets = offsets
}

// wrapText word-wraps s to width columns. A non-positive width (the thread
// viewport hasn't been sized yet) or empty input is returned unchanged.
func wrapText(s string, width int) string {
	if width <= 0 || s == "" {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

// moveMsgCursor shifts the selected message in the active conversation by
// delta, clamped to the message list bounds, and scrolls the thread
// viewport to keep the selection visible.
func (m *Model) moveMsgCursor(delta int) {
	msgs := m.messages[m.activeConvID]
	if len(msgs) == 0 {
		return
	}
	m.hidePreview() // selection moved on, so any open preview no longer applies
	cur := m.msgCursor[m.activeConvID] + delta
	if cur < 0 {
		cur = 0
	}
	if cur > len(msgs)-1 {
		cur = len(msgs) - 1
	}
	m.msgCursor[m.activeConvID] = cur
	m.renderThread(m.activeConvID)

	if cur >= len(m.msgLineOffsets) {
		return
	}
	off := m.msgLineOffsets[cur]
	if off < m.thread.YOffset {
		m.thread.SetYOffset(off)
	} else if off > m.thread.YOffset+m.thread.Height-3 {
		m.thread.SetYOffset(off - m.thread.Height + 3)
	}
}

// previewRect picks the fixed screen region an image preview overlay is
// drawn in: a box anchored to the top-right corner of the terminal, sized
// relative to it but clamped to sane bounds. It doesn't track the
// previewed message's scroll position -- ueberzug draws an X11 overlay on
// top of whatever text is there, so a fixed corner is far simpler than
// keeping a floating overlay in sync with viewport scrolling, at the cost
// of covering whatever's underneath while it's shown.
func (m *Model) previewRect() (x, y, w, h int) {
	w = m.width / 2
	if w > 60 {
		w = 60
	}
	if w < 10 {
		w = 10
	}
	h = m.height - 6
	if h > 30 {
		h = 30
	}
	if h < 5 {
		h = 5
	}
	x = m.width - w
	if x < 0 {
		x = 0
	}
	return x, 2, w, h
}

// showPreview (re)draws the current preview at previewPath, e.g. after a
// resize. It's a no-op if there's no preview loaded or ueberzug isn't
// available.
func (m *Model) showPreview() {
	if m.ueberzug == nil || m.previewPath == "" || m.width == 0 {
		return
	}
	x, y, w, h := m.previewRect()
	m.ueberzug.Add("preview", m.previewPath, x, y, w, h)
}

// hidePreview clears any currently showing image preview overlay. Safe to
// call whether or not one is actually showing.
func (m *Model) hidePreview() {
	if m.ueberzug != nil {
		m.ueberzug.Remove("preview")
	}
	m.previewShowing = false
	m.previewMsgID = ""
	m.previewPath = ""
}
