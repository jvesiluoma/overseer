// End-to-end protocol test against the real SwiftNIO server (fake media engine).
// Requires a built dev server: swift build --product overscreen-devserver
const test = require('node:test');
const assert = require('node:assert');
const { spawn } = require('node:child_process');
const http = require('node:http');
const path = require('node:path');

const BIN = process.env.OVERSCREEN_DEVSERVER || path.join(__dirname, '../../.build/debug/overscreen-devserver');
const PORT = 18000 + Math.floor(Math.random() * 1000);
const BASE = `http://127.0.0.1:${PORT}`;

let server;
let code;

test.before(async () => {
  server = spawn(BIN, ['--port', String(PORT), '--host', '127.0.0.1'], { stdio: ['ignore', 'pipe', 'inherit'] });
  await new Promise((resolve, reject) => {
    let out = '';
    server.stdout.on('data', (d) => {
      out += d;
      const m = out.match(/pairing code: (\d{6})/g);
      if (m) code = m[m.length - 1].slice(-6);
      if (code && out.includes('listening')) resolve();
    });
    server.on('exit', (c) => reject(new Error('devserver exited ' + c)));
  });
});

test.after(() => server && server.kill());

async function pair() {
  const r = await fetch(`${BASE}/api/pair`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code }) });
  assert.strictEqual(r.status, 200);
  const { token } = await r.json();
  // The code rotates after use; pick up the new one from stdout.
  await new Promise((res) => setTimeout(res, 50));
  return token;
}

test('static client and info', async () => {
  const r = await fetch(`${BASE}/`);
  assert.strictEqual(r.status, 200);
  assert.match(r.headers.get('content-security-policy'), /default-src 'self'/);
  assert.match(await r.text(), /Overscreen/);
  const info = await (await fetch(`${BASE}/api/info`)).json();
  assert.strictEqual(info.pairingRequired, true);
  assert.strictEqual(info.displays.length, 2);
});

test('raw traversal attempts get 404 (no filesystem)', async () => {
  for (const p of ['/../../etc/passwd', '/C:/Windows/win.ini', '/..%2f..%2fetc%2fpasswd', '//etc/passwd']) {
    const status = await new Promise((resolve) => {
      http.get({ host: '127.0.0.1', port: PORT, path: p, headers: { Host: 'localhost' } }, (res) => { res.resume(); resolve(res.statusCode); });
    });
    assert.strictEqual(status, 404, p);
  }
});

test('websocket rejects missing token and foreign origin', async () => {
  const token = await pair();
  const upgrade = (p, origin) => new Promise((resolve) => {
    const req = http.request({ host: '127.0.0.1', port: PORT, path: p, headers: {
      Host: `127.0.0.1:${PORT}`, Connection: 'Upgrade', Upgrade: 'websocket', 'Sec-WebSocket-Version': '13',
      'Sec-WebSocket-Key': 'dGhlIHNhbXBsZSBub25jZQ==', ...(origin ? { Origin: origin } : {}) } });
    req.on('upgrade', (res, socket) => { socket.destroy(); resolve(101); });
    req.on('response', (res) => { res.resume(); resolve(res.statusCode); });
    req.end();
  });
  assert.strictEqual(await upgrade('/ws'), 401);
  assert.strictEqual(await upgrade(`/ws?token=${token}`, 'http://evil.example'), 401);
  assert.strictEqual(await upgrade(`/ws?token=${token}`, `http://127.0.0.1:${PORT}`), 101);
});

test('signaling: hello -> preparing + offer, answer -> streaming, ping/pong', async () => {
  const token = await pair();
  const ws = new WebSocket(`ws://127.0.0.1:${PORT}/ws?token=${token}`);
  const messages = [];
  const waitFor = (pred) => new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error('timeout; got ' + JSON.stringify(messages))), 6000);
    const check = () => { const m = messages.find(pred); if (m) { clearTimeout(t); resolve(m); return true; } return false; };
    if (!check()) ws.addEventListener('message', function h() { if (check()) ws.removeEventListener('message', h); });
  });
  ws.addEventListener('message', (e) => messages.push(JSON.parse(e.data)));
  await new Promise((r) => ws.addEventListener('open', r));
  ws.send(JSON.stringify({ type: 'hello', display: 1, viewport: { width: 1280, height: 800, dpr: 1.5 } }));
  await waitFor((m) => m.type === 'status' && m.state === 'preparing');
  const offer = await waitFor((m) => m.type === 'offer');
  assert.match(offer.sdp, /^v=0/);
  ws.send(JSON.stringify({ type: 'answer', sdp: 'v=0\r\n' }));
  const streaming = await waitFor((m) => m.type === 'status' && m.state === 'streaming');
  assert.deepStrictEqual([streaming.width, streaming.height], [1920, 1200]);
  const ping = await waitFor((m) => m.type === 'ping');
  ws.send(JSON.stringify({ type: 'pong', id: ping.id }));
  ws.send(JSON.stringify({ type: 'nonsense' }));
  const err = await waitFor((m) => m.type === 'error');
  assert.strictEqual(err.code, 'bad_message');
  const info = await (await fetch(`${BASE}/api/info`)).json();
  assert.strictEqual(info.displays[1].inUse, true);

  // A second client on the same display replaces the first.
  const token2 = await pair();
  const ws2 = new WebSocket(`ws://127.0.0.1:${PORT}/ws?token=${token2}`);
  await new Promise((r) => ws2.addEventListener('open', r));
  ws2.send(JSON.stringify({ type: 'hello', display: 1, viewport: { width: 800, height: 600, dpr: 1 } }));
  const bye = await waitFor((m) => m.type === 'bye');
  assert.strictEqual(bye.reason, 'replaced');
  ws2.close();
  ws.close();
});

test('pairing lockout', async () => {
  const wrong = code === '000000' ? '111111' : '000000';
  let last;
  for (let i = 0; i < 5; i++) {
    last = await fetch(`${BASE}/api/pair`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: wrong }) });
  }
  assert.strictEqual(last.status, 429);
  assert.strictEqual(last.headers.get('retry-after'), '30');
});
