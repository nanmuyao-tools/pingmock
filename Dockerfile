# syntax=docker/dockerfile:1
#
# pingmock in a container. It needs NET_ADMIN and the TUN device:
#   docker run --cap-add NET_ADMIN --device /dev/net/tun ghcr.io/nanmuyao-tools/pingmock -rule 10.99.0.1=50ms
# Containers that share its network (--network container:NAME, or
# network_mode: service:pingmock in Compose) get their pings mocked.

# Cross-compile on the build machine instead of emulating the target.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH VERSION=devel
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/pingmock .

FROM alpine:3.22
# iputils for a ping that supports IPv6; pingmock itself needs nothing.
RUN apk add --no-cache iputils
COPY --from=build /out/pingmock /usr/local/bin/pingmock
LABEL org.opencontainers.image.title="pingmock" \
      org.opencontainers.image.description="Mock ping (ICMP and ICMPv6 echo) with per-target delay, jitter, loss and unreachable errors" \
      org.opencontainers.image.source="https://github.com/nanmuyao-tools/pingmock" \
      org.opencontainers.image.licenses="MIT"
EXPOSE 9100
ENTRYPOINT ["pingmock"]
# Listen on all addresses so the API can be published with -p 9100:9100.
# Passing other arguments replaces this; the API then listens on
# 127.0.0.1:9100, which containers sharing the network still reach.
CMD ["-api", "0.0.0.0:9100"]
