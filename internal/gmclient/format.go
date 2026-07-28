package gmclient

import (
	"fmt"
	"regexp"
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

// SenderParticipantID returns the participant ID of whoever sent msg.
func SenderParticipantID(msg *gmproto.Message) string {
	if id := msg.GetSenderParticipant().GetID().GetParticipantID(); id != "" {
		return id
	}
	return msg.GetParticipantID()
}

// SenderIsSelf reports whether msg was sent from this account.
func SenderIsSelf(conv *gmproto.Conversation, msg *gmproto.Message) bool {
	participantID := SenderParticipantID(msg)
	for _, p := range conv.GetParticipants() {
		if p.GetID().GetParticipantID() == participantID {
			return p.GetIsMe()
		}
	}
	return participantID == conv.GetDefaultOutgoingID()
}

// SenderName returns a display name for whoever sent msg: "You" for
// messages sent from this account, otherwise their first/full name or
// phone number, falling back to the raw participant ID.
func SenderName(conv *gmproto.Conversation, msg *gmproto.Message) string {
	participantID := SenderParticipantID(msg)
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

// Attachment pairs a message's media part with the identifiers needed to
// ask Google to push the full-size version (see RequestFullMedia) if it
// hasn't arrived with a decryption key yet.
type Attachment struct {
	Media           *gmproto.MediaContent
	MessageID       string
	ActionMessageID string
}

// MessageAttachments returns a message's media parts (images, video, audio,
// files), in order.
func MessageAttachments(msg *gmproto.Message) []Attachment {
	var out []Attachment
	for _, info := range msg.GetMessageInfo() {
		if media := info.GetMediaContent(); media != nil {
			out = append(out, Attachment{
				Media:           media,
				MessageID:       msg.GetMessageID(),
				ActionMessageID: info.GetActionMessageID(),
			})
		}
	}
	return out
}

// AttachmentSummary renders a message's attachments as a single display
// line, e.g. "🖼 photo.jpg  🎥 clip.mp4". Returns "" if there are none.
func AttachmentSummary(msg *gmproto.Message) string {
	attachments := MessageAttachments(msg)
	if len(attachments) == 0 {
		return ""
	}
	parts := make([]string, len(attachments))
	for i, a := range attachments {
		name := a.Media.GetMediaName()
		if name == "" {
			name = "attachment"
		}
		label := attachmentIcon(a.Media.GetFormat()) + " " + name
		if !AttachmentReady(a.Media) {
			label += " (receiving…)"
		}
		parts[i] = label
	}
	return strings.Join(parts, "  ")
}

// IsImage reports whether a media format is one of the still-image kinds
// (as opposed to video, audio, or a generic file) -- the ones an inline
// terminal preview can actually render.
func IsImage(format gmproto.MediaFormats) bool {
	return format >= gmproto.MediaFormats_IMAGE_JPEG && format <= gmproto.MediaFormats_IMAGE_UNSPECIFIED
}

// FirstImageAttachment returns the first image attachment on msg, if any.
func FirstImageAttachment(msg *gmproto.Message) (Attachment, bool) {
	for _, a := range MessageAttachments(msg) {
		if IsImage(a.Media.GetFormat()) {
			return a, true
		}
	}
	return Attachment{}, false
}

// attachmentIcon picks a display icon from a MediaFormats value based on
// which contiguous block of the enum it falls in (image/video/audio/other),
// mirroring how the format constants are grouped in gmproto.
func attachmentIcon(format gmproto.MediaFormats) string {
	switch {
	case IsImage(format):
		return "🖼"
	case format >= gmproto.MediaFormats_VIDEO_MP4 && format <= gmproto.MediaFormats_VIDEO_UNSPECIFIED:
		return "🎥"
	case format >= gmproto.MediaFormats_AUDIO_AAC && format <= gmproto.MediaFormats_AUDIO_UNSPECIFIED:
		return "🎵"
	default:
		return "📎"
	}
}

// linkPattern matches http(s) URLs in message text. It's deliberately
// simple (no full RFC 3986 validation) since it only needs to catch the
// links people actually paste into a text message.
var linkPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

// ExtractLinks returns every http(s) URL found in a message's text, in
// order, with trailing sentence punctuation (".", ",", ")", etc.) trimmed
// off since it's rarely part of the URL itself.
func ExtractLinks(msg *gmproto.Message) []string {
	spans := LinkSpans(MessageText(msg))
	matches := make([]string, len(spans))
	for i, s := range spans {
		matches[i] = s.Text
	}
	return matches
}

// LinkSpan is one link match within a piece of text: its byte range plus
// the already-trimmed link text (see LinkSpans).
type LinkSpan struct {
	Start, End int
	Text       string
}

// LinkSpans locates every http(s) URL in text and returns its byte range
// alongside the same trailing-punctuation-trimmed text ExtractLinks
// returns, so callers that need to style or otherwise act on a link in
// place (rather than just list the URLs) know exactly which bytes to
// touch.
func LinkSpans(text string) []LinkSpan {
	idxs := linkPattern.FindAllStringIndex(text, -1)
	spans := make([]LinkSpan, len(idxs))
	for i, m := range idxs {
		start, end := m[0], m[1]
		trimmed := strings.TrimRight(text[start:end], ".,!?;:)")
		end = start + len(trimmed)
		spans[i] = LinkSpan{Start: start, End: end, Text: trimmed}
	}
	return spans
}

// ReactionSummary renders a message's reactions as a single display line,
// e.g. "👍 2  ❤️ 1". Returns "" if there are none.
func ReactionSummary(msg *gmproto.Message) string {
	var parts []string
	for _, entry := range msg.GetReactions() {
		n := len(entry.GetParticipantIDs())
		if n == 0 {
			continue
		}
		emoji := entry.GetData().GetUnicode()
		if emoji == "" {
			emoji = entry.GetData().GetType().Unicode()
		}
		if emoji == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d", emoji, n))
	}
	return strings.Join(parts, "  ")
}
