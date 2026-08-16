# mor

VPN simple

## Установка

```
curl -fsSL https://github.com/FrogHoppingAmongTulips/mor/releases/latest/download/install.sh | bash
```

## Обновление

```
mor update
```

## Удаление

```
curl -fsSL https://github.com/FrogHoppingAmongTulips/mor/releases/latest/download/install.sh | bash -s uninstall
```

Удаляет mor, движки и все ключи.

## Протоколы

| | Транспорт | Когда нужен |
|---|---|---|
| **Hysteria2** | UDP | самый быстрый |
| **VLESS+Reality** | TCP | поддержан почти везде, проходит там, где блокируют остальное |
| **VLESS Encryption** | TCP | шифрование Xray без сертификатов, поддержан не всеми клиентами |
| **Shadowsocks** | TCP | работает в старых приложениях |

Работают одновременно, любой выключается: `mor proto off ss` (`hy2`, `reality`,
`enc`, `ss`).

## Проверка перед выдачей

```
bash scripts/e2e.sh
```

Создаёт временный ключ, поднимает рядом настоящий клиент по каждому протоколу,
качает через него страницу, сверяет адрес выхода со своим, ждёт пересчёт
трафика и проверяет подписку. Ключ удаляется в конце. Если проходит — ключ,
выданный человеку, работает.

## Форматы подписки

Клиенты используют разные форматы. mor определяет формат по User-Agent и отдаёт
подходящий.

| Формат | Клиенты |
|---|---|
| список ссылок | v2rayN, v2rayNG, Nekobox, Streisand, Shadowrocket, Happ |
| Clash YAML | Clash Meta, mihomo, Stash, Verge |
| sing-box JSON | sing-box, SFI/SFA/SFM, Karing, Hiddify |

Формат задаётся явно суффиксом `/clash`, `/singbox` или `/raw`.
