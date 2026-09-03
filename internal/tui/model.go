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
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/ipc"
	"github.com/inVariabl/gmesg/internal/ueberzug"
)

type focus int

const (
	focusList focus = iota
	focusThread
)

type threadMode int

const (
	modeNormal threadMode = iota
	modeInsert
)

var reactionEmojis = []string{"👍", "💀", "😭", "😂", "😐", "😳", "🥀", "🍅"}

type Model struct {
	ipcClient *ipc.Client
	cacheDir  string
	ueberzug  *ueberzug.Client

	focus      focus
	threadMode threadMode
	pendingG   bool
	pendingY   bool
	list       list.Model
	thread     viewport.Model
	compose    textarea.Model

	conversations map[string]*gmproto.Conversation
	messages      map[string][]*gmproto.Message
	activeConvID  string

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
var linkStyle = lipgloss.NewStyle().Underline(true).Foreground(accentColor)

func New(ipcClient *ipc.Client, cacheDir string, uz *ueberzug.Client) Model {
	convDelegate := list.NewDefaultDelegate()
	convDelegate.Styles = newItemStyles()
	convList := list.New(nil, convDelegate, 0, 0)
	convList.Title = "Conversations"
	convList.Styles = newListStyles()
	convList.SetShowHelp(false)

	compose := textarea.New()
	compose.Placeholder = "Type a message, Enter to send..."
	compose.ShowLineNumbers = false
	compose.SetHeight(2)
	compose.FocusedStyle.CursorLine = lipgloss.NewStyle()
	compose.FocusedStyle.Placeholder = lipgloss.NewStyle().Faint(true)
	compose.BlurredStyle.CursorLine = lipgloss.NewStyle()
	compose.BlurredStyle.Placeholder = lipgloss.NewStyle().Faint(true)
	compose.BlurredStyle.Text = lipgloss.NewStyle().Faint(true)

	attachInput := textinput.New()
	attachInput.Placeholder = "path to file (~ expands to $HOME)"
	attachInput.PlaceholderStyle = lipgloss.NewStyle().Faint(true)

	newConvInput := textinput.New()
	newConvInput.Placeholder = "phone number(s), comma-separated for a group"
	newConvInput.PlaceholderStyle = lipgloss.NewStyle().Faint(true)

	commandInput := textinput.New()
	commandInput.Placeholder = "command (:q :open :react :preview :attach :new :search :send :help)"
	commandInput.Prompt = ":"
	commandInput.PlaceholderStyle = lipgloss.NewStyle().Faint(true)

	return Model{
		ipcClient:     ipcClient,
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
	return tea.Batch(fetchConversations(m.ipcClient), waitForEvent(m.ipcClient))
}

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

	textWidth := m.thread.Width
	paddedWidth := m.thread.Width - 2

	var b strings.Builder
	offsets := make([]int, len(msgs))
	lineNo := 0
	for i, msg := range msgs {
		t := wrapText(highlightLinks(gmclient.MessageText(msg)), textWidth)
		attachments := wrapText(gmclient.AttachmentSummary(msg), paddedWidth)
		reactions := wrapText(gmclient.ReactionSummary(msg), paddedWidth)
		if t == "" && attachments == "" && reactions == "" {
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
		if t != "" {
			block.WriteString(t)
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

func wrapText(s string, width int) string {
	if width <= 0 || s == "" {
		return s
	}
	return lipgloss.NewStyle().Width(width).Render(s)
}

// highlightLinks underlines every http(s) URL in s (see linkStyle), so a
// link reads as clickable without needing a live mouse-hover state.
func highlightLinks(s string) string {
	spans := gmclient.LinkSpans(s)
	if len(spans) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, span := range spans {
		b.WriteString(s[last:span.Start])
		b.WriteString(linkStyle.Render(span.Text))
		last = span.End
	}
	b.WriteString(s[last:])
	return b.String()
}

func (m *Model) moveMsgCursor(delta int) {
	msgs := m.messages[m.activeConvID]
	if len(msgs) == 0 {
		return
	}
	m.hidePreview()
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

func (m *Model) showPreview() {
	if m.ueberzug == nil || m.previewPath == "" || m.width == 0 {
		return
	}
	x, y, w, h := m.previewRect()
	m.ueberzug.Add("preview", m.previewPath, x, y, w, h)
}

func (m *Model) hidePreview() {
	if m.ueberzug != nil {
		m.ueberzug.Remove("preview")
	}
	m.previewShowing = false
	m.previewMsgID = ""
	m.previewPath = ""
}
