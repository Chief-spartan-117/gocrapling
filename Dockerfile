# syntax=docker/dockerfile:1
FROM golang:1.27 AS build
# go.mod may require a newer toolchain than the base image; let Go fetch it.
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/gocrapling ./cmd/gocrapling

FROM debian:bookworm-slim
# chromium powers the stealth browser; fonts avoid tofu in screenshots.
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium fonts-liberation ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/gocrapling /usr/local/bin/gocrapling
# Run as an unprivileged user. Containers normally cannot create the user
# namespaces Chrome's sandbox needs, so the sandbox is off; the container
# boundary and the non-root user are the isolation. Add
# --security-opt seccomp=chrome.json and unset the variable to restore it.
RUN useradd --create-home --uid 10001 gocrapling && mkdir /data && chown gocrapling /data
ENV GOCRAPLING_NO_SANDBOX=1 HOME=/home/gocrapling
USER gocrapling
WORKDIR /data
ENTRYPOINT ["gocrapling"]
CMD ["--help"]
