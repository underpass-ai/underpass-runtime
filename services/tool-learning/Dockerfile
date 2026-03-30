FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ internal/
COPY cmd/ cmd/
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -o /app/tool-learning ./cmd/tool-learning && \
    CGO_ENABLED=1 go build -o /app/seed-lake ./cmd/seed-lake

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/tool-learning /app/tool-learning
COPY --from=builder /app/seed-lake /app/seed-lake
USER nonroot
ENTRYPOINT ["/app/tool-learning"]
