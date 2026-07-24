package gmclient

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// settingsWait bounds how long Connect waits for the phone to report its
// SIM cards after connecting, which SendText needs to pick an outgoing SIM.
const settingsWait = 10 * time.Second

// Connect brings up the long-polling connection and waits briefly for the
// phone's settings (SIM card list) to arrive. It returns the settings if
// they showed up in time, or nil if not -- callers should treat a nil
// result as "proceed without a specific SIM" rather than a hard failure,
// since single-SIM accounts don't strictly need one.
func Connect(ctx context.Context, client *libgm.Client) (*gmproto.Settings, error) {
	settingsCh := make(chan *gmproto.Settings, 1)
	fatalCh := make(chan error, 1)
	client.SetEventHandler(func(evt any) {
		switch v := evt.(type) {
		case *gmproto.Settings:
			select {
			case settingsCh <- v:
			default:
			}
		case *events.ListenFatalError:
			select {
			case fatalCh <- v.Error:
			default:
			}
		}
	})
	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	select {
	case settings := <-settingsCh:
		return settings, nil
	case err := <-fatalCh:
		client.Disconnect()
		return nil, err
	case <-time.After(settingsWait):
		return nil, nil
	case <-ctx.Done():
		client.Disconnect()
		return nil, ctx.Err()
	}
}
