package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rs/zerolog"

	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/tui"
	"github.com/inVariabl/gmesg/internal/ueberzug"
)

// connectTimeout bounds how long we'll wait for the initial connection
// (including the phone's settings push) before giving up.
const connectTimeout = 15 * time.Second

func runTUI() error {
	has, err := gmclient.HasSession(profile)
	if err != nil {
		return fmt.Errorf("check session: %w", err)
	}
	if !has {
		return fmt.Errorf("no paired session found -- run \"gmesg pair\" first")
	}

	// The TUI takes over the terminal, so protocol logs go to a file
	// instead of stderr to avoid corrupting the display.
	dataDir, err := gmclient.DataDir(profile)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(dataDir, "gmesg.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()
	logger := zerolog.New(logFile).Level(zerolog.InfoLevel).With().Timestamp().Logger()

	client, _, err := gmclient.New(logger, profile)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	settings, err := gmclient.Connect(ctx, client)
	cancel()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	cacheDir, err := gmclient.CacheDir(profile)
	if err != nil {
		return fmt.Errorf("resolve cache dir: %w", err)
	}

	// A nil uzClient (ueberzug not on PATH) just means image previews are
	// disabled -- every ueberzug.Client method tolerates a nil receiver, so
	// the rest of the TUI doesn't need to know the difference.
	uzClient, err := ueberzug.Start()
	if err != nil {
		logger.Warn().Err(err).Msg("ueberzug unavailable, image previews disabled")
	}
	defer uzClient.Close()

	events := make(chan any, 32)
	client.SetEventHandler(func(evt any) {
		select {
		case events <- evt:
		default:
			logger.Warn().Msg("Event channel full, dropping event")
		}
	})

	model := tui.New(client, settings, events, cacheDir, uzClient)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		client.Disconnect()
		return fmt.Errorf("tui: %w", err)
	}

	if err := gmclient.Save(profile, client); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}
