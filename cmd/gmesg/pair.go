package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/inVariabl/gmesg/internal/gmclient"
	"github.com/inVariabl/gmesg/internal/pairing"
)

// signInURL signs the browser in to Google and then loads a small
// configuration document from messages.google.com, so the request copied
// from the network panel carries the host-scoped OSID cookie without
// opening, and pairing, the Google Messages web application itself.
const signInURL = "https://accounts.google.com/AccountChooser?continue=https://messages.google.com/web/config"

const cookieInstructions = `To obtain the cookies:

  1. In a private browser window, open
       ` + signInURL + `
     and sign in with the Google Account that Google Messages on the
     phone is signed in to.
  2. Open the developer tools (F12), select the Network panel, and reload.
  3. Right-click the "config" request and select Copy > Copy as cURL.
  4. With the window still open, supply the copied command to gmesg
     pair by one of:

       wl-paste | gmesg pair
       xclip -o -selection clipboard | gmesg pair
       gmesg pair --cookie-file request.txt

  5. After pairing completes, close the private window. Do not sign out,
     since signing out invalidates the cookies. Without a clipboard
     manager, closing the browser before step 4 discards the copied
     command.

A JSON object of cookie names and values, or a bare Cookie header value,
is also accepted.`

func newPairCmd() *cobra.Command {
	var verbose bool
	var cookieFile string

	cmd := &cobra.Command{
		Use:   "pair",
		Short: "Pair with your phone through your Google Account",
		Long: `Pair signs in to Google with cookies copied from a browser session, then
asks Google Messages on the phone to approve the new device. The phone
must be signed in to the same Google Account (Google Messages >
Settings > Account). Confirm the pairing by selecting, on the phone, the
emoji gmesg displays.

` + cookieInstructions,
		RunE: func(cmd *cobra.Command, args []string) error {
			input, err := readCookieInput(cookieFile)
			if err != nil {
				return err
			}
			cookies, err := pairing.ParseCookies(input)
			if err != nil {
				return fmt.Errorf("%w\n\n%s", err, cookieInstructions)
			}

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

			// Cancelling the context on an interrupt lets libgm withdraw the
			// pending request from the phone rather than leaving it on screen.
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			fmt.Println("Contacting the phone registered to this Google Account...")
			client, err := pairing.Pair(ctx, logger, cookies, func(emoji string) {
				fmt.Printf("\n    %s\n\nOn the phone, select this emoji in the Google Messages prompt to approve the pairing.\n", emoji)
			})
			if err != nil {
				if ctx.Err() != nil {
					return errors.New("pairing interrupted")
				}
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
	cmd.Flags().StringVarP(&cookieFile, "cookie-file", "c", "", "Read the cookies from this file instead of standard input")
	return cmd
}

// readCookieInput returns the cookie input from path, or from standard
// input when path is empty. When standard input is a terminal it first
// prints the procedure for obtaining the cookies.
func readCookieInput(path string) (string, error) {
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read cookie file: %w", err)
		}
		return string(raw), nil
	}
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprintf(os.Stderr, "%s\n\nPaste the copied command below, then press Ctrl-D on an empty line:\n", cookieInstructions)
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read standard input: %w", err)
	}
	return string(raw), nil
}

func mustDataDir() string {
	dir, err := gmclient.DataDir(profile)
	if err != nil {
		return "~/.local/share/gmesg"
	}
	return dir
}
