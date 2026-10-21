/*
 * Touch gesture -> Overscreen input event translation. Pure logic, no DOM access, so it can be
 * unit tested with Node (see Tests/web/gestures.test.mjs).
 *
 * One finger: press/drag/release. A short delay distinguishes it from the start of a
 * two-finger gesture. Two fingers: scroll by moving, right click by tapping.
 * Coordinates are normalized (0..1) to the video content; the host counts double clicks.
 */
(function (root) {
  'use strict';

  var PRESS_DELAY_MS = 80;
  var MOVE_SLOP = 0.01;
  var TWO_FINGER_TAP_MS = 400;

  function midpoint(touches) {
    var x = 0, y = 0;
    for (var i = 0; i < touches.length; i++) { x += touches[i].x; y += touches[i].y; }
    return { x: x / touches.length, y: y / touches.length };
  }

  function dist(a, b) {
    var dx = a.x - b.x, dy = a.y - b.y;
    return Math.sqrt(dx * dx + dy * dy);
  }

  /**
   * @param {function(object)} send   receives events like {t:'down',x,y}
   * @param {object} timers           {now(), setTimeout(fn, ms), clearTimeout(id)}
   */
  function GestureTracker(send, timers) {
    this.send = send;
    this.timers = timers;
    this.mode = 'idle';
    this.timer = null;
    this.start = null;
    this.last = null;
    this.twoStart = null;
    this.twoLast = null;
    this.twoStartTime = 0;
    this.twoMoved = false;
  }

  GestureTracker.prototype._clearTimer = function () {
    if (this.timer !== null) { this.timers.clearTimeout(this.timer); this.timer = null; }
  };

  GestureTracker.prototype._press = function () {
    this._clearTimer();
    this.mode = 'drag';
    this.send({ t: 'down', x: this.start.x, y: this.start.y });
    if (this.last && (this.last.x !== this.start.x || this.last.y !== this.start.y)) {
      this.send({ t: 'move', x: this.last.x, y: this.last.y });
    }
  };

  /** @param {Array<{x:number,y:number}>} touches all fingers currently on the surface */
  GestureTracker.prototype.touchStart = function (touches) {
    if (touches.length === 1 && this.mode === 'idle') {
      this.mode = 'pending';
      this.start = { x: touches[0].x, y: touches[0].y };
      this.last = this.start;
      var self = this;
      this.timer = this.timers.setTimeout(function () { self.timer = null; if (self.mode === 'pending') self._press(); }, PRESS_DELAY_MS);
      return;
    }
    if (touches.length >= 2 && this.mode !== 'two') {
      this._clearTimer();
      if (this.mode === 'drag') this.send({ t: 'up', x: this.last.x, y: this.last.y });
      this.mode = 'two';
      this.twoStart = midpoint(touches);
      this.twoLast = this.twoStart;
      this.twoStartTime = this.timers.now();
      this.twoMoved = false;
    }
  };

  GestureTracker.prototype.touchMove = function (touches) {
    if (touches.length === 0) return;
    if (this.mode === 'pending') {
      this.last = { x: touches[0].x, y: touches[0].y };
      if (dist(this.last, this.start) > MOVE_SLOP) this._press();
    } else if (this.mode === 'drag') {
      this.last = { x: touches[0].x, y: touches[0].y };
      this.send({ t: 'move', x: this.last.x, y: this.last.y });
    } else if (this.mode === 'two' && touches.length >= 2) {
      var mid = midpoint(touches);
      var dx = mid.x - this.twoLast.x, dy = mid.y - this.twoLast.y;
      if (dist(mid, this.twoStart) > MOVE_SLOP) this.twoMoved = true;
      if (this.twoMoved && (dx !== 0 || dy !== 0)) this.send({ t: 'scroll', x: mid.x, y: mid.y, dx: dx, dy: dy });
      this.twoLast = mid;
    }
  };

  /**
   * @param {Array<{x:number,y:number}>} remaining fingers still down
   * @param {{x:number,y:number}} lifted position of the finger that was lifted
   */
  GestureTracker.prototype.touchEnd = function (remaining, lifted) {
    if (this.mode === 'pending') {
      this._clearTimer();
      this.send({ t: 'down', x: this.start.x, y: this.start.y });
      this.send({ t: 'up', x: lifted.x, y: lifted.y });
      this.mode = 'idle';
    } else if (this.mode === 'drag') {
      this.send({ t: 'up', x: lifted.x, y: lifted.y });
      this.mode = 'idle';
    } else if (this.mode === 'two' && remaining.length === 0) {
      if (!this.twoMoved && this.timers.now() - this.twoStartTime <= TWO_FINGER_TAP_MS) {
        this.send({ t: 'rightclick', x: this.twoStart.x, y: this.twoStart.y });
      }
      this.mode = 'idle';
    }
  };

  GestureTracker.prototype.cancel = function () {
    this._clearTimer();
    if (this.mode === 'drag' && this.last) this.send({ t: 'up', x: this.last.x, y: this.last.y });
    this.mode = 'idle';
  };

  /**
   * Maps a client point to normalized video content coordinates for a <video> using
   * object-fit: contain (letterboxing excluded). Returns null when there is no video yet.
   */
  function normalize(clientX, clientY, rect, videoWidth, videoHeight) {
    if (!videoWidth || !videoHeight || !rect.width || !rect.height) return null;
    var scale = Math.min(rect.width / videoWidth, rect.height / videoHeight);
    var w = videoWidth * scale, h = videoHeight * scale;
    var left = rect.left + (rect.width - w) / 2, top = rect.top + (rect.height - h) / 2;
    var x = (clientX - left) / w, y = (clientY - top) / h;
    return { x: Math.min(Math.max(x, 0), 1), y: Math.min(Math.max(y, 0), 1), width: w, height: h };
  }

  var api = { GestureTracker: GestureTracker, normalize: normalize, PRESS_DELAY_MS: PRESS_DELAY_MS };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.OverscreenGestures = api;
})(this);
