package gmclient

import (
	"strings"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// MessageText joins a message's text parts the way Google Messages
// displays them: one line per part, in order.
func MessageText(msg *gmproto.Message) string {
	var parts []string
	for _, info := range msg.GetMessageInfo() {
		content, ok := info.GetData().(*gmproto.MessageInfo_MessageContent)
		if !ok {
			continue
		}
		if text := content.MessageContent.GetContent(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// SenderName returns a display name for whoever sent msg: "You" for
// messages sent from this account, otherwise their first/full name or
// phone number, falling back to the raw participant ID.
func SenderName(conv *gmproto.Conversation, msg *gmproto.Message) string {
	participantID := msg.GetSenderParticipant().GetID().GetParticipantID()
	if participantID == "" {
		participantID = msg.GetParticipantID()
	}
	for _, p := range conv.GetParticipants() {
		if p.GetID().GetParticipantID() != participantID {
			continue
		}
		if p.GetIsMe() {
			return "You"
		}
		if p.GetFullName() != "" {
			return p.GetFullName()
		}
		if p.GetFirstName() != "" {
			return p.GetFirstName()
		}
		if p.GetFormattedNumber() != "" {
			return p.GetFormattedNumber()
		}
	}
	if participantID == conv.GetDefaultOutgoingID() {
		return "You"
	}
	return participantID
}
