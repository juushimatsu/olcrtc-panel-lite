const app = document.querySelector('#app');
const panelBase = document.querySelector('meta[name="olcrtc-panel-base"]')?.content || '/';
const subscriptionBase = (document.querySelector('meta[name="olcrtc-subscription-base"]')?.content || `${location.origin}/sub`).replace(/\/$/, '');

const state = {
  user: null,
  csrf: '',
  page: location.hash.replace('#', '') || 'dashboard',
  status: null,
  metrics: null,
  instances: [],
  subscriptions: [],
  settings: null,
  releases: null,
  automationSettings: { proxy_mode: 'direct', proxy_address: '', proxy_username: '', proxy_password_set: false },
  automationSession: { active: false, provider: 'wbstream', state: {} },
  autoSetup: { visible: false, state: null, poller: null, wbRooms: [], telemostRoom: '', skipTelemost: false },
  wbOperation: { state: 'idle', percent: 0 },
  updateOperation: { state: 'idle', percent: 0 },
  updateNoticeCatalog: null,
  updateNoticeLoading: false,
  updateNoticeDismissed: false,
  mirrorSyncRunning: false,
  settingsPolling: false,
  instancesPolling: false,
  expandedInstance: null,
  expandedSubscription: null,
  hideNetworkInfo: true,
  instanceFilters: { search: '', provider: '', transport: '', status: '', quota: '' },
  logsUnit: 'panel',
  logsLevel: '',
  logsPaused: false,
  poller: null,
  uptimeTicker: null,
};

const navItems = [
  ['dashboard', '◴', 'Дашборд'],
  ['instances', '♙', 'Инстансы'],
  ['subscriptions', '≋', 'Подписки'],
  ['settings', '⚙', 'Настройки'],
  ['logs', '⌁', 'Журнал'],
];

boot();

async function boot() {
  applyTheme(localStorage.getItem('olcrtc-theme') || 'dark');
  try {
    const me = await api('/api/v1/auth/me');
    state.user = me.username;
    state.csrf = me.csrf_token;
    await App();
  } catch (error) {
    renderLogin();
  }
}

// App is kept as a small entry point so the first-run check remains isolated
// from the regular dashboard navigation and can be reused after login.
async function App() {
  if (await maybeShowAutoSetup()) return;
  await navigate(state.page, false);
}

async function maybeShowAutoSetup() {
  try {
    const status = await api('/api/v1/auto-setup/status');
    if (!status.should_show) return false;
    state.autoSetup.visible = true;
    state.autoSetup.state = status.state || { step: 'welcome', progress: 0, completed_steps: [] };
    state.autoSetup.skipTelemost = Boolean(state.autoSetup.state.skip_telemost);
    state.autoSetup.wbRooms = [...(state.autoSetup.state.wb_room_ids || [])];
    state.autoSetup.telemostRoom = state.autoSetup.state.telemost_room_id || '';
    renderAutoSetupWizard();
    startAutoSetupPolling();
    return true;
  } catch (_) {
    // Older databases or a temporary API failure should not hide the panel.
    return false;
  }
}

const autoSetupStepOrder = ['welcome', 'playwright_check', 'playwright_install', 'wb_auth_prompt', 'wb_auth_vnc', 'telemost_prompt', 'telemost_auth_vnc', 'creating_instances', 'wb_rooms_create', 'telemost_room_create', 'starting_instances', 'completed'];

function AutoSetupWizard(payload = state.autoSetup.state || {}) {
  const current = payload || {};
  const progress = clamp(Number(current.progress) || 0, 0, 100);
  const title = autoSetupStepTitle(current.step || 'welcome');
  const content = autoSetupStepContent(current);
  const actions = autoSetupStepActions(current);
  const logs = autoSetupLogs(current);
  return `<main class="auto-setup-wizard" aria-labelledby="auto-setup-title"><header class="wizard-header"><div class="wizard-brand"><div class="brand-mark" aria-hidden="true">O</div><div><h1 id="auto-setup-title">olcRTC Panel Lite</h1><p>Автоматическая настройка первого запуска</p></div><strong class="wizard-percent">${Math.round(progress)}%</strong></div><div class="wizard-progress wizard-progress-bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${Math.round(progress)}"><span style="width:${progress}%"></span></div></header><div class="wizard-content wizard-content-wrap">${WizardStep({ step: current.step || 'welcome', title, content, actions })}</div>${ProgressLog({ logs })}</main>`;
}

function WizardStep({ step, title, content, actions }) {
  return `<section class="wizard-step" data-wizard-step="${attr(step)}"><p class="wizard-step-label">Шаг ${Math.max(1, autoSetupStepOrder.indexOf(step) + 1)} из ${autoSetupStepOrder.length}</p><h2>${esc(title)}</h2><div class="wizard-step-content">${content}</div><div class="wizard-actions">${actions || ''}</div></section>`;
}

function VNCPrompt({ provider, novncUrl, onComplete }) {
  const action = onComplete || (provider === 'Telemost' ? 'auto-setup-auth-telemost' : 'auto-setup-auth-wb');
  const targetURL = novncUrl || panelURL('/wb/novnc/vnc.html');
  return `<div class="vnc-prompt"><p>Откройте noVNC в новой вкладке и войдите в ${esc(provider)}.</p><div class="wizard-vnc-actions"><button class="btn btn-primary" data-action="auto-setup-open-vnc" data-url="${attr(targetURL)}">Открыть noVNC</button><button class="btn" data-action="${attr(action)}">Продолжить после входа</button></div><p class="warning">После входа вернитесь на эту страницу и подтвердите продолжение.</p></div>`;
}

function openAutoSetupVNC(novncUrl) { const popup = window.open(novncUrl, '_blank'); if (popup) popup.opener = null; }

function ProgressLog({ logs = [] }) {
  return `<section class="auto-setup-log" aria-live="polite"><h4>Лог выполнения</h4><div class="log-entries">${logs.length ? logs.map(entry => `<div class="log-entry"><time class="log-timestamp">${esc(entry.timestamp || '')}</time><span class="log-message">${esc(entry.message || '')}</span></div>`).join('') : '<div class="log-entry"><span class="log-message">Ожидание запуска...</span></div>'}</div></section>`;
}

function ProgressBar(value = 0) {
  const progress = clamp(Number(value) || 0, 0, 100);
  return `<div class="progress wizard-inline-progress" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${Math.round(progress)}"><span style="width:${progress}%"></span></div>`;
}

function autoSetupStepTitle(step) {
  return ({
    welcome: 'Добро пожаловать',
    playwright_check: 'Проверка компонентов автоматизации',
    playwright_install: 'Установка Playwright и Chromium',
    wb_auth_prompt: 'Вход в WB Stream',
    wb_auth_vnc: 'Подтвердите вход в WB Stream',
    telemost_prompt: 'Инстанс Telemost',
    telemost_auth_vnc: 'Вход в Telemost',
    wb_rooms_create: 'Создание комнат WB Stream',
    telemost_room_create: 'Создание комнаты Telemost',
    creating_instances: 'Создание инстансов',
    starting_instances: 'Запуск инстансов',
    completed: 'Настройка завершена',
    dismissed: 'Автонастройка пропущена',
    error: 'Автонастройка остановлена',
  })[step] || 'Автоматическая настройка';
}

function autoSetupStepContent(current) {
  const step = current.step || 'welcome';
  if (step === 'welcome') return '<p>Панель еще не настроена. Wizard поможет установить automation-компоненты и подготовить инстансы.</p><ul><li>до 3 инстансов WB Stream (60 и 120 fps)</li><li>опциональный инстанс Telemost</li></ul><p class="field-hint">Вход и CAPTCHA выполняются вручную в авторизованном окне noVNC.</p>';
  if (step === 'playwright_check') return `<p>${esc(current.message || 'Проверяем наличие Playwright, Chromium и noVNC...')}</p>${ProgressBar(current.progress || 5)}`;
  if (step === 'playwright_install') return `<p>${esc(current.message || 'Компоненты нужны для автоматического создания комнат.')}</p><p class="field-hint">Загрузка может занимать несколько минут и требует Linux/amd64.</p>${ProgressBar(current.progress || 12)}`;
  if (step === 'wb_auth_prompt' || step === 'wb_auth_vnc') return `${VNCPrompt({ provider: 'WB Stream', onComplete: 'auto-setup-auth-wb' })}<div class="manual-room-block"><label class="field-label" for="auto-setup-wb-room-1">Room ID (если noVNC недоступен)</label><input class="input" id="auto-setup-wb-room-1" data-auto-wb-room="0" placeholder="WB Room ID" value="${attr(state.autoSetup.wbRooms[0] || '')}"></div>`;
  if (step === 'telemost_prompt' || step === 'telemost_auth_vnc') return `<p>Создать комнату Telemost сейчас?</p>${VNCPrompt({ provider: 'Telemost', onComplete: 'auto-setup-auth-telemost' })}`;
  if (step === 'wb_rooms_create') {
    const rows = [0, 1, 2].map(index => `<div class="field"><label for="auto-setup-wb-room-${index + 1}">WB Stream комната ${index + 1}${index === 2 ? ' (120 fps)' : ''}</label><input class="input" id="auto-setup-wb-room-${index + 1}" data-auto-wb-room="${index}" value="${attr(state.autoSetup.wbRooms[index] || '')}" placeholder="Room ID"></div>`).join('');
    return `<p>${esc(current.message || 'Создайте комнаты по очереди через noVNC или укажите их идентификаторы вручную.')}</p><div class="auto-setup-room-grid">${rows}</div>`;
  }
  if (step === 'telemost_room_create') return `<div class="field"><label for="auto-setup-telemost-room">Telemost Room ID</label><input class="input" id="auto-setup-telemost-room" data-auto-telemost-room value="${attr(state.autoSetup.telemostRoom)}" placeholder="14 цифр"></div>`;
  if (step === 'creating_instances' || step === 'starting_instances') return `<p>${esc(current.message || 'Сохраняем конфигурации и запускаем сервисы...')}</p>${ProgressBar(current.progress || 70)}`;
  if (step === 'completed') return `<p>Создано и запущено инстансов: <strong>${(current.created_instances || []).length}</strong>.</p><ul><li># WB 1⚡ (60 fps)</li><li># WB 2⚡ (60 fps)</li><li># WB 3🚀 (120 fps)</li>${!current.skip_telemost ? '<li># TLM 1🟡</li>' : ''}</ul>${current.error ? `<div class="notice">${esc(current.error)}</div>` : '<p class="success-text">Все выбранные инстансы готовы к работе.</p>'}`;
  if (step === 'dismissed') return '<p>Wizard пропущен. Инстансы можно настроить вручную в разделе «Инстансы».</p>';
  return `<div class="notice">${esc(current.error || current.message || 'Неизвестная ошибка')}</div>`;
}

function autoSetupStepActions(current) {
  const step = current.step || 'welcome';
  if (step === 'welcome') return '<button class="btn btn-primary" data-action="auto-setup-start">Начать настройку</button><button class="btn btn-ghost" data-action="auto-setup-dismiss">Пропустить</button>';
  if (step === 'playwright_check') return '<span class="field-hint">Проверка...</span>';
  if (step === 'playwright_install') return '<button class="btn btn-primary" data-action="auto-setup-install">Установить компоненты</button><button class="btn btn-ghost" data-action="auto-setup-manual">Продолжить вручную</button>';
  if (step === 'wb_auth_prompt' || step === 'wb_auth_vnc') return '<button class="btn btn-primary" data-action="auto-setup-auth-wb">Открыть WB Stream</button><button class="btn btn-ghost" data-action="auto-setup-to-rooms">Ввести Room ID вручную</button>';
  if (step === 'telemost_prompt' || step === 'telemost_auth_vnc') return '<button class="btn btn-primary" data-action="auto-setup-auth-telemost">Создать Telemost</button><button class="btn btn-ghost" data-action="auto-setup-skip-telemost">Пропустить Telemost</button>';
  if (step === 'wb_rooms_create') return '<button class="btn btn-primary" data-action="auto-setup-create-wb-room">Создать комнаты WB</button><button class="btn btn-ghost" data-action="auto-setup-complete">Продолжить</button>';
  if (step === 'telemost_room_create') return '<button class="btn btn-primary" data-action="auto-setup-complete">Создать инстансы</button><button class="btn btn-ghost" data-action="auto-setup-skip-telemost">Пропустить Telemost</button>';
  if (step === 'creating_instances' || step === 'starting_instances') return '<span class="field-hint">Выполняется...</span>';
  if (step === 'completed' || step === 'dismissed') return '<button class="btn btn-primary" data-action="auto-setup-dashboard">Перейти к панели</button>';
  return '<button class="btn btn-primary" data-action="auto-setup-retry">Повторить</button><button class="btn btn-ghost" data-action="auto-setup-dismiss">Настроить вручную</button>';
}

function autoSetupLogs(current) {
  const completed = current.completed_steps || [];
  const logs = completed.map(step => ({ timestamp: '✓', message: autoSetupStepTitle(step) }));
  if (current.current_action || current.message) logs.push({ timestamp: '→', message: current.current_action || current.message });
  if (current.error) logs.push({ timestamp: '!', message: current.error });
  return logs;
}

function renderAutoSetupWizard() {
  if (!state.autoSetup.visible) return;
  const root = document.querySelector('#app');
  if (root) root.innerHTML = AutoSetupWizard(state.autoSetup.state || {});
}

function startAutoSetupPolling() {
  if (state.autoSetup.poller) clearInterval(state.autoSetup.poller);
  state.autoSetup.poller = setInterval(fetchProgress, 2000);
}

function fetchProgress() { return fetchAutoSetupProgress(); }

function stopAutoSetupPolling() {
  if (state.autoSetup.poller) clearInterval(state.autoSetup.poller);
  state.autoSetup.poller = null;
}

async function fetchAutoSetupProgress() {
  if (!state.autoSetup.visible) return;
  try {
    const payload = await api('/api/v1/auto-setup/progress');
    state.autoSetup.state = payload;
    state.autoSetup.skipTelemost = Boolean(payload.skip_telemost);
    state.autoSetup.wbRooms = [...(payload.wb_room_ids || state.autoSetup.wbRooms || [])];
    state.autoSetup.telemostRoom = payload.telemost_room_id || state.autoSetup.telemostRoom || '';
    renderAutoSetupWizard();
    if (['completed', 'dismissed'].includes(payload.step)) stopAutoSetupPolling();
  } catch (_) {}
}

async function startAutoSetup(restart = false) {
  const payload = await api('/api/v1/auto-setup/start', { method: 'POST', body: JSON.stringify({ skip_telemost: false, restart }) });
  state.autoSetup.state = payload;
  state.autoSetup.visible = true;
  renderAutoSetupWizard();
  startAutoSetupPolling();
}

async function installAutoSetupComponents() {
  const operation = await api('/api/v1/automation/components/install', { method: 'POST' });
  state.autoSetup.state = { ...(state.autoSetup.state || {}), step: 'playwright_install', progress: operation.percent || 12, message: 'Установка компонентов...', current_action: 'Playwright и Chromium' };
  renderAutoSetupWizard();
}

async function runAutoSetupProvider(provider) {
  const current = await runAutomationSession(provider, 'create');
  const room = normalizeRoomID(provider, current.state?.room_id || '');
  if (provider === 'telemost') {
    state.autoSetup.telemostRoom = room;
    state.autoSetup.state = { ...(state.autoSetup.state || {}), step: 'telemost_room_create', progress: 60, telemost_room_id: room, current_action: 'Telemost Room ID получен' };
  } else {
    if (room && !state.autoSetup.wbRooms.includes(room)) state.autoSetup.wbRooms = [...state.autoSetup.wbRooms, room].slice(0, 3);
    const needsTelemost = state.autoSetup.wbRooms.length >= 3 && !state.autoSetup.skipTelemost && !state.autoSetup.telemostRoom;
    state.autoSetup.state = { ...(state.autoSetup.state || {}), step: needsTelemost ? 'telemost_prompt' : state.autoSetup.wbRooms.length >= 3 ? 'creating_instances' : 'wb_rooms_create', progress: needsTelemost ? 80 : state.autoSetup.wbRooms.length >= 3 ? 75 : 65, wb_room_ids: state.autoSetup.wbRooms, current_action: 'WB Room ID получен' };
  }
  await persistAutoSetupDraft();
  renderAutoSetupWizard();
}

