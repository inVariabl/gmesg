# gmesg

A terminal client for Google Messages: an interactive TUI and a scriptable
CLI, built on [libgm](https://github.com/mautrix/gmessages) — the Go
library behind the mautrix-gmessages Matrix bridge that reverse-engineers
Google's actual RCS/pairing protocol. No browser automation involved.

## Status

Early development. Working so far:

- `gmesg pair` — pair with your phone via a QR code rendered in the terminal
  (Google Messages > Settings > Device pairing > Pair a device)
- `gmesg -p <number> -m <text>` — one-shot send from a script
- `gmesg` — interactive TUI with vim-style modal navigation (list: `j`/`k`/`gg`/`G`;
  thread: normal mode to scroll, `i`/`a` to compose, `Esc` to go back a level,
  `yy` to yank the selected message's full text, `yc` to yank just its
  one-time passcode -- uses `xclip`/`xsel`/`wl-copy` if one is installed,
  otherwise falls back to an OSC52 escape sequence)
- `gmesg daemon` — background listener that fires `notify-send` on incoming messages

Not yet implemented: attachments, group creation from inside the TUI,
multi-SIM selection. See `.claude/plans/` for the build plan.

## Usage

```sh
go build -o gmesg ./cmd/gmesg
./gmesg pair
```

### Running more than one gmesg at once

A paired session (`--profile`) is a single device identity, the same way
pairing your phone to a laptop is a separate identity from pairing it to a
desktop. Two gmesg processes sharing one profile — e.g. the TUI left open
in one terminal and `gmesg daemon` in another — will fight over the same
long-poll connection to Google's servers, and the one that loses may stop
receiving live events or, on exit, overwrite the other's saved session
with a stale auth token.

If you want the TUI and the daemon running at the same time, give the
daemon its own paired identity:

```sh
./gmesg pair --profile daemon
./gmesg daemon --profile daemon &
./gmesg   # default profile, unaffected
```

Session data (auth tokens + pairing keys) is stored at
`~/.local/share/gmesg/session.json` for the default profile, or
`~/.local/share/gmesg/profiles/<name>/session.json` for a named one
(honoring `$XDG_DATA_HOME` if set), with `0600` permissions. Treat these
files like an SSH private key — anyone who has one can read and send your
messages.

## License

GPL-3.0. This project links `go.mau.fi/mautrix-gmessages/pkg/libgm`, which
is AGPL-3.0 licensed. GPLv3 §13 grants explicit permission to combine
GPLv3 and AGPLv3 works; the combination is unaffected by this beyond the
AGPL's network-source-disclosure clause applying to the combined binary
if run as a network service.
