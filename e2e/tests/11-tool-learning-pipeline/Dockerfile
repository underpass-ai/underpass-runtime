FROM golang:1.25-bookworm AS builder
WORKDIR /src
COPY services/tool-learning/go.mod services/tool-learning/go.sum ./
RUN go mod download
COPY services/tool-learning/ ./
COPY e2e/tests/11-tool-learning-pipeline/main.go ./cmd/e2e-runner/main.go
RUN CGO_ENABLED=1 go build -o /app/tool-learning ./cmd/tool-learning && \
    CGO_ENABLED=1 go build -o /app/seed-lake ./cmd/seed-lake && \
    CGO_ENABLED=1 go build -o /app/e2e-runner ./cmd/e2e-runner

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/tool-learning /app/tool-learning
COPY --from=builder /app/seed-lake /app/seed-lake
COPY --from=builder /app/e2e-runner /app/e2e-runner
USER nonroot
ENTRYPOINT ["/app/e2e-runner"]
