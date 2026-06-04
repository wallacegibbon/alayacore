#!/usr/bin/env bash
#
# build-gif.sh — Build an optimized GIF from a VHS tape file
#
# Usage:
#   ./misc/build-gif.sh <file.tape>
#   LOSSY=1 ./misc/build-gif.sh <file.tape>
#
# By default optimizes with 128 colors. Set LOSSY=1 to use lossy
# compression with a 16-color palette (smaller files, fine for
# plainio/rawio demos — just text, no need for full color).

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
	if [[ -n "${LOSSY:-}" ]]; then
		echo "gifsicle -O3 --colors 16 --lossy=80 $gif"
		gifsicle -O3 --colors 16 --lossy=80 "$gif" -o "$gif"
	else
		echo "gifsicle -O3 --colors 128 $gif"
		gifsicle -O3 --colors 128 "$gif" -o "$gif"
	fi
	echo "Optimized $dir/$gif ($(du -h "$gif" | cut -f1))"
fi
