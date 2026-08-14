# The TypeScript and JavaScript language server, built on this host on first
# use. Deliberately never pulled as a prebuilt image with the server inside:
# whoever builds holds the licenses. The shared entrypoint, the workspace
# watcher with the exit code 64 restart contract, is appended in Go
# (registry.go), one copy for every language.
FROM node:lts-alpine
# tsgo, the native TypeScript server, speaks LSP itself: it has the workspace
# ready before it answers the first request and announces no startup work,
# which is why this profile needs none of the waiting the servers that index
# at the handshake do. The install resolves at build time and the image tag
# hashes this file, so the server only ever updates when this file changes.
RUN npm install -g @typescript/native-preview \
 && apk add --no-cache inotify-tools
# A project without a configuration of its own gets one from here, or the
# server builds a project out of the opened file and whatever it imports and
# a usages list answers a fraction that reads like the whole. An empty object
# is the whole of it on purpose: the server fills in allowJs, noEmit,
# skipLibCheck and a modern target itself, takes every file below and leaves
# node_modules out.
#
# It is written into DC_WORKSPACE, the directory the cockpit announced as the
# workspace, which is the one above the project and belongs to the container:
# this profile has its project mounted alone (dockerArgv), so nothing of ours
# can appear in the working copy. Both names count as the project's own,
# because for a JavaScript file jsconfig is searched first and ours would
# otherwise overrule a tsconfig.json. The check sits here rather than in the
# cockpit so it cannot be anything but per start: a configuration added later
# ends the container through the workspace watcher, and the container that
# replaces it looks again.
#
# The wrapper takes the name of the server so the profile still runs `tsgo`
# and the settings page still reads like the command it is.
RUN mv /usr/local/bin/tsgo /usr/local/bin/tsgo-native && printf '%s\n' \
  '#!/bin/sh' \
  '[ -n "$DC_WORKSPACE" ] && [ ! -f jsconfig.json ] && [ ! -f tsconfig.json ] \' \
  '  && echo "{}" > "$DC_WORKSPACE/jsconfig.json"' \
  'exec /usr/local/bin/tsgo-native "$@"' \
  > /usr/local/bin/tsgo && chmod +x /usr/local/bin/tsgo
