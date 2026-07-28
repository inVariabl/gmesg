package tui

import (
	"fmt"
	"testing"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

func textMessage(id, participantID, text string, ts int64) *gmproto.Message {
	return &gmproto.Message{
		MessageID:      id,
		ConversationID: "conv-1",
		ParticipantID:  participantID,
		Timestamp:      ts,
		MessageInfo: []*gmproto.MessageInfo{{
			Data: &gmproto.MessageInfo_MessageContent{
				MessageContent: &gmproto.MessageContent{Content: text},
			},
		}},
	}
}

// Regression test: applyIncomingMessage used to update m.messages and
// re-render without ever moving the viewport, so SetContent left YOffset at
// its old absolute position -- meaning a thread scrolled to the bottom fell
// one message behind every time a new message arrived (whether from the
// other side or from sending one yourself).
func TestApplyIncomingMessageFollowsBottomWhenAlreadyThere(t *testing.T) {
	var msgs []*gmproto.Message
	for i := 0; i < 15; i++ {
		msgs = append(msgs, textMessage(fmt.Sprintf("m%d", i), "them", fmt.Sprintf("message number %d", i), int64(i)))
	}
	m := newTestThreadModel(msgs...)
	m.thread.GotoBottom()

	if !m.thread.AtBottom() {
		t.Fatal("test setup: expected the thread to start at the bottom")
	}
	if m.thread.YOffset == 0 {
		t.Fatal("test setup: expected enough messages for YOffset to be nonzero at the bottom")
	}

	m.applyIncomingMessage(textMessage("new", "them", "a brand new message", 100))

	if !m.thread.AtBottom() {
		t.Fatalf("expected the thread to still be scrolled to the bottom after a new message arrived, YOffset=%d maxYOffset content", m.thread.YOffset)
	}
}

// If the user has scrolled up to read history, a new message shouldn't yank
// the view down to the bottom out from under them.
func TestApplyIncomingMessageDoesNotFollowWhenScrolledUp(t *testing.T) {
	var msgs []*gmproto.Message
	for i := 0; i < 15; i++ {
		msgs = append(msgs, textMessage(fmt.Sprintf("m%d", i), "them", fmt.Sprintf("message number %d", i), int64(i)))
	}
	m := newTestThreadModel(msgs...)
	m.thread.GotoTop()

	if m.thread.AtBottom() {
		t.Fatal("test setup: expected the thread to not be at the bottom after GotoTop")
	}
	offsetBefore := m.thread.YOffset

	m.applyIncomingMessage(textMessage("new", "them", "a brand new message", 100))

	if m.thread.YOffset != offsetBefore {
		t.Fatalf("expected scroll position to stay put while reading history, was %d, now %d", offsetBefore, m.thread.YOffset)
	}
}
