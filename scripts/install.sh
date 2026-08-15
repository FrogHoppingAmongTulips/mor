#!/usr/bin/env bash
set -euo pipefail

MOR_DIR="/etc/mor"
BIN="/usr/local/bin/mor"
MOR_REPO="${MOR_REPO:-FrogHoppingAmongTulips/mor}"
BASE_URL="${MOR_URL:-https://github.com/${MOR_REPO}/releases/latest/download}"
VPN_PORT="${VPN_PORT:-2096}"
SNI="${SNI:-www.cloudflare.com}"

log()  { printf '\033[36m[mor]\033[0m %s\n' "$*"; }
die()  { printf '\033[31m[mor] %s\033[0m\n' "$*" >&2; exit 1; }

require_root() { [ "$(id -u)" = "0" ] || die "запусти от root (sudo bash …)"; }

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "архитектура $(uname -m) не поддерживается" ;;
  esac
}

ensure_deps() {
  if ! command -v apt-get >/dev/null 2>&1; then
    command -v curl >/dev/null 2>&1 && return
    # RHEL family and friends: try their package manager before giving up.
    for mgr in dnf yum zypper pacman apk; do
      command -v "$mgr" >/dev/null 2>&1 || continue
      log "ставлю curl через $mgr…"
      case "$mgr" in
        pacman) "$mgr" -Sy --noconfirm curl ca-certificates cronie >/dev/null 2>&1 ;;
        apk)    "$mgr" add --no-cache curl ca-certificates dcron   >/dev/null 2>&1 ;;
        *)      "$mgr" install -y curl ca-certificates cronie      >/dev/null 2>&1 ;;
      esac
      command -v curl >/dev/null 2>&1 && return
    done
    die "нужен curl — установи его вручную и повтори"
  fi
  export DEBIAN_FRONTEND=noninteractive
  log "готовлю систему…"
  wait_apt_lock
  local out
  if ! out="$(apt-get update -y 2>&1)"; then
    echo "$out" | tail -5
    log "предупреждение: apt-get update прошёл с ошибками — продолжаю"
  fi
  if ! out="$(apt-get install -y curl ca-certificates cron 2>&1)"; then
    echo "$out" | tail -5
    die "не удалось поставить curl/ca-certificates — проверь сеть и репозитории сервера"
  fi
}

wait_apt_lock() {
  local locks="/var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/lib/apt/lists/lock"
  local i
  for i in $(seq 1 60); do
    if command -v fuser >/dev/null 2>&1; then
      fuser $locks >/dev/null 2>&1 || return 0
    else
      pgrep -x 'apt|apt-get|dpkg|unattended-upgr' >/dev/null 2>&1 || return 0
    fi
    [ "$i" = 1 ] && log "жду, пока фоновое обновление системы освободит apt (dpkg-lock)…"
    sleep 3
  done
  log "apt-lock всё ещё занят — пробую продолжить"
}

