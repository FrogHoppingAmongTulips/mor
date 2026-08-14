# API

Тот же интерфейс, которым пользуется панель. Для управления сервером без браузера.

## Токен

```
mor token new бот
```

Показывается один раз, хранится только хеш.

```
mor token              # какие есть и когда использовались
mor token rm бот       # отозвать, действует сразу
```

Токен даёт те же права, что и пароль панели, но отзывается отдельно.

## Запрос

```
curl -H 'Authorization: Bearer mor_…' https://адрес:9090/api/users
```

Всё под `/api/` требует токен или сессию. Исключение — `/healthz`: отвечает без
пароля и сообщает только состояние службы.

Ответы — JSON. Ошибка приходит текстом с соответствующим кодом.

## Ключи

| | |
|---|---|
| `GET /api/users` | все ключи |
| `POST /api/users` | создать |
| `GET /api/users/{id}` | один ключ со ссылкой и помесячным трафиком |
| `PATCH /api/users/{id}` | изменить |
| `DELETE /api/users/{id}` | удалить |
| `POST /api/users/{id}/ban` | отключить или включить |
| `POST /api/users/{id}/reset` | обнулить трафик |
| `POST /api/users/{id}/devices/reset` | забыть учтённые устройства |
| `GET /api/qr/{id}` | QR картинкой, `?proto=hy2` — на один протокол |

Создание:

```json
{"name":"телефон","protocols":["hy2","reality"],"time":"30d","traffic":"50gb"}
```

`time` и `traffic` необязательны. Голое число в `time` — дни, в `traffic` —
гигабайты.

Изменение — только те поля, что прислали; пустая строка снимает ограничение:

```json
{"name":"ноут","time":"","traffic":"100gb","ipLimit":2,"autoReset":true}
```

Ключ в ответе:

```json
{
  "id": "78bc…", "name": "телефон",
  "protocols": ["hy2","reality"],
  "created": "2026-08-14T10:00:00Z",
  "lastSeen": "2026-08-14T12:30:00Z",
  "traffic": 1073741824,
  "limit": 53687091200,
  "expiresAt": "2026-09-13T10:00:00Z",
  "banned": false, "ipLimit": 2, "devices": 1, "autoReset": true,
  "spark": [0, 0, 1048576],
  "subLink": "https://адрес:8880/sub/…",
  "links": {"hy2": "hysteria2://…", "reality": "vless://…"}
}
```

`subLink` и `links` приходят только в ответе на один ключ, не в списке.

## Устройства

`ipLimit` — сколько устройств может пользоваться ключом. Проверяется в двух
местах: Hysteria2 запрашивает разрешение на каждое подключение, для остальных
протоколов лимит применяется при выдаче подписки. Второе ограничивает раздачу
ссылки, но не конфига, скопированного вручную, и работает только с клиентами,
присылающими `x-hwid`; клиент без этого заголовка пропускается.

Устройство занимает место 30 дней с последнего обращения. `devices` — сколько
мест занято; `devices/reset` освобождает все. Смена `ipLimit` освобождает их
автоматически.

Идентификатор устройства и ссылка на подписку в файле не хранятся, только их
HMAC.

## Сервер

| | |
|---|---|
| `GET /api/status` | работает или нет, и что именно сломано |
| `GET /api/stats` | загрузка, трафик за месяц, версия, состояние сертификата |
| `GET /api/stats/history` | CPU и память за сутки |
| `GET /api/online` | кто подключён сейчас |
| `GET /api/disk` | что занимает место |
| `GET /api/protocols` | какие протоколы есть и на каких портах |
| `POST /api/protocols/{id}/toggle` | включить или выключить протокол |
| `GET /api/config` | настройки |
| `PUT /api/config` | изменить настройки |
| `POST /api/ports/{proto}/pick` | подобрать порт, доступный снаружи |
| `POST /api/restart` | перезапустить движки |
| `GET /api/audit` | журнал действий |
| `GET /healthz` | без пароля, для мониторинга |

`GET /api/online` отдаёт только факт подключения. Адреса клиентов и посещаемые
ими ресурсы нигде не хранятся.

## Пример

Отключить всех, кто израсходовал лимит:

```bash
TOKEN=mor_…
HOST=https://адрес:9090

curl -s -H "Authorization: Bearer $TOKEN" $HOST/api/users |
  python3 -c '
import sys, json
for u in json.load(sys.stdin):
    if u.get("limit") and u["traffic"] >= u["limit"] and not u["banned"]:
        print(u["id"])
' |
while read id; do
  curl -s -X POST -H "Authorization: Bearer $TOKEN" \
       -H 'Content-Type: application/json' \
       -d '{"banned":true}' "$HOST/api/users/$id/ban"
done
```