async function persistAutoSetupDraft() {
  try {
    const current = state.autoSetup.state || {};
    await api('/api/v1/auto-setup/start', { method: 'POST', body: JSON.stringify({ wb_room_ids: state.autoSetup.wbRooms, telemost_room_id: state.autoSetup.telemostRoom, skip_telemost: state.autoSetup.skipTelemost, step: current.step || '', progress: current.progress || 0, current_action: current.current_action || '' }) });
  } catch (_) {}
}

function collectAutoSetupRooms() {
  const values = [...document.querySelectorAll('[data-auto-wb-room]')].map(input => input.value.trim()).filter(Boolean);
  if (values.length) state.autoSetup.wbRooms = [...new Set(values)].slice(0, 3);
  const telemost = document.querySelector('[data-auto-telemost-room]');
  if (telemost?.value.trim()) state.autoSetup.telemostRoom = normalizeRoomID('telemost', telemost.value.trim());
}

async function completeAutoSetup() {
  collectAutoSetupRooms();
  // Merge manual input with server-side captured rooms (server has priority if fields are empty)
  const wbRooms = state.autoSetup.wbRooms.length > 0 ? state.autoSetup.wbRooms : (state.autoSetup.state?.wb_room_ids || []);
  const telemostRoom = state.autoSetup.skipTelemost ? '' : (state.autoSetup.telemostRoom || state.autoSetup.state?.telemost_room_id || '');
  const payload = await api('/api/v1/auto-setup/complete', { method: 'POST', body: JSON.stringify({ wb_room_ids: wbRooms, telemost_room_id: telemostRoom, skip_telemost: state.autoSetup.skipTelemost }) });
  state.autoSetup.state = payload;
  renderAutoSetupWizard();
  if (payload.step === 'completed') stopAutoSetupPolling();
}

async function skipAutoSetupTelemost() {
  const payload = await api('/api/v1/auto-setup/skip-telemost', { method: 'POST' });
  state.autoSetup.skipTelemost = true;
  state.autoSetup.state = payload;
  renderAutoSetupWizard();
}

async function dismissAutoSetup() {
  const payload = await api('/api/v1/auto-setup/dismiss', { method: 'POST' });
  state.autoSetup.state = payload;
  state.autoSetup.visible = false;
  stopAutoSetupPolling();
  await navigate('dashboard');
}

async function finishAutoSetupUI() {
  state.autoSetup.visible = false;
  stopAutoSetupPolling();
  await navigate('dashboard');
}

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json');
  if (state.csrf && options.method && !['GET', 'HEAD'].includes(options.method)) headers.set('X-CSRF-Token', state.csrf);
  const response = await fetch(panelURL(path), { credentials: 'same-origin', ...options, headers });
  if (response.status === 204) return null;
  const contentType = response.headers.get('content-type') || '';
  const payload = contentType.includes('application/json') ? await response.json() : await response.text();
  if (!response.ok) {
    const message = payload?.error?.message || payload || `HTTP ${response.status}`;
    const error = new Error(message);
    error.status = response.status;
    error.code = payload?.error?.code;
    if (response.status === 401 && state.user) {
      state.user = null;
      renderLogin();
    }
    throw error;
  }
  return payload;
}

