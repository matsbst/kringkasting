#!/bin/sh
# Builds web/static/assets/styles.css with the Tailwind standalone CLI (no
# Node needed). The CLI is downloaded once into .cache/ and checked against
# the pinned SHA-256. Used locally and by the Dockerfile.
set -eu

VERSION=4.3.3
platform="$(uname -s)-$(uname -m)"
# Alpine (musl) needs Tailwind's musl builds
if [ -f /etc/alpine-release ]; then platform="$platform-musl"; fi
case "$platform" in
  Darwin-arm64) asset=tailwindcss-macos-arm64 sum=cdf646702987a743464dff4d9c60fd4480d1c1e73dd819a9a67f1078815dce9d ;;
  Linux-x86_64) asset=tailwindcss-linux-x64 sum=dc61b3ac6b8c9ca874c0cc4c57b2409791a64c5540404ca5f5367360babc313a ;;
  Linux-aarch64 | Linux-arm64) asset=tailwindcss-linux-arm64 sum=55fd0b241214eff3de1e8ee4f22796662f2d2e7a49bcfca7477cfd0bac398195 ;;
  Linux-x86_64-musl) asset=tailwindcss-linux-x64-musl sum=a04d34ceacc8f52cbe8920ad846cdeb61d3d0021dba32db0d1f77c9d9fad7a6c ;;
  Linux-aarch64-musl | Linux-arm64-musl) asset=tailwindcss-linux-arm64-musl sum=71ea4be79c9de9827545682df3e040053fb535d37c71ed2cfdedf9385a0868e0 ;;
  *) echo "unsupported platform $platform" >&2; exit 1 ;;
esac

cd "$(dirname "$0")/.."
cli=".cache/tailwindcss-$VERSION-$asset"
if [ ! -x "$cli" ]; then
  mkdir -p .cache
  curl -fsSL -o "$cli.tmp" "https://github.com/tailwindlabs/tailwindcss/releases/download/v$VERSION/$asset"
  echo "$sum  $cli.tmp" | sha256sum -c - >/dev/null 2>&1 || echo "$sum  $cli.tmp" | shasum -a 256 -c - >/dev/null
  chmod +x "$cli.tmp"
  mv "$cli.tmp" "$cli"
fi
"$cli" --input web/styles.css --output web/static/assets/styles.css --minify
