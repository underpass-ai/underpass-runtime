# syntax=docker/dockerfile:1.7
#
# UnderPass Runtime — Execution plane for tool-driven agents
#
# Standalone build:
#   docker build -t underpass-runtime .
#
# Monorepo build (from swe-ai-fleet root):
#   docker build -f services/workspace/Dockerfile -t underpass-runtime services/workspace

FROM docker.io/library/golang:1.25-alpine AS builder
WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/underpass-runtime ./cmd/workspace

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

ENV PORT=50053
ENV WORKSPACE_ROOT=/tmp/workspaces
ENV ARTIFACT_ROOT=/tmp/artifacts

COPY --from=builder /out/underpass-runtime /app/underpass-runtime

EXPOSE 50053
ENTRYPOINT ["/app/underpass-runtime"]
