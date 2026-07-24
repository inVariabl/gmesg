// Package pairing drives libgm's QR-code device pairing flow: register a
// new browser session, hand the resulting pairing URL to the caller to
// render as a QR code, and wait for the phone to scan it. This mirrors the
// QRLoginProcess in mautrix-gmessages' own bridge login flow, since that's
// the reference implementation for the exact timing/retry behavior Google's
// servers expect.
package pairing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// QRExpiry matches the interval Google's own web client re-issues a QR
// code at; libgm's RefreshPhoneRelay must be called before it lapses.
const QRExpiry = 30 * time.Second

// MaxAttempts bounds how many QR refreshes we'll do before giving up.
const MaxAttempts = 6

// ErrTimeout is returned when the phone never scans the QR code within
// MaxAttempts refresh cycles.
var ErrTimeout = errors.New("pairing timed out waiting for phone to scan QR code")

// Pair runs the full QR pairing flow. onQR is invoked once with the initial
// pairing URL and again each time the QR code is refreshed (roughly every
// 30s) while waiting for the phone to scan it. On success it returns a
// libgm.Client with AuthData populated and already disconnected; the caller
// is responsible for persisting the session and reconnecting.
func Pair(ctx context.Context, logger zerolog.Logger, onQR func(url string)) (*libgm.Client, error) {
	client := libgm.NewClient(libgm.NewAuthData(), nil, logger)
	client.SetEventHandler(func(evt any) {
		logger.Debug().Type("event_type", evt).Msg("Pre-pairing event")
	})

	pairSuccess := make(chan *gmproto.PairedData, 1)
	callback := func(data *gmproto.PairedData) { pairSuccess <- data }
	client.PairCallback.Store(&callback)

	if err := client.FetchConfig(ctx); err != nil {
		return nil, fmt.Errorf("fetch config: %w", err)
	}
	qr, err := client.StartLogin()
	if err != nil {
		return nil, fmt.Errorf("start login: %w", err)
	}
	onQR(qr)

	start := time.Now()
	attemptsLeft := MaxAttempts
	for {
		timer := time.NewTimer(time.Until(start.Add(QRExpiry)))
		select {
		case <-pairSuccess:
			timer.Stop()
			client.Disconnect()
			return client, nil
		case <-timer.C:
			attemptsLeft--
			if attemptsLeft <= 0 {
				client.Disconnect()
				return nil, ErrTimeout
			}
			newQR, err := client.RefreshPhoneRelay()
			if err != nil {
				client.Disconnect()
				return nil, fmt.Errorf("refresh QR code: %w", err)
			}
			start = time.Now()
			onQR(newQR)
		case <-ctx.Done():
			timer.Stop()
			client.Disconnect()
			return nil, ctx.Err()
		}
	}
}
