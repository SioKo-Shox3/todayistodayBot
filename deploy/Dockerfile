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
USER botuser
ENTRYPOINT ["/usr/local/bin/todayistodaybot"]
