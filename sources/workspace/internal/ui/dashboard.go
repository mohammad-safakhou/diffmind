package ui

const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>DiffMind — Service Dependency Graph</title>
<script src="https://d3js.org/d3.v7.min.js"></script>
<style>
:root {
  --bg: #0a0e1a;
  --panel: #111827;
  --panel2: #1e293b;
  --border: #1e3a5f;
  --border2: #334155;
  --text: #e2e8f0;
  --muted: #94a3b8;
  --dim: #64748b;
  --accent: #38bdf8;
  --green: #34d399;
  --orange: #fb923c;
  --red: #f87171;
  --purple: #a78bfa;
  --yellow: #fbbf24;
  --cyan: #22d3ee;
  --radius: 10px;
}
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
  font-family: "Inter","SF Pro Display","Segoe UI",system-ui,-apple-system,sans-serif;
  background: var(--bg); color: var(--text);
  display: grid; grid-template-rows: 48px 1fr;
  grid-template-columns: 1fr 360px;
  height: 100vh; overflow: hidden;
}

/* Header */
header {
  grid-column: 1 / -1;
  background: rgba(17,24,39,.92);
  backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--border);
  display: flex; align-items: center; gap: 16px;
  padding: 0 20px; z-index: 10;
}
header .logo { font-weight: 700; font-size: 15px; letter-spacing: .02em; color: var(--accent); }
header .sep { width: 1px; height: 20px; background: var(--border); }
header select, header button {
  background: var(--panel2); color: var(--text); border: 1px solid var(--border2);
  border-radius: 6px; padding: 5px 10px; font-size: 13px; cursor: pointer;
}
header button:hover { background: #2d3a4f; }
.stats { display: flex; gap: 18px; margin-left: auto; }
.stat { text-align: center; }
.stat .n { font-size: 18px; font-weight: 700; line-height: 1; }
.stat .l { font-size: 10px; color: var(--muted); text-transform: uppercase; letter-spacing: .06em; }

/* Graph area */
#graph-container {
  position: relative; overflow: hidden;
  background: radial-gradient(ellipse at center, #0f1729 0%, #0a0e1a 70%);
}
svg { width: 100%; height: 100%; }

/* Sidebar */
#sidebar {
  background: var(--panel);
  border-left: 1px solid var(--border);
  overflow-y: auto; padding: 16px;
  display: flex; flex-direction: column; gap: 14px;
  font-size: 13px;
}
.side-section { background: var(--panel2); border: 1px solid var(--border2); border-radius: var(--radius); padding: 14px; }
.side-section h3 {
  font-size: 11px; text-transform: uppercase; letter-spacing: .08em;
  color: var(--muted); margin-bottom: 10px;
}
.side-section .kv { display: flex; justify-content: space-between; padding: 4px 0; border-bottom: 1px solid rgba(255,255,255,.04); }
.side-section .kv:last-child { border-bottom: none; }
.side-section .k { color: var(--dim); }
.side-section .v { color: var(--text); font-weight: 500; text-align: right; max-width: 200px; word-break: break-all; }

.badge {
  display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 11px; font-weight: 600;
}
.badge-http { background: rgba(56,189,248,.15); color: var(--accent); }
.badge-queue { background: rgba(52,211,153,.15); color: var(--green); }
.badge-shared_db { background: rgba(251,146,60,.15); color: var(--orange); }
.badge-rpc { background: rgba(167,139,250,.15); color: var(--purple); }
.badge-default { background: rgba(148,163,184,.15); color: var(--muted); }

.tag-list { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 6px; }
.tag { background: rgba(255,255,255,.06); border: 1px solid rgba(255,255,255,.08); border-radius: 4px; padding: 2px 7px; font-size: 11px; color: var(--muted); }

/* Legend */
#legend {
  position: absolute; bottom: 16px; left: 16px;
  background: rgba(17,24,39,.88); backdrop-filter: blur(8px);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 12px 16px; font-size: 11px; z-index: 5;
  display: flex; flex-direction: column; gap: 6px;
}
#legend .title { font-weight: 700; color: var(--muted); text-transform: uppercase; letter-spacing: .08em; margin-bottom: 2px; }
.legend-row { display: flex; align-items: center; gap: 8px; }
.legend-swatch { width: 14px; height: 14px; border-radius: 3px; }
.legend-line { width: 24px; height: 3px; border-radius: 2px; }

