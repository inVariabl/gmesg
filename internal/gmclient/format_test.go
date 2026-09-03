package gmclient

import (
	"testing"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

func textMessage(text string) *gmproto.Message {
	return &gmproto.Message{
		MessageInfo: []*gmproto.MessageInfo{{
			Data: &gmproto.MessageInfo_MessageContent{
				MessageContent: &gmproto.MessageContent{Content: text},
			},
		}},
	}
}

func TestExtractCode(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{
			name: "keyword after the digits",
			text: "Chase: DON'T share. Use code 47154471 to complete Extra Security at Sign In. We'll NEVER call to ask for this code.",
			want: "47154471",
			ok:   true,
		},
		{
			name: "keyword before the digits, picks the code over an unrelated number",
			text: "Your order #883921 has shipped. Your verification code is 4821.",
			want: "4821",
			ok:   true,
		},
		{
			name: "dash-separated code is stripped of its separator",
			text: "G-451544 is your Google verification code.",
			want: "451544",
			ok:   true,
		},
		{
			name: "no digits at all",
			text: "Running a bit late, be there in ten.",
			want: "",
			ok:   false,
		},
		{
			name: "no keyword, falls back to the only candidate",
			text: "612345",
			want: "612345",
			ok:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ExtractCode(textMessage(tt.text))
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ExtractCode(%q) = %q, %v; want %q, %v", tt.text, got, ok, tt.want, tt.ok)
			}
		})
	}
}
