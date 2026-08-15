# План исправления: Ошибка соединения клиент-сервер olcRTC

**Дата анализа:** 2026-08-15  
**Анализируемая версия клиента:** `Olcrtc_client-main` (core: `v0.0.0-20260811121518-3339cd367168`)  
**Анализируемая версия сервера:** `olcrtc-master` (commit: `a451e14f7a965b94c6a1ba17a52c5d60b1737627`)

---

## Симптомы

### Логи клиента
```
ERROR VPN stage START_CARRIER failed attempt=6 elapsed=19753ms | 
  proxyerror: wait for olcRTC: handshake: handshake client: 
  read welcome: handshake: read hdr: timeout
```

**Что происходит на клиенте:**
1. ✅ DNS lookup успешен (с попытки 6)
2. ✅ Получен guest access token от WB Stream
3. ✅ WebRTC соединение установлено (ICE connected, peer connection: connected)
4. ✅ KCP канал создан: `vp8channel: KCP started localEpoch=0x03aa1119`
5. ✅ Latched к серверу: `vp8channel: peer latched epoch=0x436d113c`
6. ❌ **Timeout при чтении welcome message от сервера** (~20 секунд)

### Логи сервера
```
2026-08-15T07:20:39 vp8channel: peer session created epoch=0x03aa1119 peers=1
2026-08-15T07:20:39 vp8channel: per-peer control KCP created peerID=03aa1119
2026-08-15T07:20:39 server: peer control session created peerID=03aa1119
2026-08-15T07:20:39 muxconn: decrypt failed len=48: open record: bad record magic
2026-08-15T07:20:49 muxconn: decrypt failed len=48: open record: bad record magic
2026-08-15T07:21:39 server: peer 03aa1119 did not handshake within 1m0s - releasing control session
2026-08-15T07:21:39 server: AcceptStream(peer control=03aa1119) error: io: read/write on closed pipe
```

**Что происходит на сервере:**
1. ✅ Peer session создана
2. ✅ Per-peer control KCP создан
3. ✅ Server peer control session создана
4. ❌ **Ошибка расшифровки:** `muxconn: decrypt failed len=48: open record: bad record magic`
5. ❌ Timeout handshake через 60 секунд → закрытие соединения

---

## Корневая причина

### Рассогласование версий протокола handshake

**Клиент использует:** handshake `ProtoVersion = 2` (из upstream от 11 августа 2026)  
**Панель собралась с:** handshake `ProtoVersion = 3` (из неверифицированных коммитов upstream)

**Проблема:**
1. Панель использует **ежедневное автоматическое обновление upstream** через GitHub Actions workflow `daily upstream bundle`
2. В последнем обновлении подтянулись **неверифицированные коммиты** с breaking changes
3. Handshake protocol был обновлен с **v2 на v3**
4. Клиент использует проверенную версию с **v2**, сервер ожидает **v3**
5. Результат: `ErrProtocolVersion` → `muxconn: decrypt failed len=48: open record: bad record magic`

**Из кода `internal/handshake/handshake.go` (строка 34):**
```go
const ProtoVersion = 3  // ← Панель использует v3
```

**Клиент ожидает:**
```go
const ProtoVersion = 2  // ← Клиент использует v2 (11 августа)
```

---

## Решение: Откат панели на проверенную версию upstream

### Подход

**Вместо изменения клиентов (которых может быть много в production):**
- ✅ Изменяем только панель (одна точка контроля)
- ✅ Откатываем upstream dependency на **проверенную версию** от 11 августа
- ✅ Временно отключаем автообновление upstream
- ✅ Не ждем верификации новых коммитов

---

## Вариант 1: Откат upstream dependency в панели (РЕКОМЕНДУЕТСЯ)

**Что делаем:**
1. В панели фиксируем версию upstream olcRTC на проверенный коммит от 11 августа
2. Пересобираем панель
3. Создаем новый release bundle с откатанной версией
4. Обновляем сервер через панель
5. **ВАЖНО:** Мигрируем все существующие инстансы на откатанную версию

