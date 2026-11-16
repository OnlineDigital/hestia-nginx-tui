#!/usr/bin/env bash
# Builds a fake HestiaCP tree so the TUI can be tried WITHOUT root and without
# touching a real server:
#
#   eval "$(scripts/sandbox.sh /tmp/hnt-sandbox)" && ./hestia-nginx-tui
#
# The fake v-change-web-domain-tpl really substitutes templates like Hestia
# does, and the fake `nginx -t` fails when a managed file contains the word
# BADDIRECTIVE (handy to see the automatic rollback).
set -euo pipefail
root=${1:-/tmp/hnt-sandbox}
here=$(cd "$(dirname "$0")/.." && pwd)
rm -rf "$root"
hd=$root/hestia
home=$root/home
state=$root/etc-hnt
tpl=$hd/data/templates/web/nginx/php-fpm
mkdir -p "$tpl" "$hd/bin" "$hd/conf" "$hd/data/users/admin" "$hd/data/users/bob" \
  "$root/confd" "$root/php/8.2/fpm/pool.d" "$home"

for ext in tpl stpl; do
  cp "$here/internal/hestia/testdata/laravel.$ext" "$tpl/laravel.$ext"
  cp "$here/internal/hestia/testdata/default.$ext" "$tpl/default.$ext"
  cp "$here/internal/hestia/testdata/default.$ext" "$tpl/wordpress.$ext"
done
printf "WEB_SYSTEM='nginx'\nWEB_BACKEND='php-fpm'\nHOMEDIR='%s'\n" "$home" >"$hd/conf/hestia.conf"

{
  echo "DOMAIN='shop.example.com' IP='10.0.0.1' IP6='' ALIAS='www.shop.example.com' TPL='laravel' SSL='yes' BACKEND='PHP-8_2' SUSPENDED='no'"
  echo "DOMAIN='blog.example.com' IP='10.0.0.1' IP6='' ALIAS='' TPL='wordpress' SSL='no' BACKEND='PHP-8_2' SUSPENDED='no'"
  echo "DOMAIN='api.acme.io' IP='10.0.0.1' IP6='' ALIAS='' TPL='default' SSL='yes' BACKEND='PHP-8_2' SUSPENDED='no'"
  echo "DOMAIN='news.acme.io' IP='10.0.0.1' IP6='' ALIAS='www.news.acme.io' TPL='wordpress' SSL='yes' BACKEND='PHP-8_2' SUSPENDED='no'"
  echo "DOMAIN='staging.shop.example.com' IP='10.0.0.1' IP6='' ALIAS='' TPL='laravel' SSL='no' BACKEND='PHP-8_2' SUSPENDED='no'"
} >"$hd/data/users/admin/web.conf"
{
  echo "DOMAIN='bobs-crm.test' IP='10.0.0.2' IP6='' ALIAS='crm.bob.test' TPL='laravel' SSL='no' BACKEND='PHP-8_2' SUSPENDED='no'"
  echo "DOMAIN='portfolio.bob.dev' IP='10.0.0.2' IP6='' ALIAS='' TPL='default' SSL='yes' BACKEND='PHP-8_2' SUSPENDED='no'"
  echo "DOMAIN='docs.bob.dev' IP='10.0.0.2' IP6='' ALIAS='' TPL='default' SSL='yes' BACKEND='PHP-8_2' SUSPENDED='no'"
} >"$hd/data/users/bob/web.conf"
printf '[shop.example.com]\nlisten = /run/php/php8.2-fpm-shop.example.com.sock\n' >"$root/php/8.2/fpm/pool.d/shop.example.com.conf"

# --- fake v-change-web-domain-tpl -------------------------------------------
{
  echo '#!/usr/bin/env bash'
  echo "HD='$hd'; HOME_DIR='$home'"
  cat <<'SH'
user=$1; domain=$2; tpl=$3
T=$HD/data/templates/web/nginx/php-fpm
sed -i "/DOMAIN='$domain'/ s/TPL='[^']*'/TPL='$tpl'/" "$HD/data/users/$user/web.conf"
out=$HOME_DIR/$user/conf/web/$domain
mkdir -p "$out"
render() {
  sed -e "s|%domain%|$domain|g" -e "s|%domain_idn%|$domain|g" -e "s|%alias_idn%||g" \
      -e "s|%home%|$HOME_DIR|g" -e "s|%user%|$user|g" -e "s|%ip%|10.0.0.1|g" \
      -e "s|%web_port%|80|g" -e "s|%web_ssl_port%|443|g" \
      -e "s|%docroot%|$HOME_DIR/$user/web/$domain/public_html|g" \
      -e "s|%sdocroot%|$HOME_DIR/$user/web/$domain/public_html|g" "$1"
}
render "$T/$tpl.tpl" >"$out/nginx.conf"
render "$T/$tpl.stpl" >"$out/nginx.ssl.conf"
echo "Hestia: web domain template applied (Domain: $domain, Template: $tpl, restart=$4)"
SH
} >"$hd/bin/v-change-web-domain-tpl"
chmod +x "$hd/bin/v-change-web-domain-tpl"

# --- fake nginx -------------------------------------------------------------
{
  echo '#!/usr/bin/env bash'
  echo "STATE='$state'"
  cat <<'SH'
if [ "$1" = "-t" ]; then
  if grep -rn BADDIRECTIVE "$STATE" 2>/dev/null; then
    echo 'nginx: [emerg] unknown directive "BADDIRECTIVE"' >&2
    echo 'nginx: configuration file test failed' >&2
    exit 1
  fi
  echo "nginx: the configuration file syntax is ok"
  echo "nginx: configuration file test is successful"
fi
SH
} >"$root/nginx"
chmod +x "$root/nginx"

echo "export HNT_HESTIA=$hd HNT_HOME=$home HNT_STATE=$state HNT_CONFD=$root/confd HNT_PHP_DIR=$root/php HNT_NGINX_BIN=$root/nginx HNT_RELOAD=true"
