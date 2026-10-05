# syntax=docker/dockerfile:1.6
#
# chora-kg-explorer Dockerfile — standalone Go service: the Knowledge Graph
# Explorer agent (kg_explorer, ADR-254 D2 / R23), subscriber-only (ADR-254 D6).
#
# Build context = this repository. Shared Chora modules (chora-adk-common,
# chora-common, chora-contracts) are resolved through the Go module proxy,
# not a workspace. ONE image, ONE binary: the container runs
# ["/usr/local/bin/kg_explorer"] with NO arguments; the health port
# (/healthz + /readyz) is the only listener.

ARG GO_VERSION=1.26.6
ARG ALPINE_VERSION=3.23
ARG SERVICE_NAME=chora-kg-explorer
ARG GIT_SHA=unknown
ARG BUILD_TIME=unknown

############################
# Stage 1 — build
############################
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG SERVICE_NAME
ARG GIT_SHA
ARG BUILD_TIME

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY . .

RUN go mod download

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64
RUN go build -trimpath \
      -ldflags "-s -w \
        -X main.serviceName=${SERVICE_NAME} \
        -X main.gitSHA=${GIT_SHA} \
        -X main.buildTime=${BUILD_TIME}" \
      -o /out/kg_explorer \
      ./cmd/kg_explorer

############################
# Stage 2 — runtime
############################
FROM gcr.io/distroless/static-debian12:nonroot

ARG SERVICE_NAME
ARG GIT_SHA
ARG BUILD_TIME

LABEL org.opencontainers.image.title="${SERVICE_NAME}" \
      org.opencontainers.image.source="https://github.com/apollo-chora/chora-kg-explorer" \
      org.opencontainers.image.revision="${GIT_SHA}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.vendor="Chora Platform" \
      org.opencontainers.image.licenses="UNLICENSED" \
      io.chora.service="${SERVICE_NAME}" \
      io.chora.git-sha="${GIT_SHA}" \
      io.chora.build-time="${BUILD_TIME}"

WORKDIR /

COPY --from=builder /out/kg_explorer /usr/local/bin/kg_explorer

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/kg_explorer"]
