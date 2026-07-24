package tui

import (
	"fmt"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// conversationItem adapts a gmproto.Conversation to bubbles/list.DefaultItem.
type conversationItem struct {
	conv *gmproto.Conversation
}

func (i conversationItem) FilterValue() string { return i.conv.GetName() }

func (i conversationItem) Title() string {
	name := i.conv.GetName()
	if name == "" {
		name = "(unknown)"
	}
	if i.conv.GetUnread() {
		return "● " + name // filled circle marks unread, same idea as an unread dot in a chat app
	}
	return "  " + name
}

func (i conversationItem) Description() string {
	snippet := i.conv.GetLatestMessage().GetDisplayContent()
	if snippet == "" {
		return " "
	}
	if i.conv.GetLatestMessage().GetFromMe() != 0 {
		return fmt.Sprintf("You: %s", snippet)
	}
	return snippet
}
