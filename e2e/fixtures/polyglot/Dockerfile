FROM docker.io/library/golang:1.25-bookworm AS build
WORKDIR /src
COPY . .
RUN go build -o /app ./cmd/app
FROM gcr.io/distroless/base-debian12
COPY --from=build /app /app
ENTRYPOINT ["/app"]
