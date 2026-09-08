package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

func newTestModelWithConversations(convs ...*gmproto.Conversation) Model {
	m := New(nil, "", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model := updated.(Model)
	model.loadingConversations = false
	for _, c := range convs {
		model.conversations[c.GetConversationID()] = c
	}
	model.refreshListItems()
	return model
}

// Regression test for a left click on a conversation row never registering:
// under both the SGR and X10 mouse protocols, a button-release event drops
// the button number, so matching on the deprecated tea.MouseEvent.Type (as
// the click handler used to) can never see MouseLeft on release. Clicks
// must be detected via Button+Action instead.
func TestHandleMouseClickOpensConversation(t *testing.T) {
	conv := &gmproto.Conversation{
		ConversationID:    "conv-1",
		Name:              "Alice",
		DefaultOutgoingID: "me",
	}
	m := newTestModelWithConversations(conv)

	if got := m.focus; got != focusList {
		t.Fatalf("expected initial focus to be focusList, got %v", got)
	}

	// Row 4 is the first item row (title bar + status bar = 4 rows above
	// the list content), matching listRowsAboveItems.
	click := tea.MouseMsg{
		X:      2,
		Y:      listRowsAboveItems,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	}

	updated, _ := m.handleMouse(click)
	next := updated.(Model)

	if next.focus != focusThread {
		t.Fatalf("expected click to switch focus to focusThread, got %v", next.focus)
	}
	if next.activeConvID != "conv-1" {
		t.Fatalf("expected activeConvID to be conv-1, got %q", next.activeConvID)
	}
}

// newTestThreadModel builds a model already sitting in a thread with msgs
// loaded and rendered, so msgLineOffsets is populated and click math against
// it is meaningful.
func newTestThreadModel(msgs ...*gmproto.Message) Model {
	conv := &gmproto.Conversation{
		ConversationID:    "conv-1",
		Name:              "Alice",
		DefaultOutgoingID: "me",
	}
	m := newTestModelWithConversations(conv)
	m.activeConvID = "conv-1"
	m.messages["conv-1"] = msgs
	m.msgCursor["conv-1"] = 0
	m.focus = focusThread
	m.threadMode = modeNormal
	m.renderThread("conv-1")
	return m
}

func TestHandleMouseClickOnImageTogglesPreview(t *testing.T) {
	img := &gmproto.Message{
		MessageID:      "img-1",
		ConversationID: "conv-1",
		ParticipantID:  "them",
		Timestamp:      1,
		MessageInfo: []*gmproto.MessageInfo{{
			Data: &gmproto.MessageInfo_MediaContent{
				MediaContent: &gmproto.MediaContent{
					MediaName: "photo.jpg",
					Format:    gmproto.MediaFormats_IMAGE_JPEG,
				},
			},
		}},
	}
	m := newTestThreadModel(img)
	if len(m.msgLineOffsets) == 0 {
		t.Fatal("expected renderThread to populate msgLineOffsets")
	}
	// offsets[0] is the header row; the attachment summary is the next
	// line down. Clicking there (rather than the header) keeps this test
	// clear of the sender-name-click-to-jump behavior.
	attachmentRow := m.msgLineOffsets[0] + 1
	click := tea.MouseMsg{
		Y:      threadHeaderRows + attachmentRow,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	}

	updated, cmd := m.handleMouse(click)
	next := updated.(Model)

	// No real ueberzug client is wired up in tests, so togglePreview takes
	// its "unavailable" branch -- that's still proof the click correctly
	// identified this as an image message rather than routing to openTarget.
	if cmd != nil {
		t.Fatalf("expected no cmd (ueberzug unavailable in test model), got one")
	}
	wantStatus := "image preview unavailable: install ueberzug to enable it"
	if next.status != wantStatus {
		t.Fatalf("expected status %q, got %q", wantStatus, next.status)
	}
	if next.msgCursor["conv-1"] != 0 {
		t.Fatalf("expected msgCursor to move to the clicked message, got %d", next.msgCursor["conv-1"])
	}
}

func TestHandleMouseClickOnLinkOpensIt(t *testing.T) {
	linkMsg := &gmproto.Message{
		MessageID:      "link-1",
		ConversationID: "conv-1",
		ParticipantID:  "them",
		Timestamp:      1,
		MessageInfo: []*gmproto.MessageInfo{{
			Data: &gmproto.MessageInfo_MessageContent{
				MessageContent: &gmproto.MessageContent{Content: "check this out https://example.com"},
			},
		}},
	}
	m := newTestThreadModel(linkMsg)
	if len(m.msgLineOffsets) == 0 {
		t.Fatal("expected renderThread to populate msgLineOffsets")
	}
	textRow := m.msgLineOffsets[0] + 1
	click := tea.MouseMsg{
		Y:      threadHeaderRows + textRow,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	}

	updated, cmd := m.handleMouse(click)
	next := updated.(Model)

	if cmd == nil {
		t.Fatal("expected clicking a message with a link to return an open command")
	}
	if next.status != "Opening..." {
		t.Fatalf("expected status %q, got %q", "Opening...", next.status)
	}
}

func TestHandleMouseRightClickOpensReactionPicker(t *testing.T) {
	textMsg := &gmproto.Message{
		MessageID:      "text-1",
		ConversationID: "conv-1",
		ParticipantID:  "them",
		Timestamp:      1,
		MessageInfo: []*gmproto.MessageInfo{{
			Data: &gmproto.MessageInfo_MessageContent{
				MessageContent: &gmproto.MessageContent{Content: "hello"},
			},
		}},
	}
	m := newTestThreadModel(textMsg)
	headerRow := m.msgLineOffsets[0]
	click := tea.MouseMsg{
		Y:      threadHeaderRows + headerRow,
		Button: tea.MouseButtonRight,
		Action: tea.MouseActionPress,
	}

	updated, cmd := m.handleMouse(click)
	next := updated.(Model)

	if cmd != nil {
		t.Fatalf("expected no cmd from opening the reaction picker, got one")
	}
	if !next.reacting {
		t.Fatal("expected right click to open the reaction picker")
	}
	if next.reactionCursor != 0 {
		t.Fatalf("expected reactionCursor to reset to 0, got %d", next.reactionCursor)
	}
	if next.msgCursor["conv-1"] != 0 {
		t.Fatalf("expected msgCursor to move to the clicked message, got %d", next.msgCursor["conv-1"])
	}
}