### ⚠️ Миграция существующих инстансов

**Проблема:**
- Инстансы, созданные с панелью v3, использовали handshake ProtoVersion = 3
- После отката панели на v2, эти инстансы продолжат использовать v3
- Клиенты v2 не смогут подключиться к ним

**Решение:**
Все существующие инстансы должны быть **пересозданы** или **обновлены** для использования v2 протокола.

**Команды:**

```bash
# 1. Перейти в корень проекта панели
cd /path/to/olcrtc-panel-lite  # Путь к вашему клону панели

# 2. Найти версию upstream, которую использует клиент
# Клиент использует: v0.0.0-20260811121518-3339cd367168
# Это означает коммит 3339cd367168 от 11 августа 2026 12:15:18

# 3. В зависимостях панели (если панель напрямую зависит от upstream):
# Проверить go.mod панели
grep "openlibrecommunity/olcrtc" go.mod

# 4. Если панель НЕ зависит напрямую от upstream, то откатываем через bundle:
# Скачать проверенный upstream с GitHub на 11 августа
cd /tmp
git clone https://github.com/openlibrecommunity/olcrtc.git olcrtc-verified
cd olcrtc-verified

# Откатиться на коммит клиента
git checkout 3339cd367168 2>/dev/null || \
  git log --all --since="2026-08-11 00:00" --until="2026-08-11 23:59" --format="%H" | head -1 | xargs git checkout

# 5. Собрать upstream binary для релиза
# Для linux/amd64
GOOS=linux GOARCH=amd64 go build -o olcrtc-linux-amd64 ./cmd/olcrtc

# Для linux/arm64
GOOS=linux GOARCH=arm64 go build -o olcrtc-linux-arm64 ./cmd/olcrtc

# 6. Скопировать name files из upstream
cp data/names olcrtc-names
cp data/surnames olcrtc-surnames

# 7. Создать manifest.json
cat > manifest.json << 'EOF'
{
  "panel_version": "0.2.0-hotfix",
  "upstream_repo": "openlibrecommunity/olcrtc",
  "upstream_sha": "3339cd367168",
  "upstream_ref": "master",
  "build_time": "2026-08-15T10:00:00Z",
  "note": "Hotfix: rolled back to verified upstream version compatible with client v0.0.0-20260811121518-3339cd367168"
}
EOF

# 8. Создать SHA256SUMS
sha256sum olcrtc-linux-amd64 olcrtc-linux-arm64 olcrtc-names olcrtc-surnames manifest.json > SHA256SUMS

# 9. На сервере где установлена панель:
# Остановить все инстансы
sudo systemctl stop 'olcrtc-instance@*'

# Заменить upstream binary
sudo cp /tmp/olcrtc-verified/olcrtc-linux-amd64 /usr/local/bin/olcrtc
sudo chmod +x /usr/local/bin/olcrtc

# Обновить name files
sudo mkdir -p /var/lib/olcrtc-panel/releases/data
sudo cp /tmp/olcrtc-verified/olcrtc-names /var/lib/olcrtc-panel/releases/data/names
sudo cp /tmp/olcrtc-verified/olcrtc-surnames /var/lib/olcrtc-panel/releases/data/surnames

# Запустить инстансы
sudo systemctl start olcrtc-instance@6.service

# 10. Проверить версию
/usr/local/bin/olcrtc --version || echo "Binary работает"

# 11. Проверить логи
sudo journalctl -u olcrtc-instance@6.service -f

# ============================================================
# КРИТИЧЕСКИ ВАЖНО: МИГРАЦИЯ СУЩЕСТВУЮЩИХ ИНСТАНСОВ
# ============================================================

# 12. Найти все существующие инстансы
sudo systemctl list-units 'olcrtc-instance@*.service' --all

# 13. Для КАЖДОГО инстанса нужно выполнить один из способов миграции:

# СПОСОБ A: Пересоздание инстанса (РЕКОМЕНДУЕТСЯ)
# Через панель UI:
# 1. Остановить инстанс
# 2. Сохранить текущую конфигурацию (Room ID, provider, transport, key)
# 3. Удалить инстанс
# 4. Создать новый инстанс с теми же параметрами
# 5. Запустить инстанс
# 6. Обновить subscription для всех клиентов (новый QR/URL)

# СПОСОБ B: Ротация ключа (если инстанс нельзя удалять)
# Через панель UI для каждого инстанса:
# 1. Остановить инстанс
# 2. Нажать "Rotate Key" (генерация нового key.hex)
# 3. Запустить инстанс
# 4. Обновить subscription для всех клиентов

# СПОСОБ C: Принудительный пересбор конфигурации (CLI)
# Для каждого инстанса:
INSTANCE_ID=6  # Замените на ID вашего инстанса

# Остановить инстанс
sudo systemctl stop olcrtc-instance@${INSTANCE_ID}.service

# Принудительно пересоздать config.yaml с новой версией протокола
# (если панель имеет функцию reconcile/rebuild)
# Или вручную пересобрать через панель API

# Запустить инстанс
sudo systemctl start olcrtc-instance@${INSTANCE_ID}.service

# 14. АВТОМАТИЧЕСКАЯ МИГРАЦИЯ ВСЕХ ИНСТАНСОВ (если панель поддерживает)
# Если в панели есть функция массовой миграции:
# Через панель UI: "Settings" → "Migrate all instances to current version"
# Или через CLI панели:
cd /path/to/olcrtc-panel-lite
go run ./cmd/olcrtc-panel migrate-instances --force-rebuild

# 15. ПРОВЕРКА: Убедиться что все инстансы используют v2
# Для каждого инстанса проверить логи при подключении клиента:
sudo journalctl -u olcrtc-instance@6.service --since "1 minute ago" | grep -E "ProtoVersion|handshake"

# Должно быть:
# ✅ Нет ошибок "protocol version mismatch"
# ✅ Клиенты v2 успешно подключаются
```

