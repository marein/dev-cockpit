# The Python language server, built on this host on first use. Deliberately
# never pulled as a prebuilt image with the server inside: whoever builds
# holds the licenses. The shared entrypoint, the workspace watcher with
# the exit code 64 restart contract, is appended in Go (registry.go), one
# copy for every language.
FROM node:lts-alpine
# Pyright navigates without a configuration and without an interpreter: it
# takes every file below the workspace and its src folder as import roots on
# its own, and the standard library is the typeshed it carries. The install
# resolves at build time and the image tag hashes this file, so the server
# only ever updates when this file changes.
RUN npm install -g pyright \
 && apk add --no-cache inotify-tools
