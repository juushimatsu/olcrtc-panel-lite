# URI и подписки

Standard projection следует официальным документам upstream:

- `temp-files/olcrtc-master/docs/uri.md`;
- `temp-files/olcrtc-master/docs/sub.md`.

## Standard URI

```text
olcrtc://<Auth>?<Transport>@<RoomID>#<EncryptionKey>$<MIMO>
olcrtc://<Auth>?<Transport><key=value&key=value>@<RoomID>#<EncryptionKey>$<MIMO>
```

Transport payload содержит только документированные поля. Значения, равные upstream defaults, опускаются. WB token, Client ID, local data path и proxy никогда не включаются.

## Standard endpoint

```text
GET /sub/<slug>
```

Ответ - `text/plain; charset=utf-8`, `Cache-Control: no-store`. Global metadata начинается с `#`, metadata entry - с `##` и относится к ближайшему предыдущему URI. Linked entry рендерится из текущей конфигурации инстанса; manual URI не переписывается.

## Exclave compatibility

```text
GET /sub/<slug>/exclave
```

Это отдельная legacy-проекция: одна Exclave-compatible URI на строку. Она не меняет standard body. Manual entry публикуется здесь только с `exclave_compatible=true`.

## QR

- standard instance QR содержит standard URI;
- Exclave instance QR содержит compatibility URI;
- standard subscription QR содержит только HTTPS URL `/sub/<slug>`;
- Exclave/Yandex QR содержит JSON bundle `olcrtc-sub`, version 2.

QR создаётся локально панелью, payload не отправляется внешним QR-сервисам.

## Traffic metadata

Exact `used` складывается из journald events связанных инстансов. При quota entry получает `used/quota` и remaining `available`; без quota публикуется `unlimited`. Manual entry имеет только вручную заданные значения.
