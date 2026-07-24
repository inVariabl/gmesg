package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/inVariabl/gmesg/internal/gmclient"
)

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
	_, err = gmclient.Connect(ctx, client)
	cancel()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	n := &notifier{client: client, logger: logger, conversations: map[string]*gmproto.Conversation{}}
	if err := n.seedConversations(); err != nil {
		logger.Warn().Err(err).Msg("Failed to seed conversation cache, sender names may be missing at first")
	}
	client.SetEventHandler(n.handleEvent)

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

// notifier turns incoming-message events into notify-send calls. It keeps
// its own small conversation cache (independent of the TUI's) since a
// daemon run has no other source of conversation metadata to draw sender
// names and chat titles from.
type notifier struct {
	client *libgm.Client
	logger zerolog.Logger

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

func (n *notifier) handleEvent(evt any) {
	switch v := evt.(type) {
	case *gmproto.Conversation:
		n.mu.Lock()
		n.conversations[v.GetConversationID()] = v
		n.mu.Unlock()
	case *libgm.WrappedMessage:
		if v.IsOld {
			return
		}
		n.handleMessage(v.Message)
	}
}

func (n *notifier) handleMessage(msg *gmproto.Message) {
	conv := n.getConversation(msg.GetConversationID())
	if conv != nil && msg.GetParticipantID() == conv.GetDefaultOutgoingID() {
		return // our own outgoing message, echoed back through the event stream
	}
	text := gmclient.MessageText(msg)
	if text == "" {
		return // no text part (e.g. a reaction or media-only message); attachments aren't handled yet
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
