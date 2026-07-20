const app = document.querySelector('#app');

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
  wbOperation: { state: 'idle', percent: 0 },
  updateOperation: { state: 'idle', percent: 0 },
  settingsPolling: false,
  expandedInstance: null,
  expandedSubscription: null,
  instanceFilters: { search: '', provider: '', transport: '', status: '', quota: '' },
  logsUnit: 'panel',
  logsLevel: '',
  logsPaused: false,
  poller: null,
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
    await navigate(state.page, false);
  } catch (error) {
    renderLogin();
  }
}

async function api(path, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json');
  if (state.csrf && options.method && !['GET', 'HEAD'].includes(options.method)) headers.set('X-CSRF-Token', state.csrf);
  const response = await fetch(path, { credentials: 'same-origin', ...options, headers });
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
          <button class="nav-button" data-action="toggle-theme"><span class="nav-icon">◐</span>Сменить тему</button>
          <button class="nav-button" data-action="logout"><span class="nav-icon">↪</span>Выход</button>
          <div class="sidebar-version">Пользователь: ${esc(state.user || '')}</div>
        </div>
      </aside>
      <div>
        <header class="mobile-topbar"><button class="icon-button" data-action="drawer" aria-label="Открыть меню">☰</button><strong>olcRTC Panel</strong><button class="icon-button" data-action="toggle-theme" aria-label="Сменить тему">◐</button></header>
        <main class="main"><div id="page-content">${content}</div></main>
      </div>
    </div>
    <div id="modal-root"></div>
    <div class="toast-region" aria-live="polite"></div>`;
}

async function navigate(page, push = true) {
  if (!navItems.some(([id]) => id === page)) page = 'dashboard';
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
      <div class="page-header"><div class="page-title"><h1>Дашборд</h1><p>Состояние VPS, панели и управляемых процессов</p></div><div class="header-actions"><button class="btn" data-action="refresh-dashboard">↻ Обновить</button><button class="btn btn-primary" data-action="create-instance">＋ Инстанс</button></div></div>
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
          ${detail('Публичный адрес', `${s.public_ip || 'не задан'}:${s.public_port}`)}${detail('TLS fingerprint', shortFingerprint(s.certificate_fingerprint))}${detail('WB automation', s.wb.installed ? 'Установлена' : (s.wb.supported ? 'Не установлена' : 'Недоступна'))}${detail('Обновления', s.update_configured ? 'Настроены' : 'Manifest не задан')}
        </div></article>
        <article class="panel" style="grid-column:1/-1"><div class="panel-header"><h2>Быстрые действия</h2></div><div class="panel-body quick-actions"><button class="btn btn-primary" data-action="create-instance">＋ Создать инстанс</button><button class="btn" data-page="subscriptions">≋ Подписки</button><button class="btn" data-page="logs">⌁ Открыть журнал</button><button class="btn" data-action="create-backup">▣ Создать backup</button><button class="btn" data-action="check-updates">↻ Проверить обновления</button><button class="btn" data-page="settings">⚙ Настройки</button></div></article>
      </div>
    </section>`;
  if (rebuild || document.querySelector('.gauge-grid')) document.querySelector('#page-content').innerHTML = body;
}

function gauge(value, caption) {
  return `<div class="gauge-item"><div class="gauge" style="--value:${value.toFixed(1)}"><strong>${value.toFixed(1)}%</strong></div><div class="gauge-caption">${esc(caption)}</div></div>`;
}

async function loadInstances() {
  const result = await api('/api/v1/instances');
  state.instances = result.items || [];
  renderInstances();
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
      <div class="page-header"><div class="page-title"><h1>Инстансы</h1><p>Один официальный olcRTC process и YAML на каждый инстанс</p></div><div class="header-actions"><button class="btn" data-action="refresh-instances">↻ Обновить</button><button class="btn btn-primary" data-action="create-instance">＋ Создать инстанс</button></div></div>
      <div class="summary-grid">
        ${summary('Отправлено', formatBytes(upload))}${summary('Получено', formatBytes(download))}${summary('Всего трафика', formatBytes(upload + download))}${summary('Запущено', running)}${summary('Ошибки', failed)}${summary('Всего', state.instances.length)}
      </div>
      <section class="panel">
        <div class="toolbar"><div class="filters"><div class="search"><input class="input" data-filter="search" placeholder="Поиск" value="${attr(f.search)}" aria-label="Поиск инстансов"></div><select class="select" data-filter="provider"><option value="">Все provider</option>${options(['jitsi','telemost','wbstream'], f.provider)}</select><select class="select" data-filter="transport"><option value="">Все transport</option>${options(['datachannel','vp8channel','seichannel','videochannel'], f.transport)}</select><select class="select" data-filter="status"><option value="">Все статусы</option>${options(['running','stopped','failed','unknown'], f.status)}</select><select class="select" data-filter="quota"><option value="">Любая quota</option>${options(['unlimited','limited','exceeded','expired'],f.quota)}</select></div><span class="muted">Найдено: ${filtered.length}</span></div>
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
  return `<tr><td><button class="expand-button" data-action="expand-instance" data-id="${item.id}" aria-label="Раскрыть ${attr(item.name)}">${state.expandedInstance === item.id ? '−' : '+'}</button></td><td>${item.id}</td><td><strong>${esc(item.name)}</strong>${tokenBadge}</td><td><span class="chip ${item.provider === 'wbstream' ? 'purple' : 'green'}">${esc(item.provider)}</span></td><td><span class="chip blue">${esc(item.transport)}</span></td><td class="mono truncate" style="max-width:190px" title="${attr(item.room_id)}">${esc(item.room_id)}</td><td><span class="chip ${esc(item.status)}">${statusLabel(item.status)}</span></td><td>${formatUptime(item.uptime_seconds)}</td><td class="traffic-cell"><div class="traffic-value"><span>${formatBytes(item.total_bytes)}</span><span>${item.quota_bytes ? `${quotaPct.toFixed(0)}%` : '∞'}</span></div><div class="progress"><span style="width:${Math.min(quotaPct,100)}%"></span></div></td><td>${quotaLabel(item)}</td><td><button class="btn btn-ghost btn-icon" data-action="expand-instance" data-id="${item.id}" aria-label="Меню">⋮</button></td></tr>`;
}