### 🔄 Сценарии миграции для разных случаев

**Сценарий 1: Новая установка панели (никогда не было v3)**
- Никаких действий не требуется
- Все инстансы будут созданы сразу с v2

**Сценарий 2: Панель была обновлена до v3, есть активные инстансы**
- **КРИТИЧНО:** Все существующие инстансы нужно мигрировать
- Используйте СПОСОБ A (пересоздание) или СПОСОБ B (ротация ключа)
- Обновите subscription для всех клиентов

**Сценарий 3: Несколько серверов с разными версиями панели**
- Сервер A: панель v3 (еще не откачена)
- Сервер B: панель v2 (уже откачена)
- **Проблема:** Клиенты v2 не подключаются к Серверу A
- **Решение:** Откатить все серверы на v2 одновременно
- Или использовать Вариант 3 (backward compatibility) на Сервере A

**Сценарий 4: Постепенный rollout отката**
1. Откатить панель на staging сервере
2. Протестировать с клиентами v2
3. Мигрировать инстансы на staging
4. После проверки — откатить production серверы
5. Мигрировать production инстансы

**Риски:**
- Временная потеря новых фич из неверифицированных коммитов
- Нужно вручную собирать bundle
- **КРИТИЧНО:** Требуется миграция всех существующих инстансов
- Downtime для каждого инстанса во время миграции
- Клиенты должны обновить subscription после миграции

**Преимущества:**
- ✅ Не требует изменения клиентов
- ✅ Быстрое решение для production
- ✅ Используем проверенную версию
- ✅ Одна точка изменения

---

## Вариант 2: Временное отключение автообновления upstream

**Что делаем:**
1. Отключаем GitHub Actions workflow `daily upstream bundle`
2. Откатываем текущую установку на последний проверенный bundle
3. Фиксируем версию до верификации новых коммитов

**Команды:**

