# The PHP language server, built on this host on first use. Deliberately
# never pulled as a prebuilt image with the server inside: whoever builds
# holds the licenses. The cache volume mounts at /tmp, where the server
# keeps what survives a container start. The shared entrypoint, the
# workspace watcher with the exit code 64 restart contract, is appended
# in Go (registry.go), one copy for every language.
FROM node:lts-alpine
# The unpinned install resolves at build time, and the image tag hashes
# this file: the server only ever updates when this file changes, so a
# release that wants newer servers bumps something here.
RUN npm install -g intelephense \
 && apk add --no-cache inotify-tools
