package tui

import (
	"fmt"
	"strings"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// conversationItem adapts a gmproto.Conversation to bubbles/list.DefaultItem.
type conversationItem struct {
	conv *gmproto.Conversation
}

func (i conversationItem) FilterValue() string { return normalizeColumnWidth(i.conv.GetName()) }

func (i conversationItem) Title() string {
	name := normalizeColumnWidth(i.conv.GetName())
	if name == "" {
		name = "(unknown)"
	}
	if i.conv.GetUnread() {
		return "● " + name // filled circle marks unread, same idea as an unread dot in a chat app
	}
	return "  " + name
}

func (i conversationItem) Description() string {
	snippet := normalizeColumnWidth(i.conv.GetLatestMessage().GetDisplayContent())
	if snippet == "" {
		return " "
	}
	if i.conv.GetLatestMessage().GetFromMe() != 0 {
		return fmt.Sprintf("You: %s", snippet)
	}
	return snippet
}

// normalizeColumnWidth removes the code points that join or modify emoji
// into a single grapheme cluster. Bubbletea measures a line by grapheme
// cluster, so it counts a sequence such as a family emoji (three emoji
// joined by U+200D) as two columns. Terminals that measure each code point
// with wcwidth(3), st among them, draw the same sequence six columns wide.
// The line then wraps, every row beneath it moves down by one, and
// bubbletea's line-by-line redraw leaves stale rows on the screen. Without
// these code points each remaining emoji is its own cluster, and both
// measurements agree.
func normalizeColumnWidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == 0x200D, // zero width joiner
			r >= 0xFE00 && r <= 0xFE0F,   // variation selectors
			r == 0x20E3,                  // combining enclosing keycap
			r >= 0x1F3FB && r <= 0x1F3FF, // skin tone modifiers
			r >= 0xE0020 && r <= 0xE007F: // tag characters (subdivision flags)
			return -1
		}
		return r
	}, s)
}
