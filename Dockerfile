# Stage 1: Build Go binary natively on host arch and cross-compile to target arch
FROM --platform=$BUILDPLATFORM golang:alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN apk add --no-cache ca-certificates tzdata && \
    mkdir -p /out/data

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/webroon ./cmd/webroon

# Stage 2: Minimal runtime image (no RUN steps needed so cross-arch builds work without QEMU)
FROM --platform=linux/amd64 alpine:3.20

WORKDIR /app

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/data /data
COPY --from=builder /out/webroon /app/webroon
COPY static /app/static

ENV PORT=80
ENV ROON_STATE_FILE=/data/roonstate.json

# HTTP Web UI port and Roon SOOD UDP discovery port
EXPOSE 80/tcp
EXPOSE 9003/udp

VOLUME ["/data"]

ENTRYPOINT ["/app/webroon"]