function instanceExpanded(item) {
  const clientUnavailable = clientQRUnavailable(item);
  const tokenStatus = item.provider !== 'wbstream' ? 'Не требуется' : !item.auth_token_set ? 'Не задан' : item.auth_token_expired ? `Истёк${item.auth_token_expires_at ? ' · '+formatDate(item.auth_token_expires_at) : ''}` : item.auth_token_expires_at ? `Действует до ${formatDate(item.auth_token_expires_at)}` : 'Задан, срок неизвестен';
  return `<tr class="expanded-row"><td colspan="11"><div class="expanded-content"><div class="expanded-actions">
    ${item.status === 'running' ? `<button class="btn btn-small" data-action="instance-stop" data-id="${item.id}">■ Остановить</button><button class="btn btn-small" data-action="instance-restart" data-id="${item.id}">↻ Перезапустить</button>` : `<button class="btn btn-primary btn-small" data-action="instance-start" data-id="${item.id}">▶ Запустить</button>`}
    <button class="btn btn-small" data-action="edit-instance" data-id="${item.id}">✎ Изменить</button><button class="btn btn-small" data-action="instance-duplicate" data-id="${item.id}">⧉ Дублировать</button><button class="btn btn-small" data-action="instance-qr" data-id="${item.id}" data-format="olcbox">QR OLCBOX</button><button class="btn btn-small" data-action="instance-qr" data-id="${item.id}" data-format="client" ${clientUnavailable ? `disabled title="${attr(clientUnavailable)}"` : 'title="QR содержит данные подключения; для WB — полный auth token"'}>QR OLCRTC Client</button><button class="btn btn-small" data-action="instance-rotate-key" data-id="${item.id}">⌘ Ротация key</button><button class="btn btn-small" data-action="instance-rotate-client-id" data-id="${item.id}">⟳ Ротация client_id</button>${item.provider === 'wbstream' ? `<button class="btn btn-small" data-action="wb-playwright-refresh">↻ Обновить WB token</button>` : ''}<button class="btn btn-small" data-action="instance-change-room" data-id="${item.id}">⌂ Room ID</button><button class="btn btn-small" data-action="instance-diagnostics" data-id="${item.id}">◇ Диагностика</button><button class="btn btn-small" data-action="instance-logs" data-id="${item.id}">⌁ Логи</button><button class="btn btn-small" data-action="instance-reset-traffic" data-id="${item.id}">↺ Сбросить трафик</button><button class="btn btn-danger btn-small" data-action="instance-delete" data-id="${item.id}">Удалить</button>
  </div>${clientUnavailable ? `<div class="notice" style="margin-bottom:14px">QR OLCRTC Client недоступен: ${esc(clientUnavailable)}. QR OLCBOX остаётся доступным.</div>` : item.provider === 'wbstream' ? '<div class="notice" style="margin-bottom:14px">QR OLCRTC Client содержит полный WB auth token. Считайте этот QR credential и передавайте только получателю.</div>' : ''}<div class="expanded-stats">${detail('Client ID', item.client_id, 'mono')}${detail('WB auth token', tokenStatus, item.auth_token_expired ? 'danger-text' : '')}${detail('Upload', formatBytes(item.upload_bytes))}${detail('Download', formatBytes(item.download_bytes))}${detail('Последний трафик', item.last_traffic_at ? formatDate(item.last_traffic_at) : 'Нет данных')}${detail('Reset policy', item.reset_policy)}${detail('DNS', item.dns)}${detail('Совместимость OLCBOX', compatibility(item.provider,item.transport) || 'Стабильная комбинация')}</div></div></td></tr>`;
}

function openInstanceForm(item = null) {
  const i = item || { provider:'jitsi', transport:'datachannel', dns:'8.8.8.8:53', reset_policy:'never', options:{}, liveness:{} };
  openModal(item ? 'Изменить инстанс' : 'Новый инстанс', `
    <form data-form="instance" data-id="${i.id || ''}">
      <div class="form-grid">
        ${field('name','Имя',i.name || '','text','Например: Jitsi RU-1',true)}
        <div class="field"><label>Provider</label><select class="select" name="provider">${options(['jitsi','telemost','wbstream'],i.provider)}</select></div>
        <div class="field"><label>Transport</label><select class="select" name="transport">${options(['datachannel','vp8channel','seichannel','videochannel'],i.transport)}</select></div>
        <div class="field"><label for="f-room_id">Room ID / URL</label><input class="input" id="f-room_id" name="room_id" list="jitsi-presets" value="${attr(i.room_id || '')}" placeholder="https://meet.example/room" required><datalist id="jitsi-presets"><option value="https://meet.jit.si/"><option value="https://meet.small-dm.ru/"><option value="https://meet1.arbitr.ru/"><option value="https://meet.handyweb.org/"></datalist><button class="btn btn-small" type="button" data-action="generate-jitsi-room">Случайная Jitsi room</button></div>
        ${field('dns','DNS upstream',i.dns || '8.8.8.8:53','text','8.8.8.8:53',true)}
        ${field('outbound_proxy','Outbound SOCKS5 / WARP','','password',item ? 'Оставьте пустым, чтобы не менять' : 'socks5://user:pass@host:port')}
        ${field('auth_token','WB account token','','password',item ? 'Оставьте пустым, чтобы не менять' : 'Только WB; входит в QR OLCRTC Client')}
        <div class="field"><label>Traffic reset</label><select class="select" name="reset_policy">${options(['never','daily','weekly','monthly','manual'],i.reset_policy)}</select></div>
        ${field('quota_gb','Quota, GB',i.quota_bytes ? (i.quota_bytes/1073741824).toFixed(2) : '','number','0 = unlimited')}
        ${field('expires_at','Срок действия',i.expires_at ? localDateTime(i.expires_at) : '','datetime-local','Необязательно')}
        <label class="checkbox"><input type="checkbox" name="debug" ${i.debug ? 'checked' : ''}> Debug logging</label>
      </div>
      <details style="margin-top:18px"><summary class="muted" style="cursor:pointer">Расширенные transport и liveness settings</summary><div class="form-grid" style="margin-top:16px">
        ${field('vp8_fps','VP8 FPS',i.options?.vp8_fps || 30,'number')}${field('vp8_batch','VP8 batch',i.options?.vp8_batch || 64,'number')}${field('sei_fps','SEI FPS',i.options?.sei_fps || 30,'number')}${field('sei_batch','SEI batch',i.options?.sei_batch || 64,'number')}${field('sei_fragment','SEI fragment',i.options?.sei_fragment || 900,'number')}${field('sei_ack_ms','SEI ACK, ms',i.options?.sei_ack_ms || 2000,'number')}
        ${field('video_width','Video width',i.options?.video_width || 1920,'number')}${field('video_height','Video height',i.options?.video_height || 1080,'number')}${field('video_fps','Video FPS',i.options?.video_fps || 30,'number')}${field('video_bitrate','Video bitrate',i.options?.video_bitrate || '2M')}
        <div class="field"><label>Video codec</label><select class="select" name="video_codec">${options(['qrcode','tile'],i.options?.video_codec || 'qrcode')}</select></div><div class="field"><label>Video HW</label><select class="select" name="video_hw">${options(['none','nvenc'],i.options?.video_hw || 'none')}</select></div>
        ${field('liveness_interval','Liveness interval',i.liveness?.interval || '10s')}${field('liveness_timeout','Liveness timeout',i.liveness?.timeout || '5s')}${field('liveness_failures','Liveness failures',i.liveness?.failures || 3,'number')}${field('max_session_duration','Max session duration',i.max_session_duration || '')}
      </div></details>
      <div class="notice" style="margin-top:18px">Outbound proxy влияет и на signalling, и на пользовательский трафик. Независимое разделение без изменения upstream невозможно.</div>
      <div class="notice" style="margin-top:18px">Для WB QR OLCRTC Client содержит полный auth token. QR OLCBOX token не содержит.</div>
      <div class="form-actions" style="justify-content:flex-start"><button class="btn" type="button" data-action="wb-fill-instance">Playwright: получить token и создать комнату</button></div>
      <div class="form-actions"><button class="btn" type="button" data-action="close-modal">Отмена</button><button class="btn btn-primary" type="submit">${item ? 'Сохранить' : 'Создать'}</button></div>
    </form>`, true);
}

