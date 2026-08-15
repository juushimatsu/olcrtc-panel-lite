import fs from 'node:fs';
import { createRequire } from 'node:module';
import net from 'node:net';
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
let activeProxyBridge;

function createSocketReader(socket) {
  let buffered = Buffer.alloc(0);
  let pending;
  let failure;

  const flush = () => {
    if (!pending || buffered.length < pending.length) return;
    const { length, resolve } = pending;
    pending = undefined;
    const result = buffered.subarray(0, length);
    buffered = buffered.subarray(length);
    resolve(result);
  };
  const fail = error => {
    failure = error;
    if (!pending) return;
    const { reject } = pending;
    pending = undefined;
    reject(error);
  };
  const onData = chunk => {
    buffered = buffered.length ? Buffer.concat([buffered, chunk]) : chunk;
    flush();
  };
  const onEnd = () => fail(new Error('SOCKS5 connection closed during handshake'));
  const onError = error => fail(error);

  socket.on('data', onData);
  socket.on('end', onEnd);
  socket.on('error', onError);

  return {
    read(length) {
      if (buffered.length >= length) {
        const result = buffered.subarray(0, length);
        buffered = buffered.subarray(length);
        return Promise.resolve(result);
      }
      if (failure) return Promise.reject(failure);
      if (pending) return Promise.reject(new Error('Concurrent SOCKS5 reads are not supported'));
      return new Promise((resolve, reject) => { pending = { length, resolve, reject }; });
    },
    release() {
      socket.pause();
      socket.off('data', onData);
      socket.off('end', onEnd);
      socket.off('error', onError);
      const result = buffered;
      buffered = Buffer.alloc(0);
      return result;
    },
  };
}

async function readSocksAddress(reader, addressType) {
  if (addressType === 0x01) return reader.read(4);
  if (addressType === 0x04) return reader.read(16);
  if (addressType === 0x03) {
    const length = await reader.read(1);
    return Buffer.concat([length, await reader.read(length[0])]);
  }
  throw new Error(`Unsupported SOCKS5 address type: ${addressType}`);
}

function socksFailureReply(code = 0x01) {
  return Buffer.from([0x05, code, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00]);
}

function connectTCP(host, port) {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ host, port });
    socket.on('error', () => {});
    socket.once('connect', () => resolve(socket));
    socket.once('error', reject);
  });
}

async function connectThroughAuthenticatedSocks5(proxy, request) {
  const upstreamURL = new URL(proxy.server);
  const upstreamHost = upstreamURL.hostname.replace(/^\[|\]$/g, '');
  const upstreamPort = Number(upstreamURL.port);
  const username = Buffer.from(proxy.username || '', 'utf8');
  const password = Buffer.from(proxy.password || '', 'utf8');
  if (!username.length || !password.length) throw new Error('Для SOCKS5 авторизации нужны логин и пароль');
  if (username.length > 255 || password.length > 255) throw new Error('Логин и пароль SOCKS5 должны занимать не более 255 байт');

  const socket = await connectTCP(upstreamHost, upstreamPort);
  socket.setTimeout(30_000, () => socket.destroy(new Error('SOCKS5 upstream timeout')));
  const reader = createSocketReader(socket);
  socket.write(Buffer.from([0x05, 0x02, 0x02, 0x00]));
  const method = await reader.read(2);
  if (method[0] !== 0x05 || (method[1] !== 0x00 && method[1] !== 0x02)) {
    socket.destroy();
    throw new Error('SOCKS5 proxy не принял поддерживаемый метод авторизации');
  }
  if (method[1] === 0x02) {
    socket.write(Buffer.concat([
      Buffer.from([0x01, username.length]), username,
      Buffer.from([password.length]), password,
    ]));
    const authentication = await reader.read(2);
    if (authentication[0] !== 0x01 || authentication[1] !== 0x00) {
      socket.destroy();
      throw new Error('SOCKS5 proxy отклонил логин или пароль');
    }
  }

  socket.write(request);
  const responseHeader = await reader.read(4);
  if (responseHeader[0] !== 0x05) {
    socket.destroy();
    throw new Error('SOCKS5 proxy вернул некорректный ответ');
  }
  const responseAddress = await readSocksAddress(reader, responseHeader[3]);
  const responsePort = await reader.read(2);
  return {
    socket,
    reader,
    response: Buffer.concat([responseHeader, responseAddress, responsePort]),
  };
}

