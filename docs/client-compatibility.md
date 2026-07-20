# Совместимость клиентов

Панель формирует два разных per-instance QR. QR OLCBOX сохраняет прежний формат. QR OLCRTC Client использует compact URI Android-клиента и доступен только для `wbstream + vp8channel`, `telemost + vp8channel` и `jitsi + datachannel`.

Панель публикует две проекции подписки: `/sub/<slug>` для OLCRTC Client и `/sub/<slug>/olcbox` для OLCBOX. Yandex encrypted mirror остаётся проекцией OLCRTC Client; OLCBOX использует plain-text URL.

Subscription bundle:

```json
{
  "type": "olcrtc-sub",
  "v": 2,
  "n": "Subscription name",
  "s": "slug",
  "u": "https://IP:8443/sub/slug",
  "m": [],
  "mk": "",
  "uc": false,
  "d": true
}
```

При включённом Yandex mirror массив `m` содержит public URL encrypted envelope, а `mk` — per-subscription AES-256-GCM key в base64url без padding. Ключ не загружается на Yandex Disk.

WB auth token включается полностью только в QR/URI OLCRTC Client. Token и mirror key визуально скрыты до явного показа. Private CA pinning автоматически не гарантируется: пользователь должен сверить fingerprint и установить CA в trust store. HTTP fallback и offline mode не предусмотрены.

| Transport | Telemost | WB Stream | Jitsi |
|---|---:|---:|---:|
| datachannel | QR Client недоступен | QR Client недоступен | поддерживается |
| vp8channel | поддерживается | поддерживается | QR Client недоступен |
| seichannel | QR Client недоступен | QR Client недоступен | QR Client недоступен |
| videochannel | QR Client недоступен | QR Client недоступен | QR Client недоступен |
