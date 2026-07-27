package tui

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/ipc"
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

type reactionResultMsg struct {
	err error
}

type openResultMsg struct {
	info string
	err  error
}

type previewReadyMsg struct {
	msgID string
	path  string
	info  string
	err   error
}

type conversationResolvedMsg struct {
	conv *gmproto.Conversation
	err  error
}

type gmEventMsg struct{ evt any }

func fetchConversations(ipcClient *ipc.Client) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Conversations []json.RawMessage `json:"conversations"`
		}
		if err := ipcClient.Call("list_conversations", nil, &result); err != nil {
			return conversationsLoadedMsg{err: err}
		}
		convs := make([]*gmproto.Conversation, len(result.Conversations))
		for i, raw := range result.Conversations {
			var conv gmproto.Conversation
			if err := protojson.Unmarshal(raw, &conv); err != nil {
				return conversationsLoadedMsg{err: fmt.Errorf("decode conversation: %w", err)}
			}
			convs[i] = &conv
		}
		return conversationsLoadedMsg{convs: convs}
	}
}

func fetchThread(ipcClient *ipc.Client, convID string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Messages []json.RawMessage `json:"messages"`
		}
		params := map[string]string{"conv_id": convID}
		if err := ipcClient.Call("fetch_messages", params, &result); err != nil {
			return threadLoadedMsg{convID: convID, err: err}
		}
		msgs := make([]*gmproto.Message, len(result.Messages))
		for i, raw := range result.Messages {
			var msg gmproto.Message
			if err := protojson.Unmarshal(raw, &msg); err != nil {
				return threadLoadedMsg{convID: convID, err: fmt.Errorf("decode message: %w", err)}
			}
			msgs[i] = &msg
		}
		return threadLoadedMsg{convID: convID, msgs: msgs}
	}
}

func resolveConversation(ipcClient *ipc.Client, phones []string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Conversation json.RawMessage `json:"conversation"`
		}
		params := map[string][]string{"phones": phones}
		if err := ipcClient.Call("resolve_conversation", params, &result); err != nil {
			return conversationResolvedMsg{err: err}
		}
		var conv gmproto.Conversation
		if err := protojson.Unmarshal(result.Conversation, &conv); err != nil {
			return conversationResolvedMsg{err: fmt.Errorf("decode conversation: %w", err)}
		}
		return conversationResolvedMsg{conv: &conv}
	}
}

func sendMessage(ipcClient *ipc.Client, conv *gmproto.Conversation, text string, attachmentPaths []string) tea.Cmd {
	return func() tea.Msg {
		params := map[string]any{
			"conv_id":          conv.GetConversationID(),
			"text":             text,
			"attachment_paths": attachmentPaths,
		}
		var result struct {
			ConvID string `json:"conv_id"`
		}
		err := ipcClient.Call("send_message", params, &result)
		return sendResultMsg{convID: conv.GetConversationID(), err: err}
	}
}

func sendReaction(ipcClient *ipc.Client, target *gmproto.Message, emoji string) tea.Cmd {
	return func() tea.Msg {
		params := map[string]string{
			"msg_id": target.GetMessageID(),
			"emoji":  emoji,
		}
		err := ipcClient.Call("send_reaction", params, nil)
		return reactionResultMsg{err: err}
	}
}

func openMessage(ipcClient *ipc.Client, cacheDir string, msg *gmproto.Message) tea.Cmd {
	return func() tea.Msg {
		attachments := gmclient.MessageAttachments(msg)
		links := gmclient.ExtractLinks(msg)
		if len(attachments) == 0 && len(links) == 0 {
			return openResultMsg{err: fmt.Errorf("selected message has no attachments or links")}
		}
		var requested []string
		for _, a := range attachments {
			if !gmclient.AttachmentReady(a.Media) {
				params := map[string]string{
					"msg_id":        a.MessageID,
					"action_msg_id": a.ActionMessageID,
				}
				if err := ipcClient.Call("request_full_media", params, nil); err != nil {
					return openResultMsg{err: err}
				}
				requested = append(requested, a.Media.GetMediaName())
				continue
			}
			params := map[string]any{
				"media_id":       a.Media.GetMediaID(),
				"media_name":     a.Media.GetMediaName(),
				"decryption_key": a.Media.GetDecryptionKey(),
				"cache_dir":      cacheDir,
			}
			var result struct {
				Path string `json:"path"`
			}
			if err := ipcClient.Call("download_attachment", params, &result); err != nil {
				return openResultMsg{err: err}
			}
			if err := openWithDefaultApp(result.Path); err != nil {
				return openResultMsg{err: err}
			}
		}
		for _, link := range links {
			if err := openWithDefaultApp(link); err != nil {
				return openResultMsg{err: err}
			}
		}
		if len(requested) > 0 {
			return openResultMsg{info: fmt.Sprintf("requested full download for %s -- give it a few seconds and press o again", strings.Join(requested, ", "))}
		}
		return openResultMsg{}
	}
}

func openWithDefaultApp(path string) error {
	cmd := exec.Command("xdg-open", path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	go cmd.Wait()
	return nil
}

func previewImage(ipcClient *ipc.Client, cacheDir string, msg *gmproto.Message) tea.Cmd {
	return func() tea.Msg {
		msgID := msg.GetMessageID()
		a, ok := gmclient.FirstImageAttachment(msg)
		if !ok {
			return previewReadyMsg{msgID: msgID, info: "selected message has no image to preview"}
		}
		if !gmclient.AttachmentReady(a.Media) {
			params := map[string]string{
				"msg_id":        a.MessageID,
				"action_msg_id": a.ActionMessageID,
			}
			if err := ipcClient.Call("request_full_media", params, nil); err != nil {
				return previewReadyMsg{msgID: msgID, err: err}
			}
			return previewReadyMsg{msgID: msgID, info: fmt.Sprintf("requested full download for %s -- give it a few seconds and press p again", a.Media.GetMediaName())}
		}
		params := map[string]any{
			"media_id":       a.Media.GetMediaID(),
			"media_name":     a.Media.GetMediaName(),
			"decryption_key": a.Media.GetDecryptionKey(),
			"cache_dir":      cacheDir,
		}
		var result struct {
			Path string `json:"path"`
		}
		if err := ipcClient.Call("download_attachment", params, &result); err != nil {
			return previewReadyMsg{msgID: msgID, err: err}
		}
		return previewReadyMsg{msgID: msgID, path: result.Path}
	}
}

func waitForEvent(ipcClient *ipc.Client) tea.Cmd {
	return func() tea.Msg {
		return gmEventMsg{evt: <-ipcClient.Events}
	}
}
