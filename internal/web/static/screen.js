// On-screen page: shows the home screen, or plays whatever the state says.
(() => {
  const cd = window.castdeck;
  const $ = (s) => document.querySelector(s);
  const stage = $('#stage'), osd = $('#osd'), msg = $('#msg');
  let state = null, mediaId = 0, seekSeq = 0, el = null, hls = null, failed = false;

  // ---------- clock ----------
  const tick = () => { $('#clock').textContent = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }); };
  tick(); setInterval(tick, 10000);

  // ---------- messages / OSD ----------
  let msgTimer = 0, osdTimer = 0;
  function flash(text, ms = 2500) {
    msg.textContent = text;
    clearTimeout(msgTimer);
    msgTimer = setTimeout(() => { msg.textContent = ''; }, ms);
  }
  function showOsd(ms = 3500) {
    osd.classList.remove('hidden');
    clearTimeout(osdTimer);
    osdTimer = setTimeout(() => { if (el && !el.paused) osd.classList.add('hidden'); }, ms);
  }

  // ---------- media ----------
  function teardown() {
    if (hls) { hls.destroy(); hls = null; }
    if (el) { if (el.pause) el.pause(); el.removeAttribute('src'); el.remove(); el = null; }
    mediaId = 0; failed = false;
    stage.classList.remove('on');
    $('#audioArt').textContent = '';
    document.getElementById('btnYouTube').focus();
  }

  function loadHls(video, url) {
    const start = () => {
      hls = new window.Hls();
      hls.loadSource(url);
      hls.attachMedia(video);
      hls.on(window.Hls.Events.ERROR, (_, d) => { if (d.fatal) fail('stream error: ' + d.details); });
    };
    if (window.Hls) return start();
    const s = document.createElement('script');
    s.src = 'https://cdn.jsdelivr.net/npm/hls.js@1.5.17/dist/hls.min.js';
    s.onload = () => (window.Hls && window.Hls.isSupported() ? start() : (video.src = url));
    s.onerror = () => { video.src = url; };
    document.head.appendChild(s);
  }

  function fail(reason) {
    if (failed) return;
    failed = true;
    flash('Could not play this media (' + reason + ')', 5000);
    const id = mediaId;
    cd.api('/api/report', { id, error: reason });
    setTimeout(() => cd.api('/api/report', { id, ended: true }), 5000);
  }

  function setup(m) {
    teardown();
    mediaId = m.id;
    seekSeq = state.seekSeq;
    stage.classList.add('on');
    osd.querySelector('.title').textContent = m.title || decodeURIComponent(m.url.split('/').pop().split('?')[0]) || 'Now playing';
    if (m.kind === 'image') {
      el = document.createElement('img');
      el.src = m.url;
      el.onerror = () => fail('image could not be loaded');
      stage.prepend(el);
      osd.style.display = 'none';
      return;
    }
    osd.style.display = '';
    if (m.kind === 'audio') $('#audioArt').textContent = '♪ ' + (m.title || 'Audio');
    const v = document.createElement('video');
    el = v;
    v.autoplay = true;
    v.playsInline = true;
    v.volume = state.volume;
    v.addEventListener('loadedmetadata', () => {
      if (m.startSeconds > 0) v.currentTime = m.startSeconds;
      else if (m.startFraction > 0 && isFinite(v.duration)) v.currentTime = m.startFraction * v.duration;
    }, { once: true });
    v.addEventListener('ended', () => cd.api('/api/report', { id: m.id, ended: true }));
    v.addEventListener('error', () => fail((v.error && v.error.message) || 'unsupported format'));
    v.addEventListener('pause', () => showOsd(1e9));
    v.addEventListener('play', () => showOsd());
    stage.prepend(v);
    if (/\.m3u8($|\?)/i.test(m.url) && !v.canPlayType('application/vnd.apple.mpegurl')) loadHls(v, m.url);
    else v.src = m.url;
    showOsd();
  }

  function render(s) {
    state = s;
    document.querySelectorAll('.devname, #name').forEach((n) => { n.textContent = s.name || 'CastDeck'; });
    $('#remotes').innerHTML = (s.remotes || []).map((u) => u.replace(/[<>&]/g, '')).join('<br>');
    if (!s.media) { if (mediaId) teardown(); return; }
    if (s.media.id !== mediaId) setup(s.media);
    if (!el || el.tagName !== 'VIDEO') return;
    el.volume = s.volume;
    if (s.seekSeq !== seekSeq) { seekSeq = s.seekSeq; el.currentTime = s.seekTo; showOsd(); }
    if (s.paused && !el.paused) el.pause();
    else if (!s.paused && el.paused && !el.ended && !failed) {
      el.play().catch((e) => { if (e.name === 'NotAllowedError') flash('Press A / Enter to start playback'); });
    }
  }

  // Report progress so senders (AirPlay, web remote) can show it.
  setInterval(() => {
    if (!el || el.tagName !== 'VIDEO' || !mediaId) return;
    const dur = isFinite(el.duration) ? el.duration : 0;
    osd.querySelector('.pos').textContent = cd.fmtTime(el.currentTime);
    osd.querySelector('.dur').textContent = dur ? cd.fmtTime(dur) : 'LIVE';
    osd.querySelector('.bar i').style.width = dur ? (100 * el.currentTime / dur) + '%' : '0';
    cd.api('/api/report', { id: mediaId, position: el.currentTime, duration: dur, ready: el.readyState >= 3 });
  }, 1000);

  cd.subscribe(render);

  // ---------- input: keyboard and gamepad ----------
  function moveFocus(dir) {
    const items = [...document.querySelectorAll('.focusable')];
    const i = items.indexOf(document.activeElement);
    const next = items[(Math.max(i, 0) + dir + items.length) % items.length];
    next.focus();
  }

  function action(a) {
    if (mediaId) {
      showOsd();
      switch (a) {
        case 'ok': cd.api('/api/control', { action: 'toggle' }); if (el && el.paused) el.play().catch(() => {}); break;
        case 'back': cd.api('/api/control', { action: 'stop' }); break;
        case 'left': cd.api('/api/control', { action: 'skip', value: -10 }); break;
        case 'right': cd.api('/api/control', { action: 'skip', value: 30 }); break;
        case 'up': cd.api('/api/control', { action: 'volume', value: Math.min(1, state.volume + 0.1) }); flash('Volume ' + Math.round(Math.min(1, state.volume + 0.1) * 100) + '%', 1000); break;
        case 'down': cd.api('/api/control', { action: 'volume', value: Math.max(0, state.volume - 0.1) }); flash('Volume ' + Math.round(Math.max(0, state.volume - 0.1) * 100) + '%', 1000); break;
      }
      return;
    }
    switch (a) {
      case 'left': case 'up': moveFocus(-1); break;
      case 'right': case 'down': moveFocus(1); break;
      case 'ok': if (document.activeElement && document.activeElement.click) document.activeElement.click(); break;
    }
  }

  const keys = { ArrowLeft: 'left', ArrowRight: 'right', ArrowUp: 'up', ArrowDown: 'down', Enter: 'ok', ' ': 'ok',
    Escape: 'back', Backspace: 'back', BrowserBack: 'back', MediaPlayPause: 'ok', MediaStop: 'back' };
  document.addEventListener('keydown', (e) => {
    const a = keys[e.key];
    if (!a) return;
    e.preventDefault();
    action(a);
  });

  // Standard gamepad mapping: 0=A 1=B 12..15=D-pad; left stick as D-pad.
  const prev = {};
  function pollPads() {
    for (const p of navigator.getGamepads ? navigator.getGamepads() : []) {
      if (!p) continue;
      const ax = p.axes[0] || 0, ay = p.axes[1] || 0;
      const now = {
        ok: p.buttons[0] && p.buttons[0].pressed,
        back: p.buttons[1] && p.buttons[1].pressed,
        up: (p.buttons[12] && p.buttons[12].pressed) || ay < -0.6,
        down: (p.buttons[13] && p.buttons[13].pressed) || ay > 0.6,
        left: (p.buttons[14] && p.buttons[14].pressed) || ax < -0.6,
        right: (p.buttons[15] && p.buttons[15].pressed) || ax > 0.6,
      };
      const was = prev[p.index] || {};
      for (const k in now) if (now[k] && !was[k]) action(k);
      prev[p.index] = now;
    }
    requestAnimationFrame(pollPads);
  }
  requestAnimationFrame(pollPads);

  // ---------- buttons ----------
  $('#btnYouTube').addEventListener('click', () => { flash('Opening YouTube…'); cd.api('/api/youtube'); });
  $('#btnQuit').addEventListener('click', () => { flash('Bye!'); cd.api('/api/quit'); });
  $('#btnYouTube').focus();
})();
