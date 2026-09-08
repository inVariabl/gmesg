#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
go build -o gmesg ./cmd/gmesg/
install -m755 gmesg ~/.local/bin/gmesg
echo "Installed to ~/.local/bin/gmesg"

mkdir -p ~/.local/share/man/man1
install -m644 man/gmesg.1 ~/.local/share/man/man1/gmesg.1
echo "Installed man page to ~/.local/share/man/man1/gmesg.1"
