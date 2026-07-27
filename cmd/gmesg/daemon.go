package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/util"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/ipc"
)

const connectTimeout = 15 * time.Second

func runDaemon() error {
	has, err := gmclient.HasSession(profile)
	if err != nil {
		return fmt.Errorf("check session: %w", err)
	}
	if !has {
		return fmt.Errorf("no paired session found -- run \"gmesg pair\" first (use --profile if this daemon should use its own paired identity, separate from the TUI)")
	}

	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.InfoLevel).With().Timestamp().Logger()
	client, _, err := gmclient.New(logger, profile)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	settings, err := gmclient.Connect(ctx, client)
	cancel()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	dataDir, err := gmclient.DataDir(profile)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	socketPath := filepath.Join(dataDir, ipc.SocketName)

	n := &notifier{
		client:        client,
		logger:        logger,
		settings:      settings,
		conversations: map[string]*gmproto.Conversation{},
	}
	if err := n.seedConversations(); err != nil {
		logger.Warn().Err(err).Msg("Failed to seed conversation cache, sender names may be missing at first")
	}

	ipcServer := ipc.NewServer(socketPath, n)
	if err := ipcServer.Start(); err != nil {
		return fmt.Errorf("start IPC server: %w", err)
	}
	defer ipcServer.Close()
	logger.Info().Str("socket", socketPath).Msg("IPC server listening")

	client.SetEventHandler(func(evt any) {
		switch v := evt.(type) {
		case *gmproto.Conversation:
			n.mu.Lock()
			n.conversations[v.GetConversationID()] = v
			n.mu.Unlock()
			convJSON, err := protojson.Marshal(v)
			if err == nil {
				ipcServer.Broadcast("conversation", json.RawMessage(convJSON))
			}
		case *libgm.WrappedMessage:
			if v.IsOld {
				return
			}
			n.handleMessage(v.Message)
			msgJSON, err := protojson.Marshal(v.Message)
			if err == nil {
				ipcServer.Broadcast("message", map[string]any{
					"message": json.RawMessage(msgJSON),
					"is_old":  false,
				})
			}
		}
	})

	logger.Info().Msg("Listening for messages, Ctrl+C to stop")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	client.Disconnect()
	if err := gmclient.Save(profile, client); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

type notifier struct {
	client   *libgm.Client
	logger   zerolog.Logger
	settings *gmproto.Settings

	mu            sync.Mutex
	conversations map[string]*gmproto.Conversation
}

func (n *notifier) seedConversations() error {
	resp, err := n.client.ListConversations(25, gmproto.ListConversationsRequest_INBOX)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, conv := range resp.GetConversations() {
		n.conversations[conv.GetConversationID()] = conv
	}
	return nil
}

func (n *notifier) getConversation(convID string) *gmproto.Conversation {
	n.mu.Lock()
	conv := n.conversations[convID]
	n.mu.Unlock()
	if conv != nil {
		return conv
	}
	conv, err := n.client.GetConversation(convID)
	if err != nil {
		n.logger.Warn().Err(err).Str("conversation_id", convID).Msg("Failed to fetch conversation for notification")
		return nil
	}
	n.mu.Lock()
	n.conversations[convID] = conv
	n.mu.Unlock()
	return conv
}

func (n *notifier) handleMessage(msg *gmproto.Message) {
	conv := n.getConversation(msg.GetConversationID())
	if conv != nil && msg.GetParticipantID() == conv.GetDefaultOutgoingID() {
		return
	}
	text := gmclient.MessageText(msg)
	if text == "" {
		text = gmclient.AttachmentSummary(msg)
	}
	if text == "" {
		return
	}

	title := "gmesg"
	body := text
	if conv != nil {
		title = conv.GetName()
		if conv.GetIsGroupChat() {
			body = gmclient.SenderName(conv, msg) + ": " + text
		}
	}

	if err := exec.Command("notify-send", "-a", "gmesg", "--", title, body).Run(); err != nil {
		n.logger.Warn().Err(err).Msg("Failed to run notify-send")
	}
}