function instancePayload(form) {
  const d = new FormData(form);
  const num = key => Number(d.get(key) || 0);
  const expires = d.get('expires_at');
  return {
    name: d.get('name').trim(), provider: d.get('provider'), transport: d.get('transport'), room_id: d.get('room_id').trim(), dns: d.get('dns').trim(), outbound_proxy: d.get('outbound_proxy'), auth_token: d.get('auth_token'), reset_policy: d.get('reset_policy'), quota_bytes: Math.max(0, Math.round(num('quota_gb') * 1073741824)), expires_at: expires ? new Date(expires).toISOString() : null, debug: d.has('debug'),
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

function renderSubscriptions() {
  document.querySelector('#page-content').innerHTML = `
    <section class="page"><div class="page-header"><div class="page-title"><h1>Подписки</h1><p>Одна подписка публикуется в форматах OLCRTC Client и OLCBOX</p></div><div class="header-actions"><button class="btn" data-action="import-subscriptions">⇧ Импорт</button><button class="btn" data-action="export-subscriptions">⇩ Экспорт</button><button class="btn btn-primary" data-action="create-subscription">＋ Подписка</button></div></div><div class="notice" style="margin-bottom:16px">OLCRTC Client получает compact URI и optional Yandex mirror. OLCBOX получает отдельный plain-text <code>sub.md</code> endpoint с обычными <code>olcrtc://</code> URI.</div>
    ${state.subscriptions.length ? `<div class="subscription-list">${state.subscriptions.map(subscriptionCard).join('')}</div>` : emptyState('≋','Подписок пока нет','Создайте bearer-secret URL и добавьте linked instances или manual URI.','<button class="btn btn-primary" data-action="create-subscription">Создать подписку</button>')}
    </section>`;
}

function subscriptionCard(sub) {
  const clientURL = `${location.origin}/sub/${sub.slug}`;
  const olcboxURL = `${location.origin}/sub/${sub.slug}/olcbox`;
  return `<article class="panel subscription-card"><div class="subscription-main"><div class="subscription-name"><div class="subscription-icon">${esc(sub.icon || '≋')}</div><div class="truncate"><strong>${esc(sub.name)}</strong><small class="mono truncate">${esc(sub.slug)}</small></div></div><div><small>Трафик</small><strong>${formatBytes(sub.used_bytes || 0)} / ${sub.available_bytes == null ? '∞' : formatBytes((sub.used_bytes||0)+sub.available_bytes)}</strong></div><div><small>Entries</small><strong>${sub.entries?.length || 0}</strong></div><div><small>Mirror Client</small><span class="chip ${esc(sub.mirror_status || 'disabled')}">${esc(sub.mirror_status || 'disabled')}</span></div><div class="toolbar-actions"><button class="btn btn-small" data-action="copy" data-value="${attr(clientURL)}">URL Client</button><button class="btn btn-small" data-action="copy" data-value="${attr(olcboxURL)}">URL OLCBOX</button><button class="btn btn-small btn-icon" data-action="expand-subscription" data-slug="${attr(sub.slug)}">${state.expandedSubscription === sub.slug ? '−' : '+'}</button></div></div>${state.expandedSubscription === sub.slug ? subscriptionExpanded(sub) : ''}</article>`;
}

function subscriptionExpanded(sub) {
  return `<div class="subscription-details"><div class="expanded-actions"><button class="btn btn-small" data-action="subscription-qr" data-format="client" data-slug="${attr(sub.slug)}">QR OLCRTC Client</button><button class="btn btn-small" data-action="subscription-qr" data-format="olcbox" data-slug="${attr(sub.slug)}">QR OLCBOX</button><button class="btn btn-small" data-action="add-entry" data-slug="${attr(sub.slug)}">＋ Entry</button>${sub.mirror_enabled ? `<button class="btn btn-small" data-action="sync-mirror" data-slug="${attr(sub.slug)}">↻ Sync mirror Client</button>` : ''}<button class="btn btn-small" data-action="edit-subscription" data-slug="${attr(sub.slug)}">✎ Изменить</button><button class="btn btn-danger btn-small" data-action="delete-subscription" data-slug="${attr(sub.slug)}">Удалить</button></div><div class="fingerprint mono">OLCRTC Client: ${esc(location.origin+'/sub/'+sub.slug)}<br>OLCBOX: ${esc(location.origin+'/sub/'+sub.slug+'/olcbox')}<br>Открыть в клиенте: ${esc(location.origin+'/sub/'+sub.slug+'/open')}</div><div class="notice info" style="margin:14px 0">OLCBOX получает plain-text sub.md и обычные OLCBOX URI. Yandex encrypted mirror остаётся проекцией OLCRTC Client.</div><div class="entry-list">${sub.entries?.length ? sub.entries.map(entryRow).join('') : '<div class="muted">Нет entries. Подписка публикует пустой список.</div>'}</div></div>`;
}

function entryRow(entry) {
  const source = entry.source_instance_id ? state.instances.find(i => i.id === entry.source_instance_id) : null;
  return `<div class="entry-row"><label class="switch" title="Публикация entry"><input type="checkbox" data-action="toggle-entry" data-slug="${attr(state.expandedSubscription)}" data-id="${entry.id}" ${entry.enabled ? 'checked' : ''}><span></span></label><div class="truncate"><strong>${esc(entry.name || source?.name || 'Без имени')}</strong><small class="muted">${source ? `Linked instance #${source.id}` : 'Manual URI'} · порядок ${entry.sort_order}</small></div><span class="chip ${source ? 'green' : 'purple'}">${source ? 'linked' : 'manual'}</span><div class="toolbar-actions"><button class="btn btn-small btn-icon" data-action="move-entry" data-dir="up" data-slug="${attr(state.expandedSubscription)}" data-id="${entry.id}" aria-label="Выше">↑</button><button class="btn btn-small btn-icon" data-action="move-entry" data-dir="down" data-slug="${attr(state.expandedSubscription)}" data-id="${entry.id}" aria-label="Ниже">↓</button><button class="btn btn-danger btn-small" data-action="delete-entry" data-slug="${attr(state.expandedSubscription)}" data-id="${entry.id}">Удалить</button></div></div>`;
}

function openSubscriptionForm(sub = null) {
  const s = sub || { enabled:true, refresh:'10m', color:'#0EA58C', mirror_enabled:false };
  openModal(sub ? 'Изменить подписку' : 'Новая подписка', `<form data-form="subscription" data-slug="${attr(s.slug || '')}"><div class="form-grid">${field('name','Название',s.name || '','text','Например: Основная подписка',true)}${field('slug','Slug',s.slug || '','text','Пустой = случайный 128-bit slug',false,!!sub)}${field('refresh','Refresh interval',s.refresh || '10m','text','10m / 6h')} ${field('color','Цвет',s.color || '#0EA58C','color')} ${field('icon','Иконка / emoji',s.icon || '')}<label class="checkbox"><input type="checkbox" name="enabled" ${s.enabled ? 'checked' : ''}> Подписка включена</label><label class="checkbox"><input type="checkbox" name="mirror_enabled" ${s.mirror_enabled ? 'checked' : ''}> Yandex encrypted mirror</label></div><div class="notice info" style="margin-top:17px">Slug является bearer secret: по URL доступны URI с encryption keys. Не публикуйте его в открытом доступе.</div><div class="form-actions"><button class="btn" type="button" data-action="close-modal">Отмена</button><button class="btn btn-primary" type="submit">Сохранить</button></div></form>`);
}

function openEntryForm(slug) {
  const instances = state.instances;
  const options = instances.map(i=>{const clientUnavailable=clientQRUnavailable(i);return `<option value="${i.id}">#${i.id} ${esc(i.name)} · ${clientUnavailable?'OLCBOX':'Client + OLCBOX'}</option>`;}).join('');
  openModal('Добавить entry подписки', `<form data-form="entry" data-slug="${attr(slug)}"><div class="field"><label>Источник</label><select class="select" name="source_type" data-role="entry-source"><option value="linked">Linked instance</option><option value="manual">Manual URI</option></select></div><div data-entry-linked style="margin-top:15px" class="field"><label>Инстанс</label><select class="select" name="source_instance_id" ${instances.length ? '' : 'disabled'}>${options}</select>${instances.length ? '<span class="field-hint">Linked instance попадает в OLCBOX; в Client feed — только если его provider/transport совместимы и WB token задан.</span>' : '<span class="field-hint">Сначала создайте инстанс.</span>'}</div><div data-entry-manual class="field hidden" style="margin-top:15px"><label>OLCRTC Client или OLCBOX olcrtc:// URI</label><textarea class="textarea mono" name="raw_uri" placeholder="olcrtc://jitsi?datachannel@room#<64 hex key>$name"></textarea><span class="field-hint">Формат определяется автоматически: Client URI публикуется в Client feed, OLCBOX URI — в OLCBOX feed.</span></div><div class="form-grid" style="margin-top:15px">${field('name','Отображаемое имя','')}${field('comment','Комментарий','')}${field('ip','Показываемый IP','')}${field('color','Цвет','#0EA58C','color')}<label class="checkbox"><input type="checkbox" name="enabled" checked> Публиковать entry</label></div><div class="form-actions"><button class="btn" type="button" data-action="close-modal">Отмена</button><button class="btn btn-primary" type="submit">Добавить</button></div></form>`);
}

async function loadSettings() {
  const [settings, wbOperation, updateOperation] = await Promise.all([
    api('/api/v1/settings'),
    api('/api/v1/wb/components/progress').catch(() => ({ state: 'idle', percent: 0 })),
    api('/api/v1/updates/progress').catch(() => ({ state: 'idle', percent: 0 })),
  ]);
  state.settings = settings;
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
  const latestRelease = releases.items?.[0];
  const updateRunning = state.updateOperation?.state === 'running';
  const wbRunning = state.wbOperation?.state === 'running';
  const wbTokenStatus = !s.wb.token_set ? 'Не задан' : s.wb.token_expired ? 'Истёк — обновите через Playwright' : s.wb.token_expires_at ? `Действует до ${formatDate(s.wb.token_expires_at)}` : 'Задан, срок неизвестен';
  document.querySelector('#page-content').innerHTML = `
    <section class="page"><div class="page-header"><div class="page-title"><h1>Настройки</h1><p>Безопасность, HTTPS, интеграции и обслуживание</p></div></div><div class="settings-layout">
      <nav class="panel settings-nav"><button data-action="scroll-setting" data-target="security">Безопасность</button><button data-action="scroll-setting" data-target="https">HTTPS и IP</button><button data-action="scroll-setting" data-target="updates">Обновления</button><button data-action="scroll-setting" data-target="wb">WB Stream</button><button data-action="scroll-setting" data-target="yandex">Yandex mirror</button><button data-action="scroll-setting" data-target="instances-settings">Инстансы</button><button data-action="scroll-setting" data-target="interface">Интерфейс</button><button data-action="scroll-setting" data-target="backup">Backup</button></nav>
      <div>
        ${settingsSection('security','Безопасность',`<form data-form="credentials" class="form-grid">${field('username','Новый логин',state.user || '')}${field('current_password','Текущий пароль','','password','Обязательно',true)}${field('new_password','Новый пароль','','password','Пусто = оставить текущий')}<div class="wide form-actions"><button class="btn" type="button" data-action="revoke-sessions">Завершить все сессии</button><button class="btn btn-primary" type="submit">Изменить credentials</button></div></form>`)}
        ${settingsSection('https','HTTPS и IP',`<form data-form="https-settings"><div class="form-grid">${field('public_ip','Публичный IP',s.https.public_ip || '')}${field('public_port','HTTPS порт',s.https.port,'number')}</div><div class="form-actions"><a class="btn" href="/ca.crt" download>Скачать CA</a><button class="btn" type="button" data-action="regenerate-cert">Регенерировать leaf</button><button class="btn btn-primary" type="submit">Сохранить IP / порт</button></div></form><p class="field-label">CA fingerprint</p><div class="fingerprint mono">${esc(s.https.ca_fingerprint || '')}</div><p class="field-label">Server fingerprint</p><div class="fingerprint mono">${esc(s.https.server_fingerprint || '')}</div><div class="notice" style="margin-top:14px">После смены порта перезапустите panel service. CA необходимо сверить с fingerprint из консоли VPS.</div>`)}
        ${settingsSection('updates','Обновления',`<div class="detail-list">${detail('Текущий bundle',currentRelease.bundle_id || 'не определён')}${detail('Версия панели',currentRelease.panel_version || s.updates.panel_version)}${detail('Upstream SHA',shortSHA(currentRelease.upstream_sha || s.updates.upstream_sha))}${detail('Release manifest',s.updates.configured ? 'Настроен' : 'Не задан')}</div><div class="form-actions"><button class="btn" data-action="check-updates" ${updateRunning ? 'disabled' : ''}>Обновить список</button><button class="btn btn-primary" data-action="install-update" ${updateRunning || !latestRelease || latestRelease.current ? 'disabled' : ''}>Обновить до последнего</button><button class="btn" data-action="rollback-update" ${updateRunning || !releases.rollback_available ? 'disabled' : ''}>Rollback</button></div><div id="update-operation">${operationProgressHTML(state.updateOperation, 'Обновление')}</div><div id="release-list">${releaseCatalogHTML(releases, updateRunning)}</div>`)}
        ${settingsSection('wb','WB Stream automation',`<div class="detail-list">${detail('Платформа',s.wb.supported ? 'linux/amd64' : 'Не поддерживается')}${detail('Components',s.wb.installed ? 'Установлены' : 'Не установлены')}${detail('Auth token',wbTokenStatus,s.wb.token_expired ? 'danger-text' : '')}</div><div class="notice info" style="margin-top:14px">Playwright использует постоянный Chromium profile. Login и CAPTCHA выполняются вручную через авторизованный noVNC route; фонового обновления token нет.</div><div class="notice" style="margin-top:10px">WB auth token входит только в URI/QR OLCRTC Client и делает такой QR credential. QR OLCBOX token не содержит.</div><div class="form-actions"><button class="btn btn-primary" data-action="wb-install" ${!s.wb.supported || wbRunning ? 'disabled' : ''}>Установить components</button><button class="btn btn-danger" data-action="wb-remove" ${!s.wb.supported || wbRunning ? 'disabled' : ''}>Удалить components</button><button class="btn" data-action="wb-session-create" ${!s.wb.installed ? 'disabled' : ''}>Playwright: создать комнату</button><button class="btn ${s.wb.token_expired ? 'btn-primary' : ''}" data-action="wb-playwright-refresh" ${!s.wb.installed ? 'disabled' : ''}>Playwright: обновить token</button><button class="btn" data-action="wb-token">Ввести token вручную (fallback)</button></div><div id="wb-operation">${operationProgressHTML(state.wbOperation, 'WB components')}</div>`)}
        ${settingsSection('yandex','Yandex encrypted mirror',`<form data-form="yandex"><div class="form-grid"><label class="checkbox"><input type="checkbox" name="yandex_enabled" ${s.yandex.enabled ? 'checked' : ''}> Включить глобально</label>${field('yandex_base_path','Base path',s.yandex.base_path || '/olcrtc/subscriptions')}${field('yandex_oauth_token','OAuth token','','password',s.yandex.token_set ? 'Token сохранён; пусто = не менять' : 'Введите token')}<label class="checkbox"><input type="checkbox" name="clear_yandex_token"> Удалить сохранённый token</label></div><div class="form-actions"><button class="btn btn-primary" type="submit">Сохранить Yandex settings</button></div></form>`)}
        ${settingsSection('instances-settings','Инстансы',`<form data-form="instance-settings" class="inline-form"><div class="field"><label>Максимум инстансов</label><input class="input" name="max_instances" type="number" min="1" max="1000" value="${s.instances.maximum}"></div><button class="btn btn-primary" type="submit">Сохранить</button></form>`)}
        ${settingsSection('interface','Интерфейс',`<form data-form="theme" class="inline-form"><div class="field"><label>Тема</label><select class="select" name="theme"><option value="dark" ${s.interface.theme==='dark'?'selected':''}>Тёмная</option><option value="light" ${s.interface.theme==='light'?'selected':''}>Светлая</option></select></div><button class="btn btn-primary" type="submit">Применить</button></form>`)}
        ${settingsSection('backup','Backup',`<p class="muted">Обычный UI backup содержит SQLite snapshot и redacted YAML. Master key, private TLS keys, key.hex и WB profile не включаются.</p><div class="form-actions"><button class="btn btn-primary" data-action="create-backup">Создать и скачать</button></div>`)}
      </div>
    </div></section>`;
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
    if (action === 'logout') { await api('/api/v1/auth/logout',{method:'POST'}); state.user=null;renderLogin(); }
    if (action === 'refresh-dashboard') await loadDashboard();
    if (action === 'refresh-instances') await loadInstances();
    if (action === 'create-instance') openInstanceForm();
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
    if (action === 'wb-install') { state.wbOperation=await api('/api/v1/wb/components/install',{method:'POST'});refreshSettingsOperationViews();toast('Установка WB components запущена'); }
    if (action === 'wb-remove') { if(confirm('Удалить WB components и browser profile?')){state.wbOperation=await api('/api/v1/wb/components/remove',{method:'POST'});refreshSettingsOperationViews();toast('Удаление WB components запущено');} }
    if (action === 'wb-session-create') await runWBSession('create');
    if (action === 'wb-playwright-refresh') await runWBSession('refresh');
    if (action === 'wb-token') openWBTokenModal();
    if (action === 'wb-fill-instance') await fillWBInstanceForm();
    if (action === 'generate-jitsi-room') { const form=document.querySelector('form[data-form="instance"]');if(form){const bytes=new Uint8Array(10);crypto.getRandomValues(bytes);const name=Array.from(bytes,b=>b.toString(16).padStart(2,'0')).join('');form.elements.provider.value='jitsi';form.elements.room_id.value=`https://meet.jit.si/olc-${name}`;} }
    if (action === 'refresh-logs') await refreshLogs();
    if (action === 'toggle-logs') { state.logsPaused=!state.logsPaused; stopPolling(); renderLogsPage(document.querySelector('#log-output')?.textContent || ''); }
    if (action === 'copy-logs') { await copyText(document.querySelector('#log-output')?.textContent || '');toast('Журнал скопирован'); }
  } catch (error) { toast('Операция не выполнена', error.message, 'error'); }
});

