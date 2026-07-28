# mor

Self-hosted VPN за одну команду. Два протокола на одном сервере, управление из терминала.

```
curl -fsSL https://github.com/FrogHoppingAmongTulips/mor/releases/latest/download/install.sh | bash
```

Дальше набери `mor` — меню, всё цифрами.

## Протоколы

| | Транспорт | Когда нужен |
|---|---|---|
| **Hysteria2** | UDP | основной: быстрый, маскируется под HTTPS-сайт |
| **VLESS Reality** | TCP | сеть режет UDP целиком |

Ключ принадлежит одному протоколу. Создаёшь ключ — выбираешь протокол, получаешь ссылку и QR.
Ключи Hysteria2 применяются на лету, Reality перезапускает свой движок.

## Меню

```
  1  Создать ключ      5  DNS
  2  Список ключей     6  SNI
  3  Показать ключ     7  Порты
  4  Удалить ключ      8  Состояние
                       0  Выход
```

Каждый пункт объясняет себя на своём экране. `Состояние` — первая проверка,
когда приложение пишет «timeout».

Ключ создаётся в три шага: протокол, имя, SNI. На шаге SNI уже напечатано `www.`,
Enter оставляет `www.cloudflare.com`. Домен проверяется на месте: если его не существует
или он не отвечает по HTTPS, mor скажет об этом и попросит ввести другой.

Позже SNI меняется в пункте 6: выбираешь ключ и задаёшь домен, у каждого ключа он свой.
При удалении можно указать сразу несколько номеров через пробел.

Для скриптов есть одиночные команды: `mor user телефон`, `mor user --reality ноут`,
`mor list`, `mor qr 1`, `mor status`, `mor dns 9.9.9.9`.

## Настройки по умолчанию

- **Порт Hysteria2 — 2096/UDP.** Операторы массово режут UDP/443, туннель отваливается
  по таймауту при исправном сервере.
- **DNS — 1.1.1.1.** Резолвер хостера видит все запросы и местами подменяет ответы.
- **SNI — www.cloudflare.com.** `www.microsoft.com` на Xray v26 ломает рукопожатие Reality.

Удалить всё с сервера:

```
curl -fsSL https://github.com/FrogHoppingAmongTulips/mor/releases/latest/download/install.sh | bash -s -- uninstall
```

## Архитектура

```
cmd/mor           меню, команды, демон, установка
internal/config   настройки и ключи сервера (config.json)
internal/store    ключи клиентов (users.json)
internal/keys     генерация секретов
internal/hysteria Hysteria2: сертификат, config.yaml, auth-коллбэк
internal/xray     VLESS Reality: config.json, ссылки
internal/qr       QR в терминале
```

Единственная зависимость — `github.com/skip2/go-qrcode`, остальное на стандартной библиотеке.

Hysteria2 спрашивает разрешение на каждое подключение у коллбэка `mor` на `127.0.0.1:9797`,
поэтому её ключи применяются без перезапуска. Отказы пишутся в `journalctl -u mor`.

## Разработка

```
make build    make vet    make test    make dist
```

Пути переопределяются переменными: `MOR_DIR`, `MOR_HY_CONFIG`, `MOR_XRAY_CONFIG`.

Релиз — пуш тега `vX.Y.Z`, дальше [release.yml](.github/workflows/release.yml) соберёт
бинарники и опубликует их.