function panelURL(path = '/') {
  if (/^https?:\/\//i.test(path)) return path;
  const route = path.startsWith('/') ? path : `/${path}`;
  if (panelBase === '/') return route;
  return `${panelBase.replace(/\/$/, '')}${route}`;
}

function subscriptionURL(slug, suffix = '') {
  return `${subscriptionBase}/${encodeURIComponent(slug)}${suffix}`;
}

function renderLogin(message = '') {
  stopPolling();
  document.body.classList.remove('drawer-open');
  app.innerHTML = `
    <main class="login-screen">
      <section class="login-card" aria-labelledby="login-title">
        <div class="login-brand">
          <div class="brand-mark" aria-hidden="true">O</div>
          <div><h1 id="login-title">olcRTC Panel Lite</h1><p>Защищённая серверная панель</p></div>
        </div>
        ${message ? `<div class="notice">${esc(message)}</div>` : ''}
        <form data-form="login" class="stack" autocomplete="on">
          <div class="field"><label for="login-username">Логин</label><input class="input" id="login-username" name="username" autocomplete="username" required autofocus></div>
          <div class="field"><label for="login-password">Пароль</label><input class="input" id="login-password" name="password" type="password" autocomplete="current-password" required></div>
          <button class="btn btn-primary" type="submit">Войти</button>
        </form>
        <p class="login-note">Соединение использует HTTPS и private CA панели.</p>
      </section>
    </main>
    <div class="toast-region" aria-live="polite"></div>`;
}

function shell(content) {
  return `
    <div class="app-shell">
      <aside class="sidebar" aria-label="Основная навигация">
        <div class="sidebar-brand"><div class="brand-mark">O</div><div><strong>olcRTC Panel</strong><span>lite edition</span></div></div>
        <nav class="nav">${navItems.map(([id, icon, label]) => `<button class="nav-button ${state.page === id ? 'active' : ''}" data-page="${id}"><span class="nav-icon">${icon}</span>${label}</button>`).join('')}</nav>
        <div class="sidebar-bottom">
          <a class="nav-button" href="https://github.com/juushimatsu/olcrtc-panel-lite" target="_blank" rel="noopener noreferrer"><span class="nav-icon" aria-hidden="true">↗</span>GitHub панели</a>
          <button class="nav-button" data-action="toggle-theme"><span class="nav-icon">◐</span>Сменить тему</button>
          <button class="nav-button" data-action="logout"><span class="nav-icon">↪</span>Выход</button>
          <div class="sidebar-version">Пользователь: ${esc(state.user || '')}</div>
        </div>
      </aside>
      <div>
        <header class="mobile-topbar"><button class="icon-button" data-action="drawer" aria-label="Открыть меню">☰</button><strong>olcRTC Panel</strong><button class="icon-button" data-action="toggle-theme" aria-label="Сменить тему">◐</button></header>
        <main class="main"><div id="update-notice" aria-live="polite"></div><div id="page-content">${content}</div></main>
      </div>
    </div>
    <div id="modal-root"></div>
    <div class="toast-region" aria-live="polite"></div>`;
}

async function navigate(page, push = true) {
  if (!navItems.some(([id]) => id === page)) page = 'dashboard';
  if (state.autoSetup.visible) { state.autoSetup.visible = false; stopAutoSetupPolling(); }
  stopPolling();
  state.page = page;
  if (push) location.hash = page;
  document.body.classList.remove('drawer-open');
  app.innerHTML = shell(pageSkeleton(page));
  try {
    if (page === 'dashboard') await loadDashboard();
    if (page === 'instances') await loadInstances();
    if (page === 'subscriptions') await loadSubscriptions();
    if (page === 'settings') await loadSettings();
    if (page === 'logs') await loadLogsPage();
  } catch (error) {
    renderPageError(error);
  }
  loadUpdateNotice();
}

function latestAvailableRelease(catalog = state.updateNoticeCatalog) {
  if (!catalog?.configured || catalog.error) return null;
  const items = [...(catalog.items || [])].sort((a, b) => new Date(b.published_at) - new Date(a.published_at));
  const latest = items.find(item => item.latest) || items[0];
  return latest && !latest.current ? latest : null;
}

async function loadUpdateNotice() {
  const root = document.querySelector('#update-notice');
  if (!root || state.updateNoticeLoading) return;
  if (state.updateNoticeCatalog) {
    renderUpdateNotice();
    return;
  }
  state.updateNoticeLoading = true;
  try {
    state.updateNoticeCatalog = await api('/api/v1/updates/releases');
  } catch (_) {
    state.updateNoticeCatalog = { configured: false, items: [] };
  } finally {
    state.updateNoticeLoading = false;
    renderUpdateNotice();
  }
}

function renderUpdateNotice() {
  const root = document.querySelector('#update-notice');
  if (!root) return;
  const release = latestAvailableRelease();
  if (state.updateNoticeDismissed || !release) {
    root.innerHTML = '';
    return;
  }
  const published = release.published_at ? formatDate(release.published_at) : '';
  root.innerHTML = `<aside class="update-notice" role="status"><div class="update-notice-copy"><strong>Доступно обновление</strong><span class="mono">${esc(release.bundle_id || release.name || 'новый release')}${published ? ` · ${esc(published)}` : ''}</span></div><div class="update-notice-actions"><button class="btn btn-small" data-action="update-notice-open">Подробнее</button><button class="btn btn-icon update-notice-dismiss" data-action="dismiss-update-notice" aria-label="Скрыть уведомление" title="Скрыть уведомление">×</button></div></aside>`;
}

function pageSkeleton(page) {
  const titles = { dashboard: 'Дашборд', instances: 'Инстансы', subscriptions: 'Подписки', settings: 'Настройки', logs: 'Журнал' };
  return `<section class="page"><div class="page-header"><div class="page-title"><h1>${titles[page]}</h1><p>Получение актуальных данных...</p></div></div><div class="panel panel-body stack"><div class="skeleton" style="width:35%"></div><div class="skeleton" style="height:160px"></div></div></section>`;
}

function renderPageError(error) {
  document.querySelector('#page-content').innerHTML = `<section class="page"><div class="empty-state panel"><div class="empty-icon">!</div><h3>Не удалось загрузить страницу</h3><p>${esc(error.message)}</p><button class="btn btn-primary" data-page="${state.page}">Повторить</button></div></section>`;
}

async function loadDashboard() {
  [state.status, state.metrics] = await Promise.all([api('/api/v1/system/status'), api('/api/v1/system/metrics')]);
  renderDashboard();
  state.poller = setInterval(async () => {
    if (state.page !== 'dashboard') return;
    try { [state.status, state.metrics] = await Promise.all([api('/api/v1/system/status'), api('/api/v1/system/metrics')]); renderDashboard(false); } catch (_) {}
  }, 5000);
}

function renderDashboard(rebuild = true) {
  const s = state.status;
  const m = state.metrics;
  const memPct = percent(m.memory_used, m.memory_total);
  const swapPct = percent(m.swap_used, m.swap_total);
  const diskPct = percent(m.disk_used, m.disk_total);
  const cpuPct = clamp(m.cpu_percent || (m.load_1 / Math.max(m.cpu_cores, 1) * 100), 0, 100);
  const body = `
    <section class="page">
      <div class="page-header"><div class="page-title"><h1>Дашборд</h1><p>Состояние VPS, панели и управляемых процессов</p></div><div class="header-actions">${networkVisibilityButton()}<button class="btn" data-action="refresh-dashboard">↻ Обновить</button><button class="btn btn-primary" data-action="create-instance">＋ Инстанс</button></div></div>
      <div class="panel gauge-grid">
        ${gauge(cpuPct, `ЦП: ${m.cpu_cores} ${plural(m.cpu_cores, 'ядро', 'ядра', 'ядер')}`)}
        ${gauge(memPct, `ОЗУ: ${formatBytes(m.memory_used)} / ${formatBytes(m.memory_total)}`)}
        ${gauge(swapPct, `Swap: ${formatBytes(m.swap_used)} / ${formatBytes(m.swap_total)}`)}
        ${gauge(diskPct, `Диск: ${formatBytes(m.disk_used)} / ${formatBytes(m.disk_total)}`)}
      </div>
      <div class="dashboard-grid">
        <article class="panel"><div class="panel-header"><h2>olcRTC Panel</h2><span><span class="status-dot running"></span>Запущена</span></div><div class="panel-body detail-list">
          ${detail('Версия панели', s.panel_version || 'dev')}${detail('Upstream SHA', shortSHA(s.upstream_sha))}${detail('Uptime панели', formatUptime(s.panel_uptime_seconds))}${detail('Uptime ОС', formatUptime(m.os_uptime_seconds))}
        </div></article>
        <article class="panel"><div class="panel-header"><h2>Инстансы</h2><button class="btn btn-ghost btn-small" data-page="instances">Открыть →</button></div><div class="panel-body detail-list">
          ${detail('Запущено', s.instances.running || 0, 'success-text')}${detail('Остановлено', s.instances.stopped || 0)}${detail('Ошибки', s.instances.failed || 0, s.instances.failed ? 'danger-text' : '')}${detail('Неизвестно', s.instances.unknown || 0)}
        </div></article>
        <article class="panel"><div class="panel-header"><h2>Точная статистика payload</h2><span class="chip green">journald</span></div><div class="panel-body detail-list">
          ${detail('Отправлено', formatBytes(s.traffic.upload_bytes))}${detail('Получено', formatBytes(s.traffic.download_bytes))}${detail('Всего', formatBytes(s.traffic.total_bytes))}${detail('Сетевая скорость с WebRTC overhead', `↑ ${formatBytes(s.network_speed?.egress_bytes_per_second || 0)}/s · ↓ ${formatBytes(s.network_speed?.ingress_bytes_per_second || 0)}/s`)}
        </div></article>
        <article class="panel"><div class="panel-header"><h2>Безопасность и интеграции</h2></div><div class="panel-body detail-list">
          ${detail('Публичный адрес', state.hideNetworkInfo ? '••••••' : `${s.public_ip || 'не задан'}:${s.public_port}`)}${detail('TLS fingerprint', shortFingerprint(s.certificate_fingerprint))}${detail('WB automation', s.wb.installed ? 'Установлена' : (s.wb.supported ? 'Не установлена' : 'Недоступна'))}${detail('Обновления', s.update_configured ? 'Настроены' : 'Manifest не задан')}
        </div></article>
        <article class="panel" style="grid-column:1/-1"><div class="panel-header"><h2>Быстрые действия</h2></div><div class="panel-body quick-actions"><button class="btn btn-primary" data-action="create-instance">＋ Создать инстанс</button><button class="btn" data-page="subscriptions">≋ Подписки</button><button class="btn" data-page="logs">⌁ Открыть журнал</button><button class="btn" data-action="create-backup">▣ Создать backup</button><button class="btn" data-action="check-updates">↻ Проверить обновления</button><button class="btn" data-page="settings">⚙ Настройки</button></div></article>
      </div>
    </section>`;
  if (rebuild || document.querySelector('.gauge-grid')) document.querySelector('#page-content').innerHTML = body;
}

function gauge(value, caption) {
  return `<div class="gauge-item"><div class="gauge" style="--value:${value.toFixed(1)}"><strong>${value.toFixed(1)}%</strong></div><div class="gauge-caption">${esc(caption)}</div></div>`;
}

function networkVisibilityButton() {
  const label = state.hideNetworkInfo ? 'Показать IP и Room ID' : 'Скрыть IP и Room ID';
  return `<button class="btn btn-icon" data-action="toggle-network-info" title="${attr(label)}" aria-label="${attr(label)}" aria-pressed="${state.hideNetworkInfo ? 'false' : 'true'}">&#128065;</button>`;
}

async function loadInstances() {
  await refreshInstances();
  if (state.page !== 'instances') return;
  if (!state.poller) state.poller = setInterval(() => refreshInstances().catch(() => {}), 7000);
  if (!state.uptimeTicker) state.uptimeTicker = setInterval(tickInstanceUptimes, 1000);
}

async function refreshInstances() {
  if (state.instancesPolling) return;
  state.instancesPolling = true;
  try {
    const result = await api('/api/v1/instances');
    if (state.page !== 'instances') return;
    state.instances = mergeInstanceSnapshots(result.items || []);
    renderInstances();
  } finally {
    state.instancesPolling = false;
  }
}

function mergeInstanceSnapshots(items) {
  const previous = new Map(state.instances.map(item => [item.id, item]));
  return items.map(item => {
    const old = previous.get(item.id);
    const runtimeChanged = !!old && !sameRuntimeIdentity(old, item);
    return { ...item, runtime_changed: runtimeChanged };
  });
}

function sameRuntimeIdentity(left, right) {
  return String(left?.invocation_id || '') === String(right?.invocation_id || '')
    && Number(left?.main_pid || 0) === Number(right?.main_pid || 0)
    && Number(left?.restart_count || 0) === Number(right?.restart_count || 0);
}

function currentInstanceUptime(item, field = 'uptime_seconds', now = Date.now()) {
  if (item?.status !== 'running') return 0;
  const base = Math.max(0, Math.floor(Number(item?.[field]) || 0));
  const source = field === 'process_uptime_seconds' ? item?.process_uptime_source : item?.uptime_source;
  const tickable = field === 'process_uptime_seconds' ? source === 'exec_main_start_monotonic' : source === 'active_enter_monotonic' || source === 'active_enter_usec';
  if (!tickable) return base;
  const observedAt = Date.parse(item?.observed_at || '');
  if (!Number.isFinite(observedAt)) return base;
  return base + Math.max(0, Math.floor((now - observedAt) / 1000));
}

function tickInstanceUptimes() {
  if (state.page !== 'instances') return;
  const byID = new Map(state.instances.map(item => [String(item.id), item]));
  document.querySelectorAll('[data-instance-uptime]').forEach(root => {
    const item = byID.get(root.dataset.instanceUptime);
    if (item) root.textContent = formatUptime(currentInstanceUptime(item));
  });
  document.querySelectorAll('[data-instance-process-uptime]').forEach(root => {
    const item = byID.get(root.dataset.instanceProcessUptime);
    if (item) root.textContent = formatUptime(currentInstanceUptime(item, 'process_uptime_seconds'));
  });
}

function renderInstances() {
  const f = state.instanceFilters;
  const filtered = state.instances.filter(item => {
    const search = f.search.toLowerCase();
    const quotaState = item.expires_at && new Date(item.expires_at) < new Date() ? 'expired' : item.quota_bytes && item.total_bytes >= item.quota_bytes ? 'exceeded' : item.quota_bytes ? 'limited' : 'unlimited';
    return (!search || `${item.id} ${item.name} ${item.room_id}`.toLowerCase().includes(search)) && (!f.provider || item.provider === f.provider) && (!f.transport || item.transport === f.transport) && (!f.status || item.status === f.status) && (!f.quota || quotaState === f.quota);
  });
  const running = state.instances.filter(i => i.status === 'running').length;
  const failed = state.instances.filter(i => i.status === 'failed').length;
  const upload = sum(state.instances, 'upload_bytes');
  const download = sum(state.instances, 'download_bytes');
  document.querySelector('#page-content').innerHTML = `
    <section class="page">
      <div class="page-header"><div class="page-title"><h1>Инстансы</h1><p>Один официальный olcRTC process и YAML на каждый инстанс</p></div><div class="header-actions">${networkVisibilityButton()}<button class="btn" data-action="start-all-instances">▶ Запустить все</button><button class="btn" data-action="stop-all-instances">■ Остановить все</button><button class="btn" data-action="refresh-instances">↻ Обновить</button><button class="btn btn-primary" data-action="create-instance">＋ Создать инстанс</button></div></div>
      <div class="summary-grid">
        ${summary('Отправлено', formatBytes(upload))}${summary('Получено', formatBytes(download))}${summary('Всего трафика', formatBytes(upload + download))}${summary('Запущено', running)}${summary('Ошибки', failed)}${summary('Всего', state.instances.length)}
      </div>
      <section class="panel">
        <div class="toolbar compact"><div class="filters"><div class="search"><input class="input" data-filter="search" placeholder="Поиск" value="${attr(f.search)}" aria-label="Поиск инстансов"></div><select class="select" data-filter="provider"><option value="">Все provider</option>${options(['jitsi','telemost','wbstream'], f.provider)}</select><select class="select" data-filter="transport"><option value="">Все transport</option>${options(['datachannel','vp8channel','seichannel','videochannel'], f.transport)}</select><select class="select" data-filter="status"><option value="">Все статусы</option>${options(['running','stopped','failed','unknown'], f.status)}</select><select class="select" data-filter="quota"><option value="">Любая quota</option>${options(['unlimited','limited','exceeded','expired'],f.quota)}</select></div><span class="muted" style="font-size:12px">Найдено: ${filtered.length}</span></div>
        ${filtered.length ? instanceTable(filtered) : emptyState('◎', 'Инстансы не найдены', state.instances.length ? 'Измените параметры фильтра.' : 'Создайте первый инстанс. Автоматически после установки он не создаётся.', '<button class="btn btn-primary" data-action="create-instance">Создать инстанс</button>')}
      </section>
    </section>`;
}

function instanceTable(items) {
  return `<div class="table-wrap"><table class="table"><thead><tr><th></th><th>ID</th><th>Имя</th><th>Provider</th><th>Transport</th><th>Room ID</th><th>Статус</th><th>Uptime</th><th>Трафик</th><th>Quota / срок</th><th></th></tr></thead><tbody>${items.map(item => `${instanceRow(item)}${state.expandedInstance === item.id ? instanceExpanded(item) : ''}`).join('')}</tbody></table></div>`;
}

function instanceRow(item) {
  const quotaPct = item.quota_bytes ? percent(item.total_bytes, item.quota_bytes) : 0;
  const tokenBadge = item.provider === 'wbstream' && item.auth_token_expired ? ' <span class="chip red" title="Обновите token через Playwright">token истёк</span>' : '';
  const runtimeBadge = item.runtime_changed ? ' <span class="chip blue" title="Изменились Invocation ID, Main PID или счётчик рестартов">перезапущен</span>' : '';
  return `<tr><td><button class="expand-button" data-action="expand-instance" data-id="${item.id}" aria-label="Раскрыть ${attr(item.name)}">${state.expandedInstance === item.id ? '−' : '+'}</button></td><td>${item.id}</td><td><strong>${esc(item.name)}</strong>${tokenBadge}${runtimeBadge}</td><td><span class="chip ${item.provider === 'wbstream' ? 'purple' : 'green'}">${esc(item.provider)}</span></td><td><span class="chip blue">${esc(item.transport)}</span></td><td class="mono truncate" style="max-width:190px" title="${state.hideNetworkInfo ? '' : attr(item.room_id)}">${state.hideNetworkInfo ? '••••••' : esc(item.room_id)}</td><td><span class="chip ${esc(item.status)}">${statusLabel(item.status)}</span></td><td><span data-instance-uptime="${item.id}" title="${attr(item.uptime_source || 'unavailable')}">${formatUptime(currentInstanceUptime(item))}</span></td><td class="traffic-cell"><div class="traffic-value"><span>${formatBytes(item.total_bytes)}</span><span>${item.quota_bytes ? `${quotaPct.toFixed(0)}%` : '∞'}</span></div><div class="progress"><span style="width:${Math.min(quotaPct,100)}%"></span></div></td><td>${quotaLabel(item)}</td><td><button class="btn btn-ghost btn-icon" data-action="expand-instance" data-id="${item.id}" aria-label="Меню">⋮</button></td></tr>`;
}

function instanceExpanded(item) {
  const clientUnavailable = clientQRUnavailable(item);
  const tokenStatus = item.provider !== 'wbstream' ? 'Не требуется' : !item.auth_token_set ? 'Не задан' : item.auth_token_expired ? `Истёк${item.auth_token_expires_at ? ' · '+formatDate(item.auth_token_expires_at) : ''}` : item.auth_token_expires_at ? `Действует до ${formatDate(item.auth_token_expires_at)}` : 'Задан, срок неизвестен';
  return `<tr class="expanded-row"><td colspan="11"><div class="expanded-content"><div class="expanded-actions">
    ${item.status === 'running' ? `<button class="btn btn-small" data-action="instance-stop" data-id="${item.id}">■ Остановить</button><button class="btn btn-small" data-action="instance-restart" data-id="${item.id}">↻ Перезапустить</button>` : `<button class="btn btn-primary btn-small" data-action="instance-start" data-id="${item.id}">▶ Запустить</button>`}
    <button class="btn btn-small" data-action="edit-instance" data-id="${item.id}">✎ Изменить</button><button class="btn btn-small" data-action="instance-qr" data-id="${item.id}" data-format="olcbox">QR OLCBOX</button><button class="btn btn-small" data-action="instance-qr" data-id="${item.id}" data-format="client" ${clientUnavailable ? `disabled title="${attr(clientUnavailable)}"` : 'title="QR содержит данные подключения; для WB — полный auth token"'}>QR OLCRTC Client</button><button class="btn btn-small" data-action="instance-rotate-key" data-id="${item.id}">⌘ Ротация key</button><button class="btn btn-small" data-action="instance-rotate-client-id" data-id="${item.id}">⟳ Ротация client_id</button>${item.provider === 'wbstream' ? `<button class="btn btn-small" data-action="wb-playwright-refresh">↻ Обновить WB token</button>` : ''}<button class="btn btn-small" data-action="instance-change-room" data-id="${item.id}">⌂ Room ID</button><button class="btn btn-small" data-action="instance-diagnostics" data-id="${item.id}">◇ Диагностика</button><button class="btn btn-small" data-action="instance-logs" data-id="${item.id}">⌁ Логи</button><button class="btn btn-small" data-action="instance-reset-traffic" data-id="${item.id}">↺ Сбросить трафик</button><button class="btn btn-danger btn-small" data-action="instance-delete" data-id="${item.id}">Удалить</button>
  </div>${clientUnavailable ? `<div class="notice" style="margin-bottom:14px">QR OLCRTC Client недоступен: ${esc(clientUnavailable)}. QR OLCBOX остаётся доступным.</div>` : item.provider === 'wbstream' ? '<div class="notice" style="margin-bottom:14px">QR OLCRTC Client содержит полный WB auth token. Считайте этот QR credential и передавайте только получателю.</div>' : ''}<div class="expanded-stats">${detail('Client ID', item.client_id, 'mono')}${detail('WB auth token', tokenStatus, item.auth_token_expired ? 'danger-text' : '')}${detail('Uptime сервиса', `<span data-instance-uptime="${item.id}">${formatUptime(currentInstanceUptime(item))}</span>`)}${detail('Uptime процесса', `<span data-instance-process-uptime="${item.id}" title="${attr(item.process_uptime_source || 'unavailable')}">${formatUptime(currentInstanceUptime(item,'process_uptime_seconds'))}</span>`)}${detail('Запущен', item.started_at ? formatDate(item.started_at) : 'Нет данных')}${detail('Uptime source', item.uptime_source || 'unavailable')}${detail('Main PID', item.main_pid || 0)}${detail('Рестарты', item.restart_count || 0)}${detail('Invocation ID', item.invocation_id || '—', 'mono')}${detail('Upload', formatBytes(item.upload_bytes))}${detail('Download', formatBytes(item.download_bytes))}${detail('Последний трафик', item.last_traffic_at ? formatDate(item.last_traffic_at) : 'Нет данных')}${detail('Reset policy', item.reset_policy)}${detail('DNS', item.dns)}${detail('Совместимость OLCBOX', compatibility(item.provider,item.transport) || 'Стабильная комбинация')}</div></div></td></tr>`;
}

function openInstanceForm(item = null) {
  const i = item || { provider:'jitsi', transport:'vp8channel', dns:'8.8.8.8:53', reset_policy:'never', omit_client_auth_token:true, options:{}, liveness:{} };
  const dns = String(i.dns || '8.8.8.8:53').trim();
  const dnsPresets = [
    ['8.8.8.8:53', 'Google (8.8.8.8)'],
    ['1.1.1.1:53', 'Cloudflare (1.1.1.1)'],
    ['77.88.8.8:53', 'Yandex (77.88.8.8)'],
    ['9.9.9.9:53', 'Quad9 (9.9.9.9)'],
    ['94.140.14.14:53', 'AdGuard (94.140.14.14)'],
  ];
  const dnsPreset = dnsPresets.some(([value]) => value === dns) ? dns : 'custom';
  const dnsOptions = dnsPresets.map(([value, label]) => `<option value="${attr(value)}" ${dnsPreset === value ? 'selected' : ''}>${esc(label)}</option>`).join('');
  openModal(item ? 'Изменить инстанс' : 'Новый инстанс', `
    <form data-form="instance" data-id="${i.id || ''}">
      <div class="form-grid">
        ${field('name','Имя',i.name || '','text','Например: Jitsi RU-1',true)}
        <div class="field"><label>Provider</label><select class="select" name="provider">${options(['jitsi','telemost','wbstream'],i.provider)}</select></div>
        <div class="field"><label>Transport</label><select class="select" name="transport">${options(['datachannel','vp8channel','seichannel','videochannel'],i.transport)}</select></div>
        <div class="field"><label for="f-room_id">Room ID / URL</label><input class="input" id="f-room_id" name="room_id" list="jitsi-presets" value="${attr(i.room_id || '')}" placeholder="https://meet.example/room" required><datalist id="jitsi-presets"><option value="https://meet.jit.si/"><option value="https://meet.small-dm.ru/"><option value="https://meet1.arbitr.ru/"><option value="https://meet.handyweb.org/"></datalist><button class="btn btn-small" type="button" data-action="generate-jitsi-room">Случайная Jitsi room</button></div>
        <div class="field"><label for="f-dns-preset">DNS-сервер</label><select class="select" id="f-dns-preset" name="dns_preset" data-role="dns-preset">${dnsOptions}<option value="custom" ${dnsPreset === 'custom' ? 'selected' : ''}>Другой DNS</option></select></div>
        <div class="field" data-role="dns-custom-row"${dnsPreset === 'custom' ? '' : ' hidden'}><label for="f-dns-custom">Свой DNS (IP:порт)</label><input class="input" id="f-dns-custom" name="dns_custom" type="text" value="${attr(dnsPreset === 'custom' ? dns : '')}" placeholder="9.9.9.9:53" ${dnsPreset === 'custom' ? 'required' : 'disabled'}><span class="field-hint">Укажите IPv4- или IPv6-адрес с портом 53.</span></div>
        ${field('outbound_proxy','Outbound SOCKS5 / WARP','','password',item ? 'Оставьте пустым, чтобы не менять' : 'socks5://user:pass@host:port')}
        <div class="field${i.provider === 'wbstream' ? '' : ' hidden'}" data-role="auth-token-row"><label for="f-auth_token">WB account token</label><input class="input" id="f-auth_token" name="auth_token" type="password" value="" placeholder="${attr(item ? 'Оставьте пустым, чтобы не менять' : 'Только WB; входит в QR OLCRTC Client')}"><span class="field-hint">Только для WB; входит в QR OLCRTC Client.</span></div>
        <div class="field"><label>Traffic reset</label><select class="select" name="reset_policy">${options(['never','daily','weekly','monthly','manual'],i.reset_policy)}</select></div>
        ${field('quota_gb','Quota, GB',i.quota_bytes ? (i.quota_bytes/1073741824).toFixed(2) : '','number','0 = unlimited')}
        ${field('expires_at','Срок действия',i.expires_at ? localDateTime(i.expires_at) : '','datetime-local','Необязательно')}
        <label class="checkbox"><input type="checkbox" name="debug" ${i.debug ? 'checked' : ''}> Debug logging</label>
        <label class="checkbox${i.provider === 'wbstream' ? '' : ' hidden'}" data-role="omit-token-row"><input type="checkbox" name="omit_client_auth_token" ${i.omit_client_auth_token ? 'checked' : ''}> Не публиковать auth token в Client URI (гостевой вход, без WB аккаунта)</label>
      </div>
      <details style="margin-top:18px"><summary class="muted" style="cursor:pointer">Расширенные transport и liveness settings</summary><div class="form-grid" style="margin-top:16px">
        ${field('vp8_fps','VP8 FPS',i.options?.vp8_fps || 60,'number')}${field('vp8_batch','VP8 batch',i.options?.vp8_batch || 64,'number')}${field('sei_fps','SEI FPS',i.options?.sei_fps || 30,'number')}${field('sei_batch','SEI batch',i.options?.sei_batch || 64,'number')}${field('sei_fragment','SEI fragment',i.options?.sei_fragment || 900,'number')}${field('sei_ack_ms','SEI ACK, ms',i.options?.sei_ack_ms || 2000,'number')}
        ${field('video_width','Video width',i.options?.video_width || 1920,'number')}${field('video_height','Video height',i.options?.video_height || 1080,'number')}${field('video_fps','Video FPS',i.options?.video_fps || 30,'number')}${field('video_bitrate','Video bitrate',i.options?.video_bitrate || '2M')}
        <div class="field"><label>Video codec</label><select class="select" name="video_codec">${options(['qrcode','tile'],i.options?.video_codec || 'qrcode')}</select></div><div class="field"><label>Video HW</label><select class="select" name="video_hw">${options(['none','nvenc'],i.options?.video_hw || 'none')}</select></div>
        ${field('liveness_interval','Liveness interval',i.liveness?.interval || '10s')}${field('liveness_timeout','Liveness timeout',i.liveness?.timeout || '5s')}${field('liveness_failures','Liveness failures',i.liveness?.failures || 3,'number')}${field('max_session_duration','Max session duration',i.max_session_duration || '')}
      </div></details>
      <div class="notice" style="margin-top:18px">Outbound proxy влияет и на signalling, и на пользовательский трафик. Независимое разделение без изменения upstream невозможно.</div>
      <div class="form-actions room-automation-actions" style="justify-content:flex-start"><button class="btn" type="button" data-action="wb-fill-instance">Playwright: WB room + token</button><button class="btn" type="button" data-action="telemost-fill-instance">Playwright: Telemost room</button></div>
      <div class="form-actions"><button class="btn" type="button" data-action="close-modal">Отмена</button><button class="btn btn-primary" type="submit">${item ? 'Сохранить' : 'Создать'}</button></div>
    </form>`, true);
  syncInstanceFormFields(document.querySelector('form[data-form="instance"]'));
}

function normalizeRoomID(provider, room) {
  const value = String(room ?? '').trim();
  if (provider !== 'telemost') return value;
  let candidate = value;
  try {
    const parsed = new URL(value);
    if (parsed.protocol === 'https:' && parsed.hostname.toLowerCase() === 'telemost.yandex.ru' && !parsed.port && !parsed.username && !parsed.password && !parsed.search && !parsed.hash && parsed.pathname.startsWith('/j/')) {
      const roomID = decodeURIComponent(parsed.pathname.slice(3));
      if (roomID && !roomID.includes('/')) candidate = roomID;
    }
  } catch (_) {}
  const compact = candidate.replaceAll(' ', '');
  return /^\d+$/.test(compact) ? compact : value;
}

function normalizeInstanceRoomField(event) {
  const form = event.target.closest?.('form[data-form="instance"]');
  if (!form || !['provider', 'room_id'].includes(event.target.name)) return;
  form.elements.room_id.value = normalizeRoomID(form.elements.provider.value, form.elements.room_id.value);
}

function syncInstanceFormFields(form) {
  if (!form) return;
  const isWB = form.elements.provider?.value === 'wbstream';
  const authRow = form.querySelector('[data-role="auth-token-row"]');
  const omitTokenRow = form.querySelector('[data-role="omit-token-row"]');
  authRow?.classList.toggle('hidden', !isWB);
  omitTokenRow?.classList.toggle('hidden', !isWB);
  if (!isWB && form.elements.auth_token?.value) form.elements.auth_token.value = '';

  const customDNS = form.elements.dns_preset?.value === 'custom';
  const dnsRow = form.querySelector('[data-role="dns-custom-row"]');
  const dnsInput = form.elements.dns_custom;
  if (dnsRow) dnsRow.hidden = !customDNS;
  if (dnsInput) {
    dnsInput.disabled = !customDNS;
    dnsInput.required = customDNS;
  }
}

function instancePayload(form) {
  const d = new FormData(form);
  const num = key => Number(d.get(key) || 0);
  const expires = d.get('expires_at');
  const provider = d.get('provider');
  const dnsPreset = String(d.get('dns_preset') || '').trim();
  const dns = dnsPreset === 'custom' ? String(d.get('dns_custom') || '').trim() : dnsPreset || '8.8.8.8:53';
  return {
    name: d.get('name').trim(), provider, transport: d.get('transport'), room_id: normalizeRoomID(provider, d.get('room_id')), dns, outbound_proxy: d.get('outbound_proxy'), auth_token: d.get('auth_token'), reset_policy: d.get('reset_policy'), quota_bytes: Math.max(0, Math.round(num('quota_gb') * 1073741824)), expires_at: expires ? new Date(expires).toISOString() : null, debug: d.has('debug'), omit_client_auth_token: d.has('omit_client_auth_token'),
    options: { vp8_fps:num('vp8_fps'), vp8_batch:num('vp8_batch'), sei_fps:num('sei_fps'), sei_batch:num('sei_batch'), sei_fragment:num('sei_fragment'), sei_ack_ms:num('sei_ack_ms'), video_width:num('video_width'), video_height:num('video_height'), video_fps:num('video_fps'), video_bitrate:d.get('video_bitrate'), video_hw:d.get('video_hw'), video_codec:d.get('video_codec'), video_qr_recovery:'low', video_tile_module:4, video_tile_rs:20 },
    liveness: { interval:d.get('liveness_interval'), timeout:d.get('liveness_timeout'), failures:num('liveness_failures') }, max_session_duration:d.get('max_session_duration'), traffic_options:{ max_payload_size:0, min_delay:'', max_delay:'' }
  };
}

async function loadSubscriptions() {
  const [subs, instances] = await Promise.all([api('/api/v1/subscriptions'), state.instances.length ? Promise.resolve({items:state.instances}) : api('/api/v1/instances')]);
  state.subscriptions = subs.items || [];
  state.instances = instances.items || [];
  renderSubscriptions();
}

async function syncAllMirrors() {
  const targets = state.subscriptions.filter(subscription => subscription.mirror_enabled);
  if (!targets.length) {
    toast('Нет активных mirror', 'Включите Yandex mirror хотя бы для одной подписки.', 'warning');
    return;
  }
  if (!confirm(`Синхронизировать mirror для ${targets.length} подписок?`)) return;
  state.mirrorSyncRunning = true;
  renderSubscriptions();
  let synced = 0;
  const failed = [];
  try {
    for (const subscription of targets) {
      try {
        await api(`/api/v1/subscriptions/${encodeURIComponent(subscription.slug)}/mirror/sync`, {method:'POST'});
        synced++;
      } catch (error) {
        failed.push(`${subscription.name}: ${error.message}`);
      }
    }
    await loadSubscriptions();
    const details = `Успешно: ${synced}; ошибок: ${failed.length}`;
    toast(failed.length ? 'Синхронизация завершена с ошибками' : 'Все mirror синхронизированы', failed.length ? `${details}. ${failed.join('; ')}` : details, failed.length ? 'warning' : 'success');
  } catch (error) {
    toast('Синхронизация mirror не завершена', error.message, 'error');
  } finally {
    state.mirrorSyncRunning = false;
    if (state.page === 'subscriptions') renderSubscriptions();
  }
}

function renderSubscriptions() {
  document.querySelector('#page-content').innerHTML = `
    <section class="page"><div class="page-header"><div class="page-title"><h1>Подписки</h1><p>Одна подписка публикуется в форматах OLCRTC Client и OLCBOX</p></div><div class="header-actions"><button class="btn" data-action="sync-mirror-all" ${state.mirrorSyncRunning ? 'disabled' : ''}>↻ Sync mirror all</button><button class="btn" data-action="import-subscriptions">⇧ Импорт</button><button class="btn" data-action="export-subscriptions">⇩ Экспорт</button><button class="btn btn-primary" data-action="create-subscription">＋ Подписка</button></div></div><div class="notice" style="margin-bottom:16px">OLCRTC Client получает compact URI и optional Yandex mirror. OLCBOX получает отдельный plain-text <code>sub.md</code> endpoint с обычными <code>olcrtc://</code> URI.</div>
    ${state.subscriptions.length ? `<div class="subscription-list">${state.subscriptions.map(subscriptionCard).join('')}</div>` : emptyState('≋','Подписок пока нет','Создайте bearer-secret URL и добавьте linked instances или manual URI.','<button class="btn btn-primary" data-action="create-subscription">Создать подписку</button>')}
    </section>`;
}

function subscriptionCard(sub) {
  const clientURL = subscriptionURL(sub.slug);
  const olcboxURL = subscriptionURL(sub.slug, '/olcbox');
  return `<article class="panel subscription-card"><div class="subscription-main"><div class="subscription-name"><div class="subscription-icon">${esc(sub.icon || '≋')}</div><div class="truncate"><strong>${esc(sub.name)}</strong><small class="mono truncate">${esc(sub.slug)}</small></div></div><div><small>Трафик</small><strong>${formatBytes(sub.used_bytes || 0)} / ${sub.available_bytes == null ? '∞' : formatBytes((sub.used_bytes||0)+sub.available_bytes)}</strong></div><div><small>Entries</small><strong>${sub.entries?.length || 0}</strong></div><div><small>Mirror Client</small><span class="chip ${esc(sub.mirror_status || 'disabled')}">${esc(sub.mirror_status || 'disabled')}</span></div><div class="toolbar-actions"><button class="btn btn-small" data-action="copy" data-value="${attr(clientURL)}">URL Client</button><button class="btn btn-small" data-action="copy" data-value="${attr(olcboxURL)}">URL OLCBOX</button><button class="btn btn-small btn-icon" data-action="expand-subscription" data-slug="${attr(sub.slug)}">${state.expandedSubscription === sub.slug ? '−' : '+'}</button></div></div>${state.expandedSubscription === sub.slug ? subscriptionExpanded(sub) : ''}</article>`;
}

function subscriptionExpanded(sub) {
  return `<div class="subscription-details"><div class="expanded-actions"><button class="btn btn-small" data-action="subscription-qr" data-format="client" data-slug="${attr(sub.slug)}">QR OLCRTC Client</button><button class="btn btn-small" data-action="subscription-qr" data-format="olcbox" data-slug="${attr(sub.slug)}">QR OLCBOX</button><button class="btn btn-small" data-action="add-entry" data-slug="${attr(sub.slug)}">＋ Entry</button>${sub.mirror_enabled ? `<button class="btn btn-small" data-action="sync-mirror" data-slug="${attr(sub.slug)}">↻ Sync mirror Client</button>` : ''}<button class="btn btn-small" data-action="edit-subscription" data-slug="${attr(sub.slug)}">✎ Изменить</button><button class="btn btn-danger btn-small" data-action="delete-subscription" data-slug="${attr(sub.slug)}">Удалить</button></div><div class="fingerprint mono">OLCRTC Client: ${esc(subscriptionURL(sub.slug))}<br>OLCBOX: ${esc(subscriptionURL(sub.slug, '/olcbox'))}<br>Открыть в клиенте: ${esc(subscriptionURL(sub.slug, '/open'))}</div><div class="notice info" style="margin:14px 0">OLCBOX получает plain-text sub.md и обычные OLCBOX URI. Yandex encrypted mirror остаётся проекцией OLCRTC Client.</div><div class="entry-list">${sub.entries?.length ? sub.entries.map(entryRow).join('') : '<div class="muted">Нет entries. Подписка публикует пустой список.</div>'}</div></div>`;
}

function entryRow(entry) {
  const source = entry.source_instance_id ? state.instances.find(i => i.id === entry.source_instance_id) : null;
  return `<div class="entry-row"><label class="switch" title="Публикация entry"><input type="checkbox" data-action="toggle-entry" data-slug="${attr(state.expandedSubscription)}" data-id="${entry.id}" ${entry.enabled ? 'checked' : ''}><span></span></label><div class="truncate"><strong>${esc(entry.name || source?.name || 'Без имени')}</strong><small class="muted">${source ? `Linked instance #${source.id}` : 'Manual URI'} · порядок ${entry.sort_order}</small></div><span class="chip ${source ? 'green' : 'purple'}">${source ? 'linked' : 'manual'}</span><div class="toolbar-actions"><button class="btn btn-small btn-icon" data-action="move-entry" data-dir="up" data-slug="${attr(state.expandedSubscription)}" data-id="${entry.id}" aria-label="Выше">↑</button><button class="btn btn-small btn-icon" data-action="move-entry" data-dir="down" data-slug="${attr(state.expandedSubscription)}" data-id="${entry.id}" aria-label="Ниже">↓</button><button class="btn btn-danger btn-small" data-action="delete-entry" data-slug="${attr(state.expandedSubscription)}" data-id="${entry.id}">Удалить</button></div></div>`;
}

function openSubscriptionForm(sub = null) {
  const s = sub || { enabled:true, refresh:'10m', color:'#0EA58C', mirror_enabled:false, omit_client_auth_token:true };
  openModal(sub ? 'Изменить подписку' : 'Новая подписка', `<form data-form="subscription" data-slug="${attr(s.slug || '')}"><div class="form-grid">${field('name','Название',s.name || '','text','Например: Основная подписка',true)}${field('slug','Slug',s.slug || '','text','Пустой = случайный 128-bit slug',false,!!sub)}${field('refresh','Refresh interval',s.refresh || '10m','text','10m / 6h')} ${field('color','Цвет',s.color || '#0EA58C','color')} ${field('icon','Иконка / emoji',s.icon || '')}<label class="checkbox"><input type="checkbox" name="enabled" ${s.enabled ? 'checked' : ''}> Подписка включена</label><label class="checkbox"><input type="checkbox" name="mirror_enabled" ${s.mirror_enabled ? 'checked' : ''}> Yandex encrypted mirror</label><label class="checkbox"><input type="checkbox" name="omit_client_auth_token" ${s.omit_client_auth_token ? 'checked' : ''}> Не публиковать WB auth token в подписку (клиент входит как гость)</label></div><div class="notice info" style="margin-top:17px">Slug является bearer secret: по URL доступны URI с encryption keys. Не публикуйте его в открытом доступе.</div><div class="form-actions"><button class="btn" type="button" data-action="close-modal">Отмена</button><button class="btn btn-primary" type="submit">Сохранить</button></div></form>`);
}

function openEntryForm(slug) {
  const instances = state.instances;
  const sub = state.subscriptions.find(s => s.slug === slug);
  const entries = sub?.entries || [];
  const addedIds = new Set(entries.filter(e => e.source_instance_id).map(e => e.source_instance_id));
  const firstFree = instances.find(i => !addedIds.has(i.id));
  const addedHtml = entries.length ? `<div style="margin-bottom:16px"><p class="field-label" style="margin-bottom:8px">Уже добавлено — ${entries.length}</p><div class="entry-added-list">${entries.map(e=>{const inst=e.source_instance_id?instances.find(i=>i.id===e.source_instance_id):null;return `<div class="entry-added-row"><div class="truncate"><strong>${esc(e.name||inst?.name||'Без имени')}</strong><span class="muted"> · ${inst?`#${inst.id} ${esc(inst.provider)} / ${esc(inst.transport)}`:'Manual URI'}</span></div><span class="chip ${inst?'green':'purple'}" style="flex-shrink:0">${inst?'linked':'manual'}</span></div>`;}).join('')}</div></div>` : '';
  const instanceRows = instances.length ? instances.map(i=>{const isAdded=addedIds.has(i.id);const clientOk=!clientQRUnavailable(i);return `<label class="instance-pick-row ${isAdded?'added':''}"><input type="radio" name="source_instance_id" value="${i.id}" ${i===firstFree?'checked':''} ${isAdded?'disabled':''}><div style="flex:1;min-width:0"><div style="display:flex;align-items:center;gap:7px;flex-wrap:wrap"><strong>${esc(i.name)}</strong><span class="chip ${i.provider==='wbstream'?'purple':'green'}">${esc(i.provider)}</span><span class="chip blue">${esc(i.transport)}</span><span class="chip ${esc(i.status)}" style="font-size:10px">${statusLabel(i.status)}</span>${isAdded?'<span class="chip" style="font-size:10px">добавлен</span>':''}</div><div style="margin-top:3px;color:var(--muted-2);font-size:11px">#${i.id} · ${clientOk?'Client + OLCBOX':'только OLCBOX'}</div></div></label>`;}).join('') : `<div class="empty-state" style="min-height:0;padding:20px;text-align:center"><p>Нет инстансов. Сначала создайте инстанс.</p></div>`;
  openModal('Добавить entry в подписку', `<form data-form="entry" data-slug="${attr(slug)}">${addedHtml}<div class="field" style="margin-bottom:12px"><label>Источник</label><select class="select" name="source_type" data-role="entry-source"><option value="linked">Linked instance</option><option value="manual">Manual URI</option></select></div><div data-entry-linked><p class="field-label" style="margin-bottom:7px">Выберите инстанс</p><div class="instance-picker">${instanceRows}</div><span class="field-hint" style="margin-top:6px;display:block">Linked instance всегда попадает в OLCBOX feed; в Client feed — только при совместимом provider/transport и заданном WB token.</span></div><div data-entry-manual class="hidden" style="margin-top:4px"><div class="field"><label>OLCRTC Client или OLCBOX olcrtc:// URI</label><textarea class="textarea mono" name="raw_uri" placeholder="olcrtc://jitsi?datachannel@room#<64 hex key>$name"></textarea><span class="field-hint">Формат определяется автоматически: Client URI публикуется в Client feed, OLCBOX URI — в OLCBOX feed.</span></div></div><div class="form-grid" style="margin-top:15px">${field('name','Отображаемое имя','')}${field('comment','Комментарий','')}${field('ip','Показываемый IP','')}${field('color','Цвет','#0EA58C','color')}<label class="checkbox"><input type="checkbox" name="enabled" checked> Публиковать entry</label></div><div class="form-actions"><button class="btn" type="button" data-action="close-modal">Отмена</button><button class="btn btn-primary" type="submit">Добавить</button></div></form>`, true);
}

async function loadSettings() {
  const [settings, automationSettings, automationSession, wbOperation, updateOperation] = await Promise.all([
    api('/api/v1/settings'),
    api('/api/v1/automation/settings').catch(() => ({ proxy_mode: 'direct', proxy_address: '', proxy_username: '', proxy_password_set: false })),
    api('/api/v1/wb/session').catch(() => ({ active: false, provider: 'wbstream', state: {} })),
    api('/api/v1/automation/components/progress').catch(() => ({ state: 'idle', percent: 0 })),
    api('/api/v1/updates/progress').catch(() => ({ state: 'idle', percent: 0 })),
  ]);
  state.settings = settings;
  state.automationSettings = automationSettings;
  state.automationSession = automationSession;
  state.releases = { configured: settings.updates.configured, items: [], loading: true };
  state.wbOperation = wbOperation;
  state.updateOperation = updateOperation;
  applyTheme(state.settings.interface?.theme || localStorage.getItem('olcrtc-theme') || 'dark');
  renderSettings();
  state.poller = setInterval(refreshSettingsOperations, 1500);
  api('/api/v1/updates/releases').then(releases=>{if(state.page==='settings'){state.releases=releases;renderSettings();}}).catch(error=>{if(state.page==='settings'){state.releases={configured:settings.updates.configured,items:[],error:error.message};renderSettings();}});
}

function renderSettings() {
  const s = state.settings;
  const releases = state.releases || { configured: false, items: [] };
  const currentRelease = releases.current || { panel_version: s.updates.panel_version, upstream_sha: s.updates.upstream_sha };
  const latestRelease = [...(releases.items || [])].sort((a,b)=>new Date(b.published_at)-new Date(a.published_at))[0];
  const updateRunning = state.updateOperation?.state === 'running';
  const wbRunning = state.wbOperation?.state === 'running';
  const wbTokenStatus = !s.wb.token_set ? 'Не задан' : s.wb.token_expired ? 'Истёк — обновите через Playwright' : s.wb.token_expires_at ? `Действует до ${formatDate(s.wb.token_expires_at)}` : 'Задан, срок неизвестен';
  document.querySelector('#page-content').innerHTML = `
    <section class="page"><div class="page-header"><div class="page-title"><h1>Настройки</h1><p>Безопасность, HTTPS, интеграции и обслуживание</p></div></div><div class="settings-layout">
      <nav class="panel settings-nav"><button data-action="scroll-setting" data-target="security">Безопасность</button><button data-action="scroll-setting" data-target="https">HTTPS и URL</button><button data-action="scroll-setting" data-target="updates">Обновления</button><button data-action="scroll-setting" data-target="automation">Автоматизация WB и Telemost</button><button data-action="scroll-setting" data-target="yandex">Yandex mirror</button><button data-action="scroll-setting" data-target="instances-settings">Инстансы</button><button data-action="scroll-setting" data-target="interface">Интерфейс</button><button data-action="scroll-setting" data-target="backup">Backup</button></nav>
      <div>
        ${settingsSection('security','Безопасность',`<form data-form="credentials" class="form-grid">${field('username','Новый логин',state.user || '')}${field('current_password','Текущий пароль','','password','Обязательно',true)}${field('new_password','Новый пароль','','password','Пусто = оставить текущий')}<div class="wide form-actions"><button class="btn" type="button" data-action="revoke-sessions">Завершить все сессии</button><button class="btn btn-primary" type="submit">Изменить credentials</button></div></form>`)}
        ${settingsSection('https','HTTPS и публичные URL',`<form data-form="https-settings"><div class="form-grid">${field('public_ip','Публичный IP',s.https.public_ip || '')}${field('public_port','HTTPS порт',s.https.port,'number')}${field('public_origin','Публичный HTTPS origin',s.https.public_origin || '','url','https://example.com')}${field('panel_path','Путь панели',s.https.panel_path || '/','text','/control-a8f3',true)}${field('subscription_path','Путь подписок',s.https.subscription_path || '/sub','text','/feeds-b19c',true)}</div><div class="notice info" data-role="public-url-preview" style="margin-top:14px">Панель: ${esc(s.https.panel_url || '')}<br>Подписка: ${esc((s.https.subscription_url || '') + '/example-slug')}</div>${s.https.restart_required ? '<div class="notice" style="margin-top:10px">Новые origin и пути сохранены, но ещё не активны. Перезапустите panel service. Старые ссылки и QR-коды после смены URL перестанут работать.</div>' : ''}<div class="form-actions"><a class="btn" href="${attr(panelURL('/ca.crt'))}" download>Скачать CA</a><button class="btn" type="button" data-action="regenerate-cert">Регенерировать leaf</button><button class="btn btn-primary" type="submit">Сохранить HTTPS и URL</button></div></form><p class="field-label">CA fingerprint</p><div class="fingerprint mono">${esc(s.https.ca_fingerprint || '')}</div><p class="field-label">Server fingerprint</p><div class="fingerprint mono">${esc(s.https.server_fingerprint || '')}</div><div class="notice" style="margin-top:14px">После смены порта или публичных URL перезапустите panel service. CA необходимо сверить с fingerprint из консоли VPS.</div>`)}
        ${settingsSection('updates','Обновления',`<div class="detail-list">${detail('Текущий bundle',currentRelease.bundle_id || 'не определён')}${detail('Версия панели',currentRelease.panel_version || s.updates.panel_version)}${detail('Upstream SHA',shortSHA(currentRelease.upstream_sha || s.updates.upstream_sha))}${detail('Release manifest',s.updates.configured ? 'Настроен' : 'Не задан')}</div><div class="form-actions"><button class="btn" data-action="check-updates" ${updateRunning ? 'disabled' : ''}>Обновить список</button><button class="btn btn-primary" data-action="install-update" ${updateRunning || !latestRelease || latestRelease.current ? 'disabled' : ''}>Обновить до последнего</button><button class="btn" data-action="rollback-update" ${updateRunning || !releases.rollback_available ? 'disabled' : ''}>Rollback</button></div><div id="update-operation">${operationProgressHTML(state.updateOperation, 'Обновление')}</div><div id="release-list">${releaseCatalogHTML(releases, updateRunning)}</div>`)}
        ${settingsSection('automation','Автоматизация WB и Telemost',automationSettingsHTML(s, wbRunning, wbTokenStatus))}
        ${settingsSection('yandex','Yandex encrypted mirror',`<form data-form="yandex"><div class="form-grid"><label class="checkbox"><input type="checkbox" name="yandex_enabled" ${s.yandex.enabled ? 'checked' : ''}> Включить глобально</label>${field('yandex_base_path','Base path',s.yandex.base_path || '/olcrtc/subscriptions')}${field('yandex_oauth_token','OAuth token','','password',s.yandex.token_set ? 'Token сохранён; пусто = не менять' : 'Введите token')}<label class="checkbox"><input type="checkbox" name="clear_yandex_token"> Удалить сохранённый token</label></div><div class="form-actions"><button class="btn btn-primary" type="submit">Сохранить Yandex settings</button></div></form><details style="margin-top:18px"><summary style="cursor:pointer;color:var(--muted);font-size:12px;font-weight:520">Инструкция: как получить Yandex OAuth-токен</summary><div style="margin-top:14px;display:grid;gap:12px;font-size:13px"><p style="margin:0"><strong>Шаг 1. Создать OAuth-приложение в Яндексе</strong></p><ol style="margin:0;padding-left:20px;color:var(--muted);line-height:1.7"><li>Открой <a href="https://oauth.yandex.ru/client/new" target="_blank" rel="noopener">oauth.yandex.ru/client/new</a> под аккаунтом, на Диск которого будет публиковаться mirror.</li><li>Заполни поля:<ul style="margin:6px 0"><li><strong>Название сервиса</strong> — произвольное, например <code>olcRTC subscription mirror</code>.</li><li><strong>Ссылка на сайт</strong> — домен вашего сервера или любой URL.</li><li><strong>Redirect URI</strong> — добавь <code>https://oauth.yandex.ru/verification_code</code>.</li><li><strong>Доступы (Яндекс Диск)</strong> — «Доступ к информации о Диске» и «Запись в любом месте на Диске». При проблемах также: «Чтение всего Диска», «Доступ к папке приложения на Диске».</li></ul></li><li>Нажми <strong>Создать приложение</strong>. Получишь ClientID вида <code>a1b2c3d4...</code>.</li></ol><p style="margin:0"><strong>Шаг 2. Получить OAuth-токен</strong></p><p style="margin:0;color:var(--muted)">Открой в браузере, подставив свой ClientID:</p><div class="fingerprint mono" style="margin:6px 0">https://oauth.yandex.ru/authorize?response_type=token&amp;client_id=&lt;CLIENTID&gt;</div><p style="margin:0;color:var(--muted)">Яндекс попросит разрешение на доступ к Диску. После согласия браузер перейдёт на <code>https://oauth.yandex.ru/verification_code#access_token=&lt;TOKEN&gt;</code>. Скопируй <code>&lt;TOKEN&gt;</code> из URL — это OAuth-токен для mirror.</p><p style="margin:0"><strong>Шаг 3. Вставить токен в панели</strong></p><p style="margin:0;color:var(--muted)">Вставь скопированный токен в поле <strong>OAuth token</strong> выше и нажми <strong>Сохранить Yandex settings</strong>. Токен хранится зашифрованно.</p></div></details>`)}
        ${settingsSection('instances-settings','Инстансы',`<form data-form="instance-settings" class="inline-form"><div class="field"><label>Максимум инстансов</label><input class="input" name="max_instances" type="number" min="1" max="1000" value="${s.instances.maximum}"></div><button class="btn btn-primary" type="submit">Сохранить</button></form>`)}
        ${settingsSection('interface','Интерфейс',`<form data-form="theme" class="inline-form"><div class="field"><label>Тема</label><select class="select" name="theme"><option value="dark" ${s.interface.theme==='dark'?'selected':''}>Тёмная</option><option value="light" ${s.interface.theme==='light'?'selected':''}>Светлая</option></select></div><button class="btn btn-primary" type="submit">Применить</button></form>`)}
        ${settingsSection('backup','Backup',`<p class="muted">Обычный UI backup содержит SQLite snapshot и redacted YAML. Master key, private TLS keys, key.hex и WB profile не включаются.</p><div class="form-actions"><button class="btn btn-primary" data-action="create-backup">Создать и скачать</button></div>`)}
      </div>
    </div></section>`;
}

function automationSettingsHTML(settings, componentsRunning, tokenStatus) {
  const proxy = state.automationSettings || {};
  const session = state.automationSession || {};
  const sessionActive = Boolean(session.active);
  const proxyMode = proxy.proxy_mode || 'direct';
  return `
    <div class="detail-list">${detail('Платформа',settings.wb.supported ? 'linux/amd64' : 'Не поддерживается')}${detail('Components',settings.wb.installed ? 'Установлены' : 'Не установлены')}</div>
    <div class="form-actions wb-actions"><button class="btn btn-primary" data-action="wb-install" ${!settings.wb.supported || componentsRunning ? 'disabled' : ''}>Установить components</button><button class="btn btn-danger" data-action="wb-remove" ${!settings.wb.supported || componentsRunning ? 'disabled' : ''}>Удалить components</button></div>
    <div id="wb-operation">${operationProgressHTML(state.wbOperation, 'Automation components')}</div>
    <div class="form-actions wb-actions"><button class="btn" data-action="trigger-auto-setup">Запустить wizard автонастройки</button></div>
    <section class="automation-provider">
      <h3>Browser proxy</h3>
      <form data-form="automation-proxy">
        <div class="form-grid">
          <div class="field"><label for="f-proxy_mode">Режим</label><select class="select" id="f-proxy_mode" name="proxy_mode"><option value="direct" ${proxyMode === 'direct' ? 'selected' : ''}>Без proxy</option><option value="http" ${proxyMode === 'http' ? 'selected' : ''}>HTTP</option><option value="https" ${proxyMode === 'https' ? 'selected' : ''}>HTTPS</option><option value="socks5" ${proxyMode === 'socks5' ? 'selected' : ''}>SOCKS5</option></select></div>
          ${field('proxy_address','Адрес proxy',proxy.proxy_address || '','text','host:port')}
          ${field('proxy_username','Логин',proxy.proxy_username || '','text','Необязательно')}
          ${field('proxy_password','Пароль','','password',proxy.proxy_password_set ? 'Пароль сохранён; пусто = не менять' : 'Необязательно')}
          <label class="checkbox"><input type="checkbox" name="clear_proxy_password"> Удалить сохранённый пароль</label>
        </div>
        <div class="notice info" style="margin-top:14px">Proxy применяется только к Chromium Playwright. Outbound proxy инстансов настраивается отдельно.</div>
        <div class="form-actions"><button class="btn btn-primary" type="submit">Сохранить browser proxy</button></div>
      </form>
    </section>
    <section class="automation-provider"><h3>Browser session</h3><div id="automation-session">${automationSessionHTML(session)}</div></section>
    <section class="automation-provider"><h3>WB Stream</h3><div class="detail-list">${detail('Auth token',tokenStatus,settings.wb.token_expired ? 'danger-text' : '')}</div><div class="notice info" style="margin-top:14px">Playwright использует отдельный постоянный Chromium profile WB Stream. Login и CAPTCHA выполняются вручную через авторизованный noVNC route.</div><div class="notice" style="margin-top:10px">WB auth token входит только в URI/QR OLCRTC Client и делает такой QR credential. QR OLCBOX token не содержит.</div><div class="form-actions wb-actions"><button class="btn" data-action="wb-session-create" ${!settings.wb.installed || sessionActive ? 'disabled' : ''}>Playwright: создать комнату</button><button class="btn ${settings.wb.token_expired ? 'btn-primary' : ''}" data-action="wb-playwright-refresh" ${!settings.wb.installed || sessionActive ? 'disabled' : ''}>Playwright: обновить token</button><button class="btn" data-action="wb-token">Ввести token вручную (fallback)</button><button class="btn btn-danger" data-action="wb-profile-reset" ${!settings.wb.installed ? 'disabled' : ''} title="Остановить сессию и очистить Chromium profile WB Stream">↺ Сбросить профиль (перезайти)</button></div></section>
    <section class="automation-provider"><h3>Telemost</h3><div class="notice info">Playwright использует отдельный постоянный Chromium profile Telemost. Если Яндекс запросит вход, завершите его через noVNC.</div><div class="form-actions wb-actions"><button class="btn" data-action="telemost-session-create" ${!settings.wb.installed || sessionActive ? 'disabled' : ''}>Playwright: создать комнату</button><button class="btn btn-danger" data-action="telemost-profile-reset" ${!settings.wb.installed ? 'disabled' : ''} title="Остановить сессию и очистить Chromium profile Telemost">↺ Сбросить профиль (перезайти)</button></div></section>`;
}

function automationSessionHTML(session = {}) {
  if (!session.active) return '<div class="notice">Browser-сессия не активна</div>';
  const provider = session.provider === 'telemost' ? 'telemost' : 'wbstream';
  const label = provider === 'telemost' ? 'Telemost' : 'WB Stream';
  const worker = session.state || {};
  const status = worker.message || worker.phase || 'Chromium запущен';
  return `<div class="notice info"><strong>${esc(label)}</strong><br>${esc(status)}${session.expires_at ? `<br>До ${formatDate(session.expires_at)}` : ''}<div class="form-actions wb-actions" style="margin-top:12px"><a class="btn btn-primary" href="${attr(session.novnc_url || '')}" target="_blank" rel="noopener">Открыть noVNC</a><button class="btn btn-danger" data-action="automation-session-stop" data-provider="${attr(provider)}">Остановить браузер</button></div></div>`;
}

async function loadLogsPage() {
  if (!state.instances.length) { const result = await api('/api/v1/instances'); state.instances = result.items || []; }
  renderLogsPage('Загрузка журнала...');
  await refreshLogs();
}

function renderLogsPage(text = '') {
  document.querySelector('#page-content').innerHTML = `<section class="page"><div class="page-header"><div class="page-title"><h1>Журнал</h1><p>Redacted systemd logs без token, cookies и encryption keys</p></div><div class="header-actions"><button class="btn" data-action="refresh-logs">↻ Обновить</button><button class="btn" data-action="toggle-logs">${state.logsPaused ? '▶ Продолжить' : 'Ⅱ Пауза'}</button><button class="btn" data-action="copy-logs">Копировать</button></div></div><section class="panel"><div class="toolbar"><div class="filters"><select class="select" data-role="logs-unit"><option value="panel" ${state.logsUnit==='panel'?'selected':''}>Panel</option><option value="wb" ${state.logsUnit==='wb'?'selected':''}>WB automation</option><option value="update" ${state.logsUnit==='update'?'selected':''}>Update</option>${state.instances.map(i=>`<option value="instance:${i.id}" ${state.logsUnit===`instance:${i.id}`?'selected':''}>Instance #${i.id} ${esc(i.name)}</option>`).join('')}</select><select class="select" data-role="logs-lines">${[100,200,500,1000,2000].map(n=>`<option>${n}</option>`).join('')}</select><select class="select" data-role="logs-level"><option value="">Все уровни</option>${options(['error','warn','info','debug'],state.logsLevel)}</select><div class="search"><input class="input" data-role="logs-search" placeholder="Фильтр строк"></div></div></div><div class="panel-body"><pre class="log-viewer" id="log-output">${esc(text)}</pre></div></section></section>`;
  if (!state.logsPaused) state.poller = setInterval(refreshLogs, 5000);
}

async function refreshLogs() {
  try {
    const lines = document.querySelector('[data-role="logs-lines"]')?.value || 200;
    const result = await api(`/api/v1/system/logs?unit=${encodeURIComponent(state.logsUnit)}&lines=${lines}`);
    const query = document.querySelector('[data-role="logs-search"]')?.value.toLowerCase() || '';
    const text = result.text.split('\n').filter(line => (!query || line.toLowerCase().includes(query)) && (!state.logsLevel || line.toLowerCase().includes(state.logsLevel))).join('\n');
    const output = document.querySelector('#log-output'); if (output) { output.textContent = text; output.scrollTop = output.scrollHeight; }
  } catch (error) { toast('Ошибка журнала', error.message, 'error'); }
}

app.addEventListener('click', async event => {
  const target = event.target.closest('[data-page],[data-action]');
  if (!target) {
    if (event.target.classList.contains('modal-backdrop')) closeModal();
    return;
  }
  if (target.dataset.page) { await navigate(target.dataset.page); return; }
  const action = target.dataset.action;
  try {
    if (action === 'drawer') document.body.classList.toggle('drawer-open');
    if (action === 'close-modal') closeModal();
    if (action === 'toggle-theme') { const theme = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark'; applyTheme(theme); localStorage.setItem('olcrtc-theme',theme); }
    if (action === 'dismiss-update-notice') { state.updateNoticeDismissed = true; renderUpdateNotice(); }
    if (action === 'update-notice-open') await navigate('settings');
    if (action === 'logout') { await api('/api/v1/auth/logout',{method:'POST'}); state.user=null;state.updateNoticeCatalog=null;state.updateNoticeDismissed=false;renderLogin(); }
    if (action === 'auto-setup-start') await startAutoSetup();
    if (action === 'auto-setup-open-vnc') openAutoSetupVNC(target.dataset.url);
    if (action === 'auto-setup-install') await installAutoSetupComponents();
    if (action === 'auto-setup-auth-wb') await runAutoSetupProvider('wbstream');
    if (action === 'auto-setup-auth-telemost') await runAutoSetupProvider('telemost');
    if (action === 'auto-setup-skip-telemost') await skipAutoSetupTelemost();
    if (action === 'auto-setup-to-rooms') { collectAutoSetupRooms(); state.autoSetup.state = { ...(state.autoSetup.state || {}), step: 'wb_rooms_create', progress: 65, wb_room_ids: state.autoSetup.wbRooms, current_action: 'Ожидание Room ID WB Stream' }; await persistAutoSetupDraft(); renderAutoSetupWizard(); }
    if (action === 'auto-setup-create-wb-room') await runAutoSetupProvider('wbstream');
    if (action === 'auto-setup-complete') await completeAutoSetup();
    if (action === 'auto-setup-manual') { state.autoSetup.state = { ...(state.autoSetup.state || {}), step: 'wb_rooms_create', progress: 65, current_action: 'Ручной ввод Room ID' }; await persistAutoSetupDraft(); renderAutoSetupWizard(); }
    if (action === 'auto-setup-retry') await startAutoSetup(true);
    if (action === 'auto-setup-dismiss') await dismissAutoSetup();
    if (action === 'auto-setup-dashboard') await finishAutoSetupUI();
    if (action === 'trigger-auto-setup') await triggerAutoSetup();
    if (action === 'refresh-dashboard') await loadDashboard();
    if (action === 'refresh-instances') await loadInstances();
    if (action === 'create-instance') openInstanceForm();
    if (action === 'toggle-network-info') { state.hideNetworkInfo = !state.hideNetworkInfo; if (state.page === 'instances') renderInstances(); else if (state.page === 'dashboard') renderDashboard(false); }
    if (action === 'start-all-instances') { const toStart = state.instances.filter(i => i.status !== 'running'); let started = 0; for (const inst of toStart) { try { await api(`/api/v1/instances/${inst.id}/start`, {method:'POST'}); started++; } catch (_) {} } await loadInstances(); toast('Запуск завершён', `Запущено: ${started} из ${toStart.length}`); }
    if (action === 'stop-all-instances') { const toStop = state.instances.filter(i => i.status === 'running'); let stopped = 0; for (const inst of toStop) { try { await api(`/api/v1/instances/${inst.id}/stop`, {method:'POST'}); stopped++; } catch (_) {} } await loadInstances(); toast('Остановка завершена', `Остановлено: ${stopped} из ${toStop.length}`); }
    if (action === 'expand-instance') { state.expandedInstance = state.expandedInstance === Number(target.dataset.id) ? null : Number(target.dataset.id); renderInstances(); }
    if (action === 'edit-instance') openInstanceForm(state.instances.find(i=>i.id===Number(target.dataset.id)));
    if (action?.startsWith('instance-')) await handleInstanceAction(action, target);
    if (action === 'create-subscription') openSubscriptionForm();
    if (action === 'expand-subscription') { state.expandedSubscription = state.expandedSubscription === target.dataset.slug ? null : target.dataset.slug; renderSubscriptions(); }
    if (action === 'edit-subscription') openSubscriptionForm(state.subscriptions.find(s=>s.slug===target.dataset.slug));
    if (action === 'add-entry') openEntryForm(target.dataset.slug);
    if (action === 'delete-entry') { if (confirm('Удалить entry из подписки?')) { await api(`/api/v1/subscriptions/${target.dataset.slug}/entries/${target.dataset.id}`,{method:'DELETE'}); await loadSubscriptions(); toast('Entry удалён'); } }
    if (action === 'move-entry') { const sub=state.subscriptions.find(item=>item.slug===target.dataset.slug);const ids=sub.entries.map(entry=>entry.id);const index=ids.indexOf(Number(target.dataset.id));const next=target.dataset.dir==='up'?index-1:index+1;if(next>=0&&next<ids.length){[ids[index],ids[next]]=[ids[next],ids[index]];await api(`/api/v1/subscriptions/${sub.slug}/reorder`,{method:'POST',body:JSON.stringify({ids})});await loadSubscriptions();} }
    if (action === 'toggle-entry') { const sub=state.subscriptions.find(item=>item.slug===target.dataset.slug);const entry=sub.entries.find(item=>item.id===Number(target.dataset.id));await api(`/api/v1/subscriptions/${sub.slug}/entries/${entry.id}`,{method:'PUT',body:JSON.stringify({...entry,enabled:target.checked})});await loadSubscriptions(); }
    if (action === 'delete-subscription') { if (confirm(`Удалить подписку ${target.dataset.slug}? Yandex mirror будет удалён первым; при ошибке удаление подписки отменится.`)) { await api(`/api/v1/subscriptions/${target.dataset.slug}`,{method:'DELETE'}); await loadSubscriptions(); toast('Подписка и Yandex mirror удалены'); } }
    if (action === 'subscription-qr') await showSubscriptionQR(target.dataset.slug, target.dataset.format || 'client');
    if (action === 'sync-mirror') { await api(`/api/v1/subscriptions/${target.dataset.slug}/mirror/sync`,{method:'POST'}); await loadSubscriptions(); toast('Mirror синхронизирован'); }
    if (action === 'sync-mirror-all') await syncAllMirrors();
    if (action === 'export-subscriptions') downloadAuthenticated('/api/v1/subscriptions/export','olcrtc-subscriptions.json');
    if (action === 'import-subscriptions') importSubscriptions();
    if (action === 'copy') { await copyText(target.dataset.value); toast('Скопировано'); }
    if (action === 'scroll-setting') document.getElementById(target.dataset.target)?.scrollIntoView({behavior:'smooth'});
    if (action === 'regenerate-cert') await regenerateCertificate();
    if (action === 'revoke-sessions') { if(confirm('Завершить все активные сессии, включая текущую?')){await api('/api/v1/auth/sessions',{method:'DELETE'});state.user=null;renderLogin('Все сессии завершены.');} }
    if (action === 'create-backup') await createBackup();
    if (action === 'check-updates') await checkUpdates();
    if (action === 'install-update') await installUpdate(target.dataset.bundle || '');
    if (action === 'rollback-update') { if(confirm('Выполнить rollback на предыдущий bundle?')){state.updateOperation=await api('/api/v1/updates/rollback',{method:'POST'});refreshSettingsOperationViews();toast('Rollback запущен');} }
    if (action === 'wb-install') { state.wbOperation=await api('/api/v1/automation/components/install',{method:'POST'});refreshSettingsOperationViews();toast('Установка automation components запущена'); }
    if (action === 'wb-remove') { if(confirm('Удалить automation components и browser profiles?')){state.wbOperation=await api('/api/v1/automation/components/remove',{method:'POST'});refreshSettingsOperationViews();toast('Удаление automation components запущено');} }
    if (action === 'wb-session-create') await runAutomationSession('wbstream','create');
    if (action === 'wb-playwright-refresh') await runAutomationSession('wbstream','refresh');
    if (action === 'telemost-session-create') await runAutomationSession('telemost','create');
    if (action === 'automation-session-stop') await stopAutomationSession(target.dataset.provider);
    if (action === 'wb-token') openWBTokenModal();
    if (action === 'wb-fill-instance') await fillWBInstanceForm();
    if (action === 'telemost-fill-instance') await fillTelemostInstanceForm();
    if (action === 'wb-profile-reset') await resetAutomationProfile('wbstream','WB Stream');
    if (action === 'telemost-profile-reset') await resetAutomationProfile('telemost','Telemost');
    if (action === 'generate-jitsi-room') { const form=document.querySelector('form[data-form="instance"]');if(form){const bytes=new Uint8Array(10);crypto.getRandomValues(bytes);const name=Array.from(bytes,b=>b.toString(16).padStart(2,'0')).join('');form.elements.provider.value='jitsi';form.elements.room_id.value=`https://meet.jit.si/olc-${name}`;} }
    if (action === 'refresh-logs') await refreshLogs();
    if (action === 'toggle-logs') { state.logsPaused=!state.logsPaused; stopPolling(); renderLogsPage(document.querySelector('#log-output')?.textContent || ''); }
    if (action === 'copy-logs') { await copyText(document.querySelector('#log-output')?.textContent || '');toast('Журнал скопирован'); }
  } catch (error) { toast('Операция не выполнена', error.message, 'error'); }
});

