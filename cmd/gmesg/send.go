package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/inVariabl/gmesg/internal/gmclient"
)

// sendTimeout bounds the whole connect -> resolve -> send round trip for a
// one-shot send, since this is meant to be used from scripts.
const sendTimeout = 30 * time.Second

func runSend(recipients, message string) error {
	has, err := gmclient.HasSession(profile)
	if err != nil {
		return fmt.Errorf("check session: %w", err)
	}
	if !has {
		return fmt.Errorf("no paired session found -- run \"gmesg pair\" first")
	}

	phones := splitRecipients(recipients)
	if len(phones) == 0 {
		return fmt.Errorf("no valid recipient in -p %q", recipients)
	}

	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.WarnLevel).With().Timestamp().Logger()
	client, _, err := gmclient.New(logger, profile)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	settings, err := gmclient.Connect(ctx, client)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	conv, err := gmclient.ResolveConversation(client, phones)
	if err != nil {
		return fmt.Errorf("resolve conversation: %w", err)
	}

	sim := gmclient.DefaultSIM(settings)
	if err := gmclient.SendText(client, conv, sim, message); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	if err := gmclient.Save(profile, client); err != nil {
		return fmt.Errorf("save refreshed session: %w", err)
	}

	fmt.Printf("Sent to %s.\n", strings.Join(phones, ", "))
	return nil
}

func splitRecipients(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
