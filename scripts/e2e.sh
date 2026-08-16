#!/usr/bin/env bash
# e2e.sh проверяет то, чего не проверяет ничто другое: что выданный ключ
# действительно подключается и пропускает трафик.
#
# Всё остальное в проекте проверяет конфиги, панель и API — то есть что mor
# написал то, что собирался. Здесь рядом с сервером поднимается настоящий
# клиент, собранный из той самой ссылки, которую получает человек, и через него
# скачивается страница. Если ссылка и конфиг движка разошлись — например
# перепутан ключ Reality или порт, — это единственная проверка, которая упадёт.
#
# Запускать на сервере, где mor уже установлен:
#   bash scripts/e2e.sh
set -euo pipefail

# CONNECT_HOST — куда стучаться клиенту. По умолчанию адрес из ссылки, то есть
# проверяется в том числе и он. На машине, где mor настроен на чужой адрес
# (сборка в CI), сюда подставляется 127.0.0.1: проверяются ключи, конфиги и
# движки, а верность самого адреса — только на настоящем сервере.
CONNECT_HOST="${CONNECT_HOST:-}"

MOR_DIR="${MOR_DIR:-/etc/mor}"
BIN="${BIN:-/usr/local/bin/mor}"
KEY_NAME="${KEY_NAME:-e2e-проверка}"
BASE_SOCKS=${BASE_SOCKS:-10800}

ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$*"; FAILED=$((FAILED + 1)); }
info() { printf '  %s\n' "$*"; }
FAILED=0

command -v "$BIN" >/dev/null 2>&1 || { echo "mor не установлен"; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "нужен python3"; exit 1; }
command -v curl    >/dev/null 2>&1 || { echo "нужен curl"; exit 1; }

WORK="$(mktemp -d)"
TOKEN=""
KEY_ID=""

