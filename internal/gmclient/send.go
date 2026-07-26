package gmclient

import (
	"fmt"
	"strings"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/util"
)

// ResolveConversation gets or creates a conversation with the given phone
// numbers. A single number resolves/creates a 1:1 DM; multiple numbers
// resolve/create a group, mirroring how the Google Messages web client
// itself starts new threads.
func ResolveConversation(client *libgm.Client, phones []string) (*gmproto.Conversation, error) {
	numbers := make([]*gmproto.ContactNumber, len(phones))
	for i, phone := range phones {
		numbers[i] = &gmproto.ContactNumber{
			MysteriousInt: 2,
			Number:        phone,
			Number2:       phone,
		}
	}
	resp, err := client.GetOrCreateConversation(&gmproto.GetOrCreateConversationRequest{Numbers: numbers})
	if err != nil {
		return nil, fmt.Errorf("get or create conversation: %w", err)
	}
	if resp.GetConversation().GetConversationID() == "" {
		return nil, fmt.Errorf("no conversation returned for %s (status: %s)", strings.Join(phones, ", "), resp.GetStatus())
	}
	return resp.Conversation, nil
}

// DefaultSIM picks an outgoing SIM from a settings snapshot returned by
// Connect. It just takes the first one; multi-SIM selection isn't
// supported yet. A nil settings/empty SIM list yields a nil SIM, which is
// fine for single-SIM accounts (SendText tolerates it).
func DefaultSIM(settings *gmproto.Settings) *gmproto.SIMCard {
	if len(settings.GetSIMCards()) == 0 {
		return nil
	}
	return settings.SIMCards[0]
}

// Send sends a message to conv using sim as the outgoing SIM (nil is fine
// for single-SIM accounts), with text and/or one or more attachments. At
// least one of text or attachments must be non-empty.
func Send(client *libgm.Client, conv *gmproto.Conversation, sim *gmproto.SIMCard, text string, attachments []*gmproto.MediaContent) error {
	if text == "" && len(attachments) == 0 {
		return fmt.Errorf("message has neither text nor attachments")
	}
	tmpID := util.GenerateTmpID()
	var info []*gmproto.MessageInfo
	if text != "" {
		info = append(info, &gmproto.MessageInfo{
			Data: &gmproto.MessageInfo_MessageContent{MessageContent: &gmproto.MessageContent{
				Content: text,
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
	resp, err := client.SendMessage(req)
	if err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	if resp.GetStatus() != gmproto.SendMessageResponse_SUCCESS {
		return fmt.Errorf("send failed: status %s", resp.GetStatus())
	}
	return nil
}
