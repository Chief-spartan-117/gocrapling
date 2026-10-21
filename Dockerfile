# syntax=docker/dockerfile:1
FROM golang:1.26 AS build
# go.mod may require a newer toolchain than the base image; let Go fetch it.
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gocrapling ./cmd/gocrapling

FROM debian:bookworm-slim
# chromium powers the stealth browser; fonts avoid tofu in screenshots.
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium fonts-liberation ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/gocrapling /usr/local/bin/gocrapling
# Chrome runs with --no-sandbox automatically when the user is root.
WORKDIR /data
ENTRYPOINT ["gocrapling"]
CMD ["--help"]
