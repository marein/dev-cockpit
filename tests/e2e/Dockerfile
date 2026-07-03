# Prebuilt e2e image: the Playwright base (browsers) plus playwright-core installed
# globally, so a run is a plain `node <feature>.js` with no per-run npm install and
# no node_modules in the repo. Build and run commands are in tests/e2e/README.md.
FROM mcr.microsoft.com/playwright:v1.60.0-jammy
RUN npm install -g playwright-core@1.60.0
ENV NODE_PATH=/usr/lib/node_modules
