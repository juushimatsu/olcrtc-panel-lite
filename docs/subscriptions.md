# URI и подписки

## QR инстанса

У каждого инстанса остаются два независимых формата:

- **QR OLCBOX** — прежний официальный URI панели. Он не содержит WB auth token;
- **QR OLCRTC Client** — compact URI Android-клиента с обязательными `key` и постоянным UUID `client_id`.

OLCRTC Client поддерживает только комбинации:

- `wbstream + vp8channel`;
- `telemost + vp8channel`;
- `jitsi + datachannel`.

Для остальных комбинаций QR OLCBOX остаётся доступным, а QR OLCRTC Client отключён с пояснением. WB URI клиента содержит полный `auth_token`, поэтому такой URI и QR являются credential. UI маскирует token до нажатия «Показать», после чего разрешает копирование. QR кодируется без сжатия и обрезки с error correction `L`.

Пример:

```text
olcrtc://wbstream@r/<room>?k=<key>&t=vp8channel&f=<fps>&b=<batch>&c=<client_id>&a=<auth_token>&d=<dns>#<name>
```

`client_id` создаётся для каждого нового и существующего инстанса. Его ротация обновляет связанные подписки и mirrors и перезапускает работающий инстанс.

## Подписки OLCRTC Client и OLCBOX

Одна панельная подписка имеет две совместимые проекции:

- **OLCRTC Client** — compact URI feed и JSON bundle/mirror;
- **OLCBOX** — plain-text `sub.md` с обычными OLCBOX URI из `docs/sub.md` upstream.

Endpoint’ы:

```text
GET /sub/<slug>
GET /sub/<slug>/olcbox
GET /sub/<slug>/open
```

`/sub/<slug>` сохраняется для OLCRTC Client: linked entry формируется из текущего key, `client_id` и, для WB, auth token. `/sub/<slug>/olcbox` возвращает `text/plain; charset=utf-8` с глобальными `#...`, обычными URI `olcrtc://<Auth>?<Transport>@<RoomID>#<Key>$<MIMO>` и локальными `##...` полями. Linked entries проецируются автоматически; manual Client URI попадает только в Client feed, manual OLCBOX URI — только в OLCBOX feed.

OLCBOX endpoint также выставляет `Profile-Update-Interval` в целых часах (1–720), который используется клиентом для фонового автообновления; точный интервал остаётся в `#refresh`.

`/open` остаётся deep-link для OLCRTC Client. OLCBOX импортирует URL `/olcbox` напрямую.

`/open` перенаправляет в `olcrtc://subscription?...`. Ответы не кэшируются. Slug содержит не менее 128 бит случайной энтропии и является bearer secret.

## QR подписки

У подписки два QR:

- **QR OLCRTC Client** — компактный JSON без профилей, gzip и multipart;
- **QR OLCBOX** — полный URL `/sub/<slug>/olcbox`.

Пример Client JSON:

```json
{"type":"olcrtc-sub","v":2,"n":"test","s":"Rg59s8rNf","u":"https://89.125.93.65:3000/sub/Rg59s8rNf","m":[{"t":"yandex_disk","u":"https://yadi.sk/d/wXp0dmxaTw6q3w","e":true,"a":"AES-256-GCM"}],"mk":"jaPGwdZdc1HaROEm7fEO_7ZriNDUNvh2pYzCh8xXKFg","uc":false,"d":true}
```

Под QR отображается точный payload. `mirror_key` Client bundle маскируется до нажатия «Показать»; OLCBOX URL отображается полностью и может быть скопирован.

## Yandex mirror

Mirror содержит OLCRTC Client feed, зашифрованный AES-256-GCM без AAD. OLCBOX использует прямой `/olcbox` URL: encrypted mirror не является его форматом. Per-subscription key остаётся в панели и в Client QR, но не загружается на Yandex Disk. Перед upload панель создаёт недостающие каталоги и допускает повторную публикацию существующего файла с тем же key/URL.

Удаление подписки сериализовано с mirror sync: сначала подтверждается удаление Yandex-файла (`404` считается успехом), затем удаляется локальная подписка. При ошибке Yandex локальное удаление отменяется, чтобы не оставить публичный orphan.

## Traffic metadata

Exact `used` складывается из journald events связанных инстансов. При quota entry получает `used/quota` и remaining `available`; без quota публикуется `unlimited`. Manual entry имеет только вручную заданные значения.
