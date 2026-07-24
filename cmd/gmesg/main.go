// Command gmesg is a terminal client for Google Messages: an interactive
// TUI plus a scriptable one-shot CLI, built on libgm (the protocol library
// behind the mautrix-gmessages bridge) instead of browser automation.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/inVariabl/gmesg/internal/gmclient"
)

// profile selects which paired identity to use, set via the --profile
// persistent flag and read directly by run*() below. Running two gmesg
// processes against the same profile at once (e.g. the TUI and the
// daemon) makes them fight over one long-poll connection with Google's
// servers, since a profile is a single paired-device identity -- use a
// separate profile (a separate "gmesg pair --profile X") for each process
// you want connected at the same time.
var profile string

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var recipients, message string

	root := &cobra.Command{
		Use:           "gmesg",
		Short:         "A terminal client for Google Messages",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `gmesg is a terminal client for Google Messages (RCS/SMS).

Run "gmesg pair" once to link your phone via a terminal QR code, then use
"gmesg" for the interactive TUI or "gmesg -p <number> -m <text>" to send a
message from a script.

Running more than one gmesg process at the same time (e.g. the TUI plus
"gmesg daemon" for notifications) needs each one on its own --profile,
since a profile is a single paired-device identity and two processes
sharing one will fight over the same connection to Google's servers.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if recipients != "" || message != "" {
				if recipients == "" || message == "" {
					return fmt.Errorf("both -p (recipient) and -m (message) are required to send")
				}
				return runSend(recipients, message)
			}
			return runTUI()
		},
	}
	root.PersistentFlags().StringVar(&profile, "profile", gmclient.DefaultProfile, "Paired identity to use (default is the un-namespaced session)")
	root.Flags().StringVarP(&recipients, "phone", "p", "", "Recipient phone number(s), comma-separated for a group")
	root.Flags().StringVarP(&message, "message", "m", "", "Message text to send")

	root.AddCommand(newPairCmd())
	root.AddCommand(newDaemonCmd())

	return root
}

func newDaemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run in the background and notify-send on incoming messages",
		Long: `Daemon keeps a connection open and fires a notify-send notification for
every incoming message. It's meant to be started from your login/autostart
setup (e.g. an xinitrc/xprofile line), the same way you'd run dunst itself.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDaemon()
		},
	}
}
