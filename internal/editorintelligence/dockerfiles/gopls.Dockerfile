# The Go language server, built on this host on first use. Deliberately
# never pulled as a prebuilt image with the server inside: whoever builds
# holds the licenses. The shared entrypoint, the workspace watcher with
# the exit code 64 restart contract, is appended in Go (registry.go), one
# copy for every language.
FROM golang:1.26
# @latest resolves at build time, and the image tag hashes this file: the
# server only ever updates when this file changes, so a release that wants
# newer servers bumps something here.
RUN go install golang.org/x/tools/gopls@latest \
 && apt-get update \
 && apt-get install -y --no-install-recommends inotify-tools \
 && rm -rf /var/lib/apt/lists/*
