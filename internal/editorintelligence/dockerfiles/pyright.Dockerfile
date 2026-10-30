# The Python language server, built on this host on first use. Deliberately
# never pulled as a prebuilt image with the server inside: whoever builds
# holds the licenses. The shared entrypoint, the workspace watcher with
# the exit code 64 restart contract, is appended in Go (registry.go), one
# copy for every language.
FROM node:24-alpine
# Pyright navigates without a configuration and without an interpreter: it
# takes every file below the workspace and its src folder as import roots on
# its own, and the standard library is the typeshed it carries. The server
# is pinned, and the base keeps the Node.js major it runs on: the type
# search, the subclass lists and the wait for its indexing were tested
# against exactly this. The image tag hashes this file, so a bump builds a
# new image on every host and wants them tested again.
RUN npm install -g pyright@1.1.414 \
 && apk add --no-cache inotify-tools