install_hysteria() {
  if command -v hysteria >/dev/null 2>&1; then
    return
  fi
  log "ставлю Hysteria2…"
  local out
  out="$(bash -c "$(curl -fsSL https://get.hy2.sh/)" 2>&1)" \
    || { echo "$out"; die "не удалось поставить Hysteria2 (get.hy2.sh) — проверь сеть и повтори"; }
}

install_xray() {
  if command -v xray >/dev/null 2>&1; then
    return
  fi
  log "ставлю Xray (Reality и VLESS Encryption)…"
  local out
  out="$(bash -c "$(curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install 2>&1)" \
    || { echo "$out" | tail -5; log "предупреждение: Xray не установился — Reality и Encryption будут недоступны"; }
}

install_mor() {
  local arch tmp; arch="$(detect_arch)"; tmp="${BIN}.new"
  log "скачиваю mor ($arch)…"
  if ! curl -fsSL "$BASE_URL/mor-linux-$arch" -o "$tmp"; then
    rm -f "$tmp"
    die "не удалось скачать бинарник mor. Проверь BASE_URL или собери из исходников: go build -o $BIN ./cmd/mor"
  fi
  chmod +x "$tmp"
  mv -f "$tmp" "$BIN"
}

public_ip() {
  curl -fsSL https://api.ipify.org 2>/dev/null \
    || curl -fsSL https://ifconfig.me 2>/dev/null \
    || hostname -I | awk '{print $1}'
}

# drop_singbox removes the engine TUIC used to need. TUIC is gone: obfuscated
# Hysteria2 covers the one case it was kept for, without a second 66 MB engine
# holding a port. An upgrade has to take it away, or it keeps running forever
# serving nobody.
drop_singbox() {
  [ -x /usr/local/bin/sing-box ] || [ -f /etc/systemd/system/sing-box.service ] || return 0
  systemctl disable --now sing-box >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/sing-box.service /usr/local/bin/sing-box
  rm -rf /etc/sing-box
  systemctl daemon-reload >/dev/null 2>&1 || true
  log "sing-box убран — TUIC больше не нужен"
}

# open_firewall opens the ports mor listens on. Distributions disagree about the
# firewall: Ubuntu and Debian ship ufw, the RHEL family firewalld. A server with
# neither is open already and needs nothing.
open_firewall() {
  local hy tcp_ports p
  hy="$(cfg_port vpn_port "${VPN_PORT}")"
  # 80 is for the ACME challenge — needed at issuance and at every renewal;
  # web_port is the panel itself, which was unreachable behind a live ufw.
  tcp_ports="$(cfg_reality_port) $(cfg_nested_port enc port 2098) $(cfg_port sub_port 8880)"
  tcp_ports="$tcp_ports $(cfg_nested_port ss port 2099) $(cfg_port web_port 9090) 80"

  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q active; then
    ufw allow "$hy"/udp >/dev/null 2>&1 || true
    for p in $tcp_ports; do ufw allow "$p"/tcp >/dev/null 2>&1 || true; done
    log "порты открыты в ufw"
    return
  fi

  if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --add-port="$hy"/udp >/dev/null 2>&1 || true
    for p in $tcp_ports; do
      firewall-cmd --permanent --add-port="$p"/tcp >/dev/null 2>&1 || true
    done
    firewall-cmd --reload >/dev/null 2>&1 || true
    log "порты открыты в firewalld"
    return
  fi

  log "firewall не найден — считаю, что порты открыты"
}

close_firewall() {
  local hy="$1" p
  shift
  if command -v ufw >/dev/null 2>&1; then
    ufw delete allow "$hy"/udp >/dev/null 2>&1 || true
    for p in "$@"; do ufw delete allow "$p"/tcp >/dev/null 2>&1 || true; done
  fi
  if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd --permanent --remove-port="$hy"/udp >/dev/null 2>&1 || true
    for p in "$@"; do
      firewall-cmd --permanent --remove-port="$p"/tcp >/dev/null 2>&1 || true
    done
    firewall-cmd --reload >/dev/null 2>&1 || true
  fi
}

cfg_port() {
  local key="$1" fallback="$2" v=""
  [ -f "$MOR_DIR/config.json" ] && v="$(sed -n "s/.*\"$key\": *\([0-9][0-9]*\).*/\1/p" "$MOR_DIR/config.json" | head -1)"
  echo "${v:-$fallback}"
}

cfg_reality_port() { cfg_nested_port reality port 443; }

# cfg_nested_port digs a port out of a nested object, e.g. "enc": {"port": 2098}.
cfg_nested_port() {
  local obj="$1" key="$2" fallback="$3" v=""
  [ -f "$MOR_DIR/config.json" ] && v="$(tr -d '\n ' <"$MOR_DIR/config.json" \
    | sed -n "s/.*\"$obj\":{[^}]*\"$key\":\([0-9][0-9]*\).*/\1/p")"
  echo "${v:-$fallback}"
}