/* Unresolved panel */
#unresolved-toggle {
  position: absolute; bottom: 16px; right: 376px;
  background: rgba(17,24,39,.88); backdrop-filter: blur(8px);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 8px 14px; font-size: 12px; color: var(--muted);
  cursor: pointer; z-index: 5;
}
#unresolved-toggle:hover { color: var(--text); }
#unresolved-panel {
  position: absolute; bottom: 56px; right: 376px;
  width: 420px; max-height: 50vh; overflow-y: auto;
  background: rgba(17,24,39,.94); backdrop-filter: blur(12px);
  border: 1px solid var(--border); border-radius: var(--radius);
  padding: 16px; font-size: 12px; z-index: 5;
  display: none;
}
#unresolved-panel.open { display: block; }
#unresolved-panel h3 { font-size: 12px; color: var(--orange); margin-bottom: 8px; }
.ur-group { margin-bottom: 10px; }
.ur-group .ur-svc { font-weight: 600; color: var(--text); margin-bottom: 4px; }
.ur-group .ur-item { color: var(--dim); padding: 2px 0 2px 12px; border-left: 2px solid var(--border2); margin-bottom: 2px; }
.ur-type { font-size: 10px; color: var(--muted); background: rgba(255,255,255,.05); border-radius: 3px; padding: 1px 5px; margin-left: 4px; }

/* Tooltip */
.tooltip {
  position: absolute; pointer-events: none; z-index: 20;
  background: rgba(17,24,39,.95); backdrop-filter: blur(8px);
  border: 1px solid var(--border); border-radius: 8px;
  padding: 10px 14px; font-size: 12px; max-width: 280px;
  box-shadow: 0 8px 32px rgba(0,0,0,.4);
  opacity: 0; transition: opacity .15s;
}
.tooltip.show { opacity: 1; }
.tooltip .tt-name { font-weight: 700; font-size: 13px; margin-bottom: 4px; }
.tooltip .tt-sub { color: var(--muted); }
</style>
</head>
<body>

<header>
  <span class="logo">DiffMind</span>
  <span class="sep"></span>
  <label style="color:var(--muted);font-size:12px">Run:</label>
  <select id="runSelect"></select>
  <button onclick="loadRun()">Refresh</button>
  <div class="stats">
    <div class="stat"><div class="n" id="st-svc">-</div><div class="l">Services</div></div>
    <div class="stat"><div class="n" id="st-edge" style="color:var(--accent)">-</div><div class="l">Edges</div></div>
    <div class="stat"><div class="n" id="st-shared" style="color:var(--orange)">-</div><div class="l">Shared</div></div>
    <div class="stat"><div class="n" id="st-unres" style="color:var(--dim)">-</div><div class="l">Unresolved</div></div>
  </div>
</header>

<div id="graph-container">
  <svg id="graph-svg"></svg>
  <div id="legend">
    <div class="title">Legend</div>
    <div class="legend-row"><div class="legend-line" style="background:var(--accent)"></div> HTTP call</div>
    <div class="legend-row"><div class="legend-line" style="background:var(--green)"></div> Queue / Message</div>
    <div class="legend-row"><div class="legend-line" style="background:var(--orange)"></div> Shared Database</div>
    <div class="legend-row"><div class="legend-line" style="background:var(--purple)"></div> RPC / gRPC</div>
    <div class="legend-row" style="margin-top:4px"><div class="legend-swatch" style="background:var(--accent);border-radius:50%"></div> Service node</div>
    <div class="legend-row"><div class="legend-swatch" style="background:var(--orange);transform:rotate(45deg);border-radius:2px;width:12px;height:12px"></div> Shared resource</div>
  </div>
  <div id="unresolved-toggle" onclick="toggleUnresolved()">Unresolved: <span id="ur-count">0</span></div>
  <div id="unresolved-panel"></div>
  <div class="tooltip" id="tooltip"></div>
</div>

<div id="sidebar">
  <div class="side-section" id="detail-placeholder">
    <h3>Details</h3>
    <p style="color:var(--dim)">Click a node or edge to inspect</p>
  </div>
</div>

<script>
// ---- Globals ----
let graphData = null;
let simulation = null;
const edgeColors = { http:'#38bdf8', queue:'#34d399', shared_db:'#fb923c', rpc:'#a78bfa' };
const defaultEdgeColor = '#475569';
const tooltip = document.getElementById('tooltip');

// ---- API ----
async function fetchJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error(await r.text());
  return r.json();
}

