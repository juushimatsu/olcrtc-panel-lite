# Data Directory Architecture

## Problem

Upstream olcRTC изменил контракт поля `data` в конфигурации. Панель создавала пустой каталог `/var/lib/olcrtc/{id}/data/`, но новый upstream требует наличия непустых файлов `names` и `surnames` для генерации имён участников комнаты.

## Solution

### Shared Data Location

Release bundle включает словари `olcrtc-names` и `olcrtc-surnames`, извлечённые из upstream `internal/names/data/`. При установке или обновлении:

```bash
/var/lib/olcrtc-panel/releases/
  ├── data/
  │   ├── names       # shared словарь имён
  │   └── surnames    # shared словарь фамилий
  ├── current -> bundle-20260814-abc123/
  └── bundle-20260814-abc123/
      ├── olcrtc-panel
      ├── olcrtc
      ├── manifest.json
      └── data/
          ├── names
          └── surnames
```

### Instance Configuration

Каждый YAML-конфиг инстанса указывает на shared path:

```yaml
# /etc/olcrtc-panel/instances/1/config.yaml
data: /var/lib/olcrtc-panel/releases/data
```

**Важно**: путь к `data` теперь **НЕ** указывает на runtime-каталог инстанса (`/var/lib/olcrtc/{id}/data`), а на shared location в releases.

### Runtime Behavior

При первом запуске инстанса systemd ExecStartPre копирует словари в runtime-каталог инстанса **только если они отсутствуют**:

```bash
# olcrtc-instance@.service ExecStartPre
/usr/local/bin/olcrtc-panel instance prepare-permissions %i

# Внутри PreparePermissions():
# 1. Создаёт /var/lib/olcrtc/{id}/data/
# 2. Если /var/lib/olcrtc-panel/releases/data/{names,surnames} существуют
#    и /var/lib/olcrtc/{id}/data/{names,surnames} отсутствуют:
#      копирует словари в runtime data/
```

Это обеспечивает:
- ✅ Автоматическое исправление старых инстансов с пустыми data/ при следующем restart
- ✅ Сохранение пользовательских словарей (если они были вручную заменены)
- ✅ Минимальное дублирование (словари shared между инстансами через symlink в YAML)

### Update & Rollback

При обновлении bundle через `update.sh`:

1. Новый bundle устанавливается в `/var/lib/olcrtc-panel/releases/{new-bundle}/`
2. Shared data обновляется: `releases/data/{names,surnames}` копируются из нового bundle
3. **Reconcile** перезаписывает все YAML-конфиги инстансов с новым `data` path (всегда `releases/data`)
4. При rollback восстанавливается shared data из предыдущего bundle

### Migration Path

**Существующие инстансы** (до патча):
- YAML содержит `data: /var/lib/olcrtc/{id}/data`
- Runtime data/ пустой → upstream ломается

**После патча**:
1. Установка/обновление создаёт `releases/data/{names,surnames}`
2. **Reconcile при старте панели** переписывает все YAML → `data: /var/lib/olcrtc-panel/releases/data`
3. При restart инстанса ExecStartPre копирует словари в runtime data/ (если они пусты)
4. Upstream находит непустые `names`/`surnames` → работает

### File Permissions

```bash
/var/lib/olcrtc-panel/releases/data/
  ├── names       # 0640 root:olcrtc
  └── surnames    # 0640 root:olcrtc

/var/lib/olcrtc/{id}/data/
  ├── names       # 0640 olcrtc:olcrtc (скопировано при первом запуске)
  └── surnames    # 0640 olcrtc:olcrtc
```

Systemd service работает от пользователя `olcrtc`, который входит в группу `olcrtc` и может читать shared словари.

## Implementation

### Modified Files

1. **CI** (`.github/workflows/daily-upstream.yml`):
   - Извлекает `upstream/internal/names/data/{names,surnames}` → `dist/olcrtc-{names,surnames}`
   - Валидирует наличие и непустоту
   - Включает в SHA256SUMS

2. **Installer** (`install.sh`):
   - Загружает `olcrtc-names`, `olcrtc-surnames` из release
   - Устанавливает в `{bundle}/data/` и `releases/data/`
   - Обновляет `repair_release_permissions()` для shared data

3. **Update script** (`internal/assets/files/update/update.sh`):
   - Загружает словари при обновлении bundle
   - Устанавливает в новый bundle и обновляет shared data
   - Копирует словари в пустые runtime data/ существующих инстансов
   - При rollback восстанавливает shared data из предыдущего bundle

4. **Manager** (`internal/instance/manager.go`):
   - Добавляет `releaseDir` в конструктор
   - `writeConfig()` использует `releaseDir/data` вместо runtime data
   - **Новый метод `Reconcile()`**: перезаписывает все YAML с актуальным `data` path

5. **Permissions** (`internal/instance/permissions.go`):
   - ExecStartPre копирует словари из `releases/data/` в runtime data/ при отсутствии

6. **Main** (`cmd/olcrtc-panel/main.go`):
   - Передаёт `cfg.ReleaseDir` в `NewManager()`
   - Вызывает `instances.Reconcile()` после инициализации, до запуска HTTP

## Testing Scenarios

### Fresh Install
1. `install.sh` устанавливает bundle с словарями
2. Создаётся инстанс → YAML содержит `data: releases/data`
3. Start инстанса → ExecStartPre копирует словари → upstream работает

### Upgrade from Old Version
1. Старый инстанс с `data: /var/lib/olcrtc/{id}/data` (пустой)
2. `update.sh install bundle-new` устанавливает словари в `releases/data/`
3. Panel restart → `Reconcile()` переписывает YAML → `data: releases/data`
4. Instance restart → ExecStartPre копирует словари → upstream работает

### Rollback
1. Update bundle-new с новыми словарями
2. Health check fails → автоматический rollback
3. `releases/data/` восстанавливается из bundle-old
4. Инстансы перезапускаются → работают с предыдущими словарями

### Custom Dictionaries
1. Admin вручную заменяет `/var/lib/olcrtc/{id}/data/names` на кастомный
2. Update bundle → shared data обновляется
3. Instance restart → ExecStartPre **не копирует** (файл уже существует)
4. Кастомный словарь сохраняется
