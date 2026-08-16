#!/usr/bin/env bash
set -euo pipefail

MOR_DIR="/etc/mor"
BIN="/usr/local/bin/mor"
MOR_REPO="${MOR_REPO:-FrogHoppingAmongTulips/mor}"
BASE_URL="${MOR_URL:-https://github.com/${MOR_REPO}/releases/latest/download}"
VPN_PORT="${VPN_PORT:-2096}"
SNI="${SNI:-www.cloudflare.com}"

# During an install nothing is printed but the bar: apt, the engine installers
# and mor itself all write into the log instead, and it is named only if
# something fails. quiet is off for uninstall, which has nothing to draw.
LOGFILE="${MOR_LOG:-/tmp/mor-install.log}"
quiet=0

log() {
  if [ "$quiet" = "1" ]; then
    printf '[mor] %s\n' "$*" >>"$LOGFILE"
  else
    printf '\033[36m[mor]\033[0m %s\n' "$*"
  fi
}

die() {
  [ "$quiet" = "1" ] && printf '\n'
  printf '\033[31m[mor] %s\033[0m\n' "$*" >&2
  [ "$quiet" = "1" ] && printf '\033[31m[mor] подробности: %s\033[0m\n' "$LOGFILE" >&2
  exit 1
}

# bar draws the progress line in place. A pipe gets nothing: carriage returns
# in a file are noise, and there is no one watching to animate for.
bar() {
  [ "$quiet" = "1" ] || return 0
  [ -t 1 ] || return 0
  local pct="$1" width=24 filled i out=""
  filled=$(( pct * width / 100 ))
  for (( i = 0; i < width; i++ )); do
    if [ "$i" -lt "$filled" ]; then out="$out█"; else out="$out░"; fi
  done
  printf '\r  %s  %3d%%' "$out" "$pct"
}

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
    log "$out"
    log "предупреждение: apt-get update прошёл с ошибками — продолжаю"
  fi
  if ! out="$(apt-get install -y curl ca-certificates cron 2>&1)"; then
    log "$out"
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
    || { log "$out"; die "не удалось поставить Hysteria2 (get.hy2.sh) — проверь сеть и повтори"; }
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
  verify_sum "$tmp" "mor-linux-$arch" || { rm -f "$tmp"; die "скачанный mor не совпал с контрольной суммой — повтори установку"; }
  chmod +x "$tmp"
  mv -f "$tmp" "$BIN"
}

# verify_sum checks the download against checksums.txt from the same release.
#
# https already protects the transfer, but not against a half-finished download
# — one of those installed a binary that segfaulted on every call — and not
# against a release that was replaced at the source. A missing checksums file
# is not fatal: older releases and local builds do not have one, and refusing
# to install then would break more than it protects.
verify_sum() {
  local file="$1" name="$2" want got
  command -v sha256sum >/dev/null 2>&1 || { log "нет sha256sum — сумму не проверяю"; return 0; }
  want="$(curl -fsSL "$BASE_URL/checksums.txt" 2>/dev/null | awk -v n="$name" '$2 == n { print $1 }')"
  [ -n "$want" ] || { log "checksums.txt недоступен — сумму не проверяю"; return 0; }
  got="$(sha256sum "$file" | cut -d" " -f1)"
  [ "$got" = "$want" ] || { log "сумма не сошлась: ждали $want, получили $got"; return 1; }
  log "контрольная сумма сошлась"
  return 0
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

  setup_ufw "$hy" "$tcp_ports"
}

