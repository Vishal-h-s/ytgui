#!/bin/bash
set -euo pipefail

export CGO_ENABLED=1
export GOOS=windows
export GOARCH=amd64
export CC=x86_64-w64-mingw32-gcc

mkdir -p builds

# Find the highest numeric suffix in existing builds (ytguiNN.exe). Start at 01 if none.
max=0
shopt -s nullglob
for f in builds/ytgui*.exe; do
	base=$(basename "$f")
	if [[ $base =~ ^ytgui([0-9]+)\.exe$ ]]; then
		num=${BASH_REMATCH[1]}
		# use 10# to avoid treating leading zeros as octal
		n=$((10#$num))
		if (( n > max )); then
			max=$n
		fi
	fi
done
shopt -u nullglob

next=$((max + 1))
# zero-pad to two digits (increase width if you expect >99 builds)
outfile=$(printf "builds/ytgui%02d.exe" "$next")

echo "Building → $outfile"
go build -ldflags='-s -w -H windowsgui' -o "$outfile"
echo "Done → $outfile"
