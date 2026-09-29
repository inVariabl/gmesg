// Package pairing drives libgm's Google account pairing flow: authenticate
// to Google with the user's browser cookies, locate the phone registered
// to that account, and wait for the user to confirm the pairing by
// selecting the matching emoji on the phone. This mirrors the
// GoogleLoginProcess in mautrix-gmessages' own bridge login flow, which is
// the reference implementation for the sequence Google's servers expect.
package pairing

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/events"
)

// Pair runs the full Google account pairing flow with the given cookies.
// onEmoji is invoked once with the emoji the user must select on the phone
// to confirm the pairing. On success it returns a libgm.Client with
// AuthData populated and already disconnected; the caller is responsible
// for persisting the session and reconnecting.
func Pair(ctx context.Context, logger zerolog.Logger, cookies map[string]string, onEmoji func(emoji string)) (*libgm.Client, error) {
	auth := libgm.NewAuthData()
	auth.SetCookies(cookies)
	client := libgm.NewClient(auth, nil, logger)
	client.SetEventHandler(func(evt any) {
		logger.Debug().Type("event_type", evt).Msg("Pre-pairing event")
	})
	ctx = logger.WithContext(ctx)

	if err := client.FetchConfig(ctx); err != nil {
		return nil, fmt.Errorf("fetch config (the cookies may be expired or signed out): %w", err)
	}
	emoji, session, err := client.StartGaiaPairing(ctx)
	if err != nil {
		client.Disconnect()
		return nil, describeError(err)
	}
	onEmoji(emoji)

	if _, err := client.FinishGaiaPairing(ctx, session); err != nil {
		client.Disconnect()
		return nil, describeError(err)
	}
	client.Disconnect()
	return client, nil
}

// describeError adds operator guidance to the pairing errors libgm reports.
func describeError(err error) error {
	switch {
	case errors.Is(err, libgm.ErrNoDevicesFound):
		return fmt.Errorf("no phone running Google Messages is registered to this Google account; "+
			"sign in to the account in Google Messages > Settings > Account on the phone: %w", err)
	case errors.Is(err, libgm.ErrPairingInitTimeout):
		return fmt.Errorf("the phone did not respond; confirm it is online and Google Messages is installed: %w", err)
	case errors.Is(err, events.ErrCallerNoPermission):
		return fmt.Errorf("Google refused the request for this account: %w", err)
	case errors.Is(err, libgm.ErrIncorrectEmoji):
		return fmt.Errorf("an incorrect emoji was selected on the phone: %w", err)
	case errors.Is(err, libgm.ErrPairingCancelled):
		return fmt.Errorf("pairing was declined on the phone: %w", err)
	case errors.Is(err, libgm.ErrPairingTimeout):
		return fmt.Errorf("no emoji was selected on the phone in time: %w", err)
	default:
		return err
	}
}