app.addEventListener('submit', async event => {
  const form = event.target.closest('form[data-form]');
  if (!form) return;
  event.preventDefault();
  const submit = form.querySelector('[type="submit"]'); if (submit) submit.disabled = true;
  try {
    if (form.dataset.form === 'login') {
      const d=new FormData(form);const result=await api('/api/v1/auth/login',{method:'POST',body:JSON.stringify({username:d.get('username'),password:d.get('password')})});state.user=result.username;state.csrf=result.csrf_token;await navigate('dashboard');
    }
    if (form.dataset.form === 'instance') {
      const payload=instancePayload(form);const id=form.dataset.id;const result=await api(id?`/api/v1/instances/${id}`:'/api/v1/instances',{method:id?'PUT':'POST',body:JSON.stringify(payload)});closeModal();await loadInstances();toast(id?'Инстанс обновлён':'Инстанс создан',result.warning || 'Конфигурация сохранена');
    }
    if (form.dataset.form === 'subscription') {
      const d=new FormData(form);const slug=form.dataset.slug;const payload={name:d.get('name').trim(),slug:slug || d.get('slug').trim(),refresh:d.get('refresh'),color:d.get('color'),icon:d.get('icon'),enabled:d.has('enabled'),mirror_enabled:d.has('mirror_enabled')};await api(slug?`/api/v1/subscriptions/${slug}`:'/api/v1/subscriptions',{method:slug?'PUT':'POST',body:JSON.stringify(payload)});closeModal();await loadSubscriptions();toast('Подписка сохранена');
    }
    if (form.dataset.form === 'entry') {
      const d=new FormData(form);const linked=d.get('source_type')==='linked';const payload={source_instance_id:linked?Number(d.get('source_instance_id')):null,raw_uri:linked?'':d.get('raw_uri').trim(),name:d.get('name'),comment:d.get('comment'),ip:d.get('ip'),color:d.get('color'),enabled:d.has('enabled'),sort_order:999};await api(`/api/v1/subscriptions/${form.dataset.slug}/entries`,{method:'POST',body:JSON.stringify(payload)});closeModal();await loadSubscriptions();toast('Entry OLCRTC Client добавлен');
    }
    if (form.dataset.form === 'credentials') { const d=new FormData(form);await api('/api/v1/auth/credentials',{method:'PUT',body:JSON.stringify({username:d.get('username'),current_password:d.get('current_password'),new_password:d.get('new_password')})});state.user=null;renderLogin('Credentials изменены. Войдите снова.'); }
    if (form.dataset.form === 'yandex') { const d=new FormData(form);await api('/api/v1/settings',{method:'PUT',body:JSON.stringify({yandex_enabled:d.has('yandex_enabled'),yandex_base_path:d.get('yandex_base_path'),yandex_oauth_token:d.get('yandex_oauth_token'),clear_yandex_token:d.has('clear_yandex_token')})});await loadSettings();toast('Yandex settings сохранены'); }
    if (form.dataset.form === 'instance-settings') { const d=new FormData(form);await api('/api/v1/settings',{method:'PUT',body:JSON.stringify({max_instances:Number(d.get('max_instances'))})});await loadSettings();toast('Лимит сохранён'); }
    if (form.dataset.form === 'theme') { const d=new FormData(form);const theme=d.get('theme');await api('/api/v1/settings',{method:'PUT',body:JSON.stringify({theme})});applyTheme(theme);localStorage.setItem('olcrtc-theme',theme);toast('Тема применена'); }
    if (form.dataset.form === 'https-settings') { const d=new FormData(form);await api('/api/v1/settings',{method:'PUT',body:JSON.stringify({public_ip:d.get('public_ip'),public_port:Number(d.get('public_port'))})});await loadSettings();toast('HTTPS settings сохранены','Перезапустите panel при смене порта.'); }
    if (form.dataset.form === 'wb-token') { const d=new FormData(form);const result=await api('/api/v1/wb/token/refresh',{method:'POST',body:JSON.stringify({token:d.get('token')})});closeModal();toast('WB token обновлён',wbApplySummary(result));if(state.page==='settings'){stopPolling();await loadSettings();}else if(state.page==='instances'){await loadInstances();} }
  } catch (error) { toast('Ошибка формы', error.message, 'error'); }
  finally { if (submit && document.body.contains(submit)) submit.disabled = false; }
});

