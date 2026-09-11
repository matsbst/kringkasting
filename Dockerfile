# Build stage: compile the Fresh app to a self-contained _fresh/ bundle
FROM denoland/deno:2.5.4 AS builder

WORKDIR /build

COPY . .
RUN deno install --allow-scripts
RUN deno task build

# Runtime stage: Cloudron base image with the deno binary and the built app.
# The _fresh/ bundle is self-contained, so no node_modules or source is needed.
FROM cloudron/base:5.1.0@sha256:1c0666c9abe9e2090d33686826d4e97769b799124573118d41e0d7485135748e

ARG GIT_REVISION=dev
# Fresh uses this as an opaque build/version id for asset caching.
ENV DENO_DEPLOYMENT_ID=${GIT_REVISION}

COPY --from=builder /usr/bin/deno /usr/local/bin/deno

RUN mkdir -p /app/code
WORKDIR /app/code

COPY --from=builder /build/_fresh /app/code/_fresh
COPY start.sh /app/code/start.sh
RUN chmod +x /app/code/start.sh

CMD [ "/app/code/start.sh" ]