cleanup() {
  local p
  for p in "$WORK"/*.pid; do
    [ -f "$p" ] || continue
    kill "$(cat "$p")" 2>/dev/null || true
  done
  [ -n "$KEY_ID" ] && curl -sk -X DELETE -H "Authorization: Bearer $TOKEN" \
    "$API/api/users/$KEY_ID" >/dev/null 2>&1 || true
  [ -n "$TOKEN" ] && "$BIN" token rm e2e >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

WEB_PORT="$(python3 -c "import json;print(json.load(open('$MOR_DIR/config.json'))['web_port'])")"
HOST="$(python3 -c "import json;print(json.load(open('$MOR_DIR/config.json'))['public_host'])")"
API="https://127.0.0.1:$WEB_PORT"

echo
echo "  проверка сквозного соединения · $HOST"
echo

TOKEN="$("$BIN" token new e2e | grep -o 'mor_[0-9a-f]*' | head -1)"
[ -n "$TOKEN" ] || { echo "не удалось выпустить токен"; exit 1; }

KEY_JSON="$(curl -sk -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"$KEY_NAME\",\"protocols\":[\"hy2\",\"reality\",\"enc\",\"ss\"]}" "$API/api/users")"
KEY_ID="$(printf '%s' "$KEY_JSON" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')"
[ -n "$KEY_ID" ] || { echo "ключ не создался"; exit 1; }

DETAIL="$(curl -sk -H "Authorization: Bearer $TOKEN" "$API/api/users/$KEY_ID")"
printf '%s' "$DETAIL" > "$WORK/detail.json"

# Ссылки собирает mor, конфиги клиентов — этот скрипт. Совпасть они обязаны
# сами, иначе клиент не подключится, что и проверяется.
python3 - "$WORK" "$CONNECT_HOST" <<'PY'
import base64, json, sys, urllib.parse as up

work = sys.argv[1]
connect = sys.argv[2] if len(sys.argv) > 2 else ""
d = json.load(open(work + "/detail.json"))
links = d.get("links") or {}
made = []

def q(u):
    return dict(up.parse_qsl(u.query))

for proto, link in links.items():
    u = up.urlsplit(link)
    p = q(u)
    host = connect or u.hostname
    port = 10800 + len(made) + 1
    if proto == "hy2":
        cfg = ["server: %s:%d" % (host, u.port),
               "auth: %s" % up.unquote(u.username or ""),
               "tls:",
               "  sni: %s" % p.get("sni", ""),
               "  insecure: true",
               "socks5:",
               "  listen: 127.0.0.1:%d" % port]
        if p.get("obfs") == "salamander":
            cfg += ["obfs:", "  type: salamander", "  salamander:",
                    "    password: %s" % p.get("obfs-password", "")]
        open("%s/%s.yaml" % (work, proto), "w").write("\n".join(cfg) + "\n")
        made.append((proto, "hysteria", port))
        continue

    inbound = {"port": port, "listen": "127.0.0.1", "protocol": "socks",
               "settings": {"udp": False}}
    if proto == "ss":
        raw = u.netloc.split("@")[0]
        pad = "=" * (-len(raw) % 4)
        method, password = base64.urlsafe_b64decode(raw + pad).decode().split(":", 1)
        out = {"protocol": "shadowsocks", "settings": {"servers": [
            {"address": host, "port": u.port, "method": method, "password": password}]}}
    else:
        user = {"id": u.username, "encryption": p.get("encryption", "none")}
        if p.get("flow"):
            user["flow"] = p["flow"]
        stream = {"network": p.get("type", "tcp")}
        if p.get("security") == "reality":
            stream["security"] = "reality"
            stream["realitySettings"] = {
                "serverName": p.get("sni", ""), "publicKey": p.get("pbk", ""),
                "shortId": p.get("sid", ""), "fingerprint": p.get("fp", "chrome"),
            }
        out = {"protocol": "vless", "settings": {"vnext": [
            {"address": host, "port": u.port, "users": [user]}]},
            "streamSettings": stream}
    json.dump({"log": {"loglevel": "warning"}, "inbounds": [inbound], "outbounds": [out]},
              open("%s/%s.json" % (work, proto), "w"))
    made.append((proto, "xray", port))

open(work + "/plan", "w").write("\n".join("%s %s %d" % m for m in made) + "\n")
PY

SERVER_IP="$(curl -s --max-time 15 https://api.ipify.org || true)"

while read -r proto engine port; do
  [ -n "$proto" ] || continue
  case "$engine" in
    hysteria)
      hysteria client -c "$WORK/$proto.yaml" >"$WORK/$proto.log" 2>&1 &
      echo $! > "$WORK/$proto.pid" ;;
    xray)
      xray run -c "$WORK/$proto.json" >"$WORK/$proto.log" 2>&1 &
      echo $! > "$WORK/$proto.pid" ;;
  esac

  # Клиент поднимает свой порт не мгновенно.
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    (exec 3<>/dev/tcp/127.0.0.1/"$port") 2>/dev/null && break
    sleep 1
  done

  got="$(curl -s --max-time 25 --socks5-hostname "127.0.0.1:$port" https://api.ipify.org || true)"
  if [ -z "$got" ]; then
    bad "$proto — через туннель ничего не пришло"
    tail -3 "$WORK/$proto.log" 2>/dev/null | sed 's/^/      /'
  elif [ -n "$SERVER_IP" ] && [ "$got" != "$SERVER_IP" ]; then
    bad "$proto — трафик вышел с $got, а не с $SERVER_IP: туннель не используется"
  else
    ok "$proto — соединение есть, выход через $got"
  fi
done < "$WORK/plan"

# Байты должны быть посчитаны mor. Проверка отдельная и важная: на одной
# машине «вышли с того же адреса» ничего не доказывает — трафик мог пойти мимо
# туннеля. Счётчик растёт только если он прошёл через движок и был опознан как
# этот ключ.
if [ "$FAILED" = "0" ]; then
  info "жду пересчёт трафика (до 80 с)…"
  spent=0
  for _ in $(seq 16); do
    spent="$(curl -sk -H "Authorization: Bearer $TOKEN" "$API/api/users/$KEY_ID" |
      python3 -c 'import sys,json;print(json.load(sys.stdin).get("traffic",0))')"
    [ "${spent:-0}" -gt 0 ] && break
    sleep 5
  done
  [ "${spent:-0}" -gt 0 ] && ok "mor насчитал $spent байт — трафик шёл через движок" \
    || bad "счётчик трафика остался нулевым — байты прошли мимо mor"
fi

# Подписка — то, что человек вставляет в приложение. Проверяется отдельно: она
# может отдавать пустоту, когда сами ссылки в порядке.
python3 - "$WORK" "$CONNECT_HOST" > "$WORK/sub.txt" <<'PY'
import json, sys, urllib.parse as up
link = json.load(open(sys.argv[1] + "/detail.json")).get("subLink", "")
host = sys.argv[2] if len(sys.argv) > 2 else ""
# The subscription URL carries the configured address, which on a build machine
# points nowhere. Same substitution as for the protocol links.
if link and host:
    u = up.urlsplit(link)
    port = ":%d" % u.port if u.port else ""
    link = up.urlunsplit((u.scheme, host + port, u.path, u.query, u.fragment))
print(link)
PY
SUB="$(cat "$WORK/sub.txt")"
if [ -n "$SUB" ]; then
  body="$(curl -sk --max-time 15 "$SUB" | base64 -d 2>/dev/null || true)"
  n="$(printf '%s' "$body" | grep -c '://' || true)"
  [ "$n" -ge 4 ] && ok "подписка отдаёт $n ссылок" || bad "в подписке $n ссылок, ожидалось 4"
else
  bad "ссылки на подписку нет"
fi

echo
[ "$FAILED" = "0" ] && { echo "  всё прошло"; exit 0; }
echo "  провалено проверок: $FAILED"
exit 1
