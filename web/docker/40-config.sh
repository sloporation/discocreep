#!/bin/sh
# Write the app's runtime config from the environment, so one image works
# for any API URL. Run by the nginx image's entrypoint before nginx starts.
set -eu

case "$BXT_API_URL" in
  *\"* | *\\* ) echo "BXT_API_URL must not contain quotes or backslashes" >&2; exit 1 ;;
esac

cat > /usr/share/nginx/html/config.js <<CONFIG
// Written at container start from BXT_API_URL.
window.__BXT_CONFIG__ = { apiUrl: "${BXT_API_URL}" };
CONFIG

echo "web: API URL is ${BXT_API_URL}"
