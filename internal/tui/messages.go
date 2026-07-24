package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/inVariabl/gmesg/internal/gmclient"
)

type conversationsLoadedMsg struct {
	convs []*gmproto.Conversation
	err   error
}

type threadLoadedMsg struct {
	convID string
	msgs   []*gmproto.Message
	err    error
}

type sendResultMsg struct {
	convID string
	err    error
}

type gmEventMsg struct{ evt any }

func fetchConversations(client *libgm.Client) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.ListConversations(25, gmproto.ListConversationsRequest_INBOX)
		if err != nil {
			return conversationsLoadedMsg{err: err}
		}
		return conversationsLoadedMsg{convs: resp.GetConversations()}
	}
}

func fetchThread(client *libgm.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		resp, err := client.FetchMessages(convID, 50, nil)
		if err != nil {
			return threadLoadedMsg{convID: convID, err: err}
		}
		msgs := resp.GetMessages()
		// The API returns newest-first; render oldest-first like a chat log.
		for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
			msgs[i], msgs[j] = msgs[j], msgs[i]
		}
		return threadLoadedMsg{convID: convID, msgs: msgs}
	}
}

func sendMessage(client *libgm.Client, conv *gmproto.Conversation, sim *gmproto.SIMCard, text string) tea.Cmd {
	return func() tea.Msg {
		err := gmclient.SendText(client, conv, sim, text)
		return sendResultMsg{convID: conv.GetConversationID(), err: err}
	}
}

func waitForEvent(ch <-chan any) tea.Cmd {
	return func() tea.Msg {
		return gmEventMsg{evt: <-ch}
	}
}