# setup_ufw puts a firewall on a server that has none.
#
# Without one every port anything ever binds is open to the internet, which on
# a machine holding the keys to a VPN is not a state to leave it in. mor knows
# exactly which ports it needs, so it can close the rest without asking.
#
# The one way this goes wrong is locking the owner out of ssh, so the ssh port
# is taken from sshd itself rather than assumed to be 22, and nothing is
# enabled if sshd cannot be asked.
setup_ufw() {
  local hy="$1" tcp_ports="$2" p ssh_ports=""
  command -v apt-get >/dev/null 2>&1 || { log "нет apt-get — firewall не ставлю"; return 0; }

  if ! command -v ufw >/dev/null 2>&1; then
    wait_apt_lock
    apt-get install -y ufw >>"$LOGFILE" 2>&1 || { log "ufw не поставился — пропускаю"; return 0; }
  fi

  ssh_ports="$(sshd -T 2>/dev/null | grep -E '^port [0-9]+' | cut -d' ' -f2)"
  if [ -z "$ssh_ports" ]; then
    ssh_ports="$(grep -Ei '^[[:space:]]*Port[[:space:]]+[0-9]+' /etc/ssh/sshd_config 2>/dev/null | tr -s ' ' | cut -d' ' -f2)"
  fi
  if [ -z "$ssh_ports" ]; then
    log "не смог узнать порт ssh — firewall не включаю, чтобы не отрезать доступ"
    return 0
  fi

  for p in $ssh_ports; do ufw allow "$p"/tcp >/dev/null 2>&1 || true; done
  ufw allow "$hy"/udp >/dev/null 2>&1 || true
  for p in $tcp_ports; do ufw allow "$p"/tcp >/dev/null 2>&1 || true; done
  ufw default deny incoming  >/dev/null 2>&1 || true
  ufw default allow outgoing >/dev/null 2>&1 || true
  # Established connections survive this, so the session running it does too.
  ufw --force enable >/dev/null 2>&1 || { log "ufw не включился"; return 0; }
  log "firewall включён, открыты ssh ($ssh_ports), $hy/udp и $tcp_ports"
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

# MOR_USER is who the daemon runs as. Not root: it serves a web panel to the
# internet, and a hole in that panel should cost the panel, not the machine.
MOR_USER="mor"

# ensure_user creates the account and hands it exactly what the daemon touches.
#
# Returns non-zero when the machine will not cooperate — no useradd, no sudo —
# and the caller then leaves the service as root rather than installing
# something that cannot start.
ensure_user() {
  command -v useradd >/dev/null 2>&1 || { log "нет useradd — служба останется от root"; return 1; }
  command -v sudo    >/dev/null 2>&1 || { log "нет sudo — служба останется от root"; return 1; }

  id -u "$MOR_USER" >/dev/null 2>&1 || \
    useradd --system --no-create-home --shell /usr/sbin/nologin "$MOR_USER" >>"$LOGFILE" 2>&1 || {
      log "не удалось создать пользователя $MOR_USER — служба останется от root"; return 1; }

  # mor's own directory: nobody else has any business in it.
  chown -R "$MOR_USER":"$MOR_USER" "$MOR_DIR" 2>/dev/null || true
  chmod 700 "$MOR_DIR" 2>/dev/null || true

  # The engine configs are written by mor and read by the engines under their
  # own users, so ownership goes to mor and the read bit stays for everyone
  # else. The directory has to be mor's too: a config is replaced by writing a
  # temp file next to it and renaming.
  grant_engine_files /etc/hysteria hysteria
  grant_engine_files /usr/local/etc/xray ""

  install_sudoers || return 1
  return 0
}

# grant_engine_files gives mor write access to one engine's config directory
# while leaving the engine able to read it.
grant_engine_files() {
  local dir="$1" reader="$2" f
  [ -d "$dir" ] || return 0
  chown "$MOR_USER" "$dir" 2>/dev/null || true
  chmod 755 "$dir" 2>/dev/null || true
  for f in "$dir"/*; do
    [ -f "$f" ] || continue
    case "$f" in
      # A private key stays unreadable to the rest of the machine: mor writes
      # it, the engine's own group reads it, nobody else.
      *.key) chown "$MOR_USER":"${reader:-$MOR_USER}" "$f" 2>/dev/null || true
             chmod 640 "$f" 2>/dev/null || true ;;
      *)     chown "$MOR_USER" "$f" 2>/dev/null || true
             chmod 644 "$f" 2>/dev/null || true ;;
    esac
  done
}

# install_sudoers spells out the two things the daemon cannot do itself.
#
# Restarting the engines and opening a port are the whole list. Everything else
# — reading its own files, binding its ports, talking to the engines' APIs — it
# does as itself.
install_sudoers() {
  local sctl ufw_bin fwcmd out
  sctl="$(command -v systemctl || echo /usr/bin/systemctl)"
  ufw_bin="$(command -v ufw || true)"
  fwcmd="$(command -v firewall-cmd || true)"

  {
    echo "# Поставлено mor. Демон работает не от root и просит ровно это."
    echo "Cmnd_Alias MOR_ENGINES = \\"
    echo "  $sctl restart hysteria-server, $sctl restart xray, \\"
    echo "  $sctl reset-failed hysteria-server, $sctl reset-failed xray, \\"
    echo "  $sctl enable hysteria-server, $sctl enable xray, \\"
    echo "  $sctl disable --now hysteria-server, $sctl disable --now xray"
    [ -n "$ufw_bin" ] && echo "Cmnd_Alias MOR_UFW = $ufw_bin status, $ufw_bin allow *"
    [ -n "$fwcmd" ]   && echo "Cmnd_Alias MOR_FW = $fwcmd --list-ports, $fwcmd --permanent --add-port=*, $fwcmd --reload"
    printf '%s ALL=(root) NOPASSWD: MOR_ENGINES' "$MOR_USER"
    [ -n "$ufw_bin" ] && printf ', MOR_UFW'
    [ -n "$fwcmd" ]   && printf ', MOR_FW'
    printf '\n'
  } > /etc/sudoers.d/mor.tmp

  # A broken sudoers file locks the machine out of sudo entirely, so it is
  # checked before it is put in place.
  if ! out="$(visudo -cf /etc/sudoers.d/mor.tmp 2>&1)"; then
    log "sudoers не прошёл проверку: $out"
    rm -f /etc/sudoers.d/mor.tmp
    return 1
  fi
  chmod 440 /etc/sudoers.d/mor.tmp
  mv -f /etc/sudoers.d/mor.tmp /etc/sudoers.d/mor
  return 0
}

# harden_engines makes the engines come back on their own.
#
# Hysteria2's own unit ships Restart=no and Xray's is on-failure, so an engine
# that dies takes the VPN with it until somebody notices — which, for something
# people rely on, means an evening. mor's unit already restarts itself; these
# drop-ins say the same for the two it drives. Drop-ins rather than edits: the
# engines' own installers overwrite their unit files on every upgrade.
harden_engines() {
  local unit dir
  for unit in hysteria-server xray; do
    systemctl list-unit-files "$unit.service" >/dev/null 2>&1 || continue
    dir="/etc/systemd/system/$unit.service.d"
    mkdir -p "$dir"
    cat >"$dir/mor-restart.conf" <<EOF
# Поставлено mor: движок должен подниматься сам.
# Предел попыток снимается в [Unit] — в [Service] systemd его игнорирует, и
# движок, падающий в цикле, встал бы навсегда после пятой попытки.
[Unit]
StartLimitIntervalSec=0

[Service]
Restart=always
RestartSec=3
EOF
  done
  systemctl daemon-reload >/dev/null 2>&1 || true
}

install_service() {
  harden_engines

  # The account is prepared first: if anything about it fails, the unit below
  # is written for root instead, because a service that cannot read its own
  # files is worse than one with too many rights.
  local run_as="root" sandbox
  if ensure_user; then
    run_as="$MOR_USER"
    log "служба будет работать от $MOR_USER"
  fi

  # Два набора ограничений, потому что они несовместимы.
  #
  # RestrictSUIDSGID, ProtectKernelTunables, PrivateDevices и соседние опции
  # молча включают NoNewPrivileges, а он запрещает sudo — то есть перезапуск
  # движков и правку firewall. Под отдельным пользователем эти запреты почти
  # ничего не добавляют: прав менять ядро или ставить suid у него всё равно
  # нет. Под root они нужны, и там sudo не требуется.
  if [ "$run_as" = "root" ]; then
    sandbox="NoNewPrivileges=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
RestrictSUIDSGID=true
RestrictRealtime=true
LockPersonality=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK"
  else
    sandbox="# Ограничения, включающие NoNewPrivileges, здесь сняты: они запрещают
# sudo, которым перезапускаются движки. Под отдельным пользователем прав,
# которые они отбирают, и так нет."
  fi

  cat >/etc/systemd/system/mor.service <<EOF
[Unit]
Description=mor VPN
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
# Права чинятся при каждом запуске, а не один раз при установке. Сертификат
# продлевает acme.sh от root, и после продления файл принадлежал бы root —
# демон перестал бы его читать через несколько дней после установки, когда
# связать поломку с ней уже никто не сможет. Плюс перед ExecStartPre означает
# «выполнить от root, несмотря на User=».
ExecStartPre=+/bin/sh -c 'chown -R ${run_as} ${MOR_DIR} 2>/dev/null || true'
ExecStart=${BIN} serve
Restart=always
RestartSec=3
User=${run_as}

# Демон отдаёт панель в интернет, поэтому держит при себе минимум: свой
# каталог, конфиги двух движков и — через sudoers — право перезапустить эти
# движки и открыть порт. Остальная машина ему недоступна.
${sandbox}
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectControlGroups=true
# ProtectSystem=strict делает всё только для чтения, поэтому пути, куда mor
# действительно пишет, названы обратно. /etc/ufw и /etc/firewalld — не для
# самого mor: запрет распространяется и на то, что он запускает, а ufw пишет
# свои правила туда. Без этой строки смена порта из панели молча не открывала
# бы его в firewall. Дефис перед путём означает «пропустить, если нет».
ReadWritePaths=${MOR_DIR} /etc/hysteria /usr/local/etc/xray -/root/.acme.sh -/var/spool/cron -/etc/ufw -/etc/firewalld -/var/lib/ufw

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
  # The drop-in mor put on Xray's unit: Xray itself may stay, so only mor's
  # file goes, not the directory somebody else may be using.
  rm -f /etc/systemd/system/xray.service.d/mor-restart.conf
  rmdir /etc/systemd/system/xray.service.d 2>/dev/null || true
  systemctl daemon-reload
  rm -f "$BIN" /usr/local/bin/aqu /usr/local/bin/hysteria
  rm -rf "$MOR_DIR" /etc/aqu /etc/hysteria /etc/sing-box
  rm -f /usr/local/bin/sing-box
  # The account and the one permission it was given go too: leaving a sudoers
  # entry for a user that no longer exists is exactly the kind of leftover
  # nobody finds later.
  rm -f /etc/sudoers.d/mor
  id -u mor >/dev/null 2>&1 && userdel mor >/dev/null 2>&1 || true
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
  quiet=1
  : >"$LOGFILE"

  bar 0
  ensure_deps
  bar 10
  install_hysteria
  bar 30
  install_xray
  bar 50
  install_mor
  bar 65
  migrate_from_aqu
  mkdir -p "$MOR_DIR"

  local fresh=0
  if [ ! -f "$MOR_DIR/config.json" ]; then
    fresh=1
    local ip; ip="$(public_ip)"
    "$BIN" setup --host "$ip" --port "${VPN_PORT}" --sni "${SNI}" >>"$LOGFILE" 2>&1 \
      || die "mor setup не удался"
  fi
  bar 72

  install_service
  systemctl enable mor >/dev/null 2>&1 || true
  systemctl restart mor >>"$LOGFILE" 2>&1 || die "не удалось запустить сервис mor"
  bar 80

  start_engine hysteria-server hy2
  if command -v xray >/dev/null 2>&1; then
    if xray_off; then log "Xray выключен в настройках mor — не запускаю"; else start_engine xray; fi
  fi
  drop_singbox
  bar 88
  open_firewall >/dev/null
  panel_cert
  bar 95

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
  # Let's Encrypt ограничивает число одинаковых сертификатов в неделю, а
  # установку запускают и по десять раз подряд. Пока есть настоящий и живой —
  # он не перевыпускается: продлевает его собственный cron acme.sh.
  if cert_is_fresh; then
    log "сертификат на месте — перевыпуск не нужен"
    return 0
  fi
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

# cert_is_fresh: сертификат выдан удостоверяющим центром и не истекает в
# ближайшие двое суток.
cert_is_fresh() {
  local crt="$MOR_DIR/web.crt"
  [ -f "$crt" ] || return 1
  command -v openssl >/dev/null 2>&1 || return 1
  openssl x509 -in "$crt" -noout -subject 2>/dev/null | grep -q "mor panel" && return 1
  openssl x509 -in "$crt" -noout -checkend 172800 >/dev/null 2>&1
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
  if "$BIN" check --fast >>"$LOGFILE" 2>&1; then
    bar 100
    done_line "установлено"
    return 0
  fi
  bar 100
  done_line "установлено, но часть движков не поднялась — mor check"
  return 0
}

# done_line replaces the bar with the one sentence worth keeping.
done_line() {
  if [ "$quiet" = "1" ] && [ -t 1 ]; then
    printf '\r\033[K'
  fi
  printf '  %s\n' "$*"
}

case "${1:-install}" in
  uninstall|remove) uninstall ;;
  install|"")       install ;;
  *) die "неизвестная команда: $1 (доступно: install, uninstall)" ;;
esac