async function loadRuns() {
  const data = await fetchJSON('/api/runs');
  const sel = document.getElementById('runSelect');
  sel.innerHTML = '';
  for (const r of (data.runs || [])) {
    const o = document.createElement('option');
    o.value = r; o.textContent = r;
    sel.appendChild(o);
  }
}

async function loadRun() {
  const runID = document.getElementById('runSelect').value || 'latest';
  const data = await fetchJSON('/api/run/' + encodeURIComponent(runID));
  graphData = data.graph;
  updateStats(data.manifest);
  renderGraph();
  renderUnresolved();
  renderSidebar(null);
}

function updateStats(manifest) {
  const c = manifest.counts || {};
  document.getElementById('st-svc').textContent = c.services || 0;
  document.getElementById('st-edge').textContent = c.edges || 0;
  document.getElementById('st-shared').textContent = c.shared_resources || 0;
  document.getElementById('st-unres').textContent = c.unresolved || 0;
  document.getElementById('ur-count').textContent = c.unresolved || 0;
}

// ---- Graph Rendering ----
function renderGraph() {
  const container = document.getElementById('graph-container');
  const W = container.clientWidth;
  const H = container.clientHeight;
  const svg = d3.select('#graph-svg');
  svg.selectAll('*').remove();

  if (!graphData) return;

  // Build nodes & links
  const services = graphData.services || [];
  const edges = graphData.edges || [];
  const shared = graphData.shared_resources || [];

  const nodes = [];
  const nodeMap = {};

  // Service nodes
  services.forEach(s => {
    const n = {
      id: s.name,
      type: 'service',
      data: s,
      radius: Math.max(22, Math.min(40, 18 + (s.exposures_count + s.dependencies_count) * 0.6)),
    };
    nodes.push(n);
    nodeMap[s.name] = n;
  });

  // Shared resource nodes
  shared.forEach((r, i) => {
    const label = r.identifier || r.kind;
    const displayLabel = typeof label === 'string' ? (label.length > 40 ? label.slice(0,37)+'...' : label) : r.kind;
    const n = {
      id: 'shared_' + i,
      type: 'shared',
      data: r,
      label: displayLabel,
      radius: 16,
    };
    nodes.push(n);
    nodeMap['shared_' + i] = n;
  });

  // Links
  const links = [];

  edges.forEach(e => {
    if (nodeMap[e.from_service] && nodeMap[e.to_service]) {
      links.push({
        source: e.from_service,
        target: e.to_service,
        type: e.type,
        data: e,
        color: edgeColors[e.type] || defaultEdgeColor,
      });
    }
  });

  // Shared resource edges
  shared.forEach((r, i) => {
    (r.services || []).forEach(svc => {
      if (nodeMap[svc]) {
        links.push({
          source: svc,
          target: 'shared_' + i,
          type: 'shared_db',
          data: r,
          color: edgeColors.shared_db,
          dashed: true,
        });
      }
    });
  });

  // Zoom
  const g = svg.append('g');
  const zoom = d3.zoom()
    .scaleExtent([0.2, 4])
    .on('zoom', e => g.attr('transform', e.transform));
  svg.call(zoom);

  // Arrow markers
  const defs = svg.append('defs');
  Object.entries(edgeColors).forEach(([type, color]) => {
    defs.append('marker')
      .attr('id', 'arrow-' + type)
      .attr('viewBox', '0 -5 10 10')
      .attr('refX', 28).attr('refY', 0)
      .attr('markerWidth', 8).attr('markerHeight', 8)
      .attr('orient', 'auto')
      .append('path')
      .attr('d', 'M0,-4L10,0L0,4')
      .attr('fill', color);
  });
  defs.append('marker')
    .attr('id', 'arrow-default')
    .attr('viewBox', '0 -5 10 10')
    .attr('refX', 28).attr('refY', 0)
    .attr('markerWidth', 8).attr('markerHeight', 8)
    .attr('orient', 'auto')
    .append('path')
    .attr('d', 'M0,-4L10,0L0,4')
    .attr('fill', defaultEdgeColor);

  // Simulation
  simulation = d3.forceSimulation(nodes)
    .force('link', d3.forceLink(links).id(d => d.id).distance(180).strength(0.4))
    .force('charge', d3.forceManyBody().strength(-800))
    .force('center', d3.forceCenter(W / 2, H / 2))
    .force('collision', d3.forceCollide().radius(d => d.radius + 20))
    .force('x', d3.forceX(W / 2).strength(0.04))
    .force('y', d3.forceY(H / 2).strength(0.04));

  // Draw links
  const link = g.append('g')
    .selectAll('line')
    .data(links)
    .join('line')
    .attr('stroke', d => d.color)
    .attr('stroke-width', d => d.dashed ? 1.5 : 2.5)
    .attr('stroke-opacity', 0.6)
    .attr('stroke-dasharray', d => d.dashed ? '6,4' : null)
    .attr('marker-end', d => d.dashed ? null : 'url(#arrow-' + (edgeColors[d.type] ? d.type : 'default') + ')')
    .style('cursor', 'pointer')
    .on('click', (ev, d) => { ev.stopPropagation(); renderSidebar({ type: 'edge', data: d }); })
    .on('mouseenter', (ev, d) => showTooltip(ev, edgeTooltip(d)))
    .on('mouseleave', hideTooltip);

  // Edge labels
  const edgeLabel = g.append('g')
    .selectAll('text')
    .data(links.filter(l => !l.dashed))
    .join('text')
    .attr('font-size', 10)
    .attr('fill', d => d.color)
    .attr('text-anchor', 'middle')
    .attr('dy', -8)
    .attr('opacity', 0.7)
    .text(d => d.type);

  // Draw nodes
  const node = g.append('g')
    .selectAll('g')
    .data(nodes)
    .join('g')
    .style('cursor', 'pointer')
    .call(d3.drag()
      .on('start', dragStart)
      .on('drag', dragging)
      .on('end', dragEnd))
    .on('click', (ev, d) => { ev.stopPropagation(); renderSidebar({ type: d.type, data: d }); })
    .on('mouseenter', (ev, d) => showTooltip(ev, nodeTooltip(d)))
    .on('mouseleave', hideTooltip);

  // Service circles
  node.filter(d => d.type === 'service')
    .append('circle')
    .attr('r', d => d.radius)
    .attr('fill', 'rgba(56,189,248,.12)')
    .attr('stroke', 'var(--accent)')
    .attr('stroke-width', 2);

  // Shared resource diamonds
  node.filter(d => d.type === 'shared')
    .append('rect')
    .attr('width', d => d.radius * 2)
    .attr('height', d => d.radius * 2)
    .attr('x', d => -d.radius)
    .attr('y', d => -d.radius)
    .attr('rx', 3)
    .attr('transform', 'rotate(45)')
    .attr('fill', 'rgba(251,146,60,.12)')
    .attr('stroke', 'var(--orange)')
    .attr('stroke-width', 1.5);

  // Node labels
  node.append('text')
    .attr('dy', d => d.type === 'service' ? (d.radius + 16) : (d.radius + 18))
    .attr('text-anchor', 'middle')
    .attr('fill', 'var(--text)')
    .attr('font-size', 12)
    .attr('font-weight', 500)
    .text(d => d.type === 'service' ? d.id : (d.label || ''));

  // Inner label for services (abbreviated)
  node.filter(d => d.type === 'service')
    .append('text')
    .attr('text-anchor', 'middle')
    .attr('dy', 4)
    .attr('fill', 'var(--accent)')
    .attr('font-size', d => Math.max(8, d.radius * 0.45))
    .attr('font-weight', 700)
    .text(d => abbreviate(d.id));

  // Click background to deselect
  svg.on('click', () => renderSidebar(null));

  // Tick
  simulation.on('tick', () => {
    link
      .attr('x1', d => d.source.x).attr('y1', d => d.source.y)
      .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
    edgeLabel
      .attr('x', d => (d.source.x + d.target.x) / 2)
      .attr('y', d => (d.source.y + d.target.y) / 2);
    node.attr('transform', d => 'translate(' + d.x + ',' + d.y + ')');
  });

  // Zoom to fit after stabilization
  simulation.on('end', () => zoomToFit(svg, g, W, H, zoom));
  setTimeout(() => zoomToFit(svg, g, W, H, zoom), 2000);
}

