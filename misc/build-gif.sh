#!/usr/bin/env bash
#
# build-gif.sh — Build an optimized GIF from a VHS tape file
#
# Usage:
#   ./misc/build-gif.sh misc/demo.tape
#   ./misc/build-gif.sh misc/demo-plainio.tape

set -euo pipefail

tape="${1:?Usage: build-gif.sh <file.tape>}"

if [[ ! -f "$tape" ]]; then
	echo "error: tape file not found: $tape" >&2
	exit 1
fi

dir="$(cd "$(dirname "$tape")" && pwd)"
tape="$(basename "$tape")"

cd "$dir"

# Record
echo "Recording $dir/$tape..."
vhs "$tape"

# Find the generated GIF (named in the Output line)
gif="$(grep -m1 '^Output' "$tape" | awk '{print $NF}')"
if [[ ! -f "$gif" ]]; then
	echo "error: expected GIF not found: $dir/$gif" >&2
	exit 1
fi

echo "Recorded $dir/$gif ($(du -h "$gif" | cut -f1))"

# Optimize if gifsicle is available
if command -v gifsicle &>/dev/null; then
	gifsicle -O3 --colors 128 "$gif" -o "$gif"
	echo "Optimized $dir/$gif ($(du -h "$gif" | cut -f1))"
fi
