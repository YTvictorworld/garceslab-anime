# syntax=docker/dockerfile:1
#
# Garceslab Anime: server image with the web interface embedded.
#
#   docker build -t garceslab-anime-server .
#
# Runs as the rootless user `seanime` (uid/gid 1000) with HOME=/home/seanime, so the
# data lives in /home/seanime/.config/Seanime, the same layout as the community
# rootless Seanime images. Mount a volume at /home/seanime to keep it.

ARG NODE_VERSION=22
ARG GO_VERSION=1.27.1
ARG ALPINE_VERSION=3.24

###############################################################################
# 1. Web interface (seanime-web -> out/)
###############################################################################
FROM node:${NODE_VERSION}-alpine AS web
WORKDIR /src/seanime-web
COPY seanime-web/package.json seanime-web/package-lock.json ./
COPY seanime-web/patches ./patches
RUN npm ci --no-audit --no-fund
COPY seanime-web/ ./
RUN npm run build

###############################################################################
# 2. Server (Go, web embedded from web/)
###############################################################################
FROM golang:${GO_VERSION}-alpine AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf web && mkdir web
COPY --from=web /src/seanime-web/out/ ./web/
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -tags=nosystray -o /out/seanime .

###############################################################################
# 3. Runtime
###############################################################################
FROM alpine:${ALPINE_VERSION}

ARG VERSION=dev
ARG REVISION=unknown
ARG SOURCE=https://github.com/YTvictorworld/garceslab-anime-server

LABEL org.opencontainers.image.title="Garceslab Anime" \
      org.opencontainers.image.description="Based on Seanime (GPL-3.0), focused on LG webOS TVs. Not affiliated with Seanime." \
      org.opencontainers.image.licenses="GPL-3.0-only" \
      org.opencontainers.image.source="${SOURCE}" \
      org.opencontainers.image.url="${SOURCE}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"

RUN apk add --no-cache ca-certificates tzdata curl ffmpeg \
 && addgroup -S seanime -g 1000 \
 && adduser -S seanime -G seanime -u 1000 -h /home/seanime -s /sbin/nologin

COPY --from=server /out/seanime /app/seanime
COPY LICENSE NOTICE MODIFICATIONS.md /app/

ENV HOME=/home/seanime
WORKDIR /app
USER 1000
EXPOSE 43211

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD curl -fsS -o /dev/null http://localhost:43211 || exit 1

CMD ["/app/seanime"]