```bash
# 1. В репозитории панели отключить workflow
cd /path/to/olcrtc-panel-lite

# Найти workflow файл
ls .github/workflows/

# Отключить daily-upstream.yml (добавить в начало файла):
cat > .github/workflows/daily-upstream.yml << 'EOF'
# TEMPORARILY DISABLED: waiting for upstream verification
# Reason: ProtoVersion v3 breaks compatibility with clients on v2
# Date: 2026-08-15
# TODO: Re-enable after client upgrade to compatible version

name: Daily upstream bundle (DISABLED)

on:
  # schedule:
  #   - cron: '0 2 * * *'  # Commented out
  workflow_dispatch:  # Only manual trigger
EOF

# Закоммитить и запушить
git add .github/workflows/daily-upstream.yml
git commit -m "Temporarily disable daily upstream bundle (handshake v2/v3 incompatibility)"
git push origin master

# 2. Откатить установку на последний проверенный bundle
# Если есть backup предыдущего bundle:
sudo systemctl stop 'olcrtc-instance@*'

# Восстановить из backup (если есть)
sudo cp /var/backups/olcrtc-panel/olcrtc-bundle-20260810.tar.gz /tmp/
cd /tmp
tar -xzf olcrtc-bundle-20260810.tar.gz
sudo cp olcrtc-linux-amd64 /usr/local/bin/olcrtc
sudo chmod +x /usr/local/bin/olcrtc

# Или использовать Вариант 1 для сборки проверенной версии

sudo systemctl start olcrtc-instance@6.service
```

**Риски:**
- Нужно вручную следить за upstream обновлениями
- Пропустим важные security fixes

**Преимущества:**
- ✅ Предотвращает будущие автоматические поломки
- ✅ Контроль над версиями upstream
- ✅ Время для тестирования новых версий

---

## Вариант 3: Патч панели для поддержки handshake v2 и v3 (backward compatibility)

**Что делаем:**
1. Модифицируем панель для поддержки обеих версий handshake
2. Сервер принимает как v2, так и v3
3. Плавный переход без breaking changes

**Патч для `internal/handshake/handshake.go`:**

```go
// ФАЙЛ: internal/handshake/handshake.go в зависимости upstream
// (если панель встраивает upstream, или патчим собранный binary)

// СТАРЫЙ КОД (строка 34):
const ProtoVersion = 3

// НОВЫЙ КОД:
const ProtoVersion = 3  // Latest supported
const MinProtoVersion = 2  // Minimum supported (backward compatibility)

// СТАРЫЙ КОД функции Server (строка 221-228):
if h.Version != ProtoVersion {
    _ = writeFrame(rw, Reject{
        Version: ProtoVersion, Type: TypeReject,
        Reason: "protocol version mismatch", Challenge: h.Challenge,
    })
    return h, "", fmt.Errorf("%w: client v%d, server v%d",
        ErrProtocolVersion, h.Version, ProtoVersion)
}

// НОВЫЙ КОД (поддержка v2 и v3):
if h.Version < MinProtoVersion || h.Version > ProtoVersion {
    _ = writeFrame(rw, Reject{
        Version: ProtoVersion, Type: TypeReject,
        Reason: fmt.Sprintf("protocol version not supported: client v%d, server supports v%d-v%d", 
            h.Version, MinProtoVersion, ProtoVersion),
        Challenge: h.Challenge,
    })
    return h, "", fmt.Errorf("%w: client v%d, server v%d-%d",
        ErrProtocolVersion, h.Version, MinProtoVersion, ProtoVersion)
}
// Server accepts client version if in range [MinProtoVersion, ProtoVersion]
```

**Применение патча:**

```bash
# 1. Клонировать upstream для патча
cd /tmp
git clone https://github.com/openlibrecommunity/olcrtc.git olcrtc-patched
cd olcrtc-patched

# 2. Применить патч вручную или через sed
# Добавить MinProtoVersion
sed -i '/const ProtoVersion = 3/a const MinProtoVersion = 2  // Backward compatibility' internal/handshake/handshake.go

# Заменить проверку версии (сложнее через sed, лучше вручную в редакторе)
# Открыть internal/handshake/handshake.go и применить изменения выше

# 3. Собрать патченый upstream
GOOS=linux GOARCH=amd64 go build -o olcrtc-linux-amd64-patched ./cmd/olcrtc

# 4. Установить на сервер
sudo systemctl stop 'olcrtc-instance@*'
sudo cp olcrtc-linux-amd64-patched /usr/local/bin/olcrtc
sudo chmod +x /usr/local/bin/olcrtc
sudo systemctl start olcrtc-instance@6.service
```

