package tui

import (
	"fmt"
	"os/exec"
	"strings"

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

type reactionResultMsg struct {
	err error
}

type openResultMsg struct {
	info string // non-error status to show, e.g. "still fetching, try again shortly"
	err  error
}

// previewReadyMsg carries the outcome of downloading a message's image
// for the inline preview pane. path is empty (with info or err set) when
// there's nothing to show yet.
type previewReadyMsg struct {
	msgID string // guards against a stale download landing after the user moved on
	path  string
	info  string
	err   error
}

type conversationResolvedMsg struct {
	conv *gmproto.Conversation
	err  error
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

// resolveConversation gets or creates a conversation with the given phone
// numbers (see gmclient.ResolveConversation) so the "n" new-conversation
// prompt can jump straight into a thread that may not have existed before.
func resolveConversation(client *libgm.Client, phones []string) tea.Cmd {
	return func() tea.Msg {
		conv, err := gmclient.ResolveConversation(client, phones)
		return conversationResolvedMsg{conv: conv, err: err}
	}
}

// sendMessage uploads any attachmentPaths and sends them alongside text.
// Uploads happen synchronously inside the returned tea.Cmd's goroutine, so
// the UI stays responsive while a large attachment is in flight.
func sendMessage(client *libgm.Client, conv *gmproto.Conversation, sim *gmproto.SIMCard, text string, attachmentPaths []string) tea.Cmd {
	return func() tea.Msg {
		var attachments []*gmproto.MediaContent
		for _, path := range attachmentPaths {
			media, err := gmclient.UploadAttachment(client, path)
			if err != nil {
				return sendResultMsg{convID: conv.GetConversationID(), err: fmt.Errorf("attach %s: %w", path, err)}
			}
			attachments = append(attachments, media)
		}
		err := gmclient.Send(client, conv, sim, text, attachments)
		return sendResultMsg{convID: conv.GetConversationID(), err: err}
	}
}

func sendReaction(client *libgm.Client, sim *gmproto.SIMCard, target *gmproto.Message, emoji string) tea.Cmd {
	return func() tea.Msg {
		err := gmclient.SendReaction(client, sim, target, emoji)
		return reactionResultMsg{err: err}
	}
}

// openMessage hands every attachment and http(s) link on msg to the
// desktop's default opener: attachments are downloaded into cacheDir first
// (skipping ones already downloaded); links are passed straight through.
// Attachments that haven't finished transferring from the phone yet get an
// explicit full-media request instead -- Google won't push the decryption
// key on its own, the client has to ask for it (see gmclient.RequestFullMedia)
// -- and the caller is expected to try again in a few seconds once the
// follow-up event lands.
func openMessage(client *libgm.Client, cacheDir string, msg *gmproto.Message) tea.Cmd {
	return func() tea.Msg {
		attachments := gmclient.MessageAttachments(msg)
		links := gmclient.ExtractLinks(msg)
		if len(attachments) == 0 && len(links) == 0 {
			return openResultMsg{err: fmt.Errorf("selected message has no attachments or links")}
		}
		var requested []string
		for _, a := range attachments {
			if !gmclient.AttachmentReady(a.Media) {
				if err := gmclient.RequestFullMedia(client, a); err != nil {
					return openResultMsg{err: err}
				}
				requested = append(requested, a.Media.GetMediaName())
				continue
			}
			path, err := gmclient.EnsureCached(client, cacheDir, a.Media)
			if err != nil {
				return openResultMsg{err: err}
			}
			if err := openWithDefaultApp(path); err != nil {
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

// openWithDefaultApp hands path (a local file path or a URL) to the
// desktop's default opener.
func openWithDefaultApp(path string) error {
	cmd := exec.Command("xdg-open", path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	go cmd.Wait() // reap the child; we don't care about its exit status
	return nil
}

// previewImage resolves the local path of msg's first image attachment for
// the inline preview pane, downloading it if needed (or requesting it from
// the phone if it hasn't arrived yet). A message with no image attachment
// resolves with an empty path and an explanatory info string.
func previewImage(client *libgm.Client, cacheDir string, msg *gmproto.Message) tea.Cmd {
	return func() tea.Msg {
		msgID := msg.GetMessageID()
		a, ok := gmclient.FirstImageAttachment(msg)
		if !ok {
			return previewReadyMsg{msgID: msgID, info: "selected message has no image to preview"}
		}
		if !gmclient.AttachmentReady(a.Media) {
			if err := gmclient.RequestFullMedia(client, a); err != nil {
				return previewReadyMsg{msgID: msgID, err: err}
			}
			return previewReadyMsg{msgID: msgID, info: fmt.Sprintf("requested full download for %s -- give it a few seconds and press p again", a.Media.GetMediaName())}
		}
		path, err := gmclient.EnsureCached(client, cacheDir, a.Media)
		if err != nil {
			return previewReadyMsg{msgID: msgID, err: err}
		}
		return previewReadyMsg{msgID: msgID, path: path}
	}
}

func waitForEvent(ch <-chan any) tea.Cmd {
	return func() tea.Msg {
		return gmEventMsg{evt: <-ch}
	}
}
