// End-to-end TLS test against the real SwiftNIO server (fake media engine).
// Requires a built dev server: swift build --product overscreen-devserver
const test = require('node:test');
const assert = require('node:assert');
const { spawn } = require('node:child_process');
const crypto = require('node:crypto');
const fs = require('node:fs');
const http = require('node:http');
const https = require('node:https');
const os = require('node:os');
const path = require('node:path');

const BIN = process.env.OVERSCREEN_DEVSERVER || path.join(__dirname, '../../.build/debug/overscreen-devserver');
const PORT = 19000 + Math.floor(Math.random() * 500);
const HTTP_PORT = PORT + 500;
const DIR = fs.mkdtempSync(path.join(os.tmpdir(), 'overscreen-tls-'));

let server;
let code;
let fingerprint;

test.before(async () => {
  server = spawn(BIN, ['--port', String(PORT), '--host', '127.0.0.1', '--tls', DIR, '--http-port', String(HTTP_PORT)],
    { stdio: ['ignore', 'pipe', 'inherit'] });
  await new Promise((resolve, reject) => {
    let out = '';
    server.stdout.on('data', (d) => {
      out += d;
      const m = out.match(/pairing code: (\d{6})/g);
      if (m) code = m[m.length - 1].slice(-6);
      const f = out.match(/certificate sha256: ([0-9A-F:]+)/);
      if (f) fingerprint = f[1];
      if (code && fingerprint && out.includes('listening')) resolve();
    });
    server.on('exit', (c) => reject(new Error('devserver exited ' + c)));
  });
});

test.after(() => {
  if (server) server.kill();
  fs.rmSync(DIR, { recursive: true, force: true });
});

// The certificate is self-signed, so verify it by pinning the fingerprint instead of a CA.
function request(method, p, { body, headers = {} } = {}) {
  return new Promise((resolve, reject) => {
    const req = https.request({
      host: '127.0.0.1', port: PORT, path: p, method, rejectUnauthorized: false,
      headers: { Host: `127.0.0.1:${PORT}`, ...headers },
    }, (res) => {
      const cert = res.socket.getPeerCertificate();
      let data = '';
      res.on('data', (d) => { data += d; });
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: data, cert, protocol: res.socket.getProtocol() }));
    });
    req.on('error', reject);
    req.on('upgrade', (res, socket) => { socket.destroy(); resolve({ status: res.statusCode }); });
    if (body) req.write(body);
    req.end();
  });
}

test('serves the client over TLS with the advertised certificate', async () => {
  const r = await request('GET', '/');
  assert.strictEqual(r.status, 200);
  assert.strictEqual(r.cert.fingerprint256, fingerprint);
  assert.match(r.protocol, /^TLSv1\.[23]$/);
  const x509 = new crypto.X509Certificate(r.cert.raw);
  assert.match(x509.subjectAltName, /IP Address:127\.0\.0\.1/);
  assert.ok(x509.keyUsage === undefined || !x509.ca, 'not a CA');
});

test('pairs and upgrades the websocket over TLS', async () => {
  const pair = await request('POST', '/api/pair', { body: JSON.stringify({ code }), headers: { 'Content-Type': 'application/json' } });
  assert.strictEqual(pair.status, 200);
  const { token } = JSON.parse(pair.body);
  const ws = await request('GET', '/ws?token=' + token, {
    headers: {
      Connection: 'Upgrade', Upgrade: 'websocket', 'Sec-WebSocket-Version': '13',
      'Sec-WebSocket-Key': crypto.randomBytes(16).toString('base64'), Origin: `https://127.0.0.1:${PORT}`,
    },
  });
  assert.strictEqual(ws.status, 101);
});

test('plain HTTP port only redirects', async () => {
  const get = (p, opts = {}) => new Promise((resolve, reject) => {
    const req = http.request({ host: '127.0.0.1', port: HTTP_PORT, path: p, headers: { Host: `127.0.0.1:${HTTP_PORT}` }, ...opts },
      (res) => { res.resume(); resolve(res); });
    req.on('error', reject);
    req.end(opts.body);
  });
  const r = await get('/');
  assert.strictEqual(r.statusCode, 307);
  assert.strictEqual(r.headers.location, `https://127.0.0.1:${PORT}/`);
  const p = await get('/api/pair', { method: 'POST', body: JSON.stringify({ code: '000000' }) });
  assert.strictEqual(p.statusCode, 307, 'the pairing API is not reachable without TLS');
});

test('the TLS port refuses plain HTTP', async () => {
  const outcome = await new Promise((resolve) => {
    const req = http.get({ host: '127.0.0.1', port: PORT, path: '/', timeout: 3000 }, (res) => { res.resume(); resolve('response ' + res.statusCode); });
    req.on('error', () => resolve('error'));
    req.on('timeout', () => { req.destroy(); resolve('error'); });
  });
  assert.strictEqual(outcome, 'error');
});