app.addEventListener('input', event => {
  if (event.target.dataset.filter) { state.instanceFilters[event.target.dataset.filter]=event.target.value;renderInstances(); }
  if (event.target.dataset.role === 'logs-search') refreshLogs();
});

app.addEventListener('change', event => {
  if (event.target.dataset.filter) { state.instanceFilters[event.target.dataset.filter]=event.target.value;renderInstances(); }
  if (event.target.dataset.role === 'logs-unit') { state.logsUnit=event.target.value;refreshLogs(); }
  if (event.target.dataset.role === 'logs-lines') refreshLogs();
  if (event.target.dataset.role === 'logs-level') { state.logsLevel=event.target.value;refreshLogs(); }
  if (event.target.dataset.role === 'entry-source') { document.querySelector('[data-entry-linked]')?.classList.toggle('hidden',event.target.value!=='linked');document.querySelector('[data-entry-manual]')?.classList.toggle('hidden',event.target.value!=='manual'); }
});

window.addEventListener('hashchange', () => { const page=location.hash.replace('#','') || 'dashboard';if(page!==state.page)navigate(page,false); });

async function handleInstanceAction(action, target) {
  const id=Number(target.dataset.id);const item=state.instances.find(i=>i.id===id);const simple=action.replace('instance-','');
  if (['start','stop','restart','duplicate','rotate-key','rotate-client-id','reset-traffic','diagnostics'].includes(simple)) {
    if (simple==='rotate-key'&&!confirm('Сменить encryption key? Linked subscriptions обновятся автоматически.')) return;
    if (simple==='rotate-client-id'&&!confirm('Сменить client_id? Инстанс будет перезапущен, linked subscriptions и Yandex mirrors обновятся.')) return;
    if (simple==='reset-traffic'&&!confirm('Сбросить точные traffic counters?')) return;
    const result=await api(`/api/v1/instances/${id}/${simple}`,{method:'POST'});
    if(simple==='diagnostics'){openModal('Диагностика provider',`<pre class="payload-box">${esc(JSON.stringify(result,null,2))}</pre>`);return;}
    await loadInstances();toast('Операция выполнена');return;
  }
  if (simple==='delete') { const name=prompt(`Для удаления введите точное имя: ${item.name}`);if(name!==item.name)return;await api(`/api/v1/instances/${id}`,{method:'DELETE',body:JSON.stringify({confirm_name:name})});await loadInstances();toast('Инстанс удалён'); }
  if (simple==='change-room') { const room=prompt('Новый Room ID / URL',item.room_id);if(!room)return;await api(`/api/v1/instances/${id}/change-room`,{method:'POST',body:JSON.stringify({room_id:room})});await loadInstances();toast('Room ID изменён'); }
  if (simple==='uri') { const format=target.dataset.format||'olcbox';const result=await api(`/api/v1/instances/${id}/uri?format=${format}`);openQRPayloadModal(`${format==='client'?'OLCRTC Client':'OLCBOX'} URI`,'',result.uri,format==='client'?maskClientURI:value=>value); }
  if (simple==='qr') await showInstanceQR(id,target.dataset.format||'olcbox');
  if (simple==='logs') { state.logsUnit=`instance:${id}`;await navigate('logs'); }
}

