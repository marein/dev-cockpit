# The PHP language server, built on this host on first use. Deliberately
# never pulled as a prebuilt image with the server inside: whoever builds
# holds the licenses. The cache volume mounts at /tmp, where the server
# keeps what survives a container start. The shared entrypoint, the
# workspace watcher with the exit code 64 restart contract, is appended
# in Go (registry.go), one copy for every language.
FROM node:24-alpine
# The server is pinned, and the base keeps the Node.js major it runs on:
# the type search, its queries holding a backslash, the implementation
# lists and the wait for its indexing were tested against exactly this.
# The image tag hashes this file, so a bump builds a new image on every
# host and wants them tested again.
RUN npm install -g intelephense@1.18.5 \
 && apk add --no-cache inotify-tools