async function relayAuthenticatedSocks5(client, proxy, sockets) {
  const clientReader = createSocketReader(client);
  let upstream;
  try {
    client.setTimeout(30_000, () => client.destroy(new Error('SOCKS5 client timeout')));
    const greeting = await clientReader.read(2);
    const methods = await clientReader.read(greeting[1]);
    if (greeting[0] !== 0x05 || !methods.includes(0x00)) {
      client.end(Buffer.from([0x05, 0xff]));
      return;
    }
    client.write(Buffer.from([0x05, 0x00]));

    const requestHeader = await clientReader.read(4);
    if (requestHeader[0] !== 0x05 || requestHeader[1] !== 0x01) {
      client.end(socksFailureReply(0x07));
      return;
    }
    const requestAddress = await readSocksAddress(clientReader, requestHeader[3]);
    const requestPort = await clientReader.read(2);
    const request = Buffer.concat([requestHeader, requestAddress, requestPort]);
    const connected = await connectThroughAuthenticatedSocks5(proxy, request);
    upstream = connected.socket;
    sockets.add(upstream);
    upstream.once('close', () => sockets.delete(upstream));
    upstream.on('error', () => client.destroy());
    client.on('error', () => upstream.destroy());

    client.write(connected.response);
    if (connected.response[1] !== 0x00) {
      client.end();
      upstream.destroy();
      return;
    }

    const clientRemainder = clientReader.release();
    const upstreamRemainder = connected.reader.release();
    client.setTimeout(0);
    upstream.setTimeout(0);
    if (clientRemainder.length) upstream.write(clientRemainder);
    if (upstreamRemainder.length) client.write(upstreamRemainder);
    client.pipe(upstream);
    upstream.pipe(client);
  } catch (error) {
    console.error(`SOCKS5 bridge connection failed: ${error?.message || error}`);
    upstream?.destroy();
    if (!client.destroyed) client.end(socksFailureReply());
  }
}

async function createAuthenticatedSocks5Bridge(proxy) {
  const username = Buffer.from(proxy.username || '', 'utf8');
  const password = Buffer.from(proxy.password || '', 'utf8');
  if (!username.length || !password.length) throw new Error('Для SOCKS5 авторизации нужны логин и пароль');
  if (username.length > 255 || password.length > 255) throw new Error('Логин и пароль SOCKS5 должны занимать не более 255 байт');

  const sockets = new Set();
  const server = net.createServer(client => {
    sockets.add(client);
    client.once('close', () => sockets.delete(client));
    client.on('error', () => {});
    void relayAuthenticatedSocks5(client, proxy, sockets);
  });
  await new Promise((resolve, reject) => {
    const onError = error => reject(error);
    server.once('error', onError);
    server.listen(0, '127.0.0.1', () => {
      server.off('error', onError);
      resolve();
    });
  });
  server.on('error', error => console.error(`SOCKS5 bridge failed: ${error?.message || error}`));
  const address = server.address();
  return {
    proxy: { server: `socks5://127.0.0.1:${address.port}` },
    async close() {
      for (const socket of sockets) socket.destroy();
      if (!server.listening) return;
      await new Promise(resolve => server.close(resolve));
    },
  };
}

async function prepareBrowserProxy() {
  if (!job.proxy?.server) return { proxy: undefined, close: async () => {} };
  const configured = {
    server: job.proxy.server,
    username: job.proxy.username || undefined,
    password: job.proxy.password || undefined,
  };
  const proxyURL = new URL(configured.server);
  if (proxyURL.protocol !== 'socks5:' || (!configured.username && !configured.password)) {
    return { proxy: configured, close: async () => {} };
  }
  return createAuthenticatedSocks5Bridge(configured);
}

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
  activeProxyBridge = await prepareBrowserProxy();
  try {
    writeState('starting', 'Запуск удалённого Chromium...', 5);
    const context = await chromium.launchPersistentContext(job.profile_dir, {
      headless: false, viewport: null, screen: { width: 1280, height: 800 }, proxy: activeProxyBridge.proxy,
      ignoreDefaultArgs: ['--enable-automation'],
      locale: 'ru-RU', timezoneId: 'Europe/Moscow',
      permissions: ['clipboard-read', 'clipboard-write'],
      args: ['--no-first-run', '--no-default-browser-check', '--disable-background-networking', '--disable-blink-features=AutomationControlled', '--window-size=1280,800'],
    });
    activeContext = context;
    await context.addInitScript(() => {
      Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
    });
    const page = context.pages()[0] || await context.newPage();
    if (provider === 'wbstream') {
      await runWB(context, page);
    } else if (provider === 'telemost') {
      await runTelemost(context, page);
    } else {
      throw new Error(`Неподдерживаемый provider автоматизации: ${provider}`);
    }
  } finally {
    const context = activeContext;
    activeContext = undefined;
    await context?.close().catch(() => {});
    const bridge = activeProxyBridge;
    activeProxyBridge = undefined;
    await bridge?.close().catch(() => {});
  }
}

main().catch(error => {
  writeState('error', error?.message || String(error), 0);
  process.exitCode = 1;
});
