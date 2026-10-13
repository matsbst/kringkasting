# Build stage: compile the Fresh app to a self-contained _fresh/ bundle
FROM denoland/deno:2.5.4 AS builder

WORKDIR /build

COPY . .
RUN deno install --allow-scripts
RUN deno task build

# Runtime stage: Cloudron base image with the deno binary and the built app.
# The _fresh/ bundle is self-contained, so no node_modules or source is needed.
FROM cloudron/base:6.0.0@sha256:9bed4c8fa880645f8e669041ee28febe941481d00e9445e3e5a5483cb541d09b

ARG GIT_REVISION=dev
# Fresh uses this as an opaque build/version id for asset caching.
ENV DENO_DEPLOYMENT_ID=${GIT_REVISION}

COPY --from=builder /usr/bin/deno /usr/local/bin/deno

RUN mkdir -p /app/code
WORKDIR /app/code

COPY --from=builder /build/_fresh /app/code/_fresh
COPY start.sh /app/code/start.sh
RUN chmod +x /app/code/start.sh

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS http://localhost:8000/ >/dev/null || exit 1

EXPOSE 8000

CMD [ "/app/code/start.sh" ]
