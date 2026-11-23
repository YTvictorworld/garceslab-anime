#!/bin/sh
# Regenerates every Garceslab Anime icon of the server and web UI from branding/logo.svg.
# Needs rsvg-convert (librsvg) and ImageMagick 7 (magick). Run from anywhere:
#   sh branding/export.sh
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
svg="$root/branding/logo.svg"
web="$root/seanime-web/public"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

png() { rsvg-convert -w "$1" -h "$1" "$svg" -o "$2"; }

# Web UI: logo shown in the sidebar, loading screens and login.
png 512 "$web/garceslab-logo.png"
# Favicons and PWA icons (seanime-web/public/manifest.json, index.html).
png 16 "$web/icons/favicon-16x16.png"
png 32 "$web/icons/favicon-32x32.png"
png 180 "$web/icons/apple-icon.png"
png 180 "$web/icons/apple-touch-icon.png"
png 192 "$web/icons/android-chrome-192x192.png"
png 512 "$web/icons/android-chrome-512x512.png"
png 48 "$tmp/48.png"
magick "$web/icons/favicon-16x16.png" "$web/icons/favicon-32x32.png" "$tmp/48.png" "$web/icons/favicon.ico"
# Server: embedded logo (notifications) and the Windows tray/executable icon.
png 512 "$root/internal/icon/logo.png"
png 256 "$tmp/256.png"
magick "$web/icons/favicon-16x16.png" "$web/icons/favicon-32x32.png" "$tmp/48.png" "$tmp/256.png" "$root/internal/icon/iconwin.ico"

echo "Icons written from $svg"
