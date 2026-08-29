# Автоматическая настройка первого запуска

Auto-setup — это необязательный wizard, который появляется после первого входа в пустую панель. Он не изменяет существующие инстансы: если в базе уже есть хотя бы один инстанс, флаг `first_run_completed` автоматически считается завершённым.

## Жизненный цикл

1. `GET /api/v1/auto-setup/status` проверяет флаг первого запуска и количество инстансов.
2. Кнопка запуска вызывает `POST /api/v1/auto-setup/start`. Сервер проверяет наличие Playwright/Chromium/noVNC и сохраняет текущий шаг в setting `auto_setup_state`.
3. Компоненты устанавливаются обычным endpoint’ом `/api/v1/automation/components/install`; прогресс wizard получает через `GET /api/v1/auto-setup/progress`.
4. Для входа и создания комнат wizard открывает существующую automation-сессию (`/api/v1/automation/wbstream/session` или `/api/v1/automation/telemost/session`). Login, CAPTCHA и подтверждение выполняются оператором в noVNC.
5. Полученные WB Room ID и Telemost Room ID передаются в `POST /api/v1/auto-setup/complete`. Сервер создаёт экземпляры через `instance.Manager`, шифрует секреты тем же способом, что и обычный CRUD, и пытается запустить каждый экземпляр.
6. После завершения сервер записывает `first_run_completed=true`. Ошибки отдельных экземпляров сохраняются в поле `error`, остальные экземпляры всё равно обрабатываются.

Telemost можно пропустить endpoint’ом `POST /api/v1/auto-setup/skip-telemost`. В этом случае создаются только WB-инстансы. Если Playwright недоступен (например, `arm64`), Room ID можно ввести вручную в wizard и завершить настройку без browser automation.

## Состояние

`auto_setup_state` — обычный JSON setting (не секрет). Его структура:

```json
{
  "step": "wb_rooms_create",
  "progress": 65,
  "message": "Создайте комнаты WB Stream через noVNC",
  "current_action": "Ожидание Room ID WB Stream",
  "completed_steps": ["playwright_check"],
  "created_instances": [],
  "skip_telemost": false,
  "wb_room_ids": [],
  "telemost_room_id": ""
}
```

Стандартные шаги: `welcome`, `playwright_check`, `playwright_install`, `wb_auth_prompt`, `wb_auth_vnc`, `telemost_prompt`, `telemost_auth_vnc`, `wb_rooms_create`, `telemost_room_create`, `creating_instances`, `starting_instances`, `completed` и `error`.

## Диагностика

- Проверить ответ status/progress и поле `error`.
- Проверить `/api/v1/automation/components/progress` и `/api/v1/automation/<provider>/session`.
- Состояние Playwright и сообщения worker доступны в разделе «Настройки → Автоматизация» и в журнале панели.
- При истёкшей VNC-сессии остановите её, запустите новую и повторите текущий шаг. Профиль Chromium сохраняется между запусками.
- Для возврата к началу используйте кнопку «Запустить wizard автонастройки» в настройках. Она сбрасывает только `first_run_completed` и `auto_setup_state`, не удаляя инстансы.

## API-ответы

Все mutating endpoints защищены обычной session cookie и CSRF-токеном панели. `start` возвращает `202 Accepted`, остальные операции возвращают JSON `AutoSetupState`. Ошибки используют стандартную форму панели `{ "error": { "code", "message", "request_id" } }`.

