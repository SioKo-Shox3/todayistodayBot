# syntax=docker/dockerfile:1

FROM golang:alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/todayistodaybot ./cmd/bot

FROM alpine:latest
RUN apk add --no-cache ca-certificates
RUN adduser -D -u 10001 botuser
COPY --from=build /out/todayistodaybot /usr/local/bin/todayistodaybot
# The stores write to paths relative to the working directory
# (data/casino.json, data/chosei-events.json), so the working directory is
# pinned here and its data/ subdirectory is created and handed to the
# unprivileged user — the image layers are not writable by botuser otherwise.
RUN mkdir -p /app/data && chown -R botuser:botuser /app
WORKDIR /app
VOLUME ["/app/data"]
USER botuser
ENTRYPOINT ["/usr/local/bin/todayistodaybot"]