app.addEventListener('focusout', normalizeInstanceRoomField);
app.addEventListener('change', normalizeInstanceRoomField);

app.addEventListener('submit', async event => {
  const form = event.target.closest('form[data-form]');
  if (!form) return;
  event.preventDefault();
  const submit = form.querySelector('[type="submit"]'); if (submit) submit.disabled = true;
  try {
    if (form.dataset.form === 'login') {
      const d=new FormData(form);const result=await api('/api/v1/auth/login',{method:'POST',body:JSON.stringify({username:d.get('username'),password:d.get('password')})});state.user=result.username;state.csrf=result.csrf_token;state.updateNoticeCatalog=null;state.updateNoticeDismissed=false;state.page='dashboard';await App();
    }
    if (form.dataset.form === 'instance') {
      const payload=instancePayload(form);const id=form.dataset.id;const result=await api(id?`/api/v1/instances/${id}`:'/api/v1/instances',{method:id?'PUT':'POST',body:JSON.stringify(payload)});closeModal();await loadInstances();toast(id?'Инстанс обновлён':'Инстанс создан',result.warning || 'Конфигурация сохранена');
    }
    if (form.dataset.form === 'subscription') {
      const d=new FormData(form);const slug=form.dataset.slug;const payload={name:d.get('name').trim(),slug:slug || d.get('slug').trim(),refresh:d.get('refresh'),color:d.get('color'),icon:d.get('icon'),enabled:d.has('enabled'),mirror_enabled:d.has('mirror_enabled'),omit_client_auth_token:d.has('omit_client_auth_token')};await api(slug?`/api/v1/subscriptions/${slug}`:'/api/v1/subscriptions',{method:slug?'PUT':'POST',body:JSON.stringify(payload)});closeModal();await loadSubscriptions();toast('Подписка сохранена');
    }
    if (form.dataset.form === 'entry') {
      const d=new FormData(form);const linked=d.get('source_type')==='linked';const payload={source_instance_id:linked?Number(d.get('source_instance_id')):null,raw_uri:linked?'':d.get('raw_uri').trim(),name:d.get('name'),comment:d.get('comment'),ip:d.get('ip'),color:d.get('color'),enabled:d.has('enabled'),sort_order:999};await api(`/api/v1/subscriptions/${form.dataset.slug}/entries`,{method:'POST',body:JSON.stringify(payload)});closeModal();await loadSubscriptions();toast('Entry OLCRTC Client добавлен');
    }
    if (form.dataset.form === 'credentials') { const d=new FormData(form);await api('/api/v1/auth/credentials',{method:'PUT',body:JSON.stringify({username:d.get('username'),current_password:d.get('current_password'),new_password:d.get('new_password')})});state.user=null;renderLogin('Credentials изменены. Войдите снова.'); }
    if (form.dataset.form === 'automation-proxy') { const d=new FormData(form);state.automationSettings=await api('/api/v1/automation/settings',{method:'PUT',body:JSON.stringify({proxy_mode:d.get('proxy_mode'),proxy_address:d.get('proxy_address'),proxy_username:d.get('proxy_username'),proxy_password:d.get('proxy_password'),clear_proxy_password:d.has('clear_proxy_password')})});renderSettings();toast('Browser proxy сохранён'); }
    if (form.dataset.form === 'yandex') { const d=new FormData(form);await api('/api/v1/settings',{method:'PUT',body:JSON.stringify({yandex_enabled:d.has('yandex_enabled'),yandex_base_path:d.get('yandex_base_path'),yandex_oauth_token:d.get('yandex_oauth_token'),clear_yandex_token:d.has('clear_yandex_token')})});await loadSettings();toast('Yandex settings сохранены'); }
    if (form.dataset.form === 'instance-settings') { const d=new FormData(form);await api('/api/v1/settings',{method:'PUT',body:JSON.stringify({max_instances:Number(d.get('max_instances'))})});await loadSettings();toast('Лимит сохранён'); }
    if (form.dataset.form === 'theme') { const d=new FormData(form);const theme=d.get('theme');await api('/api/v1/settings',{method:'PUT',body:JSON.stringify({theme})});applyTheme(theme);localStorage.setItem('olcrtc-theme',theme);toast('Тема применена'); }
    if (form.dataset.form === 'https-settings') { const d=new FormData(form);const before=state.settings?.https||{};const payload={public_ip:d.get('public_ip'),public_port:Number(d.get('public_port')),public_origin:d.get('public_origin').trim(),panel_path:d.get('panel_path'),subscription_path:d.get('subscription_path')};const linksChanged=payload.public_origin!==before.public_origin||payload.panel_path!==before.panel_path||payload.subscription_path!==before.subscription_path;if(linksChanged&&!confirm('Изменение публичного origin или путей потребует перезапуска. Старые закладки, ссылки и QR-коды перестанут указывать на новый адрес. Сохранить?'))return;await api('/api/v1/settings',{method:'PUT',body:JSON.stringify(payload)});await loadSettings();toast('HTTPS settings сохранены',linksChanged?'Перезапустите panel service, чтобы применить новые URL.':'Перезапустите panel при смене порта.'); }
    if (form.dataset.form === 'wb-token') { const d=new FormData(form);const result=await api('/api/v1/automation/wbstream/token/refresh',{method:'POST',body:JSON.stringify({token:d.get('token')})});closeModal();toast('WB token обновлён',wbApplySummary(result));if(state.page==='settings'){stopPolling();await loadSettings();}else if(state.page==='instances'){await loadInstances();} }
  } catch (error) { toast('Ошибка формы', error.message, 'error'); }
  finally { if (submit && document.body.contains(submit)) submit.disabled = false; }
});