async function showInstanceQR(id,format){
  const result=await api(`/api/v1/instances/${id}/uri?format=${format}`);
  const src=`/api/v1/instances/${id}/qr?format=${format}`;
  const warning=format==='client'&&result.uri.includes('&a=')?'Этот QR содержит полный WB auth token и является credential.':'';
  openQRPayloadModal(format==='client'?'QR OLCRTC Client':'QR OLCBOX',src,result.uri,format==='client'?maskClientURI:value=>value,warning);
}

async function showSubscriptionQR(slug,format='client'){
  const sub=state.subscriptions.find(item=>item.slug===slug);
  let warning='';
  if(format==='client'&&sub?.mirror_enabled){
    try{await api(`/api/v1/subscriptions/${encodeURIComponent(slug)}/mirror/sync`,{method:'POST'});}
    catch(error){warning=`Yandex mirror не удалось обновить: ${error.message}. QR использует прямой URL и последнее подтверждённое состояние mirror.`;}
  }
  const result=await api(`/api/v1/subscriptions/${encodeURIComponent(slug)}/payload?format=${format}`);
  const src=`/api/v1/subscriptions/${encodeURIComponent(slug)}/qr?format=${format}`;
  if(format==='olcbox')warning='QR содержит bearer-secret URL OLCBOX. Он загружает plain-text sub.md с обычными olcrtc:// URI.';
  openQRPayloadModal(format==='olcbox'?'QR подписки OLCBOX':'QR подписки OLCRTC Client',src,result.payload,format==='olcbox'?value=>value:maskSubscriptionBundle,warning);
}

