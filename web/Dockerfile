# syntax=docker/dockerfile:1.7

# --- builder ------------------------------------------------------------
# Build the static site. The API URL is NOT baked in: it's written to
# config.js when the container starts (see docker/40-config.sh).
FROM node:24-alpine AS builder

WORKDIR /src

# Install dependencies in their own layer so source-only changes reuse it.
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY . .
RUN npm run build

# --- runtime ------------------------------------------------------------
# nginx serves the files. Its entrypoint runs /docker-entrypoint.d/*.sh
# before starting, which is where config.js gets written.
FROM nginx:1.29-alpine

COPY docker/nginx.conf /etc/nginx/conf.d/default.conf
COPY docker/40-config.sh /docker-entrypoint.d/40-config.sh
COPY --from=builder /src/dist /usr/share/nginx/html

# The Go API's URL as the browser reaches it.
ENV BXT_API_URL=http://localhost:8080

EXPOSE 80
