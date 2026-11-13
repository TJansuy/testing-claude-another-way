// Shared helpers for the CastDeck pages.
window.castdeck = {
  api(path, body) {
    return fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body || {}),
    });
  },
  // Subscribe to state snapshots; reconnects automatically.
  subscribe(onState) {
    const es = new EventSource('/api/events');
    es.onmessage = (e) => onState(JSON.parse(e.data));
    return es;
  },
  fmtTime(sec) {
    if (!isFinite(sec) || sec < 0) sec = 0;
    sec = Math.floor(sec);
    const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
    const mm = h ? String(m).padStart(2, '0') : String(m);
    return (h ? h + ':' : '') + mm + ':' + String(s).padStart(2, '0');
  },
};
