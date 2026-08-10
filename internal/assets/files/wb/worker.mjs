import fs from 'node:fs';
import { createRequire } from 'node:module';
import process from 'node:process';

const require = createRequire(import.meta.url);
const { chromium } = require('/opt/olcrtc-panel/wb/node_modules/playwright');

const jobPath = process.argv[2] || '/run/olcrtc-wb/job.json';
const job = JSON.parse(fs.readFileSync(jobPath, 'utf8'));
const statePath = job.state_file || '/run/olcrtc-wb/state.json';
const controlPath = job.control_file || '/run/olcrtc-wb/control.json';
const provider = job.provider || 'wbstream';
const uuidPattern = /[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}/i;
const selectors = {
  quickMeeting: '[data-test="quick-meeting-card"]',
  roomContent: '[data-test="room-content"], [data-test="room-header"]',
  participants: '[data-test="participants-button"]',
};
const telemostSelectors = {
  createCall: '[data-testid="create-call-button"]',
  loginInput: '[data-testid="add-user-login-input"]',
  roomReady: '[data-testid="copy-link-short-button"], [class*="MeetingNumber"]',
};
let activeContext;

function writeState(phase, message, percent, extra = {}) {
  const payload = { phase, message, percent, action: job.action, provider, updated_at: Math.floor(Date.now() / 1000), ...extra };
  const temporary = `${statePath}.tmp`;
  fs.writeFileSync(temporary, JSON.stringify(payload), { mode: 0o600 });
  fs.renameSync(temporary, statePath);
}

function jwtExpiry(token) {
  try {
    const part = token.split('.')[1];
    if (!part) return 0;
    const padded = part.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - part.length % 4) % 4);
    return Number(JSON.parse(Buffer.from(padded, 'base64').toString('utf8')).exp) || 0;
  } catch { return 0; }
}

function readDeadline() {
  try { return Number(JSON.parse(fs.readFileSync(controlPath, 'utf8')).deadline_unix) * 1000; }
  catch { return Number(job.deadline_unix) * 1000; }
}

function findRoomID(value) {
  if (typeof value === 'string') return value.match(uuidPattern)?.[0] || '';
  if (!value || typeof value !== 'object') return '';
  for (const [key, child] of Object.entries(value)) {
    if (/^(room_?id|meeting_?id|room_?code|code)$/i.test(key)) {
      const match = findRoomID(child); if (match) return match;
    }
  }
  for (const child of Object.values(value)) { const match = findRoomID(child); if (match) return match; }
  return '';
}