function zoomToFit(svg, g, W, H, zoom) {
  const bounds = g.node().getBBox();
  if (bounds.width === 0) return;
  const pad = 80;
  const scale = Math.min((W - pad*2) / bounds.width, (H - pad*2) / bounds.height, 1.5);
  const tx = W/2 - (bounds.x + bounds.width/2) * scale;
  const ty = H/2 - (bounds.y + bounds.height/2) * scale;
  svg.transition().duration(500).call(zoom.transform, d3.zoomIdentity.translate(tx, ty).scale(scale));
}

function abbreviate(name) {
  // "checkout-service" -> "FMA"
  return name.split(/[-_]/).map(w => w[0] || '').join('').toUpperCase().slice(0, 4);
}

// ---- Drag ----
function dragStart(ev, d) {
  if (!ev.active) simulation.alphaTarget(0.15).restart();
  d.fx = d.x; d.fy = d.y;
}
function dragging(ev, d) { d.fx = ev.x; d.fy = ev.y; }
function dragEnd(ev, d) {
  if (!ev.active) simulation.alphaTarget(0);
  d.fx = null; d.fy = null;
}

// ---- Tooltip ----
function showTooltip(ev, html) {
  tooltip.innerHTML = html;
  tooltip.classList.add('show');
  const rect = document.getElementById('graph-container').getBoundingClientRect();
  let x = ev.clientX - rect.left + 16;
  let y = ev.clientY - rect.top + 16;
  if (x + 280 > rect.width) x = ev.clientX - rect.left - 290;
  tooltip.style.left = x + 'px';
  tooltip.style.top = y + 'px';
}
function hideTooltip() { tooltip.classList.remove('show'); }

