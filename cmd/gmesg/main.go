// Command gmesg is a terminal client for Google Messages: an interactive
// TUI backed by a background notification daemon, plus a scriptable
// one-shot CLI, built on libgm (the protocol library behind the
// mautrix-gmessages bridge) instead of browser automation.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/inVariabl/gmesg/internal/gmclient"
)

// profile selects which paired identity to use, set via the --profile
// persistent flag and read directly by run*() below. The daemon owns the
// single connection to Google's servers; the TUI connects to the daemon
// over a Unix socket so both share one profile and one pairing.
var profile string

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var recipients, message string
	var files []string

	root := &cobra.Command{
		Use:           "gmesg",
		Short:         "A terminal client for Google Messages",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `gmesg is a terminal client for Google Messages (RCS/SMS).

Run "gmesg pair" once to link your phone through your Google Account. Then
start "gmesg daemon &" for background notifications, and use "gmesg" for
the interactive TUI (or "gmesg -p <number> -m <text>" to send from a
script). The TUI connects to the daemon over a Unix socket, so both share
one pairing -- no separate profiles needed.

Attach files (photos, gifs, video, ...) with repeated -f flags; -m is
optional if at least one -f is given.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if recipients != "" || message != "" || len(files) > 0 {
				if recipients == "" {
					return fmt.Errorf("-p (recipient) is required to send")
				}
				if message == "" && len(files) == 0 {
					return fmt.Errorf("-m (message) or -f (file) is required to send")
				}
				return runSend(recipients, message, files)
			}
			return runTUI()
		},
	}
	root.PersistentFlags().StringVar(&profile, "profile", gmclient.DefaultProfile, "Paired identity to use (default is the un-namespaced session)")
	root.Flags().StringVarP(&recipients, "phone", "p", "", "Recipient phone number(s), comma-separated for a group")
	root.Flags().StringVarP(&message, "message", "m", "", "Message text to send")
	root.Flags().StringArrayVarP(&files, "file", "f", nil, "Attach a file to send (repeatable for multiple attachments)")

	root.AddCommand(newPairCmd())
	root.AddCommand(newDaemonCmd())

	return root
}

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run in the background and notify-send on incoming messages",
		Long: `Daemon keeps a connection open and fires a notify-send notification for
every incoming message. It also listens on a Unix socket for the TUI to
connect, allowing both to share one pairing.

Meant to be started from your login/autostart setup (e.g. an xinitrc or
xprofile line), the same way you'd run dunst itself.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon()
		},
	}
}
