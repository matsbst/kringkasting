#!/bin/sh
set -eu

# the SQLite database (podcast metadata + full episode archive)
export KRINGKASTING_DB_PATH="${KRINGKASTING_DB_PATH:-/app/data/kringkasting.sqlite3}"

if [ "$(id -u)" = "0" ]; then
  # volumes created by older images (or by root) are handed to the app user
  chown -R cloudron:cloudron /app/data
  echo "==> Starting Kringkasting"
  exec su-exec cloudron:cloudron /app/code/kringkasting
fi

echo "==> Starting Kringkasting"
exec /app/code/kringkasting
