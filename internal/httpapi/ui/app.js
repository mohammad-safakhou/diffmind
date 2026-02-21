const state = {
  index: null,
  graph: null,
  selectedNodeId: '',
  viewport: { scale: 1, tx: 0, ty: 0 },
  dragging: false,
  dragStart: null,
  dragMoved: false,
  suppressBackgroundClick: false,
  auth: {
    tenant: localStorage.getItem('diffmind_auth_tenant') || 'default',
    principal: localStorage.getItem('diffmind_auth_principal') || 'ui-user',
    roles: localStorage.getItem('diffmind_auth_roles') || '',
    scopes: localStorage.getItem('diffmind_auth_scopes') || '',
  },
};

const graphSelect = document.getElementById('graphSelect');
const refreshBtn = document.getElementById('refreshBtn');
const resetViewBtn = document.getElementById('resetViewBtn');
const graphSvg = document.getElementById('graphSvg');
const viewport = document.getElementById('viewport');
const selectionDetails = document.getElementById('selectionDetails');
const summary = document.getElementById('summary');

const CANVAS = { width: 1600, height: 900 };
const COL = { exposureX: 260, dependencyX: 1340 };

function authHeaders() {
  return {
    'X-DiffMind-Tenant': state.auth.tenant,
    'X-DiffMind-Principal': state.auth.principal,
    'X-DiffMind-Roles': state.auth.roles,
    'X-DiffMind-Scopes': state.auth.scopes,
  };
}

async function fetchJSON(url) {
  const res = await fetch(url, { headers: authHeaders() });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const msg = data.error || `request failed: ${res.status}`;
    throw new Error(msg);
  }
  return data;
}

