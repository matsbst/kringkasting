#!/bin/bash

set -eu

# deno wants a writable cache dir; /run is a writable tmpfs on Cloudron
export DENO_DIR=/run/deno-cache
mkdir -p "${DENO_DIR}"

# the Deno KV database (cache of podcast metadata) lives on the data volume
export NRSS_KV_PATH=/app/data/cache.sqlite3

chown -R cloudron:cloudron /app/data "${DENO_DIR}"

echo "==> Starting NRSS"
exec /usr/local/bin/gosu cloudron:cloudron /usr/local/bin/deno serve \
  --allow-net \
  --allow-env \
  --allow-read=/app/code,/app/data,/run/deno-cache \
  --allow-write=/app/data,/run/deno-cache \
  --unstable-kv \
  --host 0.0.0.0 \
  --port 8000 \
  /app/code/_fresh/server.js