app.addEventListener('input', event => {
  if (event.target.dataset.filter) { state.instanceFilters[event.target.dataset.filter]=event.target.value;renderInstances(); }
  if (event.target.dataset.role === 'logs-search') refreshLogs();
  if (event.target.closest('form[data-form="https-settings"]')) updatePublicURLPreview(event.target.form);
});

app.addEventListener('change', event => {
  if (event.target.dataset.filter) { state.instanceFilters[event.target.dataset.filter]=event.target.value;renderInstances(); }
  if (event.target.dataset.role === 'logs-unit') { state.logsUnit=event.target.value;refreshLogs(); }
  if (event.target.dataset.role === 'logs-lines') refreshLogs();
  if (event.target.dataset.role === 'logs-level') { state.logsLevel=event.target.value;refreshLogs(); }
  if (event.target.dataset.role === 'entry-source') { document.querySelector('[data-entry-linked]')?.classList.toggle('hidden',event.target.value!=='linked');document.querySelector('[data-entry-manual]')?.classList.toggle('hidden',event.target.value!=='manual'); }
  if (event.target.dataset.role === 'dns-preset' || event.target.name === 'provider') syncInstanceFormFields(event.target.closest('form[data-form="instance"]'));
});

window.addEventListener('hashchange', () => { const page=location.hash.replace('#','') || 'dashboard';if(page!==state.page)navigate(page,false); });

