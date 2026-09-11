#!/bin/bash

set -eu

# deno wants a writable cache dir; /run is a writable tmpfs on Cloudron
export DENO_DIR=/run/deno-cache
mkdir -p "${DENO_DIR}"

# the SQLite database (podcast metadata + full episode archive)
export KRINGKASTING_DB_PATH=/app/data/kringkasting.sqlite3

chown -R cloudron:cloudron /app/data "${DENO_DIR}"

echo "==> Starting Kringkasting"
exec /usr/local/bin/gosu cloudron:cloudron /usr/local/bin/deno serve \
  --allow-net \
  --allow-env \
  --allow-read=/app/code,/app/data,/run/deno-cache \
  --allow-write=/app/data,/run/deno-cache \
  --host 0.0.0.0 \
  --port 8000 \
  /app/code/_fresh/server.js
