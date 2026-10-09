package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/inVariabl/gmesg/internal/contacts"
	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/ipc"
)

const mcpInstructions = `This server reads and sends the user's SMS and RCS text messages through
Google Messages, by way of a running "gmesg daemon".

To message a person named by the user, call search_contacts first and use
the telephone number it returns; never guess a number. If a name matches
several contacts or numbers, ask the user which one is meant. Prefer
sending to an existing conversation_id from list_conversations when one
already exists with the intended recipients. Text messages reach real
people and cannot be recalled: confirm the recipient and wording with the
user before calling send_message unless the user has already stated both.`

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the Model Context Protocol on standard input and output",
		Long: `Mcp runs a Model Context Protocol server on standard input and output, so
that an AI client (Claude Code, Claude Desktop, or any other MCP client)
can list conversations, read messages, search contacts, send messages, and
react to messages. It forwards every request to the running daemon over
its Unix socket, so "gmesg daemon" must be running under the same profile.

Register it with Claude Code by running:
  claude mcp add gmesg -- gmesg mcp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMCP(cmd.Context())
		},
	}
}

func runMCP(ctx context.Context) error {
	dataDir, err := gmclient.DataDir(profile)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	d := &daemonLink{socketPath: filepath.Join(dataDir, ipc.SocketName)}
	defer d.close()

	server := mcp.NewServer(&mcp.Implementation{Name: "gmesg", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: mcpInstructions,
	})
	registerMCPTools(server, d)
	if ctx == nil {
		ctx = context.Background()
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

// daemonLink holds the IPC connection to the daemon. It connects on first
// use and again after the connection ends, so the MCP server, which an AI
// client may keep running indefinitely, survives a daemon restart.
type daemonLink struct {
	socketPath string

	mu     sync.Mutex
	client *ipc.Client
}

func (d *daemonLink) call(method string, params, result any) error {
	d.mu.Lock()
	if d.client == nil {
		c, err := ipc.Dial(d.socketPath)
		if err != nil {
			d.mu.Unlock()
			return fmt.Errorf("connect to daemon at %s: %w (start it with \"gmesg daemon\")", d.socketPath, err)
		}
		d.client = c
	}
	c := d.client
	d.mu.Unlock()

	err := c.Call(method, params, result)
	if errors.Is(err, ipc.ErrClosed) {
		d.mu.Lock()
		if d.client == c {
			d.client = nil
		}
		d.mu.Unlock()
	}
	return err
}

func (d *daemonLink) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client != nil {
		d.client.Close()
	}
}

func (d *daemonLink) conversations() ([]*gmproto.Conversation, error) {
	var result struct {
		Conversations []json.RawMessage `json:"conversations"`
	}
	if err := d.call("list_conversations", nil, &result); err != nil {
		return nil, err
	}
	convs := make([]*gmproto.Conversation, len(result.Conversations))
	for i, raw := range result.Conversations {
		var conv gmproto.Conversation
		if err := protojson.Unmarshal(raw, &conv); err != nil {
			return nil, fmt.Errorf("decode conversation: %w", err)
		}
		convs[i] = &conv
	}
	return convs, nil
}

func (d *daemonLink) conversation(method string, params any) (*gmproto.Conversation, error) {
	var result struct {
		Conversation json.RawMessage `json:"conversation"`
	}
	if err := d.call(method, params, &result); err != nil {
		return nil, err
	}
	var conv gmproto.Conversation
	if err := protojson.Unmarshal(result.Conversation, &conv); err != nil {
		return nil, fmt.Errorf("decode conversation: %w", err)
	}
	return &conv, nil
}

func (d *daemonLink) messages(convID string) ([]*gmproto.Message, error) {
	var result struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := d.call("fetch_messages", map[string]string{"conv_id": convID}, &result); err != nil {
		return nil, err
	}
	msgs := make([]*gmproto.Message, len(result.Messages))
	for i, raw := range result.Messages {
		var msg gmproto.Message
		if err := protojson.Unmarshal(raw, &msg); err != nil {
			return nil, fmt.Errorf("decode message: %w", err)
		}
		msgs[i] = &msg
	}
	return msgs, nil
}

// MCP tool inputs and outputs. The field descriptions become the JSON
// schema the AI client is shown.

type mcpParticipant struct {
	Name   string `json:"name,omitempty"`
	Number string `json:"number,omitempty"`
}

type mcpConversation struct {
	ConversationID  string           `json:"conversation_id"`
	Name            string           `json:"name"`
	IsGroup         bool             `json:"is_group"`
	Unread          bool             `json:"unread"`
	Pinned          bool             `json:"pinned,omitempty"`
	Participants    []mcpParticipant `json:"participants"`
	LatestMessage   string           `json:"latest_message,omitempty"`
	LatestMessageAt string           `json:"latest_message_at,omitempty"`
}

type mcpMessage struct {
	MessageID   string `json:"message_id"`
	Sender      string `json:"sender"`
	FromMe      bool   `json:"from_me"`
	SentAt      string `json:"sent_at"`
	Text        string `json:"text,omitempty"`
	Attachments string `json:"attachments,omitempty"`
	Reactions   string `json:"reactions,omitempty"`
}

type noInput struct{}

type listConversationsOutput struct {
	Conversations []mcpConversation `json:"conversations"`
}

type readMessagesInput struct {
	ConversationID string `json:"conversation_id" jsonschema:"conversation_id from list_conversations or send_message"`
}

type readMessagesOutput struct {
	Conversation mcpConversation `json:"conversation"`
	Messages     []mcpMessage    `json:"messages" jsonschema:"the most recent messages, oldest first"`
}

type searchContactsInput struct {
	Query string `json:"query" jsonschema:"part of a name or nickname (matched loosely, so a first name or initials suffice) or part of a telephone number"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of results; default 10"`
}