async function handleInstanceAction(action, target) {
  const id=Number(target.dataset.id);const item=state.instances.find(i=>i.id===id);const simple=action.replace('instance-','');
  if (['start','stop','restart','rotate-key','rotate-client-id','reset-traffic','diagnostics'].includes(simple)) {
    if (simple==='rotate-key'&&!confirm('Сменить encryption key? Linked subscriptions обновятся автоматически.')) return;
    if (simple==='rotate-client-id'&&!confirm('Сменить client_id? Инстанс будет перезапущен, linked subscriptions и Yandex mirrors обновятся.')) return;
    if (simple==='reset-traffic'&&!confirm('Сбросить точные traffic counters?')) return;
    const result=await api(`/api/v1/instances/${id}/${simple}`,{method:'POST'});
    if(simple==='diagnostics'){openModal('Диагностика provider',`<pre class="payload-box">${esc(JSON.stringify(result,null,2))}</pre>`);return;}
    await loadInstances();toast('Операция выполнена');return;
  }
  if (simple==='delete') { const name=prompt(`Для удаления введите точное имя: ${item.name}`);if(name!==item.name)return;await api(`/api/v1/instances/${id}`,{method:'DELETE',body:JSON.stringify({confirm_name:name})});await loadInstances();toast('Инстанс удалён'); }
  if (simple==='change-room') { const value=prompt('Новый Room ID / URL',item.room_id);if(!value)return;const room=normalizeRoomID(item.provider,value);if(!room)return;await api(`/api/v1/instances/${id}/change-room`,{method:'POST',body:JSON.stringify({room_id:room})});await loadInstances();toast('Room ID изменён'); }
  if (simple==='uri') { const format=target.dataset.format||'olcbox';const result=await api(`/api/v1/instances/${id}/uri?format=${format}`);openQRPayloadModal(`${format==='client'?'OLCRTC Client':'OLCBOX'} URI`,'',result.uri,format==='client'?maskClientURI:value=>value); }
  if (simple==='qr') await showInstanceQR(id,target.dataset.format||'olcbox');
  if (simple==='logs') { state.logsUnit=`instance:${id}`;await navigate('logs'); }
}

