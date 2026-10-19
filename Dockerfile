# Multi-stage image. The base target is the CLI. The full target adds
# Chromium and Mermaid so PDF export can run inside the container.
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
	&& mkdir -p /work /config \
	&& chown konsolai:konsolai /work

COPY --from=build /out/konsolai /usr/local/bin/konsolai

USER konsolai
WORKDIR /work
ENV KONSOLAI_CONFIG=/config/config.yaml \
	TERM=xterm-256color \
	COLORTERM=truecolor
ENTRYPOINT ["konsolai"]

FROM node:22.23.0-trixie-slim@sha256:cfd8f2a5bc50526aee08e88970979f92722828e7dcc6d8983607fb8bff4bdb82 AS mermaid

ARG MERMAID_CLI_VERSION=12.0.0
# The full image uses the distro Chromium. Skip Puppeteer's own download.
ENV PUPPETEER_SKIP_DOWNLOAD=true
RUN npm install -g "@mermaid-js/mermaid-cli@${MERMAID_CLI_VERSION}"

FROM base AS full

USER root
ARG CHROMIUM_VERSION=150.0.7871.181-1~deb13u1
RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		"chromium=${CHROMIUM_VERSION}" \
		"chromium-common=${CHROMIUM_VERSION}" \
	&& rm -rf /var/lib/apt/lists/*
COPY --from=mermaid /usr/local /usr/local
# PDF uses KONSOLAI_CHROME. mmdc launches Puppeteer without executablePath;
# PUPPETEER_EXECUTABLE_PATH is that default. The program's puppeteer config
# passes --no-sandbox, which Chromium needs when the user is not root.
ENV KONSOLAI_CHROME=/usr/bin/chromium \
	PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium
USER konsolai
