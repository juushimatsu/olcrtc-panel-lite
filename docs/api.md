# Внутренний HTTP API

Все административные routes имеют prefix `/api/v1`, требуют session cookie и CSRF для mutating methods.

Основные группы:

- `/auth/login`, `/auth/logout`, `/auth/me`, `/auth/credentials`, `/auth/sessions`;
- `/system/status`, `/system/metrics`, `/system/certificate`, `/system/logs`, `/system/backup`;
- `/instances` и `/instances/<id>/{start,stop,restart,duplicate,rotate-key,rotate-client-id,change-room,reset-traffic,diagnostics,uri,qr,logs}`;
- `/subscriptions`, OLCRTC Client entries, payload/QR, reorder и mirror sync;
- `/wb/components`, `/wb/components/progress`, `/wb/session` (`action=create|refresh`), `/wb/token/refresh`;
- `/updates/check`, `/updates/releases`, `/updates/install`, `/updates/progress`, `/updates/rollback`;
- `/settings`.

Ошибка имеет стабильную форму:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "Понятное сообщение",
    "request_id": "..."
  }
}
```

Public routes без admin metadata:

```text
GET /sub/<slug>
GET /sub/<slug>/open
GET /ca.crt
```

`/wb/components/progress` и `/updates/progress` возвращают состояние операции, текущую фазу, сообщение и процент выполнения. `/updates/releases` перечисляет до десяти доступных GitHub bundle-релизов и отмечает `latest` и текущий установленный bundle.