# proto_off says whether the owner switched a protocol off in mor. Reinstalling
# must not quietly bring it back.
proto_off() {
  [ -f "$MOR_DIR/config.json" ] || return 1
  tr -d '\n ' <"$MOR_DIR/config.json" | grep -q "\"off\":\[[^]]*\"$1\""
}

# reality and enc share one Xray: it stops only when both are off.
xray_off() { proto_off reality && proto_off enc; }

# start_engine always restarts, even when the unit is already up. Xray's own
# installer starts it with a stock config, and mor writes its own a moment
# later; leaving a running engine alone would keep it serving the config it
# read at boot, so a fresh server would answer on no ports at all until
# something else happened to restart it.
start_engine() {
  local unit="$1" proto="${2:-}"
  if [ -n "${proto:-}" ] && proto_off "$proto"; then
    log "$unit выключен в настройках mor — не запускаю"
    return 0
  fi
  systemctl enable "$unit" >/dev/null 2>&1 || true
  systemctl restart "$unit" >/dev/null 2>&1 \
    || log "предупреждение: $unit не запустился — проверь journalctl -u $unit"
}

install_service() {
  cat >/etc/systemd/system/mor.service <<EOF
[Unit]
Description=mor VPN
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${BIN} serve
Restart=always
RestartSec=3
User=root

# mor stays root: it drives systemd, writes engine configs under /etc and opens
# privileged ports. What it does not need is the rest of the machine, so the
# blast radius of a bug in the web panel is fenced in here instead.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictRealtime=true
LockPersonality=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
# ProtectSystem=strict makes everything read-only, so the paths mor genuinely
# writes are named back in: its own state, the engine configs it generates and
# acme.sh's account and certificates.
ReadWritePaths=${MOR_DIR} /etc/hysteria /usr/local/etc/xray /root/.acme.sh /var/spool/cron

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

uninstall() {
  require_root
  log "удаляю mor и движки…"
  local hy_port reality_port enc_port sub_port
  hy_port="$(cfg_port vpn_port "${VPN_PORT}")"
  reality_port="$(cfg_reality_port)"
  enc_port="$(cfg_nested_port enc port 2098)"
  sub_port="$(cfg_port sub_port 8880)"

  systemctl disable --now mor >/dev/null 2>&1 || true
  systemctl disable --now aqu >/dev/null 2>&1 || true
  systemctl disable --now hysteria-server >/dev/null 2>&1 || true
  systemctl disable --now xray >/dev/null 2>&1 || true
  systemctl disable --now sing-box >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/mor.service /etc/systemd/system/aqu.service
  rm -f /etc/systemd/system/hysteria-server.service /etc/systemd/system/hysteria-server@.service
  rm -f /etc/systemd/system/sing-box.service
  rm -rf /etc/systemd/system/hysteria-server.service.d
  systemctl daemon-reload
  rm -f "$BIN" /usr/local/bin/aqu /usr/local/bin/hysteria
  rm -rf "$MOR_DIR" /etc/aqu /etc/hysteria /etc/sing-box
  rm -f /usr/local/bin/sing-box
  # acme.sh leaves an account key and a renewal cron behind; both are useless
  # once mor is gone and the cron would fail nightly forever.
  if [ -x /root/.acme.sh/acme.sh ]; then
    /root/.acme.sh/acme.sh --uninstall >/dev/null 2>&1 || true
    rm -rf /root/.acme.sh
  fi
  userdel hysteria >/dev/null 2>&1 || true
  if command -v xray >/dev/null 2>&1; then
    bash -c "$(curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ remove --purge >/dev/null 2>&1 || true
  fi
  close_firewall "${hy_port}" "${reality_port}" "${enc_port}" "${sub_port}"
  log "удалено. Данные пользователей (${MOR_DIR}) стёрты."
}

migrate_from_aqu() {
  [ -d /etc/aqu ] || return 0
  [ -e "$MOR_DIR/config.json" ] && return 0
  log "переношу данные из /etc/aqu (проект переименован в mor)…"
  systemctl disable --now aqu >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/aqu.service
  systemctl daemon-reload
  rm -rf "$MOR_DIR"
  mv /etc/aqu "$MOR_DIR"
  rm -f /usr/local/bin/aqu
  log "перенесено — ключи остались прежними"
}

install() {
  require_root
  ensure_deps
  install_hysteria
  install_xray
  install_mor
  migrate_from_aqu
  mkdir -p "$MOR_DIR"

  local fresh=0
  if [ ! -f "$MOR_DIR/config.json" ]; then
    fresh=1
    local ip; ip="$(public_ip)"
    "$BIN" setup --host "$ip" --port "${VPN_PORT}" --sni "${SNI}" \
      || die "mor setup не удался (см. вывод выше)"
  fi

  install_service
  systemctl enable mor >/dev/null 2>&1 || true
  systemctl restart mor || die "не удалось запустить сервис mor"

  start_engine hysteria-server hy2
  if command -v xray >/dev/null 2>&1; then
    if xray_off; then log "Xray выключен в настройках mor — не запускаю"; else start_engine xray; fi
  fi
  drop_singbox
  open_firewall >/dev/null
  panel_cert

  echo
  self_check "$fresh"
}

# panel_cert gets the web panel a real certificate.
#
# The panel carries the server password and every key it issues, so it is served
# over TLS always: mor writes itself a self-signed certificate on first start,
# and this replaces it with a trusted one when the machine can get one. A bare
# IP works — Let's Encrypt issues for addresses under its short-lived profile,
# renewed by acme.sh's own cron. Failure here is not fatal: the panel keeps
# working on the self-signed certificate and "mor panel cert" retries later.
panel_cert() {
  local host
  host="$(cfg_host)"
  [ -n "$host" ] || return 0
  # Port 80 must be free and reachable for the ACME challenge. If something
  # already listens there, leave it alone rather than fighting over it.
  if ss -tln 2>/dev/null | grep -q ":80 "; then
    log "порт 80 занят — сертификат не выпускаю, панель на своём"
    return 0
  fi
  log "выпускаю сертификат для панели…"
  if "$BIN" panel cert "$host" >/dev/null 2>&1; then
    log "сертификат выпущен"
  else
    log "сертификат не выпустился — панель работает на своём, повтори: mor panel cert"
  fi
}

# cfg_host reads the public address mor is configured with.
cfg_host() {
  grep -o '"public_host"[[:space:]]*:[[:space:]]*"[^"]*"' "$MOR_DIR/config.json" 2>/dev/null |
    head -1 | cut -d'"' -f4
}

# self_check makes the installer answer its own question: did the thing that was
# just installed actually come up? Without it a broken install looks identical
# to a working one until somebody is handed a link that does not connect.
self_check() {
  local fresh="$1"
  log "проверяю, что всё поднялось…"
  # systemctl returns as soon as the unit is started, not once the engine has
  # bound its ports. Checking immediately catches a healthy server mid-start
  # and greets the owner with a fault that fixes itself a second later.
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    "$BIN" check --fast >/dev/null 2>&1 && break
    sleep 1
  done
  if "$BIN" check --fast; then
    echo
    if [ "$fresh" = "1" ]; then
      log "готово. Набери mor"
      # The generated password scrolled past during setup; it is what the owner
      # needs first, so it is repeated where they are actually looking.
      "$BIN" panel | grep -E "адрес:|пароль:" || true
    else
      log "обновлено. Управление: mor"
    fi
    log "проверить, доходят ли порты снаружи: mor check"
    return 0
  fi
  echo
  log "часть движков не запустилась — mor установлен, но пока работает не всё"
  log "смотри: journalctl -u mor -u hysteria-server -u xray --since '5 min ago'"
  return 0
}

case "${1:-install}" in
  uninstall|remove) uninstall ;;
  install|"")       install ;;
  *) die "неизвестная команда: $1 (доступно: install, uninstall)" ;;
esac
