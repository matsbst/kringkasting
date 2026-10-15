# Build stage: runs natively on the build machine and cross-compiles, so
# multi-arch images need no emulation. Tailwind's standalone CLI builds the
# stylesheet; no Node needed.
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
# libstdc++/libgcc: runtime for the Tailwind CLI (musl build)
RUN apk add --no-cache curl libstdc++ libgcc
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN ./tools/build-css.sh \
  && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
     go build -trimpath -ldflags="-s -w" -o /out/kringkasting ./cmd/kringkasting

# Runtime: small Alpine base. curl stays because existing compose.yaml
# files run `curl` as their healthcheck; sqlite is for backups
# (`sqlite3 … ".backup …"`); su-exec drops root after fixing data ownership.
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk add --no-cache ca-certificates curl sqlite su-exec tzdata \
  # same user and ids as the previous cloudron/base image, so existing
  # data volumes stay readable
  && addgroup -g 1000 cloudron \
  && adduser -D -H -u 1000 -G cloudron cloudron \
  && mkdir -p /app/code /app/data

ARG GIT_REVISION=dev
ENV APP_BUILD_ID=${GIT_REVISION}
# soft memory ceiling: the garbage collector works harder near it instead
# of growing the process (Cloudron's limit is 384 MiB); override if needed
ENV GOMEMLIMIT=256MiB

COPY --from=build /out/kringkasting /app/code/kringkasting
COPY start.sh /app/code/start.sh

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/app/code/kringkasting", "healthcheck"]

EXPOSE 8000
CMD ["/app/code/start.sh"]
