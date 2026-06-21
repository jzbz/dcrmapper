// Stylised world map: dotted continents drawn on a canvas with a glowing,
// pulsing SVG marker per node. No tiles, no external dependencies — the
// landmass is a handful of coarse polygons rasterised into a lookup mask.
// Supports zoom (wheel / buttons) and pan (drag).
(function () {
  'use strict';

  // Coarse continent outlines as [lat, lon] vertices. Rasterised into a land
  // mask below; deliberately low-fidelity, enough to read as continents under
  // a dot grid, not an accurate coastline.
  const CONTINENTS = [
    [[71,-156],[70,-128],[66,-95],[68,-74],[58,-64],[47,-52],[45,-66],[40,-74],[31,-81],[25,-81],[18,-94],[15,-97],[20,-105],[23,-110],[32,-117],[40,-124],[48,-125],[58,-138],[62,-148],[71,-156]],
    [[12,-71],[10,-61],[5,-52],[-5,-35],[-23,-41],[-34,-54],[-52,-69],[-44,-74],[-18,-70],[-5,-81],[2,-78],[8,-77],[12,-71]],
    [[37,-6],[34,10],[32,22],[31,33],[12,43],[11,51],[-1,42],[-26,33],[-34,26],[-34,19],[-22,14],[-5,9],[5,-4],[8,-13],[15,-17],[21,-17],[28,-12],[37,-6]],
    [[71,24],[68,40],[60,30],[54,38],[45,40],[40,28],[41,20],[37,15],[36,-6],[43,-9],[48,-5],[50,2],[58,5],[62,5],[71,24]],
    [[78,68],[80,105],[73,140],[66,170],[60,162],[52,142],[43,132],[35,127],[30,122],[22,108],[10,105],[8,98],[7,80],[20,72],[24,67],[25,57],[30,48],[40,50],[50,58],[62,62],[72,64],[78,68]],
    [[-11,131],[-12,142],[-20,149],[-28,153],[-38,146],[-38,140],[-32,134],[-31,115],[-22,114],[-14,127],[-11,131]],
    [[83,-30],[80,-18],[70,-22],[60,-44],[68,-52],[76,-58],[81,-45],[83,-30]],
    [[58,-5],[57,-2],[51,1],[50,-5],[54,-6],[58,-5]],
    [[45,142],[43,145],[35,140],[33,131],[37,137],[45,142]],
  ];

  // Equirectangular bounds. Cropped to populated latitudes so the continents
  // fill the card rather than leaving polar dead space.
  const LON_MIN = -168, LON_MAX = 192, LAT_MIN = -56, LAT_MAX = 80;
  const NS = 'http://www.w3.org/2000/svg';
  const ACCENT = '#2ED6A1', VIOLET = '#9B8CFF';
  const MAX_ZOOM = 6;

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"]/g,
      (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
  }

  // buildIsLand rasterises the continent polygons into a 720x360 alpha mask and
  // returns a (lon,lat) -> bool lookup.
  function buildIsLand() {
    const mW = 720, mH = 360;
    const cv = document.createElement('canvas');
    cv.width = mW; cv.height = mH;
    const mc = cv.getContext('2d');
    mc.fillStyle = '#fff';
    for (const poly of CONTINENTS) {
      mc.beginPath();
      poly.forEach(([lat, lon], i) => {
        const mx = (lon + 180) / 360 * mW, my = (90 - lat) / 180 * mH;
        if (i === 0) mc.moveTo(mx, my); else mc.lineTo(mx, my);
      });
      mc.closePath();
      mc.fill();
    }
    const data = mc.getImageData(0, 0, mW, mH).data;
    return function (lon, lat) {
      const mx = Math.floor((lon + 180) / 360 * mW);
      const my = Math.floor((90 - lat) / 180 * mH);
      if (mx < 0 || mx >= mW || my < 0 || my >= mH) return false;
      return data[(my * mW + mx) * 4 + 3] > 128;
    };
  }

  // mount renders nodes into host and wires up zoom/pan. Re-runs on resize; an
  // AbortController tears down the previous run's listeners so they don't stack.
  function mount(host, nodes) {
    if (host._dcrAbort) host._dcrAbort.abort();
    const ac = new AbortController();
    host._dcrAbort = ac;
    const on = ac.signal;

    host.innerHTML = '';
    const W = host.clientWidth || 1000, H = host.clientHeight || 460;
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const light = document.documentElement.classList.contains('light');
    const solo = nodes.length === 1;

    // Base (unzoomed) projection. The view transform x*k+tx is applied on top.
    const baseX = (lon) => ((((lon - LON_MIN) % 360) + 360) % 360) / (LON_MAX - LON_MIN) * W;
    const baseY = (lat) => (LAT_MAX - lat) / (LAT_MAX - LAT_MIN) * H;

    const cv = document.createElement('canvas');
    cv.width = W * dpr; cv.height = H * dpr;
    cv.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;';
    host.appendChild(cv);
    const ctx = cv.getContext('2d');
    ctx.scale(dpr, dpr);
    const dotFill = light ? 'rgba(34,46,76,0.16)' : 'rgba(255,255,255,0.085)';

    // Precompute land dot positions once so re-painting on zoom/pan is cheap.
    const isLand = buildIsLand();
    const land = [];
    for (let lon = LON_MIN; lon <= LON_MAX; lon += 1.9) {
      for (let lat = LAT_MIN; lat <= LAT_MAX; lat += 1.9) {
        if (isLand(lon, lat)) land.push([baseX(lon), baseY(lat)]);
      }
    }

    const svg = document.createElementNS(NS, 'svg');
    svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
    svg.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;overflow:visible;';
    host.appendChild(svg);

    const tip = document.createElement('div');
    tip.className = 'map-tip';
    host.appendChild(tip);

    let dragMoved = false;
    const markers = nodes.map((n, i) => {
      const color = n.v6 ? VIOLET : ACCENT;
      const baseR = solo ? 5 : (n.v6 ? 2.6 : 2.2);
      const hoverR = solo ? 6 : 3.6;

      const halo = document.createElementNS(NS, 'circle');
      halo.setAttribute('r', solo ? 10 : 6);
      halo.setAttribute('fill', color);
      halo.setAttribute('opacity', '0');
      halo.style.transformBox = 'fill-box';
      halo.style.transformOrigin = 'center';
      halo.style.animation = 'dcrPing 2.6s ease-out ' + ((i % 14) * 0.18) + 's infinite';
      svg.appendChild(halo);

      const dot = document.createElementNS(NS, 'circle');
      dot.setAttribute('r', baseR);
      dot.setAttribute('fill', color);
      dot.style.filter = 'drop-shadow(0 0 ' + (solo ? 8 : 5) + 'px ' + color + ')';
      dot.style.cursor = solo ? 'default' : 'pointer';
      dot.addEventListener('mouseenter', () => {
        const meta = [n.asn, n.ua].filter(Boolean).map(esc).join(' · ');
        tip.innerHTML = '<span style="color:' + color + '">' + esc(n.country) + '</span> · ' + esc(n.ip) +
          (meta ? '<br><span class="map-tip-sub">' + meta + '</span>' : '');
        tip.style.left = dot.getAttribute('cx') + 'px';
        tip.style.top = dot.getAttribute('cy') + 'px';
        tip.style.opacity = '1';
        dot.setAttribute('r', hoverR);
      }, { signal: on });
      dot.addEventListener('mouseleave', () => {
        tip.style.opacity = '0';
        dot.setAttribute('r', baseR);
      }, { signal: on });
      // Each world-map dot links to its node page; suppress the navigation when
      // the click was really the end of a drag.
      if (!solo) {
        dot.addEventListener('click', () => {
          if (!dragMoved) window.location.href = '/node?ip=' + encodeURIComponent(n.ip);
        }, { signal: on });
      }
      svg.appendChild(dot);
      return { bx: baseX(n.lon), by: baseY(n.lat), halo: halo, dot: dot };
    });

    const view = { k: 1, tx: 0, ty: 0 };
    function clampPan() {
      // Keep the (scaled) map covering the viewport — no empty edges.
      view.tx = Math.min(0, Math.max(W * (1 - view.k), view.tx));
      view.ty = Math.min(0, Math.max(H * (1 - view.k), view.ty));
    }
    function paint() {
      ctx.clearRect(0, 0, W, H);
      ctx.fillStyle = dotFill;
      const r = 0.9 * (0.7 + 0.3 * view.k);
      for (let i = 0; i < land.length; i++) {
        const x = land[i][0] * view.k + view.tx, y = land[i][1] * view.k + view.ty;
        if (x < -2 || x > W + 2 || y < -2 || y > H + 2) continue;
        ctx.beginPath();
        ctx.arc(x, y, r, 0, 7);
        ctx.fill();
      }
      for (let i = 0; i < markers.length; i++) {
        const m = markers[i];
        const x = m.bx * view.k + view.tx, y = m.by * view.k + view.ty;
        m.halo.setAttribute('cx', x); m.halo.setAttribute('cy', y);
        m.dot.setAttribute('cx', x); m.dot.setAttribute('cy', y);
      }
    }
    let raf = 0;
    function schedule() { if (!raf) raf = requestAnimationFrame(() => { raf = 0; paint(); }); }

    // zoomAt scales about the (cx,cy) screen point so it stays put under the
    // cursor / button.
    function zoomAt(cx, cy, factor) {
      const nk = Math.min(MAX_ZOOM, Math.max(1, view.k * factor));
      if (nk === view.k) return;
      view.tx = cx - (cx - view.tx) * (nk / view.k);
      view.ty = cy - (cy - view.ty) * (nk / view.k);
      view.k = nk;
      clampPan();
      schedule();
    }

    host.addEventListener('wheel', (e) => {
      e.preventDefault();
      const r = host.getBoundingClientRect();
      zoomAt(e.clientX - r.left, e.clientY - r.top, Math.exp(-e.deltaY * 0.0015));
    }, { passive: false, signal: on });

    let dragging = false, lx = 0, ly = 0;
    host.addEventListener('pointerdown', (e) => {
      dragging = true; dragMoved = false; lx = e.clientX; ly = e.clientY;
      host.setPointerCapture(e.pointerId);
      host.style.cursor = 'grabbing';
    }, { signal: on });
    host.addEventListener('pointermove', (e) => {
      if (!dragging) return;
      const dx = e.clientX - lx, dy = e.clientY - ly;
      if (Math.abs(dx) + Math.abs(dy) > 3) dragMoved = true;
      view.tx += dx; view.ty += dy; lx = e.clientX; ly = e.clientY;
      clampPan();
      schedule();
    }, { signal: on });
    const endDrag = (e) => {
      if (!dragging) return;
      dragging = false;
      host.style.cursor = 'grab';
      try { host.releasePointerCapture(e.pointerId); } catch (_) {}
    };
    host.addEventListener('pointerup', endDrag, { signal: on });
    host.addEventListener('pointercancel', endDrag, { signal: on });
    host.style.cursor = 'grab';

    const ctrl = document.createElement('div');
    ctrl.className = 'map-zoom';
    const mkBtn = (label, aria) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.textContent = label;
      b.setAttribute('aria-label', aria);
      return b;
    };
    const zin = mkBtn('+', 'Zoom in'), zout = mkBtn('−', 'Zoom out');
    zin.addEventListener('click', () => zoomAt(W / 2, H / 2, 1.6), { signal: on });
    zout.addEventListener('click', () => zoomAt(W / 2, H / 2, 1 / 1.6), { signal: on });
    ctrl.appendChild(zin);
    ctrl.appendChild(zout);
    host.appendChild(ctrl);

    paint();
  }

  // DCRWorldMap renders nodes into the element matching selector and re-renders
  // on resize. Pass an explicit nodes array (e.g. the single node on a detail
  // page) to render it directly; omit it to fetch the full node list.
  window.DCRWorldMap = function (selector, data) {
    const host = document.querySelector(selector);
    if (!host) return;
    let nodes = Array.isArray(data) ? data : [];
    const draw = () => mount(host, nodes);

    let timer;
    window.addEventListener('resize', () => {
      clearTimeout(timer);
      timer = setTimeout(draw, 200);
    });

    if (Array.isArray(data)) {
      draw();
      return;
    }
    fetch('/world_nodes')
      .then((res) => res.json())
      .then((d) => { nodes = d || []; draw(); })
      .catch(() => {});
  };
})();