function nodeTooltip(d) {
  if (d.type === 'shared') {
    const r = d.data;
    return '<div class="tt-name">' + esc(r.kind) + '</div>'
      + '<div class="tt-sub">' + esc(String(r.identifier || '')) + '</div>'
      + '<div class="tt-sub" style="margin-top:4px">Used by: ' + (r.services || []).map(esc).join(', ') + '</div>';
  }
  const s = d.data;
  return '<div class="tt-name">' + esc(s.name) + '</div>'
    + '<div class="tt-sub">Exposures: ' + s.exposures_count + ' &middot; Dependencies: ' + s.dependencies_count + '</div>';
}

function edgeTooltip(d) {
  const e = d.data;
  if (d.dashed) {
    return '<div class="tt-name">Shared ' + esc(e.kind || '') + '</div>'
      + '<div class="tt-sub">' + esc(String(e.identifier || '')) + '</div>';
  }
  return '<div class="tt-name">' + esc(e.from_service) + ' &rarr; ' + esc(e.to_service) + '</div>'
    + '<div class="tt-sub"><span class="badge badge-' + e.type + '">' + e.type + '</span> conf: ' + (e.confidence || 0).toFixed(2) + '</div>';
}

// ---- Sidebar ----
function renderSidebar(selection) {
  const sb = document.getElementById('sidebar');
  if (!selection) {
    sb.innerHTML = '<div class="side-section" id="detail-placeholder"><h3>Details</h3><p style="color:var(--dim)">Click a node or edge to inspect</p></div>';
    if (graphData) sb.innerHTML += buildOverviewPanel();
    return;
  }

  if (selection.type === 'service') {
    sb.innerHTML = buildServicePanel(selection.data.data);
  } else if (selection.type === 'shared') {
    sb.innerHTML = buildSharedPanel(selection.data.data);
  } else if (selection.type === 'edge') {
    sb.innerHTML = buildEdgePanel(selection.data);
  }
}

function buildOverviewPanel() {
  if (!graphData) return '';
  const edges = graphData.edges || [];
  let html = '<div class="side-section"><h3>Connections Overview</h3>';
  if (edges.length === 0) {
    html += '<p style="color:var(--dim)">No cross-service connections resolved</p>';
  } else {
    edges.forEach(e => {
      html += '<div style="padding:4px 0;border-bottom:1px solid rgba(255,255,255,.04)">'
        + '<span style="color:var(--text)">' + esc(e.from_service) + '</span>'
        + ' <span style="color:var(--dim)">&rarr;</span> '
        + '<span style="color:var(--text)">' + esc(e.to_service) + '</span>'
        + ' <span class="badge badge-' + e.type + '">' + e.type + '</span>'
        + '</div>';
    });
  }
  html += '</div>';
  return html;
}