async function showInstanceQR(id,format){
  const result=await api(`/api/v1/instances/${id}/uri?format=${format}`);
  const src=panelURL(`/api/v1/instances/${id}/qr?format=${format}`);
  const warning=format==='client'&&result.uri.includes('&a=')?'Этот QR содержит полный WB auth token и является credential.':'';
  const appLink=format==='client'?'https://github.com/Oleglog/Olcrtc_client':'https://github.com/alananisimov/olcbox';
  openQRPayloadModal(format==='client'?'QR OLCRTC Client':'QR OLCBOX',src,result.uri,format==='client'?maskClientURI:value=>value,warning,appLink);
}

async function showSubscriptionQR(slug,format='client'){
  const sub=state.subscriptions.find(item=>item.slug===slug);
  let warning='';
  if(format==='client'&&sub?.mirror_enabled){
    try{await api(`/api/v1/subscriptions/${encodeURIComponent(slug)}/mirror/sync`,{method:'POST'});}
    catch(error){warning=`Yandex mirror не удалось обновить: ${error.message}. QR использует прямой URL и последнее подтверждённое состояние mirror.`;}
  }
  const result=await api(`/api/v1/subscriptions/${encodeURIComponent(slug)}/payload?format=${format}`);
  const src=panelURL(`/api/v1/subscriptions/${encodeURIComponent(slug)}/qr?format=${format}`);
  if(format==='olcbox')warning='QR содержит bearer-secret URL OLCBOX. Он загружает plain-text sub.md с обычными olcrtc:// URI.';
  const appLink=format==='client'?'https://github.com/Oleglog/Olcrtc_client':'https://github.com/alananisimov/olcbox';
  openQRPayloadModal(format==='olcbox'?'QR подписки OLCBOX':'QR подписки OLCRTC Client',src,result.payload,format==='olcbox'?value=>value:maskSubscriptionBundle,warning,appLink);
}

function openQRPayloadModal(title,src,payload,masker=value=>value,warning='',appLink=''){
  const masked=masker(payload),secret=masked!==payload;
  openModal(title,`${warning?`<div class="notice" style="margin-bottom:14px">${esc(warning)}</div>`:''}${src?`<div class="qr-wrap"><img src="${src}" alt="${attr(title)}"><a class="btn" href="${src}" download>Скачать PNG</a></div>`:''}${appLink?`<div class="notice info" style="margin:10px 0 14px">Загрузить клиент: <a href="${attr(appLink)}" target="_blank" rel="noopener">${esc(appLink)}</a></div>`:''}<p class="field-label">URI / URL / payload</p><div style="position:relative"><pre class="payload-box" id="qr-payload-value">${esc(masked)}</pre><button class="btn btn-small" type="button" id="qr-uri-copy" style="position:absolute;top:7px;right:7px" title="Скопировать">⎘ Копировать</button></div><div class="form-actions"><button class="btn ${secret?'':'hidden'}" type="button" id="qr-payload-show">Показать</button><button class="btn btn-primary ${secret?'hidden':''}" type="button" id="qr-payload-copy">Копировать</button></div>`);
  const value=document.querySelector('#qr-payload-value'),show=document.querySelector('#qr-payload-show'),copy=document.querySelector('#qr-payload-copy'),uriCopy=document.querySelector('#qr-uri-copy');
  if(show)show.addEventListener('click',()=>{value.textContent=payload;show.remove();copy.classList.remove('hidden');});
  if(copy)copy.addEventListener('click',async()=>{await copyText(payload);toast('Payload скопирован');});
  if(uriCopy)uriCopy.addEventListener('click',async()=>{await copyText(payload);toast('Payload скопирован');});
}

