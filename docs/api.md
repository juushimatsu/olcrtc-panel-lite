# Внутренний HTTP API

Все указанные ниже административные routes относительны к `<panel_path>/api/v1`, требуют session cookie и CSRF для mutating methods. При legacy `panel_path: "/"` их адреса по-прежнему начинаются с `/api/v1`; например, при `panel_path: "/control-a8f3"` статус доступен по `/control-a8f3/api/v1/system/status`.

Основные группы:

- `/auth/login`, `/auth/logout`, `/auth/me`, `/auth/credentials`, `/auth/sessions`;
- `/system/status`, `/system/metrics`, `/system/certificate`, `/system/logs`, `/system/backup`;
- `/instances` и `/instances/<id>/{start,stop,restart,duplicate,rotate-key,rotate-client-id,change-room,reset-traffic,diagnostics,uri,qr,logs}`;
- `/subscriptions`, dual OLCRTC Client/OLCBOX entries, `payload?format=client|olcbox`, QR, reorder и mirror sync;
- `/automation/components`, `/automation/components/{install,remove}`, `/automation/components/progress`, `/automation/settings`;
- `/automation/<provider>/session`, `/automation/<provider>/session/extend`, `/automation/<provider>/profile/reset`, где `<provider>` равен `wbstream` или `telemost`;
- `/automation/wbstream/token/refresh`; создание и refresh поддерживаются для WB Stream, Telemost поддерживает только создание комнаты;
- `/updates/check`, `/updates/releases`, `/updates/install`, `/updates/progress`, `/updates/rollback`;
- `/settings`.

Старые `/wb/...` routes сохранены как compatibility aliases на релиз `0.3.0`. Новые клиенты должны использовать `/automation/...`.

`PUT /automation/settings` принимает `proxy_mode` (`direct`, `http`, `https`, `socks5`), `proxy_address` в формате `host:port`, необязательные `proxy_username`, `proxy_password` и `clear_proxy_password`. Пароль никогда не возвращается API. `DELETE /automation/<provider>/session` немедленно останавливает Chromium/noVNC, сохраняя постоянный browser profile.

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

Публичные routes подписок находятся под настроенным `subscription_path` и не содержат admin metadata:

```text
GET <subscription_path>/<slug>
GET <subscription_path>/<slug>/olcbox
GET <subscription_path>/<slug>/open
```

`GET <panel_path>/ca.crt` также не требует session cookie, но намеренно остаётся внутри mount панели. noVNC расположен по `<panel_path>/wb/novnc/` и требует авторизацию.

`/automation/components/progress` и `/updates/progress` возвращают состояние операции, текущую фазу, сообщение и процент выполнения. `/updates/releases` перечисляет до десяти доступных GitHub bundle-релизов и отмечает `latest` и текущий установленный bundle.
