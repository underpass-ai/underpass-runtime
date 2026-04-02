# syntax=docker/dockerfile:1.7
#
# UnderPass Runtime — Execution plane for tool-driven agents
#
# The runtime server is a static binary that manages sessions, policies,
# and invocations. Tool execution happens in separate runner pods (K8s
# backend) or in-process (local backend). Runner images with git, bash,
# etc. live in runner-images/.

FROM docker.io/library/golang:1.26-alpine AS builder
WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
ARG BUILD_TAGS="k8s"
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags "${BUILD_TAGS}" -o /out/underpass-runtime ./cmd/workspace

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

ENV PORT=50053
ENV WORKSPACE_ROOT=/tmp/workspaces
ENV ARTIFACT_ROOT=/tmp/artifacts

COPY --from=builder /out/underpass-runtime /app/underpass-runtime

EXPOSE 50053
ENTRYPOINT ["/app/underpass-runtime"]
