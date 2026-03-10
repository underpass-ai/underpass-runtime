FROM golang:1.25-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /app/tool-learning ./cmd/tool-learning

FROM gcr.io/distroless/cc-debian12:nonroot
COPY --from=builder /app/tool-learning /app/tool-learning
USER nonroot
ENTRYPOINT ["/app/tool-learning"]
