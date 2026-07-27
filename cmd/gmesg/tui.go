package main

import (
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/ipc"
	"github.com/inVariabl/gmesg/internal/tui"
	"github.com/inVariabl/gmesg/internal/ueberzug"
)

func runTUI() error {
	has, err := gmclient.HasSession(profile)
	if err != nil {
		return fmt.Errorf("check session: %w", err)
	}
	if !has {
		return fmt.Errorf("no paired session found -- run \"gmesg pair\" first")
	}

	dataDir, err := gmclient.DataDir(profile)
	if err != nil {
		return fmt.Errorf("resolve data dir: %w", err)
	}
	socketPath := filepath.Join(dataDir, ipc.SocketName)

	ipcClient, err := ipc.Dial(socketPath)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w\n\nMake sure gmesg daemon is running:\n  gmesg daemon &", err)
	}
	defer ipcClient.Close()

	cacheDir, err := gmclient.CacheDir(profile)
	if err != nil {
		return fmt.Errorf("resolve cache dir: %w", err)
	}

	uzClient, err := ueberzug.Start()
	if err != nil {
		// nil is fine — every ueberzug.Client method tolerates a nil receiver
	}
	defer uzClient.Close()

	model := tui.New(ipcClient, cacheDir, uzClient)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
