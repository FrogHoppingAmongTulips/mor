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
    command -v curl >/dev/null 2>&1 || die "нужен curl — установи его вручную и повтори"
    return
  fi
  export DEBIAN_FRONTEND=noninteractive
  log "готовлю систему…"
  wait_apt_lock
  local out
  if ! out="$(apt-get update -y 2>&1)"; then
    echo "$out" | tail -5
    log "предупреждение: apt-get update прошёл с ошибками — продолжаю"
  fi
  if ! out="$(apt-get install -y curl ca-certificates 2>&1)"; then
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
  log "ставлю Xray (VLESS Reality)…"
  local out
  out="$(bash -c "$(curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install 2>&1)" \
    || { echo "$out" | tail -5; log "предупреждение: Xray не установился — Reality будет недоступен"; }
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

open_firewall() {
  if command -v ufw >/dev/null 2>&1 && ufw status | grep -q active; then
    ufw allow "$(cfg_port vpn_port "${VPN_PORT}")"/udp >/dev/null 2>&1 || true
    ufw allow "$(cfg_reality_port)"/tcp                >/dev/null 2>&1 || true
    log "порты открыты в ufw"
  fi
}

cfg_port() {
  local key="$1" fallback="$2" v=""
  [ -f "$MOR_DIR/config.json" ] && v="$(sed -n "s/.*\"$key\": *\([0-9][0-9]*\).*/\1/p" "$MOR_DIR/config.json" | head -1)"
  echo "${v:-$fallback}"
}

cfg_reality_port() {
  local v=""
  [ -f "$MOR_DIR/config.json" ] && v="$(tr -d '\n ' <"$MOR_DIR/config.json" | sed -n 's/.*"reality":{[^}]*"port":\([0-9][0-9]*\).*/\1/p')"
  echo "${v:-443}"
}

start_engine() {
  local unit="$1"
  systemctl enable "$unit" >/dev/null 2>&1 || true
  if systemctl is-active --quiet "$unit"; then
    return 0
  fi
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

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

uninstall() {
  require_root
  log "удаляю mor и движки…"
  local hy_port reality_port
  hy_port="$(cfg_port vpn_port "${VPN_PORT}")"
  reality_port="$(cfg_reality_port)"

  systemctl disable --now mor >/dev/null 2>&1 || true
  systemctl disable --now aqu >/dev/null 2>&1 || true
  systemctl disable --now hysteria-server >/dev/null 2>&1 || true
  systemctl disable --now xray >/dev/null 2>&1 || true
  rm -f /etc/systemd/system/mor.service /etc/systemd/system/aqu.service
  rm -f /etc/systemd/system/hysteria-server.service /etc/systemd/system/hysteria-server@.service
  rm -rf /etc/systemd/system/hysteria-server.service.d
  systemctl daemon-reload
  rm -f "$BIN" /usr/local/bin/aqu /usr/local/bin/hysteria
  rm -rf "$MOR_DIR" /etc/aqu /etc/hysteria
  userdel hysteria >/dev/null 2>&1 || true
  if command -v xray >/dev/null 2>&1; then
    bash -c "$(curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ remove --purge >/dev/null 2>&1 || true
  fi
  if command -v ufw >/dev/null 2>&1; then
    ufw delete allow "${hy_port}"/udp      >/dev/null 2>&1 || true
    ufw delete allow "${reality_port}"/tcp >/dev/null 2>&1 || true
  fi
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

  start_engine hysteria-server
  if command -v xray >/dev/null 2>&1; then start_engine xray; fi
  open_firewall >/dev/null

  echo
  if [ "$fresh" = "1" ]; then
    log "готово. Набери mor"
  else
    log "обновлено. Управление: mor"
    "$BIN" status
  fi
}

case "${1:-install}" in
  uninstall|remove) uninstall ;;
  install|"")       install ;;
  *) die "неизвестная команда: $1 (доступно: install, uninstall)" ;;
esac
