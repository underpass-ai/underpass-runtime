FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app/tool-learning ./cmd/tool-learning

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /app/tool-learning /app/tool-learning
ENTRYPOINT ["/app/tool-learning"]
