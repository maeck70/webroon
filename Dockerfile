# Stage 1: Build Go binary
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Install templ CLI and build dependencies
RUN go install github.com/a-h/templ/cmd/templ@v0.3.1020

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN templ generate && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/webroon ./cmd/webroon

# Stage 2: Minimal runtime image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    mkdir -p /app /data

WORKDIR /app

COPY --from=builder /out/webroon /app/webroon
COPY static /app/static

ENV PORT=8080
ENV ROON_STATE_FILE=/data/roonstate.json

# HTTP Web UI port and Roon SOOD UDP discovery port
EXPOSE 8080/tcp
EXPOSE 9003/udp

VOLUME ["/data"]

ENTRYPOINT ["/app/webroon"]
