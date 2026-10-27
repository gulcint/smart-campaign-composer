# Web UI image: build the Vite bundle with Node, serve it with unprivileged nginx, which
# also proxies /api to the Go API (same origin, no CORS). Base images are pinned by digest
# (docs/PLAN.md §6 #16). No secret is ever needed at build time: the bundle talks only to
# /api, and web/.dockerignore keeps .env files out of this build context.
#
# Targets: `runtime` (compose.yml `web`), `test` (compose.test.yml `web-test`, Vitest).

ARG NODE_IMAGE=node:24-bookworm-slim@sha256:55fb5f5a527ea2c5c4a95f963e48ef90aa38c7b346054fdc8ee9957a2b55d205
ARG NGINX_IMAGE=nginxinc/nginx-unprivileged:1.30-alpine@sha256:f5116ed4606f3eb015dd4a8e28cb9b351476dc0ad4bc6e9f93b19cec130cdf03

FROM ${NODE_IMAGE} AS source
ENV NPM_CONFIG_UPDATE_NOTIFIER=false NPM_CONFIG_FUND=false NPM_CONFIG_AUDIT=false
RUN mkdir /app && chown node:node /app
USER node
WORKDIR /app
COPY --chown=node:node package.json package-lock.json ./
RUN npm ci
COPY --chown=node:node . .

# Vitest over web/tests (docs/contracts/ui.md §0); passes with no tests until they exist.
FROM source AS test
CMD ["npm", "test"]

FROM source AS build
# PLAN §6 #4: the API key never reaches the bundle.
RUN npm run build && ! grep -rqE 'sk-ant-|ANTHROPIC' dist

FROM ${NGINX_IMAGE} AS runtime
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
# The base image already runs as the unprivileged nginx user (uid 101) on port 8080.
USER 101
EXPOSE 8080