function maskClientURI(value){return String(value).replace(/([?&](?:a|auth_token|auth\.token)=)[^&#]*/i,'$1••••••••');}
function maskSubscriptionBundle(value){try{const payload=JSON.parse(value);if(payload.mk){payload.mk='••••••••';return JSON.stringify(payload);}return value;}catch{return value;}}

async function regenerateCertificate(){const ip=prompt('Публичный IP для SAN',state.settings?.https?.public_ip || '');if(!ip)return;const result=await api('/api/v1/system/certificate/regenerate',{method:'POST',body:JSON.stringify({public_ip:ip})});openModal('Сертификат создан',`<div class="notice">Перезапустите panel service, чтобы новый leaf certificate начал использоваться.</div><p class="field-label">Fingerprint</p><div class="fingerprint mono">${esc(result.server_fingerprint)}</div>`);}
async function createBackup(){const result=await api('/api/v1/system/backup',{method:'POST'});toast('Backup создан');window.location.href=result.download_url;}
async function checkUpdates(){state.releases=await api('/api/v1/updates/releases');if(state.page==='settings'&&state.settings){renderSettings();toast('Список релизов обновлён');}else{openModal('Доступные релизы',releaseCatalogHTML(state.releases,false),true);}}
async function installUpdate(bundleID=''){const item=bundleID?(state.releases?.items||[]).find(release=>release.bundle_id===bundleID):(state.releases?.items||[]).find(release=>release.latest&&!release.current);const id=bundleID||item?.bundle_id;if(!id)throw new Error('Нет доступного release bundle');if(!confirm(`Установить release ${id}? Активные инстансы будут перезапущены.`))return;state.updateOperation=await api('/api/v1/updates/install',{method:'POST',body:JSON.stringify({bundle_id:id})});if(state.page!=='settings')closeModal();refreshSettingsOperationViews();toast(item?.latest?'Обновление запущено':'Установка выбранного релиза запущена',id);}

function openWBTokenModal(){openModal('Обновить общий WB token вручную',`<form data-form="wb-token"><div class="field"><label>Bearer token</label><textarea class="textarea mono" name="token" required></textarea><span class="field-hint">Аварийный fallback. Token хранится зашифрованно, best-effort применяется ко всем WB-инстансам и входит только в их URI/QR OLCRTC Client.</span></div><div class="form-actions"><button class="btn" type="button" data-action="close-modal">Отмена</button><button class="btn btn-primary" type="submit">Сохранить и применить</button></div></form>`);}

async function fillWBInstanceForm(){
  const form=document.querySelector('form[data-form="instance"]');if(!form)return;
  const session=await api('/api/v1/automation/wbstream/session',{method:'POST',body:JSON.stringify({action:'create'})});window.open(session.novnc_url,'olcrtc-wb-novnc','noopener');toast('WB-сессия запущена','Войдите в WB Stream и пройдите CAPTCHA.');
  const current=await waitForAutomationSession('wbstream');const room=current.state?.room_id||'',token=current.state?.token||'';if(!token)throw new Error('WB token не получен из успешной Playwright-сессии');if(room)form.elements.room_id.value=room;form.elements.auth_token.value=token;form.elements.provider.value='wbstream';syncInstanceFormFields(form);toast('WB данные получены',`Room ID и WB account token заполнены. ${wbApplySummary(current.state?.applied)}`);
}

async function fillTelemostInstanceForm(){
  const form=document.querySelector('form[data-form="instance"]');if(!form)return;
  const session=await api('/api/v1/automation/telemost/session',{method:'POST',body:JSON.stringify({action:'create'})});window.open(session.novnc_url,'olcrtc-telemost-novnc','noopener');toast('Telemost-сессия запущена','Если Яндекс запросит вход, завершите его через noVNC.');
  const current=await waitForAutomationSession('telemost');const room=normalizeRoomID('telemost',current.state?.room_id||'');if(!room)throw new Error('Room ID не получен из успешной Telemost-сессии');form.elements.provider.value='telemost';form.elements.transport.value='vp8channel';form.elements.room_id.value=room;form.elements.auth_token.value='';syncInstanceFormFields(form);toast('Комната Telemost создана',`Room ID ${room} подставлен в форму инстанса.`);
}

async function runAutomationSession(provider,action){
  const session=await api(`/api/v1/automation/${provider}/session`,{method:'POST',body:JSON.stringify({action})});
  state.automationSession=session;
  refreshAutomationSessionView();
  window.open(session.novnc_url,provider==='telemost'?'olcrtc-telemost-novnc':'olcrtc-wb-novnc','noopener');
  const title=provider==='telemost'?'Playwright: создать комнату Telemost':action==='create'?'Playwright: создать WB комнату':'Playwright: обновить WB token';
  openModal(title,`<div class="notice info">Сессия активна до ${formatDate(session.expires_at)}. Выполните login/CAPTCHA в noVNC.</div><div class="form-actions"><a class="btn btn-primary" href="${attr(session.novnc_url)}" target="_blank" rel="noopener">Открыть noVNC</a><button class="btn btn-danger" data-action="automation-session-stop" data-provider="${attr(provider)}">Остановить браузер</button></div><div class="operation-card running" id="automation-session-state">Ожидание Chromium...</div>`);
  const current=await waitForAutomationSession(provider,statePayload=>{const root=document.querySelector('#automation-session-state');if(root)root.textContent=`${statePayload.message||statePayload.phase||'Ожидание'} · ${statePayload.percent||0}%`;});
  state.automationSession=current;
  refreshAutomationSessionView();
  const root=document.querySelector('#automation-session-state');if(root){root.className='operation-card completed';root.textContent=provider==='telemost'?'Комната Telemost создана.':`Готово. ${wbApplySummary(current.state?.applied)}`;}
  toast(provider==='telemost'?'Комната Telemost создана':'WB token получен через Playwright',provider==='telemost'?(current.state?.room_id||''):wbApplySummary(current.state?.applied));
  if(state.page==='settings'){stopPolling();await loadSettings();}else if(state.page==='instances'){await loadInstances();}
  return current;
}

async function waitForAutomationSession(provider,onProgress=()=>{}){
  const label=provider==='telemost'?'Telemost':'WB';
  for(let attempt=0;attempt<450;attempt++){
    await new Promise(resolve=>setTimeout(resolve,2000));
    const current=await api(`/api/v1/automation/${provider}/session`);const worker=current.state||{};onProgress(worker);
    if(worker.phase==='success')return current;
    if(worker.phase==='error')throw new Error(worker.message||`${label} automation завершилась с ошибкой`);
    if(!current.active)throw new Error(`Время ${label}-сессии истекло`);
  }
  throw new Error(`${label} automation не завершилась вовремя`);
}

async function stopAutomationSession(provider=state.automationSession?.provider||'wbstream'){
  await api(`/api/v1/automation/${provider}/session`,{method:'DELETE'});
  state.automationSession={active:false,provider,state:{}};
  refreshAutomationSessionView();
  const root=document.querySelector('#automation-session-state');if(root){root.className='operation-card muted';root.textContent='Browser-сессия остановлена';}
  toast('Browser-сессия остановлена');
}

async function triggerAutoSetup(){
  await api('/api/v1/settings/trigger-auto-setup',{method:'POST'});
  state.autoSetup={visible:true,state:{step:'welcome',progress:0,completed_steps:[]},poller:null,wbRooms:[],telemostRoom:'',skipTelemost:false};
  renderAutoSetupWizard();
  startAutoSetupPolling();
}

async function resetAutomationProfile(provider,label){
  if(!confirm(`Очистить Chromium профиль ${label}? Сохранённые cookie и активная сессия этого provider будут удалены. При следующем запуске Playwright потребуется войти заново.`))return;
  await api(`/api/v1/automation/${provider}/profile/reset`,{method:'POST'});
  toast(`Профиль ${label} очищен`,'Профиль другого provider сохранён.');
  if(state.page==='settings'){stopPolling();await loadSettings();}
}

function wbApplySummary(result={}){const updated=result?.updated?.length||0,subscriptions=result?.subscriptions_updated?.length||0,mirrors=result?.mirrors_scheduled?.length||0;const failed=Object.entries(result?.failed||{});return `Обновлено WB-инстансов: ${updated}; подписок: ${subscriptions}; mirrors запланировано: ${mirrors}${failed.length?`. Ошибки: ${failed.map(([id,message])=>`#${id} ${message}`).join('; ')}`:'. Ошибок нет.'}`;}

function openModal(title, body, wide=false){const root=document.querySelector('#modal-root');if(!root)return;root.innerHTML=`<div class="modal-backdrop"><section class="modal ${wide?'wide':''}" role="dialog" aria-modal="true" aria-labelledby="modal-title"><header class="modal-header"><h2 id="modal-title">${esc(title)}</h2><button class="modal-close" data-action="close-modal" aria-label="Закрыть">×</button></header><div class="modal-body">${body}</div></section></div>`;setTimeout(()=>root.querySelector('input,select,textarea,button')?.focus(),0);}
function closeModal(){const root=document.querySelector('#modal-root');if(root)root.innerHTML='';}
function toast(title,message='',type='success'){const region=document.querySelector('.toast-region');if(!region)return;const el=document.createElement('div');el.className=`toast ${type}`;el.innerHTML=`<strong>${esc(title)}</strong>${message?`<span>${esc(message)}</span>`:''}`;region.append(el);setTimeout(()=>el.remove(),5000);}

function field(name,label,value='',type='text',placeholder='',required=false,disabled=false){return `<div class="field"><label for="f-${name}">${esc(label)}</label><input class="input" id="f-${name}" name="${attr(name)}" type="${attr(type)}" value="${attr(value ?? '')}" placeholder="${attr(placeholder)}" ${required?'required':''} ${disabled?'disabled':''}></div>`;}
function settingsSection(id,title,body){return `<section class="panel settings-section" id="${id}"><div class="panel-header"><h2>${esc(title)}</h2></div><div class="panel-body">${body}</div></section>`;}
function detail(label,value,cls=''){return `<div class="detail-item"><span>${esc(label)}</span><strong class="${cls}">${typeof value==='string'&&value.startsWith('<')?value:esc(value ?? '—')}</strong></div>`;}
function summary(label,value){return `<div class="panel summary-card"><span>${esc(label)}</span><strong>${esc(value)}</strong></div>`;}
function emptyState(icon,title,text,action=''){return `<div class="empty-state"><div class="empty-icon">${icon}</div><h3>${esc(title)}</h3><p>${esc(text)}</p>${action}</div>`;}
function options(values,current){return values.map(v=>`<option value="${attr(v)}" ${v===current?'selected':''}>${esc(v)}</option>`).join('');}
function compatibility(provider,transport){const map={'telemost:datachannel':'Не поддерживается upstream','telemost:seichannel':'Не поддерживается upstream','telemost:videochannel':'Медленно и нестабильно','wbstream:datachannel':'Требуется moderator token','jitsi:vp8channel':'Нестабильно','jitsi:seichannel':'Нестабильно','jitsi:videochannel':'Нестабильно'};return map[`${provider}:${transport}`]||'';}
function clientQRUnavailable(item){const supported=(item.provider==='wbstream'||item.provider==='telemost')&&item.transport==='vp8channel'||item.provider==='jitsi'&&item.transport==='datachannel';if(!supported)return `комбинация ${item.provider} + ${item.transport} не поддерживается OLCRTC Client`;if(item.provider==='wbstream'&&!item.auth_token_set&&!item.omit_client_auth_token)return 'для WB сначала получите auth token через Playwright или введите его вручную';return '';}
function quotaLabel(item){if(item.expires_at&&new Date(item.expires_at)<new Date())return '<span class="chip red">expired</span>';if(item.quota_bytes&&item.total_bytes>=item.quota_bytes)return '<span class="chip red">exceeded</span>';return item.quota_bytes?`${formatBytes(item.quota_bytes)}`:'∞';}
function statusLabel(value){return ({running:'Запущен',stopped:'Остановлен',starting:'Запуск',stopping:'Остановка',failed:'Ошибка',updating:'Обновление',unknown:'Неизвестно'})[value]||value;}
function formatBytes(value=0){const units=['Б','КБ','МБ','ГБ','ТБ','ПБ'];let n=Number(value)||0,i=0;while(n>=1024&&i<units.length-1){n/=1024;i++;}return `${i? n.toFixed(n>=100?0:n>=10?1:2):Math.round(n)} ${units[i]}`;}
function formatUptime(seconds=0){seconds=Math.max(0,Math.floor(seconds||0));const d=Math.floor(seconds/86400),h=Math.floor(seconds%86400/3600),m=Math.floor(seconds%3600/60),s=seconds%60;return d?`${d}д ${h}ч`:h?`${h}ч ${m}м`:`${m}м ${String(s).padStart(2,'0')}с`;}
function formatDate(value){try{return new Intl.DateTimeFormat('ru',{dateStyle:'medium',timeStyle:'short'}).format(new Date(value));}catch{return '—';}}
function localDateTime(value){const d=new Date(value);const pad=n=>String(n).padStart(2,'0');return `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;}
function shortSHA(value=''){return value?`<span class="mono" title="${attr(value)}">${esc(value.slice(0,12))}</span>`:'—';}
function shortFingerprint(value=''){return value?`<span class="mono" title="${attr(value)}">${esc(value.slice(0,23))}…</span>`:'—';}
function percent(used,total){return total?clamp(used/total*100,0,100):0;}
function clamp(value,min,max){return Math.min(max,Math.max(min,Number(value)||0));}
function sum(items,key){return items.reduce((total,item)=>total+(Number(item[key])||0),0);}
function plural(n,one,few,many){const x=Math.abs(n)%100,y=x%10;return x>10&&x<20?many:y>1&&y<5?few:y===1?one:many;}
function esc(value){return String(value ?? '').replace(/[&<>'"]/g,ch=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[ch]));}
function attr(value){return esc(value).replace(/`/g,'&#96;');}
function applyTheme(theme){document.documentElement.dataset.theme=theme==='light'?'light':'dark';}
function stopPolling(){if(state.poller){clearInterval(state.poller);state.poller=null;}if(state.uptimeTicker){clearInterval(state.uptimeTicker);state.uptimeTicker=null;}}
async function copyText(value){if(navigator.clipboard?.writeText)return navigator.clipboard.writeText(value);const area=document.createElement('textarea');area.value=value;document.body.append(area);area.select();document.execCommand('copy');area.remove();}
async function downloadAuthenticated(url,filename){const response=await fetch(panelURL(url),{credentials:'same-origin'});if(!response.ok)throw new Error('Не удалось скачать файл');const blob=await response.blob();const link=document.createElement('a');link.href=URL.createObjectURL(blob);link.download=filename;link.click();setTimeout(()=>URL.revokeObjectURL(link.href),1000);}
function importSubscriptions(){const input=document.createElement('input');input.type='file';input.accept='application/json,.json';input.onchange=async()=>{try{const text=await input.files[0].text();const payload=JSON.parse(text);const result=await api('/api/v1/subscriptions/import',{method:'POST',body:JSON.stringify(payload)});await loadSubscriptions();toast('Импорт завершён',`Создано: ${result.created}`);}catch(error){toast('Ошибка импорта',error.message,'error');}};input.click();}

function updatePublicURLPreview(form) {
  const preview=form?.querySelector('[data-role="public-url-preview"]');if(!preview)return;
  const explicit=form.elements.public_origin.value.trim().replace(/\/$/,'');
  let host=form.elements.public_ip.value.trim()||'127.0.0.1';
  if(host.includes(':')&&!host.startsWith('['))host=`[${host}]`;
  const origin=explicit||`https://${host}:${form.elements.public_port.value||8443}`;
  const panelPath=form.elements.panel_path.value||'/';
  const subscriptionPath=form.elements.subscription_path.value||'/sub';
  preview.innerHTML=`Панель: ${esc(origin+panelPath)}<br>Подписка: ${esc(origin+subscriptionPath+'/example-slug')}`;
}

async function refreshSettingsOperations(){
  if(state.page!=='settings'||state.settingsPolling)return;
  state.settingsPolling=true;
  const previousWB=state.wbOperation?.state,previousUpdate=state.updateOperation?.state;
  try{
    const [wbOperation,updateOperation,automationSession]=await Promise.all([
      api('/api/v1/automation/components/progress').catch(()=>null),
      api('/api/v1/updates/progress').catch(()=>null),
      api('/api/v1/wb/session').catch(()=>null),
    ]);
    if(wbOperation)state.wbOperation=wbOperation;
    if(updateOperation)state.updateOperation=updateOperation;
    if(automationSession)state.automationSession=automationSession;
    const wbFinished=previousWB==='running'&&state.wbOperation?.state!=='running';
    const updateFinished=previousUpdate==='running'&&state.updateOperation?.state!=='running';
    if(wbFinished||updateFinished){
      const [settings,releases]=await Promise.all([
        api('/api/v1/settings').catch(()=>null),
        api('/api/v1/updates/releases').catch(()=>null),
      ]);
      if(settings)state.settings=settings;
      if(releases)state.releases=releases;
      renderSettings();
    }else{
      refreshSettingsOperationViews();
    }
  }finally{state.settingsPolling=false;}
}

function refreshSettingsOperationViews(){
  const updateRunning=state.updateOperation?.state==='running';
  const wbRunning=state.wbOperation?.state==='running';
  const updateRoot=document.querySelector('#update-operation');if(updateRoot)updateRoot.innerHTML=operationProgressHTML(state.updateOperation,'Обновление');
  const wbRoot=document.querySelector('#wb-operation');if(wbRoot)wbRoot.innerHTML=operationProgressHTML(state.wbOperation,'Automation components');
  const releasesRoot=document.querySelector('#release-list');if(releasesRoot)releasesRoot.innerHTML=releaseCatalogHTML(state.releases||{configured:false,items:[]},updateRunning);
  const latest=state.releases?.items?.length?[...state.releases.items].sort((a,b)=>new Date(b.published_at)-new Date(a.published_at))[0]:null;
  const checkButton=document.querySelector('[data-action="check-updates"]');if(checkButton)checkButton.disabled=updateRunning;
  const updateButton=document.querySelector('[data-action="install-update"]:not([data-bundle])');if(updateButton)updateButton.disabled=updateRunning||!latest||latest.current;
  const rollbackButton=document.querySelector('[data-action="rollback-update"]');if(rollbackButton)rollbackButton.disabled=updateRunning||!state.releases?.rollback_available;
  document.querySelectorAll('[data-action="wb-install"],[data-action="wb-remove"]').forEach(button=>{button.disabled=wbRunning||!state.settings?.wb?.supported;});
  refreshAutomationSessionView();
}

function refreshAutomationSessionView(){
  const root=document.querySelector('#automation-session');if(root)root.innerHTML=automationSessionHTML(state.automationSession||{});
  const unavailable=!state.settings?.wb?.installed||Boolean(state.automationSession?.active);
  document.querySelectorAll('[data-action="wb-session-create"],[data-action="wb-playwright-refresh"],[data-action="telemost-session-create"]').forEach(button=>{button.disabled=unavailable;});
}

function operationProgressHTML(operation={},title='Операция'){
  const current=operation||{};
  if(!current.state||current.state==='idle')return '<div class="operation-card muted">Нет активной операции</div>';
  const value=clamp(current.percent,0,100);
  const labels={running:'Выполняется',completed:'Завершено',failed:'Ошибка'};
  const message=current.error||current.message||labels[current.state]||current.state;
  const output=current.output?`<details class="operation-output"><summary>Технический вывод</summary><pre class="payload-box">${esc(current.output)}</pre></details>`:'';
  return `<div class="operation-card ${attr(current.state)}"><div class="operation-heading"><strong>${esc(title)}: ${esc(labels[current.state]||current.state)}</strong><span>${Math.round(value)}%</span></div><div class="progress operation-progress"><span style="width:${value}%"></span></div><p>${esc(message)}</p>${output}</div>`;
}

function releaseCatalogHTML(catalog={},operationRunning=false){
  if(catalog.loading)return '<div class="operation-card muted">Загрузка списка релизов...</div>';
  if(catalog.error)return `<div class="notice" style="margin-top:14px">${esc(catalog.error)}</div>`;
  if(!catalog.configured)return '<div class="notice" style="margin-top:14px">Источник GitHub Releases не настроен.</div>';
  const items=[...(catalog.items||[])].sort((a,b)=>new Date(b.published_at)-new Date(a.published_at));
  if(!items.length)return '<div class="empty-state compact"><p>Опубликованные bundle-релизы не найдены.</p></div>';
  const rows=items.map(item=>`<tr><td><strong class="mono">${esc(item.bundle_id)}</strong>${item.latest?' <span class="chip green">latest</span>':''}${item.current?' <span class="chip blue">current</span>':''}</td><td>${item.published_at?formatDate(item.published_at):'—'}</td><td><a class="btn btn-ghost" href="${attr(item.url)}" target="_blank" rel="noopener">GitHub</a></td><td><button class="btn ${item.latest?'btn-primary':''}" data-action="install-update" data-bundle="${attr(item.bundle_id)}" ${operationRunning||item.current?'disabled':''}>${item.current?'Установлен':item.latest?'Обновить':'Установить эту версию'}</button></td></tr>`).join('');
  return `<div class="release-catalog"><h3>Доступные релизы</h3><div class="table-wrap"><table class="table release-table"><thead><tr><th>Bundle</th><th>Дата</th><th>Страница</th><th>Действие</th></tr></thead><tbody>${rows}</tbody></table></div></div>`;
}