**Риски:**
- Требует знания Go и структуры upstream
- Патч нужно поддерживать при обновлениях
- Может конфликтовать с будущими изменениями upstream

**Преимущества:**
- ✅ Поддержка старых и новых клиентов одновременно
- ✅ Плавная миграция без downtime
- ✅ Можно постепенно обновлять клиентов
- ✅ Правильное долгосрочное решение

---

## Рекомендованный план действий

### Фаза 1: Немедленное восстановление работоспособности (4-8 часов)

1. **Выполнить Вариант 1 (откат upstream в панели)**
   - Собрать проверенную версию upstream от 11 августа
   - Заменить binary на сервере
   - **КРИТИЧНО:** Мигрировать все существующие инстансы
   - Проверить работу с существующими клиентами
   - ✅ Решает проблему немедленно

2. **Миграция инстансов (ОБЯЗАТЕЛЬНЫЙ ШАГ)**
   - Составить список всех существующих инстансов
   - Для каждого инстанса: пересоздать ИЛИ ротация ключа
   - Обновить subscription для всех пользователей
   - Проверить подключение клиентов к каждому инстансу
   - ⚠️ **Без миграции клиенты v2 не смогут подключаться к старым инстансам v3**

3. **Выполнить Вариант 2 (отключить автообновление)**
   - Отключить GitHub Actions workflow `daily upstream bundle`
   - Зафиксировать проверенную версию
   - Предотвратить будущие автоматические поломки

### Фаза 2: Долгосрочное решение (1-2 дня)

3. **Рассмотреть Вариант 3 (backward compatibility)**
   - Создать патч для поддержки handshake v2 и v3
   - Протестировать с клиентами обеих версий
   - Применить как постоянное решение

4. **Процесс верификации upstream**
   - Установить процесс тестирования новых upstream коммитов
   - Создать staging окружение для проверки совместимости
   - Автоматические E2E тесты между панелью и клиентом
   - Ручное одобрение перед production deploy

### Фаза 3: Предотвращение повторения (ongoing)

5. **CI проверки совместимости**
   ```yaml
   # В .github/workflows/ добавить:
   name: Upstream compatibility check
   
   on:
     schedule:
       - cron: '0 3 * * *'  # После daily bundle
     workflow_dispatch:
   
   jobs:
     compatibility-test:
       runs-on: ubuntu-latest
       steps:
         - name: Test handshake compatibility
           run: |
             # Запустить тестовый клиент v2
             # Запустить тестовый сервер (новый bundle)
             # Проверить успешное соединение
             # Если fail → rollback bundle
   ```

6. **Версионирование и changelog**
   - Документировать breaking changes в upstream
   - Semantic versioning для bundle releases
   - Уведомления об обязательных обновлениях клиента

7. **Feature flags**
   - Добавить в панель feature flag для новых версий протокола
   - Постепенный rollout новых версий
   - Возможность быстрого отката без redeploy

---

## Критерии успеха

✅ Клиент успешно проходит handshake с сервером  
✅ В логах сервера отсутствует `muxconn: decrypt failed`  
✅ В логах клиента отсутствует `handshake: read welcome: timeout`  
✅ VPN соединение устанавливается за <5 секунд  
✅ Соединение стабильно работает >5 минут без разрывов  
✅ Существующие клиенты продолжают работать  
✅ Автообновление upstream не создает breaking changes  
✅ **ВСЕ существующие инстансы успешно мигрированы на v2**  
✅ **Клиенты v2 подключаются ко ВСЕМ инстансам (новым и мигрированным)**  
✅ **Subscription обновлены для всех пользователей после миграции**  

