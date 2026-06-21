// Stylised world map: dotted continents drawn on a canvas with a glowing,
// pulsing SVG marker per node. No tiles, no external dependencies — the
// landmass is a handful of coarse polygons rasterised into a lookup mask.
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

  const ACCENT = '#2ED6A1', VIOLET = '#9B8CFF', BLUE = '#5BA8FF';

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

  function render(host, nodes) {
    host.innerHTML = '';
    const W = host.clientWidth || 1000, H = host.clientHeight || 460;
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const light = document.documentElement.classList.contains('light');

    const px = (lon, lat) => ({
      x: ((((lon - LON_MIN) % 360) + 360) % 360) / (LON_MAX - LON_MIN) * W,
      y: (LAT_MAX - lat) / (LAT_MAX - LAT_MIN) * H,
    });

    // Dotted continents.
    const isLand = buildIsLand();
    const cv = document.createElement('canvas');
    cv.width = W * dpr; cv.height = H * dpr;
    cv.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;';
    host.appendChild(cv);
    const ctx = cv.getContext('2d');
    ctx.scale(dpr, dpr);
    ctx.fillStyle = light ? 'rgba(34,46,76,0.16)' : 'rgba(255,255,255,0.085)';
    for (let lon = LON_MIN; lon <= LON_MAX; lon += 1.9) {
      for (let lat = LAT_MIN; lat <= LAT_MAX; lat += 1.9) {
        if (!isLand(lon, lat)) continue;
        const p = px(lon, lat);
        ctx.beginPath();
        ctx.arc(p.x, p.y, 0.9, 0, 7);
        ctx.fill();
      }
    }

    // Marker overlay.
    const svg = document.createElementNS(NS, 'svg');
    svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
    svg.style.cssText = 'position:absolute;inset:0;width:100%;height:100%;overflow:visible;';
    host.appendChild(svg);

    const pts = nodes.map((n) => ({ n: n, p: px(n.lon, n.lat) }));

    // Decorative relay arcs fanning out from a central hub. Purely cosmetic —
    // the crawler does not measure peer links.
    if (pts.length > 3) {
      const defs = document.createElementNS(NS, 'defs');
      defs.innerHTML = '<linearGradient id="dcr-arc" x1="0" x2="1">' +
        '<stop offset="0" stop-color="' + ACCENT + '" stop-opacity="0.85"/>' +
        '<stop offset="1" stop-color="' + BLUE + '" stop-opacity="0.12"/></linearGradient>';
      svg.appendChild(defs);

      const hub = px(39, -98);
      const stride = Math.max(1, Math.floor(pts.length / 22));
      const targets = pts.filter((_, i) => i % stride === 0).slice(0, 22);
      targets.forEach((t, i) => {
        const mx = (hub.x + t.p.x) / 2;
        const my = (hub.y + t.p.y) / 2 - Math.abs(hub.x - t.p.x) * 0.18 - 30;
        const path = document.createElementNS(NS, 'path');
        path.setAttribute('d', 'M' + hub.x + ',' + hub.y + ' Q' + mx + ',' + my + ' ' + t.p.x + ',' + t.p.y);
        path.setAttribute('fill', 'none');
        path.setAttribute('stroke', 'url(#dcr-arc)');
        path.setAttribute('stroke-width', '1.1');
        const len = Math.hypot(t.p.x - hub.x, t.p.y - hub.y) * 1.4;
        path.setAttribute('stroke-dasharray', len);
        path.setAttribute('stroke-dashoffset', len);
        path.style.transition = 'stroke-dashoffset 1.4s ease';
        svg.appendChild(path);
        requestAnimationFrame(() => setTimeout(() => path.setAttribute('stroke-dashoffset', '0'), 120 + i * 45));
      });
    }

    const tip = document.createElement('div');
    tip.className = 'map-tip';
    host.appendChild(tip);

    pts.forEach((d, i) => {
      const color = d.n.v6 ? VIOLET : ACCENT;
      const baseR = d.n.v6 ? 2.6 : 2.2;

      // Pulsing halo behind the dot (radar ping), staggered so they don't all
      // fire in unison.
      const halo = document.createElementNS(NS, 'circle');
      halo.setAttribute('cx', d.p.x);
      halo.setAttribute('cy', d.p.y);
      halo.setAttribute('r', 6);
      halo.setAttribute('fill', color);
      halo.setAttribute('opacity', '0');
      halo.style.transformBox = 'fill-box';
      halo.style.transformOrigin = 'center';
      halo.style.animation = 'dcrPing 2.6s ease-out ' + ((i % 14) * 0.18) + 's infinite';
      svg.appendChild(halo);

      const dot = document.createElementNS(NS, 'circle');
      dot.setAttribute('cx', d.p.x);
      dot.setAttribute('cy', d.p.y);
      dot.setAttribute('r', baseR);
      dot.setAttribute('fill', color);
      dot.style.filter = 'drop-shadow(0 0 5px ' + color + ')';
      dot.style.cursor = 'pointer';
      dot.addEventListener('mouseenter', () => {
        const meta = [d.n.asn, d.n.ua].filter(Boolean).map(esc).join(' · ');
        tip.innerHTML = '<span style="color:' + color + '">' + esc(d.n.country) + '</span> · ' + esc(d.n.ip) +
          (meta ? '<br><span class="map-tip-sub">' + meta + '</span>' : '');
        tip.style.left = d.p.x + 'px';
        tip.style.top = d.p.y + 'px';
        tip.style.opacity = '1';
        dot.setAttribute('r', 3.6);
      });
      dot.addEventListener('mouseleave', () => {
        tip.style.opacity = '0';
        dot.setAttribute('r', baseR);
      });
      dot.addEventListener('click', () => {
        window.location.href = '/node?ip=' + encodeURIComponent(d.n.ip);
      });
      svg.appendChild(dot);
    });
  }

  // DCRWorldMap fetches the node list and renders it into the element matching
  // selector, re-rendering on resize so the projection stays crisp.
  window.DCRWorldMap = function (selector) {
    const host = document.querySelector(selector);
    if (!host) return;
    let nodes = [];
    const draw = () => render(host, nodes);
    fetch('/world_nodes')
      .then((res) => res.json())
      .then((data) => { nodes = data || []; draw(); })
      .catch(() => {});
    let timer;
    window.addEventListener('resize', () => {
      clearTimeout(timer);
      timer = setTimeout(draw, 200);
    });
  };
})();
