package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mdp/qrterminal/v3"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/pairing"
)

func newPairCmd() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair with your phone via a terminal QR code",
		Long: `Pair displays a QR code in the terminal. Scan it from your Android phone:

  Google Messages > Settings > Device pairing > Pair a device

The QR code refreshes automatically every 30 seconds until it's scanned.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			has, err := gmclient.HasSession(profile)
			if err != nil {
				return fmt.Errorf("check existing session: %w", err)
			}
			if has {
				fmt.Println("A session already exists for this profile. Re-pairing will replace it.")
			}

			level := zerolog.InfoLevel
			if verbose {
				level = zerolog.DebugLevel
			}
			logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).Level(level).With().Timestamp().Logger()

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			first := true
			client, err := pairing.Pair(ctx, logger, func(url string) {
				if !first {
					fmt.Println("\nQR code expired, here's a fresh one:")
				}
				first = false
				fmt.Println()
				qrterminal.GenerateHalfBlock(url, qrterminal.L, os.Stdout)
				fmt.Println("\nWaiting for you to scan this in Google Messages > Settings > Device pairing...")
			})
			if err != nil {
				return fmt.Errorf("pairing failed: %w", err)
			}

			if err := gmclient.Save(profile, client); err != nil {
				return fmt.Errorf("save session: %w", err)
			}
			fmt.Printf("\nPaired successfully as %s. Session saved to %s.\n", client.AuthData.Mobile.GetSourceID(), mustDataDir())
			return nil
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show debug-level protocol logs")
	return cmd
}

func mustDataDir() string {
	dir, err := gmclient.DataDir(profile)
	if err != nil {
		return "~/.local/share/gmesg"
	}
	return dir
}
