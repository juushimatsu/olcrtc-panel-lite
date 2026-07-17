# Эксплуатация

## Пути

```text
/usr/local/bin/olcrtc-panel
/usr/local/bin/olcrtc
/etc/olcrtc-panel/config.yaml
/etc/olcrtc-panel/master.key
/etc/olcrtc-panel/instances/<id>/{config.yaml,key.hex}
/var/lib/olcrtc-panel/panel.db
/var/lib/olcrtc-panel/tls/
/var/lib/olcrtc-panel/releases/
/var/lib/olcrtc/<id>/data/
```

## Credentials

```bash
sudo olcrtc-panel credentials reset --config /etc/olcrtc-panel/config.yaml
sudo olcrtc-panel credentials set --username new_admin --config /etc/olcrtc-panel/config.yaml
```

Reset печатает новый password один раз и отзывает все sessions.

## Certificate

```bash
sudo olcrtc-panel certificate ensure --config /etc/olcrtc-panel/config.yaml
sudo olcrtc-panel certificate regenerate --public-ip 203.0.113.20 --config /etc/olcrtc-panel/config.yaml
sudo systemctl restart olcrtc-panel
```

## HTTPS-порт

Предпочтительный способ для установленной панели: открыть `Настройки → HTTPS и IP`, изменить `HTTPS порт`, сохранить и перезапустить службу:

```bash
sudo ufw allow 9443/tcp 2>/dev/null || true
sudo systemctl restart olcrtc-panel.service
sudo systemctl --no-pager --full status olcrtc-panel.service
```

Новый адрес будет иметь вид `https://IP_СЕРВЕРА:9443`. Порт не входит в TLS-сертификат, поэтому перевыпускать сертификат не требуется. После проверки нового адреса старое правило можно удалить: `sudo ufw delete allow 8443/tcp`.

## Диагностика

```bash
systemctl status olcrtc-panel
journalctl -u olcrtc-panel -n 200 --no-pager
systemctl status olcrtc-instance@1
journalctl -u olcrtc-instance@1 -n 200 --no-pager
```

Перед каждым запуском `olcrtc-instance@<id>.service` выполняет root-only `ExecStartPre`, который восстанавливает безопасные owner/mode для текущего `olcrtc` binary, instance YAML/key и runtime directory. Ручная эквивалентная проверка:

```bash
sudo olcrtc-panel instance prepare --config /etc/olcrtc-panel/config.yaml --id 1
```

## Релизы и обновления

Страница `Настройки → Обновления` показывает последние десять опубликованных bundle-релизов. Из неё можно обновиться до последнего bundle, установить одну из прошлых версий или выполнить rollback на предыдущий локальный bundle. Workflow после успешной публикации автоматически удаляет более старые bundle-релизы и соответствующие tags.

Exact traffic появляется после закрытия tunnel stream. Dashboard network speed, если добавлена в будущем через IPAccounting delta, не должна смешиваться с exact payload total.

## Восстановление

1. остановить `olcrtc-panel` и instance units;
2. распаковать recovery archive в `/`;
3. проверить владельцев и mode private files;
4. выполнить `olcrtc-panel assets install --root /`;
5. `systemctl daemon-reload`;
6. запустить panel, затем нужные instances.

SQLite использует WAL. Не копируйте live `panel.db` обычным `cp`; UI backup использует SQLite `VACUUM INTO` snapshot.
