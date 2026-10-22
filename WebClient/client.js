/* Overscreen browser client: pairing, display picker, WebRTC viewer and input forwarding. */
(function () {
  'use strict';

  var G = window.OverscreenGestures;
  var TOKEN_KEY = 'overscreen.token';
  var CODE_LENGTH = 6;

  var $ = function (id) { return document.getElementById(id); };
  var els = {
    setup: $('setup'), hostName: $('host-name'), setupError: $('setup-error'), insecure: $('insecure'),
    pair: $('pair'), codeDisplay: $('code-display'), keypad: $('keypad'), pairError: $('pair-error'),
    pick: $('pick'), displayList: $('display-list'), forget: $('forget'),
    stage: $('stage'), video: $('video'), overlay: $('overlay'), overlayText: $('overlay-text'),
    overlayAction: $('overlay-action'), handle: $('handle'), menu: $('menu'), hud: $('hud'),
    menuFullscreen: $('menu-fullscreen'), menuStats: $('menu-stats'), menuDisconnect: $('menu-disconnect')
  };

  // ---------------------------------------------------------------- storage (may be unavailable)
  function loadToken() { try { return localStorage.getItem(TOKEN_KEY); } catch (e) { return null; } }
  function saveToken(t) { try { localStorage.setItem(TOKEN_KEY, t); } catch (e) { /* session only */ } }
  function clearToken() { try { localStorage.removeItem(TOKEN_KEY); } catch (e) { /* ignore */ } }

  var token = loadToken();
  var info = null;

  function api(method, path, body) {
    var opts = { method: method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    if (token) opts.headers.Authorization = 'Bearer ' + token;
    return fetch(path, opts).then(function (r) {
      if (r.status === 204) return { status: 204, body: null };
      return r.json().then(function (j) { return { status: r.status, body: j }; },
                           function () { return { status: r.status, body: null }; });
    });
  }

  // ---------------------------------------------------------------- setup flow
  function isLoopback(h) { return h === 'localhost' || h === '127.0.0.1' || h === '[::1]'; }

  function boot() {
    els.setupError.textContent = '';
    els.insecure.hidden = location.protocol === 'https:' || isLoopback(location.hostname);
    api('GET', '/api/info').then(function (r) {
      info = r.body;
      els.hostName.textContent = info.name + ' · v' + info.version;
      if (!token) return needToken();
      return api('GET', '/api/auth').then(function (a) {
        if (a.status === 204) return showPicker();
        clearToken(); token = null;
        return needToken();
      });
    }).catch(function () {
      els.setupError.textContent = 'Cannot reach the host. Retrying…';
      setTimeout(boot, 3000);
    });
  }

  function needToken() {
    if (!info.pairingRequired) {
      return api('POST', '/api/pair', {}).then(function (r) {
        if (r.status === 200) { token = r.body.token; saveToken(token); showPicker(); }
        else els.setupError.textContent = 'Could not connect (' + r.status + ')';
      });
    }
    showPairing();
  }

  var code = '';
  function showPairing() {
    els.pick.hidden = true;
    els.pair.hidden = false;
    code = '';
    renderCode();
  }

  function renderCode() {
    var s = '';
    for (var i = 0; i < CODE_LENGTH; i++) s += i < code.length ? code[i] : '·';
    els.codeDisplay.textContent = s;
  }

  function buildKeypad() {
    var keys = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '⌫', '0', 'OK'];
    keys.forEach(function (k) {
      var b = document.createElement('button');
      b.type = 'button';
      b.textContent = k;
      b.addEventListener('click', function () { keyPressed(k); });
      els.keypad.appendChild(b);
    });
    document.addEventListener('keydown', function (e) {
      if (els.pair.hidden) return;
      if (/^[0-9]$/.test(e.key)) keyPressed(e.key);
      else if (e.key === 'Backspace') keyPressed('⌫');
      else if (e.key === 'Enter') keyPressed('OK');
    });
  }

  function keyPressed(k) {
    els.pairError.textContent = '';
    if (k === '⌫') code = code.slice(0, -1);
    else if (k === 'OK') { if (code.length === CODE_LENGTH) submitCode(); }
    else if (code.length < CODE_LENGTH) code += k;
    renderCode();
    if (code.length === CODE_LENGTH && k !== 'OK' && k !== '⌫') submitCode();
  }

  function submitCode() {
    var attempt = code;
    api('POST', '/api/pair', { code: attempt }).then(function (r) {
      if (r.status === 200) {
        token = r.body.token;
        saveToken(token);
        els.pair.hidden = true;
        showPicker();
        return;
      }
      code = '';
      renderCode();
      if (r.status === 429) els.pairError.textContent = 'Too many attempts. Try again in ' + r.body.retryAfter + ' s.';
      else if (r.status === 401) els.pairError.textContent = 'Wrong code. ' + r.body.remainingAttempts + ' attempts left.';
      else els.pairError.textContent = 'Pairing failed (' + r.status + ').';
    }).catch(function () { els.pairError.textContent = 'Network error.'; });
  }

  function showPicker() {
    els.pair.hidden = true;
    els.pick.hidden = false;
    els.displayList.textContent = '';
    info.displays.forEach(function (d) {
      var b = document.createElement('button');
      b.type = 'button';
      var name = document.createElement('span');
      name.textContent = d.name; // never innerHTML: names come from the host's settings
      b.appendChild(name);
      if (d.inUse) {
        var tag = document.createElement('span');
        tag.className = 'tag';
        tag.textContent = 'in use';
        b.appendChild(tag);
      }
      b.addEventListener('click', function () { connect(d.id); });
      els.displayList.appendChild(b);
    });
  }

  els.forget.addEventListener('click', function () {
    clearToken(); token = null; boot();
  });

  // ---------------------------------------------------------------- streaming
  var session = null;

  function enterFullscreen() {
    var el = document.documentElement;
    var req = el.requestFullscreen || el.webkitRequestFullscreen;
    if (!req) return Promise.resolve();
    try {
      var p = req.call(el);
      return (p && p.then ? p : Promise.resolve()).catch(function () {});
    } catch (e) { return Promise.resolve(); }
  }

  function viewport() {
    return { width: window.innerWidth, height: window.innerHeight, dpr: window.devicePixelRatio || 1 };
  }

  function connect(displayId) {
    els.setup.hidden = true;
    els.stage.hidden = false;
    showOverlay('Starting…', true);
    // Must run inside the click gesture; give the browser a moment to settle the new size.
    enterFullscreen().then(function () {
      setTimeout(function () { startSession(displayId); }, 250);
    });
    try { if (navigator.wakeLock) navigator.wakeLock.request('screen').catch(function () {}); } catch (e) { /* insecure origin */ }
  }

  function showOverlay(text, busy, actionLabel, action) {
    els.overlay.hidden = false;
    els.overlay.classList.toggle('idle', !busy);
    els.overlayText.textContent = text;
    els.overlayAction.hidden = !actionLabel;
    if (actionLabel) {
      els.overlayAction.textContent = actionLabel;
      els.overlayAction.onclick = action;
    }
  }

  function Session(displayId) {
    this.displayId = displayId;
    this.retry = 0;
    this.stopped = false;
    this.ws = null;
    this.pc = null;
    this.channel = null;
    this.pendingCandidates = [];
    this.statsTimer = null;
    this.retryTimer = null;
    this.latencyMs = null;
    this.lastStats = null;
  }

  Session.prototype.open = function () {
    var self = this;
    this.cleanupPeer();
    var url = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws?token=' + encodeURIComponent(token);
    var ws = new WebSocket(url);
    this.ws = ws;
    ws.onopen = function () {
      self.retry = 0;
      self.send({ type: 'hello', display: self.displayId, viewport: viewport() });
    };
    ws.onmessage = function (e) {
      var msg;
      try { msg = JSON.parse(e.data); } catch (err) { return; }
      self.onMessage(msg);
    };
    ws.onclose = function () {
      if (self.ws !== ws) return;
      self.ws = null;
      if (!self.stopped) self.scheduleReconnect();
    };
  };

  Session.prototype.send = function (msg) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg));
  };

  Session.prototype.makePeer = function () {
    var self = this;
    var pc = new RTCPeerConnection({ iceServers: [] });
    this.pc = pc;
    pc.ontrack = function (e) {
      var stream = e.streams && e.streams[0] ? e.streams[0] : new MediaStream([e.track]);
      els.video.srcObject = stream;
      var p = els.video.play();
      if (p && p.catch) p.catch(function () {});
    };
    pc.onicecandidate = function (e) {
      if (e.candidate) {
        self.send({ type: 'candidate', candidate: e.candidate.candidate, sdpMid: e.candidate.sdpMid,
                    sdpMLineIndex: e.candidate.sdpMLineIndex });
      }
    };
    pc.ondatachannel = function (e) {
      if (e.channel.label === 'input') self.channel = e.channel;
    };
    pc.onconnectionstatechange = function () {
      if (pc.connectionState === 'failed') self.reconnectNow();
    };
    this.statsTimer = setInterval(function () { self.collectStats(); }, 1000);
    return pc;
  };

  Session.prototype.onMessage = function (msg) {
    var self = this;
    switch (msg.type) {
      case 'offer':
        this.cleanupPeer();
        var pc = this.makePeer();
        pc.setRemoteDescription({ type: 'offer', sdp: msg.sdp })
          .then(function () {
            self.pendingCandidates.forEach(function (c) { pc.addIceCandidate(c).catch(function () {}); });
            self.pendingCandidates = [];
            return pc.createAnswer();
          })
          .then(function (answer) { return pc.setLocalDescription(answer); })
          .then(function () { self.send({ type: 'answer', sdp: pc.localDescription.sdp }); })
          .catch(function (err) { showOverlay('Negotiation failed: ' + err, false, 'Retry', function () { self.reconnectNow(); }); });
        break;
      case 'candidate':
        var c = { candidate: msg.candidate, sdpMid: msg.sdpMid, sdpMLineIndex: msg.sdpMLineIndex };
        if (this.pc && this.pc.remoteDescription) this.pc.addIceCandidate(c).catch(function () {});
        else this.pendingCandidates.push(c);
        break;
      case 'ping':
        this.latencyMs = msg.latencyMs;
        this.send({ type: 'pong', id: msg.id });
        break;
      case 'status':
        if (msg.state === 'preparing') showOverlay('Preparing display…', true);
        if (msg.state === 'streaming') waitForVideo();
        break;
      case 'error':
        showOverlay('Host error: ' + msg.message, false, 'Back', stopSession);
        this.stopped = true;
        break;
      case 'bye':
        this.stopped = true;
        if (msg.reason === 'replaced') showOverlay('Another device is now using this display.', false, 'Reconnect', restartSession);
        else if (msg.reason === 'host_stopped') showOverlay('The host stopped sharing.', false, 'Reconnect', restartSession);
        else if (msg.reason === 'driver_missing') showOverlay('The computer needs one more thing: Extend mode needs the free Virtual Display Driver.\n\n' +
          'On the computer, click the Overscreen icon in the notification area and choose "Show setup steps". ' +
          'Or choose "Mirror main screen" there to share the existing screen without a driver.', false, 'Reconnect', restartSession);
        else showOverlay('Disconnected (' + msg.reason + ').', false, 'Reconnect', restartSession);
        break;
    }
  };

  function waitForVideo() {
    if (els.video.readyState >= 2 && els.video.videoWidth) { els.overlay.hidden = true; return; }
    showOverlay('Waiting for video…', true);
    els.video.addEventListener('playing', function onPlaying() {
      els.video.removeEventListener('playing', onPlaying);
      els.overlay.hidden = true;
    });
  }

  Session.prototype.collectStats = function () {
    var self = this;
    if (!this.pc || !this.pc.getStats) return;
    this.pc.getStats().then(function (report) {
      var s = { t: 'stats' };
      report.forEach(function (r) {
        if (r.type === 'inbound-rtp' && r.kind === 'video') {
          if (r.framesPerSecond !== undefined) s.fps = r.framesPerSecond;
          if (r.jitterBufferEmittedCount) s.jitter = 1000 * r.jitterBufferDelay / r.jitterBufferEmittedCount;
          if (r.framesDecoded) s.decode = 1000 * r.totalDecodeTime / r.framesDecoded;
        }
        if (r.type === 'candidate-pair' && r.nominated && r.currentRoundTripTime !== undefined) {
          s.rtt = 1000 * r.currentRoundTripTime;
        }
      });
      self.lastStats = s;
      sendInput(s);
      renderHud();
    }).catch(function () {});
  };

  Session.prototype.cleanupPeer = function () {
    if (this.statsTimer) { clearInterval(this.statsTimer); this.statsTimer = null; }
    if (this.pc) { try { this.pc.close(); } catch (e) { /* ignore */ } }
    this.pc = null;
    this.channel = null;
    this.pendingCandidates = [];
  };

  Session.prototype.scheduleReconnect = function () {
    var self = this;
    this.cleanupPeer();
    var delay = Math.min(1000 * Math.pow(2, this.retry++), 10000);
    showOverlay('Connection lost. Reconnecting…', true);
    clearTimeout(this.retryTimer);
    this.retryTimer = setTimeout(function () {
      // The token may have been revoked on the host; fall back to pairing if so.
      api('GET', '/api/auth').then(function (r) {
        if (self.stopped) return;
        if (r.status === 204) self.open();
        else { clearToken(); token = null; stopSession(); }
      }).catch(function () { if (!self.stopped) self.scheduleReconnect(); });
    }, delay);
  };

  Session.prototype.reconnectNow = function () {
    if (this.ws) { var ws = this.ws; this.ws = null; ws.close(); }
    this.stopped = false;
    this.scheduleReconnect();
  };

  Session.prototype.close = function () {
    this.stopped = true;
    clearTimeout(this.retryTimer);
    this.send({ type: 'bye' });
    if (this.ws) { var ws = this.ws; this.ws = null; ws.close(); }
    this.cleanupPeer();
  };

  function startSession(displayId) {
    if (session) session.close();
    session = new Session(displayId);
    session.open();
  }

  function restartSession() {
    var id = session ? session.displayId : 0;
    startSession(id);
  }

  function stopSession() {
    if (session) session.close();
    session = null;
    els.video.srcObject = null;
    els.stage.hidden = true;
    els.menu.hidden = true;
    els.setup.hidden = false;
    if (document.fullscreenElement && document.exitFullscreen) document.exitFullscreen().catch(function () {});
    boot();
  }

  // Tell the host when our size changes (rotation, fullscreen toggles).
  var resizeTimer = null;
  window.addEventListener('resize', function () {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () {
      if (session) session.send({ type: 'viewport', viewport: viewport() });
    }, 500);
  });

  // ---------------------------------------------------------------- input
  function sendInput(evt) {
    if (session && session.channel && session.channel.readyState === 'open') {
      session.channel.send(JSON.stringify(evt));
    }
  }

  // Coalesce move/scroll events to one per animation frame.
  var pendingMove = null, pendingScroll = null, frameQueued = false;
  function queue(evt) {
    if (evt.t === 'move') pendingMove = evt;
    else if (evt.t === 'scroll') {
      if (pendingScroll) { pendingScroll.dx += evt.dx; pendingScroll.dy += evt.dy; pendingScroll.x = evt.x; pendingScroll.y = evt.y; }
      else pendingScroll = { t: 'scroll', x: evt.x, y: evt.y, dx: evt.dx, dy: evt.dy };
    } else {
      flush();
      sendInput(evt);
      return;
    }
    if (!frameQueued) { frameQueued = true; requestAnimationFrame(flush); }
  }
  function flush() {
    frameQueued = false;
    if (pendingMove) { sendInput(pendingMove); pendingMove = null; }
    if (pendingScroll) { sendInput(pendingScroll); pendingScroll = null; }
  }

  var tracker = new G.GestureTracker(queue, {
    now: function () { return Date.now(); },
    setTimeout: function (fn, ms) { return setTimeout(fn, ms); },
    clearTimeout: function (id) { clearTimeout(id); }
  });

  function norm(clientX, clientY) {
    return G.normalize(clientX, clientY, els.stage.getBoundingClientRect(), els.video.videoWidth, els.video.videoHeight);
  }

  function touchList(list) {
    var out = [];
    for (var i = 0; i < list.length; i++) {
      var p = norm(list[i].clientX, list[i].clientY);
      if (p) out.push(p);
    }
    return out;
  }

  function isChrome(target) {
    return target === els.handle || els.menu.contains(target) || els.overlay.contains(target);
  }

  els.stage.addEventListener('touchstart', function (e) {
    if (isChrome(e.target) || !els.overlay.hidden) return;
    e.preventDefault();
    els.menu.hidden = true;
    tracker.touchStart(touchList(e.touches));
  }, { passive: false });
  els.stage.addEventListener('touchmove', function (e) {
    if (isChrome(e.target)) return;
    e.preventDefault();
    tracker.touchMove(touchList(e.touches));
  }, { passive: false });
  function touchEnd(e) {
    if (isChrome(e.target)) return;
    e.preventDefault();
    var lifted = touchList(e.changedTouches)[0];
    if (lifted) tracker.touchEnd(touchList(e.touches), lifted);
    else tracker.cancel();
  }
  els.stage.addEventListener('touchend', touchEnd, { passive: false });
  els.stage.addEventListener('touchcancel', function () { tracker.cancel(); });

  // Mouse/trackpad (desktop browsers).
  var mouseDown = false;
  els.stage.addEventListener('mousedown', function (e) {
    if (isChrome(e.target) || e.button !== 0) return;
    var p = norm(e.clientX, e.clientY); if (!p) return;
    mouseDown = true;
    els.menu.hidden = true;
    queue({ t: 'down', x: p.x, y: p.y });
  });
  els.stage.addEventListener('mousemove', function (e) {
    var p = norm(e.clientX, e.clientY); if (!p) return;
    queue({ t: 'move', x: p.x, y: p.y });
  });
  window.addEventListener('mouseup', function (e) {
    if (!mouseDown) return;
    mouseDown = false;
    var p = norm(e.clientX, e.clientY); if (!p) return;
    queue({ t: 'up', x: p.x, y: p.y });
  });
  els.stage.addEventListener('contextmenu', function (e) {
    e.preventDefault();
    if (isChrome(e.target)) return;
    var p = norm(e.clientX, e.clientY); if (!p) return;
    queue({ t: 'rightclick', x: p.x, y: p.y });
  });
  els.stage.addEventListener('wheel', function (e) {
    e.preventDefault();
    var p = norm(e.clientX, e.clientY); if (!p) return;
    var unit = e.deltaMode === 1 ? 16 : (e.deltaMode === 2 ? p.height : 1);
    // A wheel "down" moves content up, i.e. the opposite of a finger pulling down.
    queue({ t: 'scroll', x: p.x, y: p.y, dx: -e.deltaX * unit / p.width, dy: -e.deltaY * unit / p.height });
  }, { passive: false });

  // ---------------------------------------------------------------- menu / HUD
  var hudVisible = false;
  els.handle.addEventListener('click', function () { els.menu.hidden = !els.menu.hidden; });
  els.menuFullscreen.addEventListener('click', function () { enterFullscreen(); els.menu.hidden = true; });
  els.menuStats.addEventListener('click', function () {
    hudVisible = !hudVisible;
    els.hud.hidden = !hudVisible;
    els.menuStats.textContent = hudVisible ? 'Hide stats' : 'Show stats';
    els.menu.hidden = true;
    renderHud();
  });
  els.menuDisconnect.addEventListener('click', stopSession);

  function renderHud() {
    if (!hudVisible || !session) return;
    var s = session.lastStats || {};
    function f(v, unit) { return v === undefined || v === null ? '–' : Math.round(v) + unit; }
    els.hud.textContent = els.video.videoWidth + '×' + els.video.videoHeight + '  ' + f(s.fps, ' fps') +
      '\nnetwork ' + f(s.rtt, ' ms') + '  jitter ' + f(s.jitter, ' ms') + '  decode ' + f(s.decode, ' ms') +
      '\nsignaling ' + f(session.latencyMs, ' ms');
  }

  window.addEventListener('pagehide', function () { if (session) session.close(); });

  buildKeypad();
  boot();
})();
