#!/bin/sh
# Write the app's runtime config and security headers from the environment,
# so one image works for any API URL. Run by the nginx image's entrypoint
# before nginx starts.
set -eu

case "$BXT_API_URL" in
  *\"* | *\\* | *\'* | *\;* | *" "* ) echo "BXT_API_URL must not contain quotes, backslashes, semicolons or spaces" >&2; exit 1 ;;
esac

# The API's origin (scheme://host[:port]) for the Content-Security-Policy.
api_origin=$(printf '%s' "$BXT_API_URL" | sed -nE 's#^(https?://[^/?#]+).*$#\1#p')
if [ -z "$api_origin" ]; then
  echo "BXT_API_URL must be an http:// or https:// URL, got: $BXT_API_URL" >&2
  exit 1
fi

# The browser calls this URL, so it must be a name browsers can resolve, not
# a container name like http://bxt-api:8080 (which only works inside Docker).
api_host=$(printf '%s' "$api_origin" | sed -E 's#^https?://##; s#:[0-9]+$##; s#^\[(.*)\]$#\1#')
case "$api_host" in
  localhost | *.* | *:* ) ;;
  * ) echo "web: WARNING: BXT_API_URL ($BXT_API_URL) looks like an internal container name. Browsers call the API directly, so use its public URL (e.g. https://api.example.com)." >&2 ;;
esac

cat > /usr/share/nginx/html/config.js <<CONFIG
// Written at container start from BXT_API_URL.
window.__BXT_CONFIG__ = { apiUrl: "${BXT_API_URL}" };
CONFIG

# Security headers, included by every location in nginx.conf. The app only
# loads its own scripts and styles, calls the API, and shows avatars from
# Discord's and Steam's CDNs; nothing may frame it.
hsts=""
case "$api_origin" in
  https://*) hsts='add_header Strict-Transport-Security "max-age=31536000" always;' ;;
esac
cat > /etc/nginx/security-headers.conf <<HEADERS
add_header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https://cdn.discordapp.com https://*.steamstatic.com; connect-src 'self' ${api_origin}; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'" always;
add_header X-Content-Type-Options "nosniff" always;
add_header X-Frame-Options "DENY" always;
add_header Referrer-Policy "strict-origin-when-cross-origin" always;
add_header Permissions-Policy "camera=(), microphone=(), geolocation=()" always;
${hsts}
HEADERS

echo "web: API URL is ${BXT_API_URL}"