function buildServicePanel(svc) {
  let html = '<div class="side-section"><h3>Service</h3>';
  html += kv('Name', svc.name);
  html += kv('Exposures', svc.exposures_count);
  html += kv('Dependencies', svc.dependencies_count);
  html += '</div>';

  const id = svc.identity;
  if (id) {
    html += '<div class="side-section"><h3>Identity</h3>';
    if (id.aliases && id.aliases.length) {
      html += '<div style="margin-bottom:8px"><span class="k">Aliases</span>';
      html += '<div class="tag-list">';
      id.aliases.forEach(a => {
        html += '<span class="tag">' + esc(a.kind) + ': ' + esc(a.value) + '</span>';
      });
      html += '</div></div>';
    }
    if (id.resources && id.resources.length) {
      html += '<div><span class="k">Resources</span>';
      html += '<div class="tag-list">';
      id.resources.forEach(r => {
        html += '<span class="tag">' + esc(r.kind) + ': ' + esc(String(r.identifier || '').slice(0,60)) + '</span>';
      });
      html += '</div></div>';
    }
    html += '</div>';
  }

  // Connected edges
  const edges = (graphData.edges || []).filter(e => e.from_service === svc.name || e.to_service === svc.name);
  if (edges.length) {
    html += '<div class="side-section"><h3>Connections (' + edges.length + ')</h3>';
    edges.forEach(e => {
      const dir = e.from_service === svc.name ? 'outbound' : 'inbound';
      const other = dir === 'outbound' ? e.to_service : e.from_service;
      html += '<div class="kv"><span class="k">' + dir + '</span>'
        + '<span class="v"><span class="badge badge-' + e.type + '">' + e.type + '</span> ' + esc(other) + '</span></div>';
    });
    html += '</div>';
  }

  // Unresolved for this service
  const unresolved = (graphData.unresolved || []).filter(u => u.service === svc.name);
  if (unresolved.length) {
    html += '<div class="side-section"><h3>Unresolved (' + unresolved.length + ')</h3>';
    unresolved.forEach(u => {
      html += '<div style="padding:3px 0;border-bottom:1px solid rgba(255,255,255,.04);color:var(--dim)">'
        + esc(u.dependency_name) + ' <span class="ur-type">' + esc(u.type) + '</span></div>';
    });
    html += '</div>';
  }

  return html;
}

function buildSharedPanel(r) {
  let html = '<div class="side-section"><h3>Shared Resource</h3>';
  html += kv('Kind', r.kind);
  html += kv('Identifier', String(r.identifier || '-'));
  html += '</div>';
  html += '<div class="side-section"><h3>Used By</h3>';
  (r.services || []).forEach(s => {
    html += '<div class="kv"><span class="v">' + esc(s) + '</span></div>';
  });
  html += '</div>';
  return html;
}

function buildEdgePanel(d) {
  const e = d.data;
  if (d.dashed) {
    return buildSharedPanel(e);
  }
  let html = '<div class="side-section"><h3>Edge</h3>';
  html += kv('From', e.from_service);
  html += kv('To', e.to_service);
  html += kv('Type', '<span class="badge badge-' + e.type + '">' + e.type + '</span>');
  html += kv('Confidence', ((e.confidence || 0) * 100).toFixed(0) + '%');
  if (e.from_dependency) html += kv('Dependency', e.from_dependency);
  html += '</div>';
  if (e.label) {
    html += '<div class="side-section"><h3>Match Reasoning</h3>';
    html += '<p style="color:var(--dim);line-height:1.5;word-break:break-word">' + esc(e.label) + '</p>';
    html += '</div>';
  }
  return html;
}

function kv(key, val) {
  return '<div class="kv"><span class="k">' + esc(key) + '</span><span class="v">' + val + '</span></div>';
}

function esc(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML; }

// ---- Unresolved Panel ----
function toggleUnresolved() {
  document.getElementById('unresolved-panel').classList.toggle('open');
}

function renderUnresolved() {
  const panel = document.getElementById('unresolved-panel');
  const items = (graphData && graphData.unresolved) || [];
  if (items.length === 0) {
    panel.innerHTML = '<h3>Unresolved Dependencies</h3><p style="color:var(--dim)">None</p>';
    return;
  }
  // Group by service
  const groups = {};
  items.forEach(u => {
    (groups[u.service] = groups[u.service] || []).push(u);
  });
  let html = '<h3>Unresolved Dependencies (' + items.length + ')</h3>';
  Object.keys(groups).sort().forEach(svc => {
    html += '<div class="ur-group">';
    html += '<div class="ur-svc">' + esc(svc) + ' (' + groups[svc].length + ')</div>';
    groups[svc].forEach(u => {
      html += '<div class="ur-item">' + esc(u.dependency_name) + ' <span class="ur-type">' + esc(u.type) + '</span></div>';
    });
    html += '</div>';
  });
  panel.innerHTML = html;
}

// ---- Init ----
(async function init() {
  await loadRuns();
  await loadRun();
})();
</script>
</body>
</html>` + ""
