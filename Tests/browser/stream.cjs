// Real-browser check: the Go dev server streams over WebRTC to Chrome, input comes back.
//   node stream.cjs <overscreen-devserver binary> [extra devserver flags]
// Defaults to --media test (FFmpeg test pattern, any OS). On Windows, pass --media native
// (virtual display) or --media native --mirror. CHROME=<path> selects the browser.
const { spawn } = require('node:child_process');
const puppeteer = require('puppeteer-core');

const BIN = process.argv[2];
const EXTRA = process.argv.slice(3);
const MEDIA = EXTRA.includes('--media') ? EXTRA : ['--media', 'test', ...EXTRA];
const NATIVE = MEDIA.includes('native');
const CHROME = process.env.CHROME || {
  darwin: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  win32: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  linux: '/usr/bin/google-chrome',
}[process.platform];
const PORT = 20000 + Math.floor(Math.random() * 1000);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const server = spawn(BIN, ['--port', String(PORT), '--host', '127.0.0.1', '--no-pairing', ...MEDIA]);
  let log = '';
  server.stdout.on('data', (d) => { log += d; });
  server.stderr.on('data', (d) => { log += d; });
  for (let i = 0; i < 100 && !log.includes('listening'); i++) await sleep(100);
  if (!log.includes('listening')) throw new Error('server did not start:\n' + log);

  const browser = await puppeteer.launch({
    executablePath: CHROME,
    headless: 'new',
    args: ['--autoplay-policy=no-user-gesture-required', '--no-first-run'],
  });
  let ok = false;
  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 1280, height: 800, deviceScaleFactor: 1 });
    page.on('console', (m) => console.log('page:', m.text()));
    await page.goto(`http://127.0.0.1:${PORT}/`);
    await page.waitForSelector('#display-list button', { visible: true, timeout: 10000 });
    await page.click('#display-list button');
    // Video must start and keep advancing.
    await page.waitForFunction(() => { const v = document.getElementById('video'); return v && v.videoWidth > 0 && v.currentTime > 0.5; }, { timeout: 20000 });
    const first = await page.evaluate(() => { const v = document.getElementById('video'); return { w: v.videoWidth, h: v.videoHeight, t: v.currentTime }; });
    await sleep(2000);
    const second = await page.evaluate(() => document.getElementById('video').currentTime);
    const stats = await page.evaluate(async () => {
      // The client keeps its RTCPeerConnection private; read inbound stats via the video instead.
      const v = document.getElementById('video');
      const q = v.getVideoPlaybackQuality ? v.getVideoPlaybackQuality() : {};
      return { decoded: q.totalVideoFrames, dropped: q.droppedVideoFrames };
    });
    console.log('video', first, 'advanced to', second.toFixed(2), 'frames', stats);
    // Tap in the middle of the video → input over the DataChannel.
    const box = await (await page.$('#video')).boundingBox();
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
    await sleep(800);
    // The test platform logs injected input; the native one really moves the pointer.
    const gotInput = NATIVE || (/input: leftDown \d+,\d+/.test(log) && /input: leftUp/.test(log));
    console.log('input reached host:', gotInput);
    // Resize the browser: the host resizes the display and restarts the encoder; playback must
    // continue. (Mirror mode keeps the monitor's own size.)
    const mirror = MEDIA.includes('--mirror');
    await page.setViewport({ width: 1000, height: 700, deviceScaleFactor: 1 });
    if (!mirror) await page.waitForFunction(() => document.getElementById('video').videoWidth === 1000, { timeout: 30000 });
    const t1 = await page.evaluate(() => document.getElementById('video').currentTime);
    await sleep(1500);
    const after = await page.evaluate(() => { const v = document.getElementById('video'); return { w: v.videoWidth, h: v.videoHeight, t: v.currentTime }; });
    console.log('after resize', after, 'advanced from', t1.toFixed(2));
    ok = first.w > 0 && second > first.t + 1 && stats.decoded > 20 && gotInput && (mirror || after.h === 700) && after.t > t1 + 1;
  } finally {
    await browser.close();
    server.kill();
  }
  console.log(log.split('\n').filter((l) => /encoder|input:|capture|failed|error/i.test(l)).slice(0, 12).join('\n'));
  console.log(ok ? 'PASS' : 'FAIL');
  process.exit(ok ? 0 : 1);
})().catch((e) => { console.error(e); process.exit(1); });