type searchContactsOutput struct {
	Matches []contacts.Candidate `json:"matches" jsonschema:"one entry per telephone number, best match first"`
}

type sendMessageInput struct {
	ConversationID  string   `json:"conversation_id,omitempty" jsonschema:"send into this existing conversation; give this or phone_numbers, not both"`
	PhoneNumbers    []string `json:"phone_numbers,omitempty" jsonschema:"recipients' telephone numbers: one for a direct conversation, several for a group; an existing conversation with exactly these recipients is reused"`
	Text            string   `json:"text,omitempty" jsonschema:"message body"`
	AttachmentPaths []string `json:"attachment_paths,omitempty" jsonschema:"absolute paths of local files to attach"`
}

type sendMessageOutput struct {
	ConversationID string `json:"conversation_id"`
	Conversation   string `json:"conversation" jsonschema:"name of the conversation the message was sent to"`
}

type reactInput struct {
	MessageID string `json:"message_id" jsonschema:"message_id from read_messages"`
	Emoji     string `json:"emoji" jsonschema:"a single emoji"`
}

type reactOutput struct{}

func registerMCPTools(server *mcp.Server, d *daemonLink) {
	openWorld := true
	notDestructive := false

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_conversations",
		Description: "List the most recent text message conversations, pinned first, with their participants and latest message.",
		Annotations: &mcp.ToolAnnotations{Title: "List conversations", ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, listConversationsOutput, error) {
		convs, err := d.conversations()
		if err != nil {
			return nil, listConversationsOutput{}, err
		}
		out := listConversationsOutput{Conversations: make([]mcpConversation, len(convs))}
		for i, c := range convs {
			out.Conversations[i] = describeConversation(c)
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_messages",
		Description: "Read the most recent messages (up to 50) of one conversation, oldest first.",
		Annotations: &mcp.ToolAnnotations{Title: "Read messages", ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in readMessagesInput) (*mcp.CallToolResult, readMessagesOutput, error) {
		if in.ConversationID == "" {
			return nil, readMessagesOutput{}, errors.New("conversation_id is required")
		}
		conv, err := d.conversation("get_conversation", map[string]string{"conv_id": in.ConversationID})
		if err != nil {
			return nil, readMessagesOutput{}, err
		}
		msgs, err := d.messages(in.ConversationID)
		if err != nil {
			return nil, readMessagesOutput{}, err
		}
		out := readMessagesOutput{Conversation: describeConversation(conv), Messages: []mcpMessage{}}
		for _, m := range msgs {
			msg := mcpMessage{
				MessageID:   m.GetMessageID(),
				Sender:      gmclient.SenderName(conv, m),
				FromMe:      gmclient.SenderIsSelf(conv, m),
				SentAt:      formatMicros(m.GetTimestamp()),
				Text:        gmclient.MessageText(m),
				Attachments: gmclient.AttachmentSummary(m),
				Reactions:   gmclient.ReactionSummary(m),
			}
			if msg.Text == "" && msg.Attachments == "" {
				continue
			}
			out.Messages = append(out.Messages, msg)
		}
		return nil, out, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_contacts",
		Description: "Find contacts by name or number in the user's abook address book and the phone's contact list. Use this to obtain a telephone number before messaging a person by name.",
		Annotations: &mcp.ToolAnnotations{Title: "Search contacts", ReadOnlyHint: true, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchContactsInput) (*mcp.CallToolResult, searchContactsOutput, error) {
		var result struct {
			Contacts []contacts.Contact `json:"contacts"`
		}
		if err := d.call("list_contacts", nil, &result); err != nil {
			return nil, searchContactsOutput{}, err
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 10
		}
		matches := contacts.Search(result.Contacts, in.Query, limit)
		if matches == nil {
			matches = []contacts.Candidate{}
		}
		return nil, searchContactsOutput{Matches: matches}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "send_message",
		Description: "Send a text message, with optional attachments, to an existing conversation or to one or more telephone numbers " +
			"(several numbers form a group). The message is delivered immediately and cannot be recalled.",
		Annotations: &mcp.ToolAnnotations{Title: "Send message", DestructiveHint: &notDestructive, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in sendMessageInput) (*mcp.CallToolResult, sendMessageOutput, error) {
		if (in.ConversationID == "") == (len(in.PhoneNumbers) == 0) {
			return nil, sendMessageOutput{}, errors.New("give exactly one of conversation_id or phone_numbers")
		}
		if in.Text == "" && len(in.AttachmentPaths) == 0 {
			return nil, sendMessageOutput{}, errors.New("text or attachment_paths is required")
		}
		for _, p := range in.AttachmentPaths {
			if !filepath.IsAbs(p) {
				return nil, sendMessageOutput{}, fmt.Errorf("attachment path %q is not absolute", p)
			}
		}

		var conv *gmproto.Conversation
		var err error
		if in.ConversationID != "" {
			conv, err = d.conversation("get_conversation", map[string]string{"conv_id": in.ConversationID})
		} else {
			conv, err = d.conversation("resolve_conversation", map[string][]string{"phones": in.PhoneNumbers})
		}
		if err != nil {
			return nil, sendMessageOutput{}, err
		}

		params := map[string]any{
			"conv_id":          conv.GetConversationID(),
			"text":             in.Text,
			"attachment_paths": in.AttachmentPaths,
		}
		if err := d.call("send_message", params, nil); err != nil {
			return nil, sendMessageOutput{}, err
		}
		return nil, sendMessageOutput{ConversationID: conv.GetConversationID(), Conversation: conv.GetName()}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "react_to_message",
		Description: "Add an emoji reaction to a message.",
		Annotations: &mcp.ToolAnnotations{Title: "React to message", DestructiveHint: &notDestructive, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, req *mcp.CallToolRequest, in reactInput) (*mcp.CallToolResult, reactOutput, error) {
		if in.MessageID == "" || in.Emoji == "" {
			return nil, reactOutput{}, errors.New("message_id and emoji are required")
		}
		err := d.call("send_reaction", map[string]string{"msg_id": in.MessageID, "emoji": in.Emoji}, nil)
		return nil, reactOutput{}, err
	})
}

func describeConversation(c *gmproto.Conversation) mcpConversation {
	out := mcpConversation{
		ConversationID:  c.GetConversationID(),
		Name:            c.GetName(),
		IsGroup:         c.GetIsGroupChat(),
		Unread:          c.GetUnread(),
		Pinned:          c.GetPinned(),
		Participants:    []mcpParticipant{},
		LatestMessage:   c.GetLatestMessage().GetDisplayContent(),
		LatestMessageAt: formatMicros(c.GetLastMessageTimestamp()),
	}
	for _, p := range c.GetParticipants() {
		if p.GetIsMe() {
			continue
		}
		out.Participants = append(out.Participants, mcpParticipant{
			Name:   p.GetFullName(),
			Number: p.GetID().GetNumber(),
		})
	}
	return out
}

func formatMicros(us int64) string {
	if us == 0 {
		return ""
	}
	return time.UnixMicro(us).Local().Format(time.RFC3339)
}
