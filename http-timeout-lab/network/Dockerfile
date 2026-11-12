# syntax=docker/dockerfile:1
ARG GO_VERSION=1.27.4
FROM golang:${GO_VERSION}-bookworm AS build

WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
COPY go.mod ./
COPY *.go ./
RUN mkdir -p /out \
    && go version > /out/build-go-version.txt \
    && go build -buildvcs=false -trimpath -o /out/timeoutlab .

FROM debian:bookworm-slim
ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       ca-certificates coreutils iproute2 netcat-openbsd procps tcpdump tshark \
    && rm -rf /var/lib/apt/lists/*

ENV LC_ALL=C LANG=C
COPY --from=build /out/timeoutlab /usr/local/bin/timeoutlab
COPY --from=build /out/build-go-version.txt /usr/local/share/timeoutlab-build-go-version.txt
WORKDIR /data
CMD ["sleep", "infinity"]
