FROM golang:1.25-alpine AS builder
RUN apk add --no-cache build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /app/tool-learning ./cmd/tool-learning

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=builder /app/tool-learning /app/tool-learning
ENTRYPOINT ["/app/tool-learning"]