func (n *notifier) Handle(method string, params json.RawMessage) (json.RawMessage, error) {
	switch method {
	case "list_conversations":
		return n.handleListConversations()
	case "fetch_messages":
		return n.handleFetchMessages(params)
	case "get_conversation":
		return n.handleGetConversation(params)
	case "resolve_conversation":
		return n.handleResolveConversation(params)
	case "send_message":
		return n.handleSendMessage(params)
	case "send_reaction":
		return n.handleSendReaction(params)
	case "request_full_media":
		return n.handleRequestFullMedia(params)
	case "download_attachment":
		return n.handleDownloadAttachment(params)
	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

func (n *notifier) handleListConversations() (json.RawMessage, error) {
	resp, err := n.client.ListConversations(25, gmproto.ListConversationsRequest_INBOX)
	if err != nil {
		return nil, err
	}
	convs := resp.GetConversations()
	raw := make([]json.RawMessage, len(convs))
	for i, c := range convs {
		b, err := protojson.Marshal(c)
		if err != nil {
			return nil, fmt.Errorf("marshal conversation: %w", err)
		}
		raw[i] = b
	}
	return json.Marshal(map[string]any{"conversations": raw})
}

func (n *notifier) handleFetchMessages(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		ConvID string `json:"conv_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	resp, err := n.client.FetchMessages(p.ConvID, 50, nil)
	if err != nil {
		return nil, err
	}
	msgs := resp.GetMessages()
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	raw := make([]json.RawMessage, len(msgs))
	for i, m := range msgs {
		b, err := protojson.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("marshal message: %w", err)
		}
		raw[i] = b
	}
	return json.Marshal(map[string]any{"messages": raw})
}

func (n *notifier) handleGetConversation(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		ConvID string `json:"conv_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	conv := n.getConversation(p.ConvID)
	if conv == nil {
		return nil, fmt.Errorf("conversation not found")
	}
	b, err := protojson.Marshal(conv)
	if err != nil {
		return nil, fmt.Errorf("marshal conversation: %w", err)
	}
	return json.Marshal(map[string]any{"conversation": json.RawMessage(b)})
}

func (n *notifier) handleResolveConversation(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		Phones []string `json:"phones"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	conv, err := gmclient.ResolveConversation(n.client, p.Phones)
	if err != nil {
		return nil, err
	}
	b, err := protojson.Marshal(conv)
	if err != nil {
		return nil, fmt.Errorf("marshal conversation: %w", err)
	}
	return json.Marshal(map[string]any{"conversation": json.RawMessage(b)})
}

func (n *notifier) handleSendMessage(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		ConvID          string   `json:"conv_id"`
		Text            string   `json:"text"`
		AttachmentPaths []string `json:"attachment_paths"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	conv := n.getConversation(p.ConvID)
	if conv == nil {
		return nil, fmt.Errorf("conversation not found")
	}
	sim := gmclient.DefaultSIM(n.settings)

	var attachments []*gmproto.MediaContent
	for _, path := range p.AttachmentPaths {
		media, err := gmclient.UploadAttachment(n.client, path)
		if err != nil {
			return nil, fmt.Errorf("attach %s: %w", path, err)
		}
		attachments = append(attachments, media)
	}

	if p.Text == "" && len(attachments) == 0 {
		return nil, fmt.Errorf("message has neither text nor attachments")
	}

	tmpID := util.GenerateTmpID()
	var info []*gmproto.MessageInfo
	if p.Text != "" {
		info = append(info, &gmproto.MessageInfo{
			Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{
				Content: p.Text,
			}},
		})
	}
	for _, media := range attachments {
		info = append(info, &gmproto.MessageInfo{
			Data: &gmproto.MessageInfo_MediaContent{MediaContent: media},
		})
	}
	req := &gmproto.SendMessageRequest{
		ConversationID: conv.ConversationID,
		MessagePayload: &gmproto.MessagePayload{
			TmpID:          tmpID,
			ConversationID: conv.ConversationID,
			ParticipantID:  conv.DefaultOutgoingID,
			TmpID2:         tmpID,
			MessageInfo:    info,
		},
		SIMPayload: sim.GetSIMData().GetSIMPayload(),
		TmpID:      tmpID,
	}
	sendResp, err := n.client.SendMessage(req)
	if err != nil {
		return nil, fmt.Errorf("send message: %w", err)
	}
	if sendResp.GetStatus() != gmproto.SendMessageResponse_SUCCESS {
		return nil, fmt.Errorf("send failed: status %s", sendResp.GetStatus())
	}
	return json.Marshal(map[string]any{"conv_id": p.ConvID})
}

func (n *notifier) handleSendReaction(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		MsgID string `json:"msg_id"`
		Emoji string `json:"emoji"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	sim := gmclient.DefaultSIM(n.settings)
	resp, err := n.client.SendReaction(&gmproto.SendReactionRequest{
		MessageID:    p.MsgID,
		ReactionData: gmproto.MakeReactionData(p.Emoji),
		Action:       gmproto.SendReactionRequest_ADD,
		SIMPayload:   sim.GetSIMData().GetSIMPayload(),
	})
	if err != nil {
		return nil, fmt.Errorf("send reaction: %w", err)
	}
	if !resp.GetSuccess() {
		return nil, fmt.Errorf("send reaction failed")
	}
	return json.Marshal(map[string]any{})
}

func (n *notifier) handleRequestFullMedia(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		MsgID         string `json:"msg_id"`
		ActionMsgID   string `json:"action_msg_id"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if _, err := n.client.GetFullSizeImage(p.MsgID, p.ActionMsgID); err != nil {
		return nil, fmt.Errorf("request full media: %w", err)
	}
	return json.Marshal(map[string]any{})
}

func (n *notifier) handleDownloadAttachment(params json.RawMessage) (json.RawMessage, error) {
	var p struct {
		MediaID        string `json:"media_id"`
		MediaName      string `json:"media_name"`
		DecryptionKey  []byte `json:"decryption_key"`
		CacheDir       string `json:"cache_dir"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	media := &gmproto.MediaContent{
		MediaID:       p.MediaID,
		MediaName:     p.MediaName,
		DecryptionKey: p.DecryptionKey,
	}
	path, err := gmclient.EnsureCached(n.client, p.CacheDir, media)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"path": path})
}
