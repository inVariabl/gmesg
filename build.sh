#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
go build -o gmesg ./cmd/gmesg/
cp gmesg ~/.local/bin/gmesg
echo "Installed to ~/.local/bin/gmesg"