---

## Проверки после исправления

```bash
# 1. Проверить версию upstream на сервере
/usr/local/bin/olcrtc --version 2>&1 | head -5

# 2. Проверить логи сервера
sudo journalctl -u olcrtc-instance@6.service --since "5 minutes ago" | grep -E "handshake|decrypt|peer"

# Должны исчезнуть ошибки:
# ✅ НЕТ "muxconn: decrypt failed"
# ✅ ЕСТЬ "server: peer control session created"
# ✅ ЕСТЬ успешный handshake без timeout

# 3. Проверить логи клиента (на Android устройстве)
adb logcat | grep -E "olcRTC|VPN|handshake"

# Должны появиться:
# ✅ "VPN state: CONNECTED"
# ✅ НЕТ "handshake: read welcome: timeout"
# ✅ НЕТ "VPN stage START_CARRIER failed"

# 4. Проверить, что автообновление отключено
cat .github/workflows/daily-upstream.yml | grep -E "schedule|cron"

# Должно быть закомментировано или отсутствовать

# 5. Проверить manifest текущего bundle
cat /var/lib/olcrtc-panel/releases/current/manifest.json

# Должно содержать:
# - upstream_sha близкий к 3339cd367168
# - build_time около 11 августа или newer hotfix

# 6. Stress test соединения
# Держать VPN активным 10+ минут
# Прогонять трафик через туннель
# Мониторить стабильность
```

---

## Откат изменений (если что-то пошло не так)

```bash
# Если откат панели вызвал другие проблемы:

# 1. Восстановить предыдущий binary (если есть backup)
sudo systemctl stop 'olcrtc-instance@*'
sudo cp /usr/local/bin/olcrtc.backup /usr/local/bin/olcrtc
sudo systemctl start olcrtc-instance@6.service

# 2. Включить обратно автообновление (если отключали)
cd /path/to/olcrtc-panel-lite
git revert HEAD  # Откатить коммит с отключением workflow
git push origin master

# 3. Обратиться к Варианту из старого плана:
# Обновить всех клиентов на новую версию (долгосрочное решение)
```

---

## Дополнительная информация

### Почему произошла проблема

1. **GitHub Actions workflow `daily upstream bundle`** автоматически собирает новый bundle каждый день
2. Workflow подтягивает **latest commit** из upstream без проверки compatibility
3. В upstream появился **breaking change**: handshake v2 → v3
4. Bundle собрался и задеплоился автоматически
5. Старые клиенты перестали подключаться

### Как предотвратить в будущем

1. **Staging окружение** для тестирования новых bundle перед production
2. **E2E тесты** с клиентами разных версий
3. **Manual approval** перед deploy критичных обновлений
4. **Semantic versioning** для bundle releases
5. **Changelog** с breaking changes
6. **Backward compatibility** в протоколе (Вариант 3)
7. **Миграционные скрипты** для автоматической миграции инстансов при обновлении/откате панели
8. **Version tracking** для каждого инстанса (какую версию протокола он использует)
9. **Health checks** для проверки совместимости клиент-сервер после обновлений

---

## Контакты и референсы

- **Репозиторий панели:** https://github.com/juushimatsu/olcrtc-panel-lite
- **Upstream olcRTC:** https://github.com/openlibrecommunity/olcrtc
- **Документация handshake:** https://github.com/openlibrecommunity/olcrtc/blob/master/internal/handshake/handshake.go
- **Документация crypto:** https://github.com/openlibrecommunity/olcrtc/blob/master/internal/crypto/chacha.go

## Версии в момент анализа

- **Клиент mobilecore core:** `v0.0.0-20260811121518-3339cd367168` (11 августа 2026)
- **Сервер olcrtc (панель):** коммит `a451e14` (15 августа 2026)
- **Разница:** 4 дня → неверифицированные коммиты с breaking changes

---

**Автор плана:** Claude (Kiro AI)  
**Дата создания:** 2026-08-15  
**Версия документа:** 2.0 (Panel-only fix)
