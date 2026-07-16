# Совместимость клиентов

Standard URI/subscription - основной формат проекта. Exclave projection существует параллельно и не считается стандартом upstream.

Текущий compatibility bundle:

```json
{
  "type": "olcrtc-sub",
  "v": 2,
  "n": "Subscription name",
  "s": "slug",
  "u": "https://IP:8443/sub/slug/exclave",
  "m": [],
  "mk": "",
  "uc": true,
  "d": true
}
```

При включённом Yandex mirror массив `m` содержит public URL encrypted envelope, а `mk` - per-subscription AES-256-GCM key в base64url без padding. Ключ не загружается на Yandex Disk.

Private CA pinning текущим клиентским flow автоматически не гарантируется. Пользователь должен сверить fingerprint и отдельно настроить доверие CA. HTTP fallback не предусмотрен.

## Provider/transport

| Transport | Telemost | WB Stream | Jitsi |
|---|---:|---:|---:|
| datachannel | не работает | нужен moderator token | стабильно |
| vp8channel | стабильно | стабильно | нестабильно |
| seichannel | не работает | стабильно | нестабильно |
| videochannel | медленно | стабильно | нестабильно |

Рекомендуются `jitsi + datachannel` и `wbstream + vp8channel`.
