#!/bin/sh
set -eu

# the SQLite database (podcast metadata + full episode archive)
export KRINGKASTING_DB_PATH="${KRINGKASTING_DB_PATH:-/app/data/kringkasting.sqlite3}"
data_dir="$(dirname "$KRINGKASTING_DB_PATH")"

if [ "$(id -u)" = "0" ]; then
  # volumes created by older images (or by root) are handed to the app
  # user. Some NAS shares refuse chown (ACLs, root squash) but are still
  # writable, so a failure here is only a warning.
  mkdir -p "$data_dir" 2>/dev/null || true
  if ! chown -R cloudron:cloudron "$data_dir" 2>/dev/null; then
    echo "==> Warning: could not change the owner of $data_dir to 1000:1000"
  fi
  if ! su-exec cloudron:cloudron test -w "$data_dir"; then
    echo "==> Error: $data_dir is not writable by UID/GID 1000:1000."
    echo "    Give that user write access to the data folder on the host."
    exit 1
  fi
  echo "==> Starting Kringkasting"
  exec su-exec cloudron:cloudron /app/code/kringkasting
fi

echo "==> Starting Kringkasting"
exec /app/code/kringkasting
