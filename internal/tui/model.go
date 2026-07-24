// Package tui implements gmesg's interactive terminal UI: a conversation
// list and a thread view with a compose box, kept live by libgm's event
// stream.
package tui

import (
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/inVariabl/gmesg/internal/gmclient"
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

// Model is the root bubbletea model for gmesg's TUI.
type Model struct {
	client   *libgm.Client
	settings *gmproto.Settings
	events   chan any

	focus      focus
	threadMode threadMode
	pendingG   bool
	list       list.Model
	thread     viewport.Model
	compose    textarea.Model

	conversations map[string]*gmproto.Conversation
	messages      map[string][]*gmproto.Message
	activeConvID  string

	status string
	err    error

	width, height int
	ready         bool
}

var statusStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
var errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Padding(0, 1)
var headerStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1)

// New builds the TUI model. client must already be connected (see
// gmclient.Connect); events is the channel that client's event handler
// feeds raw libgm events into.
func New(client *libgm.Client, settings *gmproto.Settings, events chan any) Model {
	convList := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	convList.Title = "Conversations"
	convList.SetShowHelp(false)

	compose := textarea.New()
	compose.Placeholder = "Type a message, Enter to send..."
	compose.ShowLineNumbers = false
	compose.SetHeight(2)

	return Model{
		client:        client,
		settings:      settings,
		events:        events,
		list:          convList,
		thread:        viewport.New(0, 0),
		compose:       compose,
		conversations: map[string]*gmproto.Conversation{},
		messages:      map[string][]*gmproto.Message{},
		status:        "Loading conversations...",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(fetchConversations(m.client), waitForEvent(m.events))
}

func (m *Model) outgoingSIM() *gmproto.SIMCard {
	return gmclient.DefaultSIM(m.settings)
}

func (m *Model) renderThread(convID string) {
	conv := m.conversations[convID]
	msgs := m.messages[convID]
	if conv == nil || len(msgs) == 0 {
		m.thread.SetContent("(no messages yet)")
		return
	}
	var b []byte
	for _, msg := range msgs {
		sender := gmclient.SenderName(conv, msg)
		text := gmclient.MessageText(msg)
		if text == "" {
			continue
		}
		ts := time.UnixMicro(msg.GetTimestamp()).Local().Format("15:04")
		line := headerStyle.Render(sender+" · "+ts) + "\n" + text + "\n\n"
		b = append(b, []byte(line)...)
	}
	m.thread.SetContent(string(b))
	m.thread.GotoBottom()
}