function openQRPayloadModal(title,src,payload,masker=value=>value,warning=''){
  const masked=masker(payload),secret=masked!==payload;
  openModal(title,`${warning?`<div class="notice" style="margin-bottom:14px">${esc(warning)}</div>`:''}${src?`<div class="qr-wrap"><img src="${src}" alt="${attr(title)}"><a class="btn" href="${src}" download>Скачать PNG</a></div>`:''}<p class="field-label">URI / URL / payload</p><pre class="payload-box" id="qr-payload-value">${esc(masked)}</pre><div class="form-actions"><button class="btn ${secret?'':'hidden'}" type="button" id="qr-payload-show">Показать</button><button class="btn btn-primary ${secret?'hidden':''}" type="button" id="qr-payload-copy">Копировать</button></div>`);
  const value=document.querySelector('#qr-payload-value'),show=document.querySelector('#qr-payload-show'),copy=document.querySelector('#qr-payload-copy');
  if(show)show.addEventListener('click',()=>{value.textContent=payload;show.remove();copy.classList.remove('hidden');});
  if(copy)copy.addEventListener('click',async()=>{await copyText(payload);toast('Payload скопирован');});
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
  const session=await api('/api/v1/wb/session',{method:'POST',body:JSON.stringify({action:'create'})});window.open(session.novnc_url,'olcrtc-wb-novnc','noopener');toast('WB-сессия запущена','Войдите в WB Stream и пройдите CAPTCHA.');
  const current=await waitForWBSession();const room=current.state?.room_id||'',token=current.state?.token||'';if(!token)throw new Error('WB token не получен из успешной Playwright-сессии');if(room)form.elements.room_id.value=room;form.elements.auth_token.value=token;form.elements.provider.value='wbstream';toast('WB данные получены',`Room ID и WB account token заполнены. ${wbApplySummary(current.state?.applied)}`);
}

async function runWBSession(action){
  const session=await api('/api/v1/wb/session',{method:'POST',body:JSON.stringify({action})});
  window.open(session.novnc_url,'olcrtc-wb-novnc','noopener');
  openModal(action==='create'?'Playwright: создать WB комнату':'Playwright: обновить WB token',`<div class="notice info">Сессия активна до ${formatDate(session.expires_at)}. Выполните login/CAPTCHA в noVNC.</div><div class="form-actions"><a class="btn btn-primary" href="${attr(session.novnc_url)}" target="_blank" rel="noopener">Открыть noVNC</a></div><div class="operation-card running" id="wb-session-state">Ожидание Chromium...</div>`);
  const current=await waitForWBSession(statePayload=>{const root=document.querySelector('#wb-session-state');if(root)root.textContent=`${statePayload.message||statePayload.phase||'Ожидание'} · ${statePayload.percent||0}%`;});
  const root=document.querySelector('#wb-session-state');if(root){root.className='operation-card completed';root.textContent=`Готово. ${wbApplySummary(current.state?.applied)}`;}
  toast('WB token получен через Playwright',wbApplySummary(current.state?.applied));
  if(state.page==='settings'){stopPolling();await loadSettings();}else if(state.page==='instances'){await loadInstances();}
  return current;
}

