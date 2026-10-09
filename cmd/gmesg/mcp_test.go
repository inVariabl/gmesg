package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/inVariabl/gmesg/internal/contacts"
	"github.com/inVariabl/gmesg/internal/ipc"
)

// stubDaemon answers the IPC methods the MCP server uses with fixed data
// and records every send_message request.
type stubDaemon struct {
	sent []map[string]any
}

func (s *stubDaemon) Handle(method string, params json.RawMessage) (json.RawMessage, error) {
	conv := &gmproto.Conversation{
		ConversationID: "c1",
		Name:           "Ada Lovelace",
		Participants: []*gmproto.Participant{
			{ID: &gmproto.SmallInfo{ParticipantID: "p1", Number: "+15550100001"}, FullName: "Ada Lovelace"},
			{ID: &gmproto.SmallInfo{ParticipantID: "me"}, IsMe: true},
		},
	}
	convJSON, _ := protojson.Marshal(conv)
	switch method {
	case "list_conversations":
		return json.Marshal(map[string]any{"conversations": []json.RawMessage{convJSON}})
	case "get_conversation", "resolve_conversation":
		return json.Marshal(map[string]any{"conversation": json.RawMessage(convJSON)})
	case "list_contacts":
		return json.Marshal(map[string]any{"contacts": []contacts.Contact{
			{Name: "Ada Lovelace", Numbers: []contacts.Number{{Value: "+15550100001", Label: "mobile"}}},
		}})
	case "send_message":
		var p map[string]any
		json.Unmarshal(params, &p)
		s.sent = append(s.sent, p)
		return json.Marshal(map[string]any{"conv_id": p["conv_id"]})
	}
	return nil, fmt.Errorf("unknown method: %s", method)
}

func TestMCPToolsForwardToDaemon(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), ipc.SocketName)
	stub := &stubDaemon{}
	srv := ipc.NewServer(socketPath, stub)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	d := &daemonLink{socketPath: socketPath}
	defer d.close()
	server := mcp.NewServer(&mcp.Implementation{Name: "gmesg"}, nil)
	registerMCPTools(server, d)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	call := func(name string, args map[string]any) string {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("%s returned a tool error: %s", name, text)
		}
		return text
	}

	if got := call("search_contacts", map[string]any{"query": "ada"}); !strings.Contains(got, "+15550100001") {
		t.Fatalf("search_contacts = %s", got)
	}
	if got := call("list_conversations", nil); !strings.Contains(got, `"conversation_id":"c1"`) || strings.Contains(got, `"name":""`) {
		t.Fatalf("list_conversations = %s (the user's own participant must be omitted)", got)
	}
	call("send_message", map[string]any{"phone_numbers": []string{"+15550100001"}, "text": "Hello"})
	if len(stub.sent) != 1 || stub.sent[0]["conv_id"] != "c1" || stub.sent[0]["text"] != "Hello" {
		t.Fatalf("daemon received %+v", stub.sent)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "send_message", Arguments: map[string]any{
		"conversation_id": "c1", "phone_numbers": []string{"+15550100001"}, "text": "Hello",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || len(stub.sent) != 1 {
		t.Fatalf("send_message with both conversation_id and phone_numbers must be refused")
	}
}
