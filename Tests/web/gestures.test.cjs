// node --test Tests/web/*.test.cjs
const test = require('node:test');
const assert = require('node:assert');
const path = require('node:path');
const { GestureTracker, normalize, PRESS_DELAY_MS } = require(path.join(__dirname, '../../WebClient/gestures.js'));

function setup() {
  const sent = [];
  let now = 0;
  let timers = [];
  const clock = {
    now: () => now,
    setTimeout: (fn, ms) => { const t = { fn, at: now + ms }; timers.push(t); return t; },
    clearTimeout: (t) => { timers = timers.filter((x) => x !== t); },
  };
  const advance = (ms) => {
    now += ms;
    const due = timers.filter((t) => t.at <= now);
    timers = timers.filter((t) => t.at > now);
    due.forEach((t) => t.fn());
  };
  return { tracker: new GestureTracker((e) => sent.push(e), clock), sent, advance };
}

const p = (x, y) => ({ x, y });

test('quick tap sends down+up immediately on release', () => {
  const { tracker, sent } = setup();
  tracker.touchStart([p(0.5, 0.5)]);
  assert.deepStrictEqual(sent, []);
  tracker.touchEnd([], p(0.5, 0.5));
  assert.deepStrictEqual(sent, [{ t: 'down', x: 0.5, y: 0.5 }, { t: 'up', x: 0.5, y: 0.5 }]);
});

test('hold presses after the delay, then drags', () => {
  const { tracker, sent, advance } = setup();
  tracker.touchStart([p(0.1, 0.1)]);
  advance(PRESS_DELAY_MS);
  tracker.touchMove([p(0.2, 0.1)]);
  tracker.touchEnd([], p(0.2, 0.1));
  assert.deepStrictEqual(sent.map((e) => e.t), ['down', 'move', 'up']);
});

test('moving before the delay presses at the start point', () => {
  const { tracker, sent } = setup();
  tracker.touchStart([p(0.1, 0.1)]);
  tracker.touchMove([p(0.3, 0.1)]);
  assert.deepStrictEqual(sent, [{ t: 'down', x: 0.1, y: 0.1 }, { t: 'move', x: 0.3, y: 0.1 }]);
});

test('two-finger tap is a right click and never presses the left button', () => {
  const { tracker, sent, advance } = setup();
  tracker.touchStart([p(0.4, 0.5)]);
  tracker.touchStart([p(0.4, 0.5), p(0.6, 0.5)]);
  advance(200);
  tracker.touchEnd([p(0.6, 0.5)], p(0.4, 0.5));
  tracker.touchEnd([], p(0.6, 0.5));
  assert.deepStrictEqual(sent, [{ t: 'rightclick', x: 0.5, y: 0.5 }]);
});

test('two-finger pan scrolls by midpoint delta', () => {
  const { tracker, sent } = setup();
  tracker.touchStart([p(0.4, 0.5), p(0.6, 0.5)]);
  tracker.touchMove([p(0.4, 0.6), p(0.6, 0.6)]);
  tracker.touchEnd([], p(0.4, 0.6));
  assert.strictEqual(sent.length, 1);
  assert.strictEqual(sent[0].t, 'scroll');
  assert.ok(Math.abs(sent[0].dy - 0.1) < 1e-9);
  assert.strictEqual(sent[0].dx, 0);
});

test('second finger during a drag releases the button first', () => {
  const { tracker, sent, advance } = setup();
  tracker.touchStart([p(0.4, 0.5)]);
  advance(100);
  tracker.touchStart([p(0.4, 0.5), p(0.6, 0.5)]);
  assert.deepStrictEqual(sent.map((e) => e.t), ['down', 'up']);
});

test('cancel releases a held button', () => {
  const { tracker, sent, advance } = setup();
  tracker.touchStart([p(0.4, 0.5)]);
  advance(100);
  tracker.cancel();
  assert.deepStrictEqual(sent.map((e) => e.t), ['down', 'up']);
});

test('normalize removes letterboxing', () => {
  // 16:10 video in a 4:3 box: bars top and bottom.
  const rect = { left: 0, top: 0, width: 1000, height: 750 };
  const top = normalize(500, 62.5, rect, 1600, 1000); // 625px-high content, 62.5px bars
  assert.deepStrictEqual([top.x, top.y], [0.5, 0]);
  const mid = normalize(500, 375, rect, 1600, 1000);
  assert.deepStrictEqual([mid.x, mid.y], [0.5, 0.5]);
  const outside = normalize(-50, 9999, rect, 1600, 1000);
  assert.deepStrictEqual([outside.x, outside.y], [0, 1]);
  assert.strictEqual(normalize(1, 1, rect, 0, 0), null);
});