async function waitForWBSession(onProgress=()=>{}){
  for(let attempt=0;attempt<450;attempt++){
    await new Promise(resolve=>setTimeout(resolve,2000));
    const current=await api('/api/v1/wb/session');const worker=current.state||{};onProgress(worker);
    if(worker.phase==='success')return current;
    if(worker.phase==='error')throw new Error(worker.message||'WB automation завершилась с ошибкой');
    if(!current.active)throw new Error('Время WB-сессии истекло');
  }
  throw new Error('WB automation не завершилась вовремя');
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
function clientQRUnavailable(item){const supported=(item.provider==='wbstream'||item.provider==='telemost')&&item.transport==='vp8channel'||item.provider==='jitsi'&&item.transport==='datachannel';if(!supported)return `комбинация ${item.provider} + ${item.transport} не поддерживается OLCRTC Client`;if(item.provider==='wbstream'&&!item.auth_token_set)return 'для WB сначала получите auth token через Playwright или введите его вручную';return '';}
function quotaLabel(item){if(item.expires_at&&new Date(item.expires_at)<new Date())return '<span class="chip red">expired</span>';if(item.quota_bytes&&item.total_bytes>=item.quota_bytes)return '<span class="chip red">exceeded</span>';return item.quota_bytes?`${formatBytes(item.quota_bytes)}`:'∞';}
function statusLabel(value){return ({running:'Запущен',stopped:'Остановлен',starting:'Запуск',stopping:'Остановка',failed:'Ошибка',updating:'Обновление',unknown:'Неизвестно'})[value]||value;}
function formatBytes(value=0){const units=['Б','КБ','МБ','ГБ','ТБ','ПБ'];let n=Number(value)||0,i=0;while(n>=1024&&i<units.length-1){n/=1024;i++;}return `${i? n.toFixed(n>=100?0:n>=10?1:2):Math.round(n)} ${units[i]}`;}
function formatUptime(seconds=0){seconds=Math.max(0,Math.floor(seconds||0));const d=Math.floor(seconds/86400),h=Math.floor(seconds%86400/3600),m=Math.floor(seconds%3600/60);return d?`${d}д ${h}ч`:h?`${h}ч ${m}м`:`${m}м`;}
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
function stopPolling(){if(state.poller){clearInterval(state.poller);state.poller=null;}}
async function copyText(value){if(navigator.clipboard?.writeText)return navigator.clipboard.writeText(value);const area=document.createElement('textarea');area.value=value;document.body.append(area);area.select();document.execCommand('copy');area.remove();}
async function downloadAuthenticated(url,filename){const response=await fetch(url,{credentials:'same-origin'});if(!response.ok)throw new Error('Не удалось скачать файл');const blob=await response.blob();const link=document.createElement('a');link.href=URL.createObjectURL(blob);link.download=filename;link.click();setTimeout(()=>URL.revokeObjectURL(link.href),1000);}
function importSubscriptions(){const input=document.createElement('input');input.type='file';input.accept='application/json,.json';input.onchange=async()=>{try{const text=await input.files[0].text();const payload=JSON.parse(text);const result=await api('/api/v1/subscriptions/import',{method:'POST',body:JSON.stringify(payload)});await loadSubscriptions();toast('Импорт завершён',`Создано: ${result.created}`);}catch(error){toast('Ошибка импорта',error.message,'error');}};input.click();}

async function refreshSettingsOperations(){
  if(state.page!=='settings'||state.settingsPolling)return;
  state.settingsPolling=true;
  const previousWB=state.wbOperation?.state,previousUpdate=state.updateOperation?.state;
  try{
    const [wbOperation,updateOperation]=await Promise.all([
      api('/api/v1/wb/components/progress').catch(()=>null),
      api('/api/v1/updates/progress').catch(()=>null),
    ]);
    if(wbOperation)state.wbOperation=wbOperation;
    if(updateOperation)state.updateOperation=updateOperation;
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
  const wbRoot=document.querySelector('#wb-operation');if(wbRoot)wbRoot.innerHTML=operationProgressHTML(state.wbOperation,'WB components');
  const releasesRoot=document.querySelector('#release-list');if(releasesRoot)releasesRoot.innerHTML=releaseCatalogHTML(state.releases||{configured:false,items:[]},updateRunning);
  const latest=state.releases?.items?.[0];
  const checkButton=document.querySelector('[data-action="check-updates"]');if(checkButton)checkButton.disabled=updateRunning;
  const updateButton=document.querySelector('[data-action="install-update"]:not([data-bundle])');if(updateButton)updateButton.disabled=updateRunning||!latest||latest.current;
  const rollbackButton=document.querySelector('[data-action="rollback-update"]');if(rollbackButton)rollbackButton.disabled=updateRunning||!state.releases?.rollback_available;
  document.querySelectorAll('[data-action="wb-install"],[data-action="wb-remove"]').forEach(button=>{button.disabled=wbRunning||!state.settings?.wb?.supported;});
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
  const items=catalog.items||[];
  if(!items.length)return '<div class="empty-state compact"><p>Опубликованные bundle-релизы не найдены.</p></div>';
  const rows=items.map(item=>`<tr><td><strong class="mono">${esc(item.bundle_id)}</strong>${item.latest?' <span class="chip green">latest</span>':''}${item.current?' <span class="chip blue">current</span>':''}</td><td>${item.published_at?formatDate(item.published_at):'—'}</td><td><a class="btn btn-ghost" href="${attr(item.url)}" target="_blank" rel="noopener">GitHub</a></td><td><button class="btn ${item.latest?'btn-primary':''}" data-action="install-update" data-bundle="${attr(item.bundle_id)}" ${operationRunning||item.current?'disabled':''}>${item.current?'Установлен':item.latest?'Обновить':'Установить эту версию'}</button></td></tr>`).join('');
  return `<div class="release-catalog"><h3>Доступные релизы</h3><div class="table-wrap"><table class="table release-table"><thead><tr><th>Bundle</th><th>Дата</th><th>Страница</th><th>Действие</th></tr></thead><tbody>${rows}</tbody></table></div></div>`;
}
