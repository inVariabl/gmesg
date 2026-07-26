package gmclient

import (
	"fmt"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// SendReaction adds emoji as a reaction to msg. emoji must be one of the
// canonical Google Messages reaction emoji (see gmproto.UnicodeToEmojiType);
// anything else is sent as a custom reaction, which the phone may not
// render.
func SendReaction(client *libgm.Client, sim *gmproto.SIMCard, msg *gmproto.Message, emoji string) error {
	resp, err := client.SendReaction(&gmproto.SendReactionRequest{
		MessageID:    msg.GetMessageID(),
		ReactionData: gmproto.MakeReactionData(emoji),
		Action:       gmproto.SendReactionRequest_ADD,
		SIMPayload:   sim.GetSIMData().GetSIMPayload(),
	})
	if err != nil {
		return fmt.Errorf("send reaction: %w", err)
	}
	if !resp.GetSuccess() {
		return fmt.Errorf("send reaction failed")
	}
	return nil
}
