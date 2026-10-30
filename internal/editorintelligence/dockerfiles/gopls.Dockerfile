# The Go language server, built on this host on first use. Deliberately
# never pulled as a prebuilt image with the server inside: whoever builds
# holds the licenses. The shared entrypoint, the workspace watcher with
# the exit code 64 restart contract, is appended in Go (registry.go), one
# copy for every language.
FROM golang:1.26-trixie
# The server is pinned, and the base keeps the Go release it type checks
# with: the type search, the implementation lists and the wait for its
# indexing were tested against exactly this. The image tag hashes this
# file, so a bump builds a new image on every host and wants them tested
# again.
RUN go install golang.org/x/tools/gopls@v0.23.0 \
 && apt-get update \
 && apt-get install -y --no-install-recommends inotify-tools \
 && rm -rf /var/lib/apt/lists/*
