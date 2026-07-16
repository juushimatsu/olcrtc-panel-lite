# olcrtc-panel-lite

Самостоятельная HTTPS-панель для управления официальным [olcRTC](https://github.com/openlibrecommunity/olcrtc) на Ubuntu 22.04/24.04 и Debian 12.

Панель не изменяет upstream и не встраивается в него. Каждый инстанс запускается отдельным `olcrtc-instance@<id>.service` с официальным YAML-файлом. В одном Go-бинарнике находятся web UI, внутренний API, subscription server, traffic collector и updater controller.

## Возможности

- один администратор, Argon2id, Secure/HttpOnly cookies, CSRF и rate limit;
- private CA и HTTPS по IP без домена и HTTP fallback;
- CRUD, start/stop/restart, атомарный rollback YAML, key rotation и Room ID;
- `jitsi`, `telemost`, `wbstream` и четыре официальных transport;
- standard URI/subscription по `docs/uri.md` и `docs/sub.md` upstream;
- отдельная Exclave-compatible проекция и QR;
- exact payload traffic из journald с дедупликацией по cursor;
- AES-256-GCM Yandex Disk mirror;
- опциональный Playwright/Chromium/noVNC workflow для WB Stream на `linux/amd64`;
- backup, manual update и rollback проверенных release bundles;
- тёмная и светлая адаптивные темы без CDN/runtime Node.js.

## Установка

После публикации репозитория и release bundle:

```bash
curl -fsSL https://raw.githubusercontent.com/juushimatsu/olcrtc-panel-lite/master/install.sh | sudo bash
```

Одних исходников в ветке `master` недостаточно: установщик загружает проверенные бинарники из последнего GitHub Release. Workflow `daily upstream bundle` запускается при push в `master`; его также можно запустить вручную на вкладке GitHub Actions.

Для другого owner/repository:

```bash
curl -fsSL https://raw.githubusercontent.com/OWNER/olcrtc-panel-lite/master/install.sh \
  | sudo OLCRTC_PANEL_REPO=OWNER/olcrtc-panel-lite bash
```

Installer выводит URL, случайные credentials, CA fingerprint и server fingerprint один раз. Первый olcRTC-инстанс автоматически не создаётся.

Установщик предпочитает HTTPS-порт `8443`. Если он занят, автоматически выбирается свободный TCP-порт из диапазона `10000–65535`. Порт можно задать явно:

```bash
curl -fsSL https://raw.githubusercontent.com/juushimatsu/olcrtc-panel-lite/master/install.sh \
  | sudo OLCRTC_PUBLIC_PORT=9443 bash
```

Полезные режимы:

```bash
sudo bash install.sh --status
sudo bash install.sh --update
sudo bash install.sh --version bundle-20260716-abcdef12
sudo bash install.sh --reset-credentials
sudo bash install.sh --regenerate-cert
sudo bash install.sh --configure-firewall
```

По умолчанию firewall не изменяется. Панель слушает выбранный установщиком порт на `0.0.0.0` только по HTTPS.

## Удаление

```bash
curl -fsSL https://raw.githubusercontent.com/juushimatsu/olcrtc-panel-lite/master/uninstall.sh | sudo bash
```

Обычное удаление сначала создаёт recovery archive с mode `0600` в `/var/backups/olcrtc-panel/`. Такой archive содержит секреты.

```bash
sudo bash uninstall.sh --purge
```

`--purge` удаляет live state, а backups - только после отдельного подтверждения.

## Локальная разработка

Требуется Go 1.26+.

```bash
go test ./...
go run ./cmd/olcrtc-panel serve --dev --dev-dir .dev-data
```

Dev credentials печатаются в терминал. Откройте `https://127.0.0.1:8443`; сертификат и данные находятся в `.dev-data`.

Проверки перед изменением:

```bash
gofmt -w cmd internal
go vet ./...
go test -race ./...
node --check internal/web/static/app.js
node --check internal/assets/files/wb/worker.mjs
bash -n install.sh uninstall.sh internal/assets/files/wb/*.sh internal/assets/files/update/*.sh
```

## Архитектура

```text
browser
  -> HTTPS olcrtc-panel (root, internal API + embedded SPA)
       -> SQLite /var/lib/olcrtc-panel/panel.db
       -> fixed systemctl/journalctl allowlist
       -> /etc/olcrtc-panel/instances/<id>/config.yaml + key.hex
       -> subscription and encrypted mirror renderers
       -> optional WB session (olcrtc-wb)
  -> olcrtc-instance@<id>.service (user olcrtc)
       -> official /usr/local/bin/olcrtc <config.yaml>
```

Upstream source не является частью runtime-репозитория панели. CI клонирует `openlibrecommunity/olcrtc:master`, проверяет чистое дерево, фиксирует SHA и публикует только полностью прошедший gates bundle.

## Документация

- [Security model](docs/security.md)
- [URI и подписки](docs/subscriptions.md)
- [Совместимость клиентов](docs/client-compatibility.md)
- [Эксплуатация и восстановление](docs/operations.md)
- [Release manifest schema](docs/manifest.schema.json)
- [HTTP API](docs/api.md)

## Ограничения первой версии

- нет доменов, ACME/Let's Encrypt, multi-user/RBAC, 2FA и billing;
- quota/expiry только отображаются и не останавливают общий инстанс;
- WB Playwright недоступен на arm64, ручной Room ID/token остаётся доступен;
- private CA необходимо отдельно установить в trust store клиента и сверить fingerprint;
- provider DOM/API может меняться, поэтому WB automation имеет ручной fallback.

## Лицензия

Код панели распространяется по MIT. Зависимости и официальный olcRTC сохраняют собственные лицензии, перечисленные в [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
