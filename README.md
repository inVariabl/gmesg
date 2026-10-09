# gmesg

A terminal client for Google Messages: an interactive TUI and a scriptable
CLI, built on [libgm](https://github.com/mautrix/gmessages) — the Go
library behind the mautrix-gmessages Matrix bridge that reverse-engineers
Google's actual RCS/pairing protocol. No browser automation involved.

## Status

Early development. Working so far:

- `gmesg pair` — pair with your phone through your Google Account, using
  cookies copied from a browser session and confirmed by selecting an emoji
  on the phone (see "Pairing" below)
- `gmesg -p <number> -m <text>` — one-shot send from a script
- `gmesg` — interactive TUI with vim-style modal navigation (list: `j`/`k`/`gg`/`G`;
  thread: normal mode to scroll, `i`/`a` to compose, `Esc` to go back a level,
  `yy` to yank the selected message's full text, `yc` to yank just its
  one-time passcode, `yl` to yank its first link -- uses
  `xclip`/`xsel`/`wl-copy` if one is installed, otherwise falls back to an
  OSC52 escape sequence)
- `gmesg daemon` — background listener that fires `notify-send` on incoming messages
- New conversations (`n` in the TUI) complete names from the
  [abook](https://abook.sourceforge.io/) address book (`~/.abook/addressbook`)
  merged with the phone's contact list; add several recipients for a group
- `gmesg mcp` — a Model Context Protocol server for AI clients (see "AI
  clients" below)

Not yet implemented: multi-SIM selection. See `.claude/plans/` for the build plan.

## Usage

```sh
go build -o gmesg ./cmd/gmesg
wl-paste | ./gmesg pair
```

`./build.sh` builds and installs both the binary (`~/.local/bin/gmesg`) and
its man page (`~/.local/share/man/man1/gmesg.1`) -- `man gmesg` covers every
flag, TUI key binding, and file location in one place.

### Pairing

Google Messages no longer offers QR code pairing; a new device is
authorized through the Google Account the phone's Messages app is signed
in to (Google Messages > Settings > Account). `gmesg pair` takes that
account's browser cookies:

1. In a private browser window, open
   <https://accounts.google.com/AccountChooser?continue=https://messages.google.com/web/config>
   and sign in.
2. Open the developer tools, select the Network panel, and reload.
3. Right-click the `config` request and select Copy > Copy as cURL.
4. With the window still open, run `wl-paste | gmesg pair` (or `xclip -o
   -selection clipboard | gmesg pair`, or save the command and run `gmesg
   pair --cookie-file FILE`), then select the emoji gmesg displays in the
   prompt on the phone.
5. Close the private window without signing out (signing out invalidates
   the cookies). Without a clipboard manager, closing the browser before
   step 4 discards the copied command.

A JSON object of cookie names and values, or a bare `Cookie:` header value,
is also accepted. Only the `SID`, `HSID`, `SSID`, `APISID`, `SAPISID`,
`OSID`, and `__Secure-1PSIDTS` cookies are retained, in the session file.

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

### AI clients

`gmesg mcp` serves the [Model Context Protocol](https://modelcontextprotocol.io)
on standard input and output, forwarding each request to the running daemon.
It provides `list_conversations`, `read_messages`, `search_contacts`,
`send_message`, and `react_to_message`. To register it with Claude Code:

```sh
gmesg daemon &
claude mcp add gmesg -- gmesg mcp
```

Claude Code asks for approval before each tool call unless the tool is
allowed in its permission settings; leaving `send_message` unapproved
keeps every outgoing message subject to confirmation.