function findTelemostRoomID(value) {
  if (typeof value === 'string') {
    const candidates = [value];
    try { candidates.push(decodeURIComponent(value)); } catch { /* Keep the original value. */ }
    for (const candidate of candidates) {
      const urlMatch = candidate.match(/https:\/\/telemost\.yandex\.ru\/j\/(\d{14})(?:$|[/?#])/i);
      if (urlMatch) return urlMatch[1];
      const plainMatch = candidate.trim().match(/^\d{14}$/);
      if (plainMatch) return plainMatch[0];
      const groupedMatch = candidate.match(/(?:^|\D)(\d{4}[\s-]+\d{4}[\s-]+\d{4}[\s-]+\d{2})(?!\d)/);
      if (groupedMatch) return groupedMatch[1].replace(/[^0-9]/g, '');
    }
    return '';
  }
  if (!value || typeof value !== 'object') return '';
  for (const child of Object.values(value)) {
    const match = findTelemostRoomID(child);
    if (match) return match;
  }
  return '';
}

function isWBURL(rawURL) {
  try { const hostname = new URL(rawURL).hostname.toLowerCase(); return hostname === 'wb.ru' || hostname.endsWith('.wb.ru'); }
  catch { return false; }
}

function isTelemostURL(rawURL) {
  try {
    const parsed = new URL(rawURL);
    return parsed.hostname.toLowerCase() === 'telemost.yandex.ru' ||
      (parsed.hostname.toLowerCase() === 'cloud-api.yandex.ru' && parsed.pathname.includes('/telemost'));
  } catch { return false; }
}

async function runWB(context, initialPage) {
  let accountToken = '';
  let roomID = '';
  context.on('request', request => {
    try {
      if (!isWBURL(request.url())) return;
      const authorization = request.headers()['authorization'] || '';
      if (/^Bearer\s+\S+/i.test(authorization)) accountToken = authorization.replace(/^Bearer\s+/i, '').trim();
      if (/room|meeting/i.test(request.url())) roomID ||= findRoomID(request.url());
    } catch { /* Ignore malformed third-party requests. */ }
  });
  context.on('response', async response => {
    if (!isWBURL(response.url()) || !/room|meeting|connection/i.test(response.url())) return;
    roomID ||= findRoomID(response.url());
    if (!(response.headers()['content-type'] || '').includes('json')) return;
    try { roomID ||= findRoomID(await response.json()); } catch { /* Empty or consumed body. */ }
  });
  let page = initialPage;
  await page.goto(job.home_url || 'https://stream.wb.ru', { waitUntil: 'domcontentloaded', timeout: 60_000 });
  writeState('awaiting_login', 'Войдите в WB Stream и пройдите CAPTCHA', 20);
  for (;;) {
    if (Date.now() > readDeadline()) throw new Error('Время авторизации истекло');
    page = context.pages().at(-1) || page;
    const home = page.locator(selectors.quickMeeting);
    if (await home.count() > 0 && await home.first().isVisible().catch(() => false)) break;
    await page.waitForTimeout(1000);
  }
  writeState('authorized', 'Авторизация WB подтверждена', 45);
  if (job.action === 'create') {
    await page.locator(selectors.quickMeeting).first().click({ timeout: 30_000 });
    writeState('creating_room', 'Создание новой комнаты WB Stream...', 65);
    await page.waitForSelector(selectors.roomContent, { timeout: 60_000 });
    roomID ||= findRoomID(page.url());
    if (!roomID) {
      const participants = page.locator(selectors.participants);
      if (await participants.count() > 0) {
        await participants.first().click().catch(() => {});
        await page.waitForTimeout(1000);
        roomID ||= findRoomID(await page.locator('body').innerText());
      }
    }
  } else {
    writeState('refreshing_token', 'Получение свежего токена WB...', 65);
    await page.reload({ waitUntil: 'domcontentloaded', timeout: 60_000 });
    await page.waitForTimeout(3000);
    if (!accountToken && job.existing_room_id) {
      await page.goto(`https://stream.wb.ru/${encodeURIComponent(job.existing_room_id)}`, {
        waitUntil: 'domcontentloaded', timeout: 60_000,
      });
      await page.waitForTimeout(3000);
    }
  }
  const waitUntil = Date.now() + 45_000;
  while ((!accountToken || (job.action === 'create' && !roomID)) && Date.now() < waitUntil) {
    page = context.pages().at(-1) || page;
    roomID ||= findRoomID(page.url());
    await page.waitForTimeout(500);
  }
  if (!accountToken) throw new Error('WB account Bearer не найден в сетевых запросах');
  if (job.action === 'create' && !roomID) throw new Error('Room ID новой встречи не найден');
  writeState('success', 'Данные WB Stream получены', 100, { token: accountToken, token_expires_at: jwtExpiry(accountToken), room_id: roomID });
}

async function runTelemost(context, initialPage) {
  if (job.action !== 'create') throw new Error('Telemost поддерживает только создание комнаты');
  let roomID = '';
  let page = initialPage;
  let sawLogin = false;
  let retriedAfterLogin = false;

  context.on('request', request => {
    if (isTelemostURL(request.url())) roomID ||= findTelemostRoomID(request.url());
  });
  context.on('response', async response => {
    if (!isTelemostURL(response.url())) return;
    roomID ||= findTelemostRoomID(response.url());
    roomID ||= findTelemostRoomID(response.headers()['location'] || '');
    if (!(response.headers()['content-type'] || '').includes('json')) return;
    try { roomID ||= findTelemostRoomID(await response.json()); } catch { /* The 201 response may have no saved body. */ }
  });

  await page.goto(job.home_url || 'https://telemost.yandex.ru', { waitUntil: 'domcontentloaded', timeout: 60_000 });
  const createCall = page.locator(telemostSelectors.createCall).first();
  await createCall.waitFor({ state: 'visible', timeout: 60_000 });
  writeState('creating_room', 'Создание новой комнаты Telemost...', 40);
  await createCall.click({ timeout: 30_000 });

  while (Date.now() <= readDeadline()) {
    page = context.pages().at(-1) || page;
    roomID ||= findTelemostRoomID(page.url());
    if (!roomID && await page.locator(telemostSelectors.roomReady).count() > 0) {
      roomID ||= findTelemostRoomID(await page.locator('body').innerText().catch(() => ''));
    }
    if (roomID) {
      writeState('success', 'Комната Telemost создана', 100, { room_id: roomID });
      return;
    }

    const hostname = (() => { try { return new URL(page.url()).hostname.toLowerCase(); } catch { return ''; } })();
    const loginVisible = hostname === 'passport.yandex.ru' || hostname.endsWith('.passport.yandex.ru') ||
      await page.locator(telemostSelectors.loginInput).first().isVisible().catch(() => false);
    if (loginVisible) {
      if (!sawLogin) writeState('awaiting_login', 'Войдите в аккаунт Яндекса через noVNC', 25);
      sawLogin = true;
    } else if (sawLogin && !retriedAfterLogin && hostname === 'telemost.yandex.ru') {
      const retryButton = page.locator(telemostSelectors.createCall).first();
      if (await retryButton.isVisible().catch(() => false)) {
        writeState('creating_room', 'Авторизация подтверждена, создание комнаты Telemost...', 65);
        await retryButton.click({ timeout: 30_000 });
        retriedAfterLogin = true;
      }
    }
    await page.waitForTimeout(500);
  }
  throw new Error('Время авторизации Telemost истекло');
}

async function main() {
  const proxy = job.proxy?.server ? { server: job.proxy.server, username: job.proxy.username || undefined, password: job.proxy.password || undefined } : undefined;
  writeState('starting', 'Запуск удалённого Chromium...', 5);
  const context = await chromium.launchPersistentContext(job.profile_dir, {
    headless: false, viewport: null, screen: { width: 1280, height: 800 }, proxy,
    permissions: ['clipboard-read', 'clipboard-write'],
    args: ['--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--window-size=1280,800'],
  });
  activeContext = context;
  const page = context.pages()[0] || await context.newPage();
  if (provider === 'wbstream') {
    await runWB(context, page);
  } else if (provider === 'telemost') {
    await runTelemost(context, page);
  } else {
    throw new Error(`Неподдерживаемый provider автоматизации: ${provider}`);
  }
  await context.close();
  activeContext = undefined;
}

main().catch(async error => {
  writeState('error', error?.message || String(error), 0);
  await activeContext?.close().catch(() => {});
  process.exitCode = 1;
});
