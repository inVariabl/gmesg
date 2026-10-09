package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/util"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/inVariabl/gmesg/internal/contacts"
	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/ipc"
)

const connectTimeout = 15 * time.Second

func runDaemon() error {
	// The daemon spends nearly all of its life idle, waiting on the long-poll
	// connection, so a soft cap keeps its resident set proportional to actual
	// idle usage instead of the working-set peak from the last attachment
	// transfer. The limit is soft: the garbage collector works harder to stay
	// under it but will exceed it rather than fail an allocation.
	debug.SetMemoryLimit(64 << 20)

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
	if err := n.seedConversations(); errors.Is(err, events.ErrCallerNoPermission) {
		// Google refreshes the token of a session it no longer serves, then
		// refuses every request made with it, so the daemon would otherwise
		// report that it is listening while receiving nothing.
		client.Disconnect()
		if !client.AuthData.IsGoogleAccount() {
			return fmt.Errorf("Google refused this session, which was paired by QR code and is no longer served -- run \"gmesg pair\" to pair through the Google Account: %w", err)
		}
		return fmt.Errorf("Google refused this session -- run \"gmesg pair\" again: %w", err)
	} else if err != nil {
		logger.Warn().Err(err).Msg("Failed to seed conversation cache, sender names may be missing at first")
	}

	ipcServer := ipc.NewServer(socketPath, n)
	if err := ipcServer.Start(); err != nil {
		return fmt.Errorf("start IPC server: %w", err)
	}
	defer ipcServer.Close()
	logger.Info().Str("socket", socketPath).Msg("IPC server listening")

	sup := newSupervisor(client, logger)
	fatalCh := make(chan error, 1)
	reportFatal := func(err error) {
		select {
		case fatalCh <- err:
		default:
		}
	}

	client.SetEventHandler(func(evt any) {
		switch v := evt.(type) {
		case *events.AuthTokenRefreshed:
			// The refreshed token must reach the disk now: if the daemon is
			// terminated without the shutdown path running, the next start
			// would otherwise present the superseded token.
			go func() {
				if err := gmclient.Save(profile, client); err != nil {
					logger.Warn().Err(err).Msg("Failed to save refreshed session")
				}
			}()
		case *events.PhoneNotResponding:
			sup.setResponsive(false)
		case *events.PhoneRespondingAgain:
			sup.setResponsive(true)
		case *events.PingFailed:
			logger.Warn().Err(v.Error).Msg("Ping failed, requesting reconnection")
			sup.requestReconnect()
		case *events.ListenFatalError:
			reportFatal(fmt.Errorf("connection terminated by the server: %w", v.Error))
		case *events.GaiaLoggedOut:
			reportFatal(fmt.Errorf("the Google account session was signed out -- run \"gmesg pair\" again"))
		case *gmproto.RevokePairData:
			reportFatal(fmt.Errorf("the phone revoked this pairing -- run \"gmesg pair\" again"))
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
	stopSupervisor := make(chan struct{})
	go sup.run(stopSupervisor)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	var fatalErr error
	select {
	case <-sigCh:
	case fatalErr = <-fatalCh:
	}
	close(stopSupervisor)

	client.Disconnect()
	if err := gmclient.Save(profile, client); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return fatalErr
}

const (
	// superviseInterval is how often the supervisor inspects the connection.
	superviseInterval = time.Minute
	// unresponsiveLimit is how long the phone may fail to answer pings
	// before the long-poll stream is presumed dead and replaced.
	unresponsiveLimit = 5 * time.Minute
	// reconnectInterval is the minimum time between two reconnections, so
	// that a phone which is genuinely offline does not cause a reconnection
	// on every inspection.
	reconnectInterval = 10 * time.Minute
	// suspendThreshold is the wall-clock gap between two inspections beyond
	// which the host is presumed to have been suspended.
	suspendThreshold = 3 * superviseInterval
)

// supervisor keeps the long-poll connection alive across conditions libgm
// does not recover from by itself. libgm refreshes the tachyon auth token
// only when it opens a new long-poll request, so a stream that stalls
// without closing -- as one does after a host suspend or a network change --
// is never replaced, the token expires roughly a day later, and every
// subsequent request fails with HTTP 401. The supervisor replaces the
// stream when the token approaches expiry, when the phone has stopped
// answering pings, when a ping fails, and when the host resumes from
// suspend. Client.Reconnect refreshes the token before reopening the
// stream.
type supervisor struct {
	client *libgm.Client
	logger zerolog.Logger

	reconnectCh chan struct{}

	mu                sync.Mutex
	unresponsiveSince time.Time
}

func newSupervisor(client *libgm.Client, logger zerolog.Logger) *supervisor {
	return &supervisor{client: client, logger: logger, reconnectCh: make(chan struct{}, 1)}
}

func (s *supervisor) setResponsive(responsive bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if responsive {
		s.unresponsiveSince = time.Time{}
	} else if s.unresponsiveSince.IsZero() {
		s.unresponsiveSince = time.Now()
	}
}

func (s *supervisor) requestReconnect() {
	select {
	case s.reconnectCh <- struct{}{}:
	default:
	}
}

func (s *supervisor) run(stop <-chan struct{}) {
	ticker := time.NewTicker(superviseInterval)
	defer ticker.Stop()
	// Round(0) discards the monotonic reading, so that the comparison uses
	// the wall clock, which advances during suspend while the monotonic
	// clock does not.
	lastInspection := time.Now().Round(0)
	var lastReconnect time.Time
	for {
		var reason string
		select {
		case <-stop:
			return
		case <-s.reconnectCh:
			reason = "ping failure"
		case now := <-ticker.C:
			wallNow := now.Round(0)
			gap := wallNow.Sub(lastInspection)
			lastInspection = wallNow
			s.mu.Lock()
			unresponsiveSince := s.unresponsiveSince
			s.mu.Unlock()
			switch {
			case gap > suspendThreshold:
				reason = "resume from suspend"
				// A resume justifies an immediate reconnection regardless
				// of when the previous one took place.
				lastReconnect = time.Time{}
			case time.Until(s.client.AuthData.TachyonExpiry) < libgm.RefreshTachyonBuffer:
				reason = "auth token expiry"
				lastReconnect = time.Time{}
			case !unresponsiveSince.IsZero() && time.Since(unresponsiveSince) > unresponsiveLimit:
				reason = "phone unresponsive"
			default:
				continue
			}
		}
		if !lastReconnect.IsZero() && time.Since(lastReconnect) < reconnectInterval {
			continue
		}
		lastReconnect = time.Now()
		s.logger.Info().Str("reason", reason).Msg("Reconnecting")
		if err := s.client.Reconnect(); err != nil {
			s.logger.Err(err).Msg("Reconnection failed, retrying later")
			continue
		}
		s.setResponsive(true)
	}
}

type notifier struct {
	client   *libgm.Client
	logger   zerolog.Logger
	settings *gmproto.Settings

	mu            sync.Mutex
	conversations map[string]*gmproto.Conversation

	// phoneContacts caches the paired phone's contact list after the first
	// successful LIST_CONTACTS call, since that request is relayed through
	// the phone and the list changes rarely.
	contactsMu    sync.Mutex
	phoneContacts []contacts.Contact
}

func (n *notifier) seedConversations() error {
	_, err := n.fetchConversations()
	return err
}

// fetchConversations issues a live LIST_CONVERSATIONS call, which under the
// RCS transport is relayed through the paired phone and answered only once
// the phone responds -- a round trip that can take several seconds if the
// phone was asleep. It refreshes the cache as a side effect.
func (n *notifier) fetchConversations() ([]*gmproto.Conversation, error) {
	resp, err := n.client.ListConversations(25, gmproto.ListConversationsRequest_INBOX)
	if err != nil {
		return nil, err
	}
	convs := resp.GetConversations()
	n.mu.Lock()
	for _, conv := range convs {
		n.conversations[conv.GetConversationID()] = conv
	}
	n.mu.Unlock()
	return convs, nil
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
	case "list_contacts":
		return n.handleListContacts()
	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

// handleListConversations answers from the conversation cache -- populated
// at startup by seedConversations and kept current by the conversation-event
// case in the client's event handler -- rather than issuing a fresh
// LIST_CONVERSATIONS round trip through the phone on every call. It falls
// back to a live fetch only when the cache hasn't been populated yet.
func (n *notifier) handleListConversations() (json.RawMessage, error) {
	n.mu.Lock()
	convs := make([]*gmproto.Conversation, 0, len(n.conversations))
	for _, conv := range n.conversations {
		if conv.GetStatus() == gmproto.ConversationStatus_ACTIVE {
			convs = append(convs, conv)
		}
	}
	n.mu.Unlock()

	if len(convs) == 0 {
		fetched, err := n.fetchConversations()
		if err != nil {
			return nil, err
		}
		convs = fetched
	}

	sort.Slice(convs, func(i, j int) bool {
		if convs[i].GetPinned() != convs[j].GetPinned() {
			return convs[i].GetPinned()
		}
		return convs[i].GetLastMessageTimestamp() > convs[j].GetLastMessageTimestamp()
	})
	if len(convs) > 25 {
		convs = convs[:25]
	}

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

// handleListContacts returns the abook address book merged with the paired
// phone's contact list. The address book is read on every call so that an
// edit made in abook takes effect without restarting the daemon. If the
// phone's list cannot be obtained, the address book alone is returned.
func (n *notifier) handleListContacts() (json.RawMessage, error) {
	path, err := contacts.DefaultAbookPath()
	if err != nil {
		return nil, fmt.Errorf("resolve address book path: %w", err)
	}
	abook, err := contacts.LoadAbook(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	phone, err := n.loadPhoneContacts()
	if err != nil {
		n.logger.Warn().Err(err).Msg("Failed to list phone contacts, using the address book alone")
	}
	return json.Marshal(map[string]any{"contacts": contacts.Merge(abook, phone)})
}

func (n *notifier) loadPhoneContacts() ([]contacts.Contact, error) {
	n.contactsMu.Lock()
	defer n.contactsMu.Unlock()
	if n.phoneContacts != nil {
		return n.phoneContacts, nil
	}
	resp, err := n.client.ListContacts()
	if err != nil {
		return nil, err
	}
	list := []contacts.Contact{}
	for _, c := range resp.GetContacts() {
		number := c.GetNumber().GetNumber()
		if c.GetName() == "" || contacts.NormalizeNumber(number) == "" {
			continue
		}
		list = append(list, contacts.Contact{
			Name:    c.GetName(),
			Numbers: []contacts.Number{{Value: number, Label: "phone"}},
		})
	}
	n.phoneContacts = list
	return list, nil
}
