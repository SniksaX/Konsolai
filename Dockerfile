# Multi-stage image. The base target is the CLI. The full target adds
# Debian Chromium so PDF and Mermaid export can run inside the container.
# The image ships no config and no secrets. Mount the config at /config.

FROM golang:1.27.1-bookworm@sha256:8d48e12ec56735e9358640898b9d9b9fcca110612ed8a5567438c0a1baa24e66 AS build

ARG VERSION=docker

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
	-ldflags "-s -w -X main.version=${VERSION}" \
	-o /out/konsolai ./cmd/konsolai

FROM debian:stable-slim@sha256:eb593cf2c358cacef45ca0a424bbc7d30cfa3466265fc2662b9466a0ca6ba1c5 AS base

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		bash \
		ca-certificates \
		git \
		iproute2 \
		less \
		lsof \
		procps \
		ripgrep \
		tree \
	&& rm -rf /var/lib/apt/lists/* \
	&& groupadd --gid 1000 konsolai \
	&& useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash konsolai \
	&& mkdir -p /work /config /tmp/home \
	&& chmod 1777 /tmp/home \
	&& chown konsolai:konsolai /work

COPY --from=build /out/konsolai /usr/local/bin/konsolai
COPY scripts/container-entrypoint.sh /usr/local/bin/konsolai-entrypoint
RUN chmod 755 /usr/local/bin/konsolai-entrypoint

USER konsolai
WORKDIR /work
# HOME is writable by any uid. The entrypoint points config, cache, and the
# runtime directory under it so Chromium does not need a passwd home.
ENV HOME=/tmp/home \
	KONSOLAI_CONFIG=/config/config.yaml \
	TERM=xterm-256color \
	COLORTERM=truecolor
ENTRYPOINT ["/usr/local/bin/konsolai-entrypoint"]

FROM base AS full

USER root
# fonts-dejavu-core is the smaller Latin face (about 2 MB installed).
# Chromium needs --no-sandbox when the konsolai user starts it.
RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		chromium \
		fonts-dejavu-core \
	&& rm -rf /var/lib/apt/lists/*
ENV KONSOLAI_CHROME=/usr/bin/chromium
USER konsolai