function escapeHTML(v) {
  return String(v)
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function clearViewport() {
  while (viewport.firstChild) viewport.removeChild(viewport.firstChild);
}

function setTransform() {
  viewport.setAttribute('transform', `translate(${state.viewport.tx} ${state.viewport.ty}) scale(${state.viewport.scale})`);
}

function resetView() {
  state.viewport = { scale: 1, tx: 0, ty: 0 };
  setTransform();
}

function nodeType(node) {
  return String(node.type || '').toLowerCase();
}

function nodeLabel(node) {
  return String(node.label || node.id || '').trim() || node.id;
}

function isExposure(node) {
  const t = nodeType(node);
  return t === 'endpoint';
}

function isDependency(node) {
  const t = nodeType(node);
  return t === 'dependency_operation' || t === 'database' || t === 'queue';
}

function buildFocusedGraph(raw) {
  const nodes = Array.isArray(raw.nodes) ? raw.nodes : [];
  const edges = Array.isArray(raw.edges) ? raw.edges : [];
  const nodeById = new Map(nodes.map((n) => [n.id, n]));

  const focusedEdges = edges.filter((e) => {
    if (String(e.type) !== 'exposure_reaches_dependency') return false;
    const s = nodeById.get(e.source_id);
    const t = nodeById.get(e.target_id);
    return !!s && !!t && isExposure(s) && isDependency(t);
  });

  const keep = new Set();
  focusedEdges.forEach((e) => {
    keep.add(e.source_id);
    keep.add(e.target_id);
  });

  const focusedNodes = nodes.filter((n) => keep.has(n.id));
  return { nodes: focusedNodes, edges: focusedEdges };
}

function nodeNeighborhood(graph, nodeID) {
  if (!nodeID) return { nodes: new Set(), edges: new Set() };
  const nodes = new Set([nodeID]);
  const edges = new Set();
  for (let i = 0; i < graph.edges.length; i += 1) {
    const e = graph.edges[i];
    if (e.source_id === nodeID || e.target_id === nodeID) {
      edges.add(i);
      nodes.add(e.source_id);
      nodes.add(e.target_id);
    }
  }
  return { nodes, edges };
}

function extractInputDetails(node) {
  const attrs = node && node.attributes && typeof node.attributes === 'object' ? node.attributes : {};
  const lines = [];

  const addField = (label, value) => {
    if (value === undefined || value === null || value === '') return;
    if (Array.isArray(value)) {
      if (value.length === 0) return;
      lines.push(`${label}: ${value.join(', ')}`);
      return;
    }
    if (typeof value === 'object') {
      const keys = Object.keys(value);
      if (keys.length === 0) return;
      lines.push(`${label}: ${keys.join(', ')}`);
      return;
    }
    lines.push(`${label}: ${String(value)}`);
  };

  addField('Method', attrs.method);
  addField('Path', attrs.path || attrs.route || attrs.uri);
  addField('Path Params', attrs.path_params || attrs.pathParams);
  addField('Query Params', attrs.query_params || attrs.queryParams);
  addField('Headers', attrs.headers || attrs.request_headers || attrs.requestHeaders);
  addField('Body', attrs.body || attrs.request_body || attrs.requestBody || attrs.payload);
  addField('Inputs', attrs.inputs || attrs.input || attrs.request_params || attrs.requestParams);

  if (lines.length === 0) return ['No input metadata available'];
  return lines.slice(0, 7);
}

function layoutGraph(graph) {
  const exposures = graph.nodes.filter(isExposure).sort((a, b) => nodeLabel(a).localeCompare(nodeLabel(b)));
  const dependencies = graph.nodes.filter(isDependency).sort((a, b) => nodeLabel(a).localeCompare(nodeLabel(b)));

  const spacingExposure = Math.max(26, Math.floor((CANVAS.height - 140) / Math.max(1, exposures.length)));
  const spacingDependency = Math.max(26, Math.floor((CANVAS.height - 140) / Math.max(1, dependencies.length)));

  const pos = new Map();
  exposures.forEach((n, i) => pos.set(n.id, { x: COL.exposureX, y: 70 + i * spacingExposure }));
  dependencies.forEach((n, i) => pos.set(n.id, { x: COL.dependencyX, y: 70 + i * spacingDependency }));

  return { exposures, dependencies, pos };
}

function renderGraph() {
  clearViewport();
  const graph = state.graph;
  if (!graph || graph.nodes.length === 0) {
    summary.textContent = 'No exposure-to-dependency links found in this graph.';
    selectionDetails.textContent = 'Click a node to see details.';
    return;
  }

  const { exposures, dependencies, pos } = layoutGraph(graph);
  const nodeById = new Map(graph.nodes.map((n) => [n.id, n]));
  const selectedID = state.selectedNodeId;
  const hasSelection = !!selectedID;
  const neighborhood = nodeNeighborhood(graph, selectedID);

  const leftTitle = document.createElementNS('http://www.w3.org/2000/svg', 'text');
  leftTitle.setAttribute('x', String(COL.exposureX - 80));
  leftTitle.setAttribute('y', '28');
  leftTitle.setAttribute('font-size', '16');
  leftTitle.setAttribute('font-weight', '700');
  leftTitle.textContent = 'Exposure';
  viewport.appendChild(leftTitle);

  const rightTitle = document.createElementNS('http://www.w3.org/2000/svg', 'text');
  rightTitle.setAttribute('x', String(COL.dependencyX - 95));
  rightTitle.setAttribute('y', '28');
  rightTitle.setAttribute('font-size', '16');
  rightTitle.setAttribute('font-weight', '700');
  rightTitle.textContent = 'Dependency';
  viewport.appendChild(rightTitle);

  for (let i = 0; i < graph.edges.length; i += 1) {
    const e = graph.edges[i];
    const s = pos.get(e.source_id);
    const t = pos.get(e.target_id);
    if (!s || !t) continue;
    const activeEdge = !hasSelection || neighborhood.edges.has(i);
    const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
    line.setAttribute('x1', String(s.x + 8));
    line.setAttribute('y1', String(s.y));
    line.setAttribute('x2', String(t.x - 8));
    line.setAttribute('y2', String(t.y));
    line.setAttribute('stroke', activeEdge ? '#1f2937' : '#94a3b8');
    line.setAttribute('stroke-opacity', activeEdge ? '0.95' : '0.08');
    line.setAttribute('stroke-width', activeEdge ? '1.8' : '1');
    viewport.appendChild(line);
  }

  const drawNode = (node, color) => {
    const p = pos.get(node.id);
    if (!p) return;

    const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
    g.setAttribute('data-node-id', node.id);
    g.style.cursor = 'pointer';
    const activeNode = !hasSelection || neighborhood.nodes.has(node.id);
    const isSelected = selectedID === node.id;
    g.setAttribute('opacity', activeNode ? '1' : '0.14');

    const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
    circle.setAttribute('cx', String(p.x));
    circle.setAttribute('cy', String(p.y));
    circle.setAttribute('r', isSelected ? '7' : '5');
    circle.setAttribute('fill', isSelected ? '#ef4444' : color);
    g.appendChild(circle);

    const text = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    text.setAttribute('x', String(p.x + (isExposure(node) ? -14 : 14)));
    text.setAttribute('y', String(p.y + 4));
    text.setAttribute('font-size', '11');
    text.setAttribute('fill', activeNode ? '#0f172a' : '#64748b');
    text.setAttribute('text-anchor', isExposure(node) ? 'end' : 'start');
    text.textContent = nodeLabel(node);
    g.appendChild(text);

    g.addEventListener('click', (ev) => {
      ev.stopPropagation();
      state.selectedNodeId = node.id;
      renderGraph();
      renderSelection(node);
    });

    viewport.appendChild(g);

    if (isSelected && isExposure(node)) {
      const lines = extractInputDetails(node);
      const panel = document.createElementNS('http://www.w3.org/2000/svg', 'g');
      const boxX = p.x + 18;
      const boxY = p.y - 12;
      const lineHeight = 13;
      const boxWidth = 380;
      const boxHeight = Math.max(22, 10 + lines.length * lineHeight);

      const rect = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
      rect.setAttribute('x', String(boxX));
      rect.setAttribute('y', String(boxY));
      rect.setAttribute('width', String(boxWidth));
      rect.setAttribute('height', String(boxHeight));
      rect.setAttribute('rx', '5');
      rect.setAttribute('fill', '#fffbeb');
      rect.setAttribute('stroke', '#f59e0b');
      rect.setAttribute('stroke-width', '1');
      panel.appendChild(rect);

      for (let i = 0; i < lines.length; i += 1) {
        const row = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        row.setAttribute('x', String(boxX + 8));
        row.setAttribute('y', String(boxY + 16 + i * lineHeight));
        row.setAttribute('font-size', '11');
        row.setAttribute('fill', '#111827');
        row.setAttribute('text-anchor', 'start');
        row.textContent = lines[i];
        panel.appendChild(row);
      }
      viewport.appendChild(panel);
    }
  };

  exposures.forEach((n) => drawNode(n, '#1d4ed8'));
  dependencies.forEach((n) => drawNode(n, '#9333ea'));

  summary.innerHTML = `
    <div><strong>Exposures:</strong> ${exposures.length}</div>
    <div><strong>Dependencies:</strong> ${dependencies.length}</div>
    <div><strong>Links:</strong> ${graph.edges.length}</div>
  `;

  if (state.selectedNodeId) {
    const selected = nodeById.get(state.selectedNodeId);
    if (selected) renderSelection(selected);
  }
}

function renderSelection(node) {
  const attrs = node.attributes || {};
  const out = {
    id: node.id,
    type: node.type,
    label: nodeLabel(node),
    attributes: attrs,
  };
  selectionDetails.textContent = JSON.stringify(out, null, 2);
}

async function loadGraphsList() {
  const index = await fetchJSON('/graphs');
  state.index = index;
  const graphs = Array.isArray(index.graphs) ? index.graphs : [];

  graphSelect.innerHTML = '';
  if (graphs.length === 0) {
    const o = document.createElement('option');
    o.value = '';
    o.textContent = 'No graphs available';
    graphSelect.appendChild(o);
    state.graph = null;
    renderGraph();
    return;
  }

  for (const g of graphs) {
    const o = document.createElement('option');
    o.value = g.graph_id;
    o.textContent = `${g.graph_id}`;
    graphSelect.appendChild(o);
  }

  await loadGraph();
}

async function loadGraph() {
  const graphID = graphSelect.value;
  if (!graphID) return;
  const raw = await fetchJSON(`/graphs/${encodeURIComponent(graphID)}`);
  state.selectedNodeId = '';
  state.graph = buildFocusedGraph(raw);
  resetView();
  renderGraph();
}

function clientPointToSVG(clientX, clientY) {
  const pt = graphSvg.createSVGPoint();
  pt.x = clientX;
  pt.y = clientY;
  const ctm = graphSvg.getScreenCTM();
  if (!ctm) return { x: 0, y: 0 };
  const p = pt.matrixTransform(ctm.inverse());
  return { x: p.x, y: p.y };
}

function wireGraphInteraction() {
  graphSvg.addEventListener('click', () => {
    if (state.suppressBackgroundClick) {
      state.suppressBackgroundClick = false;
      return;
    }
    state.selectedNodeId = '';
    selectionDetails.textContent = 'Click a node to see details.';
    renderGraph();
  });

  graphSvg.addEventListener('mousedown', (ev) => {
    if (ev.button !== 0) return;
    ev.preventDefault();
    state.dragging = true;
    state.dragMoved = false;
    state.dragStart = { x: ev.clientX, y: ev.clientY, tx: state.viewport.tx, ty: state.viewport.ty };
  });

  window.addEventListener('mousemove', (ev) => {
    if (!state.dragging || !state.dragStart) return;
    const dx = ev.clientX - state.dragStart.x;
    const dy = ev.clientY - state.dragStart.y;
    if (Math.abs(dx) > 2 || Math.abs(dy) > 2) state.dragMoved = true;
    state.viewport.tx = state.dragStart.tx + dx;
    state.viewport.ty = state.dragStart.ty + dy;
    setTransform();
  });

  window.addEventListener('mouseup', () => {
    if (state.dragMoved) state.suppressBackgroundClick = true;
    state.dragging = false;
    state.dragStart = null;
    state.dragMoved = false;
  });

  graphSvg.addEventListener('wheel', (ev) => {
    ev.preventDefault();
    const step = ev.deltaY < 0 ? 1.08 : 0.92;
    const newScale = Math.max(0.2, Math.min(4, state.viewport.scale * step));
    const mouse = clientPointToSVG(ev.clientX, ev.clientY);
    const worldX = (mouse.x - state.viewport.tx) / state.viewport.scale;
    const worldY = (mouse.y - state.viewport.ty) / state.viewport.scale;

    state.viewport.scale = newScale;
    state.viewport.tx = mouse.x - worldX * newScale;
    state.viewport.ty = mouse.y - worldY * newScale;
    setTransform();
  }, { passive: false });

  graphSvg.addEventListener('touchstart', (ev) => {
    ev.preventDefault();
  }, { passive: false });

  graphSvg.addEventListener('touchmove', (ev) => {
    ev.preventDefault();
  }, { passive: false });
}

refreshBtn.addEventListener('click', async () => {
  try {
    await loadGraphsList();
  } catch (err) {
    summary.textContent = err.message;
  }
});

resetViewBtn.addEventListener('click', () => resetView());
graphSelect.addEventListener('change', () => loadGraph().catch((err) => { summary.textContent = err.message; }));

(async function init() {
  wireGraphInteraction();
  try {
    await loadGraphsList();
  } catch (err) {
    summary.textContent = escapeHTML(err.message);
  }
})();
