#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
go build -o gmesg ./cmd/gmesg/
install -m755 gmesg ~/.local/bin/gmesg
echo "Installed to ~/.local/bin/gmesg"
