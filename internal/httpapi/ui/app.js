const state = {
  index: null,
  graph: null,
  defaults: null,
  nodeById: {},
  layout: 'grid',
  viewport: { scale: 1, tx: 0, ty: 0 },
  dragging: false,
  dragStart: null,
  selectedNodeId: '',
  selectedEdgeId: '',
  lastCompare: null,
  compareHistoryNextBefore: '',
};

const graphSelect = document.getElementById('graphSelect');
const includeInferred = document.getElementById('includeInferred');
const edgeTypes = document.getElementById('edgeTypes');
const serviceFilter = document.getElementById('serviceFilter');
const repoFilter = document.getElementById('repoFilter');
const confidenceMin = document.getElementById('confidenceMin');
const confidenceMinLabel = document.getElementById('confidenceMinLabel');
const summary = document.getElementById('summary');
const legend = document.getElementById('legend');
const metrics = document.getElementById('metrics');
const compareFrom = document.getElementById('compareFrom');
const compareTo = document.getElementById('compareTo');
const compareHistory = document.getElementById('compareHistory');
const compareSummary = document.getElementById('compareSummary');
const compareResults = document.getElementById('compareResults');
const edgesBody = document.querySelector('#edgesTable tbody');
const details = document.getElementById('selectionDetails');
const graphSvg = document.getElementById('graphSvg');
const buildStatus = document.getElementById('buildStatus');
const layoutMode = document.getElementById('layoutMode');

const edgeColors = {
  service_calls_service: '#264653',
  service_calls_endpoint: '#1d3557',
  service_publishes_queue: '#ef476f',
  queue_delivers_to_service: '#f4a261',
  service_reads_db: '#118ab2',
  service_writes_db: '#2a9d8f',
};

async function fetchJSON(url, options = {}) {
  const res = await fetch(url, options);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const message = data && data.error ? data.error : `request failed: ${res.status}`;
    throw new Error(message);
  }
  return data;
}

async function loadGraphsList() {
  const index = await fetchJSON('/graphs');
  state.index = index;
  graphSelect.innerHTML = '';
  compareFrom.innerHTML = '';
  compareTo.innerHTML = '';
  compareHistory.innerHTML = '';

  const graphs = Array.isArray(index.graphs) ? index.graphs : [];
  if (graphs.length === 0) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'No graphs available';
    graphSelect.appendChild(option);
    summary.textContent = 'Build a graph from the form on the left.';
    legend.innerHTML = '';
    metrics.innerHTML = '';
    state.graph = null;
    state.selectedNodeId = '';
    state.selectedEdgeId = '';
    renderEdges();
    renderSVG();
    compareSummary.textContent = '';
    compareResults.textContent = '';
    state.lastCompare = null;
    return;
  }

  for (const g of graphs) {
    const option = document.createElement('option');
    option.value = g.graph_id;
    option.textContent = `${g.graph_id} (${g.mode})`;
    graphSelect.appendChild(option);
    const compareOptionA = document.createElement('option');
    compareOptionA.value = g.graph_id;
    compareOptionA.textContent = `${g.graph_id} (${g.mode})`;
    compareFrom.appendChild(compareOptionA);
    const compareOptionB = document.createElement('option');
    compareOptionB.value = g.graph_id;
    compareOptionB.textContent = `${g.graph_id} (${g.mode})`;
    compareTo.appendChild(compareOptionB);
  }
  if (graphs.length > 1) {
    compareFrom.value = graphs[1].graph_id;
    compareTo.value = graphs[0].graph_id;
  } else if (graphs.length === 1) {
    compareFrom.value = graphs[0].graph_id;
    compareTo.value = graphs[0].graph_id;
  }

  await refreshCompareHistory();
  await loadGraph();
}

async function refreshCompareHistory(append = false) {
  let url = '/graphs/compare?limit=20';
  if (append) {
    if (!state.compareHistoryNextBefore) {
      return;
    }
    url += `&before=${encodeURIComponent(state.compareHistoryNextBefore)}`;
  }
  const index = await fetchJSON(url);
  if (!append) {
    compareHistory.innerHTML = '';
  }
  const entries = Array.isArray(index.compares) ? index.compares : [];
  if (entries.length === 0 && !append) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'No compare history';
    compareHistory.appendChild(option);
    state.compareHistoryNextBefore = '';
    document.getElementById('loadMoreCompareHistoryBtn').disabled = true;
    return;
  }
  const existing = new Set(Array.from(compareHistory.options).map((o) => o.value));
  for (const c of entries) {
    if (existing.has(c.compare_id)) {
      continue;
    }
    const option = document.createElement('option');
    option.value = c.compare_id;
    option.textContent = `${c.compare_id} (${c.from_graph_id} -> ${c.to_graph_id})`;
    compareHistory.appendChild(option);
  }
  state.compareHistoryNextBefore = index.next_before || '';
  document.getElementById('loadMoreCompareHistoryBtn').disabled = state.compareHistoryNextBefore === '';
}

function graphQuery() {
  const params = new URLSearchParams();
  if (includeInferred.checked) {
    params.set('include_inferred', 'true');
  }
  if (edgeTypes.value.trim() !== '') {
    params.set('edge_types', edgeTypes.value.trim());
  }
  if (serviceFilter.value.trim() !== '') {
    params.set('service', serviceFilter.value.trim());
  }
  if (repoFilter.value.trim() !== '') {
    params.set('repo', repoFilter.value.trim());
  }
  const minConfidence = Number(confidenceMin.value || '0');
  if (minConfidence > 0) {
    params.set('confidence_min', minConfidence.toFixed(2));
  }
  const query = params.toString();
  return query ? `?${query}` : '';
}

async function loadGraph() {
  const graphID = graphSelect.value;
  if (!graphID) {
    return;
  }

  const query = graphQuery();
  const [graph, metricsPayload] = await Promise.all([
    fetchJSON(`/graphs/${encodeURIComponent(graphID)}${query}`),
    fetchJSON(`/graphs/${encodeURIComponent(graphID)}/metrics${query}`),
  ]);
  state.graph = graph;
  state.nodeById = {};
  state.selectedNodeId = '';
  state.selectedEdgeId = '';
  for (const n of graph.nodes || []) {
    state.nodeById[n.id] = n;
  }

  summary.textContent = `Graph ${graph.graph_id} | mode=${graph.mode} | nodes=${(graph.nodes || []).length} | edges=${(graph.edges || []).length}`;
  details.textContent = 'Single click node: focus neighborhood. Double click node: filter by that service.';
  renderLegend(graph);
  renderMetrics(metricsPayload);
  renderEdges();
  renderSVG();
}

function short(v, max = 42) {
  if (typeof v !== 'string') {
    return '';
  }
  return v.length > max ? `${v.slice(0, max)}...` : v;
}

function renderLegend(graph) {
  const nodeTypes = new Set((graph.nodes || []).map((n) => n.type));
  const edgeTypesInGraph = new Set((graph.edges || []).map((e) => e.type));

  const items = [];
  for (const t of Array.from(nodeTypes).sort()) {
    items.push(`<span class="legendItem"><span class="legendDot" style="background:${nodeColor(t)}"></span>node:${t}</span>`);
  }
  for (const t of Array.from(edgeTypesInGraph).sort()) {
    items.push(`<span class="legendItem"><span class="legendDot" style="background:${edgeColor(t)}"></span>edge:${t}</span>`);
  }
  legend.innerHTML = items.join('');
}

function renderMetrics(payload) {
  const nodeCount = Number(payload.node_count || 0);
  const edgeCount = Number(payload.edge_count || 0);
  const topCaller = (payload.top_callers || [])[0] || null;
  const topDependency = (payload.top_dependencies || [])[0] || null;
  const topCallerText = topCaller ? `${short(topCaller.label || topCaller.node_id, 18)} (${topCaller.count})` : 'n/a';
  const topDependencyText = topDependency ? `${short(topDependency.label || topDependency.node_id, 18)} (${topDependency.count})` : 'n/a';
  const callersList = (payload.top_callers || [])
    .map((i) => `<li>${short(i.label || i.node_id, 22)} <strong>${i.count}</strong></li>`)
    .join('');
  const depsList = (payload.top_dependencies || [])
    .map((i) => `<li>${short(i.label || i.node_id, 22)} <strong>${i.count}</strong></li>`)
    .join('');
  metrics.innerHTML = `
    <div class="metricCard"><div class="metricLabel">Visible Nodes</div><div class="metricValue">${nodeCount}</div></div>
    <div class="metricCard"><div class="metricLabel">Visible Edges</div><div class="metricValue">${edgeCount}</div></div>
    <div class="metricCard"><div class="metricLabel">Top Caller</div><div class="metricValue">${topCallerText}</div></div>
    <div class="metricCard"><div class="metricLabel">Top Dependency</div><div class="metricValue">${topDependencyText}</div></div>
    <div class="metricCard"><div class="metricLabel">Top Callers</div><ul class="metricList">${callersList || '<li>n/a</li>'}</ul></div>
    <div class="metricCard"><div class="metricLabel">Top Dependencies</div><ul class="metricList">${depsList || '<li>n/a</li>'}</ul></div>
  `;
}

function renderCompare(payload) {
  compareSummary.textContent = `from=${payload.from_graph_id} to=${payload.to_graph_id} | +nodes=${(payload.added_nodes || []).length}, -nodes=${(payload.removed_nodes || []).length}, ~nodes=${(payload.changed_nodes || []).length}, +edges=${(payload.added_edges || []).length}, -edges=${(payload.removed_edges || []).length}, ~edges=${(payload.changed_edges || []).length}`;
  const addedNodes = (payload.added_nodes || []).slice(0, 8).map((n) => `<li>${n.id} (${n.type})</li>`).join('');
  const removedNodes = (payload.removed_nodes || []).slice(0, 8).map((n) => `<li>${n.id} (${n.type})</li>`).join('');
  const changedNodes = (payload.changed_nodes || []).slice(0, 8).map((n) => `<li>${n.id}: ${((n.keys || []).join(', ') || 'changed')} ${formatChangePreview(n)}</li>`).join('');
  const addedEdges = (payload.added_edges || []).slice(0, 8).map((e) => `<li>${e.id}: ${e.source_id} -> ${e.target_id}</li>`).join('');
  const removedEdges = (payload.removed_edges || []).slice(0, 8).map((e) => `<li>${e.id}: ${e.source_id} -> ${e.target_id}</li>`).join('');
  const changedEdges = (payload.changed_edges || []).slice(0, 8).map((e) => `<li>${e.id}: ${((e.keys || []).join(', ') || 'changed')} ${formatChangePreview(e)}</li>`).join('');
  compareResults.innerHTML = `
    <div class="row">
      <div><strong>Added Nodes</strong><ul class="metricList">${addedNodes || '<li>none</li>'}</ul></div>
      <div><strong>Removed Nodes</strong><ul class="metricList">${removedNodes || '<li>none</li>'}</ul></div>
    </div>
    <div class="row">
      <div><strong>Changed Nodes</strong><ul class="metricList">${changedNodes || '<li>none</li>'}</ul></div>
      <div><strong>Changed Edges</strong><ul class="metricList">${changedEdges || '<li>none</li>'}</ul></div>
    </div>
    <div class="row">
      <div><strong>Added Edges</strong><ul class="metricList">${addedEdges || '<li>none</li>'}</ul></div>
      <div><strong>Removed Edges</strong><ul class="metricList">${removedEdges || '<li>none</li>'}</ul></div>
    </div>
  `;
}

function formatChangePreview(changeItem) {
  const keys = Array.isArray(changeItem.keys) ? changeItem.keys : [];
  const before = changeItem.before || {};
  const after = changeItem.after || {};
  if (keys.length === 0) {
    return '';
  }
  const snippets = [];
  for (const key of keys.slice(0, 2)) {
    const left = stringifyShort(before[key]);
    const right = stringifyShort(after[key]);
    snippets.push(`${key}: ${left} -> ${right}`);
  }
  return snippets.length > 0 ? `(${snippets.join('; ')})` : '';
}

function stringifyShort(v) {
  if (typeof v === 'string') {
    return short(v, 18);
  }
  if (v === null || v === undefined) {
    return 'null';
  }
  if (typeof v === 'object') {
    return short(JSON.stringify(v), 18);
  }
  return String(v);
}

function renderEdges() {
  edgesBody.innerHTML = '';
  const graph = state.graph;
  if (!graph || !Array.isArray(graph.edges)) {
    return;
  }

  for (const edge of graph.edges) {
    const tr = document.createElement('tr');
    const sourceNode = state.nodeById[edge.source_id];
    const targetNode = state.nodeById[edge.target_id];

    tr.innerHTML = `
      <td title="${edge.id}">${short(edge.id, 28)}</td>
      <td><span style="display:inline-flex;align-items:center;gap:6px"><span class="legendDot" style="background:${edgeColor(edge.type)}"></span>${edge.type}</span></td>
      <td title="${edge.source_id}">${sourceNode ? sourceNode.label : edge.source_id}</td>
      <td title="${edge.target_id}">${targetNode ? targetNode.label : edge.target_id}</td>
      <td>${Number(edge.confidence || 0).toFixed(2)}</td>
      <td><button type="button">Open</button></td>
    `;

    if (edge.id === state.selectedEdgeId) {
      tr.style.backgroundColor = '#e9f5f3';
    }

    tr.addEventListener('click', () => {
      state.selectedEdgeId = edge.id;
      renderEdges();
      renderSVG();
    });

    tr.querySelector('button').addEventListener('click', async (evt) => {
      evt.stopPropagation();
      details.textContent = 'Loading evidence...';
      state.selectedEdgeId = edge.id;
      renderEdges();
      renderSVG();
      try {
        const payload = await fetchJSON(`/graphs/${encodeURIComponent(graph.graph_id)}/evidence/${encodeURIComponent(edge.id)}`);
        const refs = payload.evidence_refs || [];
        if (refs.length === 0) {
          details.textContent = `Edge ${edge.id} has no evidence refs.`;
          return;
        }
        const lines = refs.map((ref, idx) => {
          const span = ref.file_path ? `${ref.file_path}:${ref.start_line || 0}:${ref.start_col || 0}` : 'unknown';
          return `${idx + 1}. ${span} ${ref.evidence_id ? `(evidence=${ref.evidence_id})` : ''}`;
        });
        details.textContent = `Edge ${edge.id}\n${lines.join('\n')}`;
      } catch (err) {
        details.textContent = `Failed to load evidence: ${err.message}`;
      }
    });

    edgesBody.appendChild(tr);
  }
}

function nodeColor(type) {
  const byType = {
    service: '#006d77',
    endpoint: '#1d3557',
    queue: '#ef476f',
    database: '#118ab2',
    topic: '#f4a261',
    table: '#2a9d8f',
  };
  return byType[type] || '#4a5568';
}

function edgeColor(type) {
  return edgeColors[type] || '#0f172a';
}

function computeGridPositions(nodes, w, h) {
  const padding = 70;
  const cols = Math.ceil(Math.sqrt(nodes.length));
  const rows = Math.ceil(nodes.length / cols);
  const xStep = (w - padding * 2) / Math.max(cols - 1, 1);
  const yStep = (h - padding * 2) / Math.max(rows - 1, 1);
  const position = {};
  nodes.forEach((node, i) => {
    const col = i % cols;
    const row = Math.floor(i / cols);
    position[node.id] = {
      x: padding + col * xStep,
      y: padding + row * yStep,
    };
  });
  return position;
}

function computeServiceLanePositions(nodes, w, h) {
  const lanes = new Map();
  for (const node of nodes) {
    const serviceKey = node.service_id || 'shared';
    if (!lanes.has(serviceKey)) {
      lanes.set(serviceKey, []);
    }
    lanes.get(serviceKey).push(node);
  }
  const serviceKeys = Array.from(lanes.keys()).sort();
  const paddingX = 80;
  const laneY = 96;
  const laneGap = Math.max((h - laneY * 2) / Math.max(serviceKeys.length - 1, 1), 75);
  const position = {};
  for (let i = 0; i < serviceKeys.length; i++) {
    const key = serviceKeys[i];
    const rowNodes = lanes.get(key) || [];
    const xStep = (w - paddingX * 2) / Math.max(rowNodes.length - 1, 1);
    rowNodes
      .sort((a, b) => (a.type + a.id).localeCompare(b.type + b.id))
      .forEach((node, idx) => {
        position[node.id] = {
          x: paddingX + idx * xStep,
          y: laneY + i * laneGap,
        };
      });
  }
  return position;
}

function svgPoint(evt) {
  const rect = graphSvg.getBoundingClientRect();
  return { x: evt.clientX - rect.left, y: evt.clientY - rect.top };
}

function applyViewport(g) {
  const { scale, tx, ty } = state.viewport;
  g.setAttribute('transform', `translate(${tx} ${ty}) scale(${scale})`);
}

function renderSVG() {
  graphSvg.innerHTML = '';
  const graph = state.graph;
  if (!graph || !Array.isArray(graph.nodes) || graph.nodes.length === 0) {
    return;
  }

  const w = 1200;
  const h = 520;
  const nodes = graph.nodes;
  const position = state.layout === 'service_lanes'
    ? computeServiceLanePositions(nodes, w, h)
    : computeGridPositions(nodes, w, h);

  const selectedNode = state.selectedNodeId;
  const neighborhood = new Set();
  const selectedNodeEdges = new Set();
  if (selectedNode) {
    neighborhood.add(selectedNode);
    for (const e of graph.edges || []) {
      if (e.source_id === selectedNode || e.target_id === selectedNode) {
        selectedNodeEdges.add(e.id);
        neighborhood.add(e.source_id);
        neighborhood.add(e.target_id);
      }
    }
  }

  const root = document.createElementNS('http://www.w3.org/2000/svg', 'g');
  applyViewport(root);
  graphSvg.appendChild(root);

  for (const edge of graph.edges || []) {
    const s = position[edge.source_id];
    const t = position[edge.target_id];
    if (!s || !t) continue;

    const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
    line.setAttribute('x1', s.x);
    line.setAttribute('y1', s.y);
    line.setAttribute('x2', t.x);
    line.setAttribute('y2', t.y);

    let stroke = edgeColor(edge.type);
    let opacity = edge.inferred ? 0.4 : 0.75;
    let width = edge.inferred ? 1.2 : 1.8;

    if (edge.id === state.selectedEdgeId) {
      stroke = '#d62828';
      opacity = 0.95;
      width = 3.2;
    } else if (selectedNode) {
      if (selectedNodeEdges.has(edge.id)) {
        stroke = '#007f5f';
        opacity = 0.95;
        width = 2.8;
      } else {
        opacity = 0.1;
      }
    }

    line.setAttribute('stroke', stroke);
    line.setAttribute('stroke-opacity', `${opacity}`);
    line.setAttribute('stroke-width', `${width}`);
    root.appendChild(line);
  }

  for (const node of nodes) {
    const p = position[node.id];
    const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
    g.style.cursor = 'pointer';

    const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
    circle.setAttribute('cx', p.x);
    circle.setAttribute('cy', p.y);
    circle.setAttribute('r', 10);
    circle.setAttribute('fill', nodeColor(node.type));

    const label = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    label.setAttribute('x', p.x + 12);
    label.setAttribute('y', p.y + 4);
    label.setAttribute('font-size', '11');
    label.setAttribute('fill', '#0f172a');
    label.textContent = short(node.label || node.id, 28);

    if (node.id === selectedNode) {
      circle.setAttribute('stroke', '#1d3557');
      circle.setAttribute('stroke-width', '3');
    } else if (selectedNode && !neighborhood.has(node.id)) {
      circle.setAttribute('opacity', '0.2');
      label.setAttribute('opacity', '0.2');
    }

    g.addEventListener('click', (evt) => {
      evt.stopPropagation();
      if (state.selectedNodeId === node.id) {
        state.selectedNodeId = '';
        details.textContent = 'Node focus cleared.';
      } else {
        state.selectedNodeId = node.id;
        state.selectedEdgeId = '';
        const inbound = (graph.edges || []).filter((e) => e.target_id === node.id).length;
        const outbound = (graph.edges || []).filter((e) => e.source_id === node.id).length;
        details.textContent = `Node ${node.label || node.id}\nType: ${node.type}\nService: ${node.service_id || 'n/a'}\nInbound edges: ${inbound}\nOutbound edges: ${outbound}\nDouble click to filter graph to this service.`;
      }
      renderEdges();
      renderSVG();
    });

    g.addEventListener('dblclick', (evt) => {
      evt.stopPropagation();
      if (!node.service_id) {
        return;
      }
      serviceFilter.value = node.service_id;
      details.textContent = `Applied service filter: ${node.service_id}`;
      loadGraph().catch((err) => {
        summary.textContent = `Failed loading graph: ${err.message}`;
      });
    });

    g.appendChild(circle);
    g.appendChild(label);
    root.appendChild(g);
  }

  graphSvg.onclick = () => {
    state.selectedNodeId = '';
    state.selectedEdgeId = '';
    details.textContent = 'Focus cleared.';
    renderEdges();
    renderSVG();
  };
}

function setScale(nextScale) {
  state.viewport.scale = Math.max(0.2, Math.min(3, nextScale));
  renderSVG();
}

function fitView() {
  state.viewport = { scale: 1, tx: 0, ty: 0 };
  renderSVG();
}

async function buildGraph(e) {
  e.preventDefault();
  buildStatus.textContent = 'Building graph...';

  const payload = {
    mode: document.getElementById('mode').value || 'auto',
    out_dir: document.getElementById('outDir').value.trim(),
    manifest_path: document.getElementById('manifestPath').value.trim(),
    sources: document.getElementById('sources').value
      .split(',')
      .map((v) => v.trim())
      .filter((v) => v !== ''),
    service_id: document.getElementById('serviceID').value.trim(),
    service_name: document.getElementById('serviceName').value.trim(),
    bundle_path: document.getElementById('bundlePath').value.trim(),
    analyzer_bundle_path: document.getElementById('analyzerPath').value.trim(),
    persist: document.getElementById('persist').checked,
    base_urls: document.getElementById('baseURLs').value
      .split(',')
      .map((v) => v.trim())
      .filter((v) => v !== ''),
  };

  try {
    const res = await fetchJSON('/graphs', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    buildStatus.textContent = `Build complete: graph_id=${res.graph_id}, nodes=${res.node_count}, edges=${res.edge_count}`;
    await loadGraphsList();
    graphSelect.value = res.graph_id;
    await loadGraph();
  } catch (err) {
    buildStatus.textContent = `Build failed: ${err.message}`;
  }
}

async function loadDefaults() {
  const payload = await fetchJSON('/defaults');
  state.defaults = payload;
  const buildDefaults = payload.build_defaults || {};

  const mode = document.getElementById('mode');
  const outDir = document.getElementById('outDir');
  const manifestPath = document.getElementById('manifestPath');
  const serviceID = document.getElementById('serviceID');
  const serviceName = document.getElementById('serviceName');
  const bundlePath = document.getElementById('bundlePath');
  const analyzerPath = document.getElementById('analyzerPath');

  if (!mode.value && buildDefaults.mode) mode.value = buildDefaults.mode;
  if (!outDir.value && buildDefaults.out_dir) outDir.value = buildDefaults.out_dir;
  if (!manifestPath.value && buildDefaults.manifest_path) manifestPath.value = buildDefaults.manifest_path;
  if (!serviceID.value && buildDefaults.service_id) serviceID.value = buildDefaults.service_id;
  if (!serviceName.value && buildDefaults.service_name) serviceName.value = buildDefaults.service_name;
  if (!bundlePath.value && buildDefaults.bundle_path) bundlePath.value = buildDefaults.bundle_path;
  if (!analyzerPath.value && buildDefaults.analyzer_bundle_path) analyzerPath.value = buildDefaults.analyzer_bundle_path;

  if (!outDir.placeholder && buildDefaults.out_dir) outDir.placeholder = buildDefaults.out_dir;
  if (!manifestPath.placeholder && buildDefaults.manifest_path) manifestPath.placeholder = buildDefaults.manifest_path;
  if (!bundlePath.placeholder && buildDefaults.bundle_path) bundlePath.placeholder = buildDefaults.bundle_path;
  if (!analyzerPath.placeholder && buildDefaults.analyzer_bundle_path) analyzerPath.placeholder = buildDefaults.analyzer_bundle_path;
}

async function compareGraphs() {
  const fromGraphID = compareFrom.value;
  const toGraphID = compareTo.value;
  if (!fromGraphID || !toGraphID) {
    compareSummary.textContent = 'Select both graphs to compare.';
    compareResults.textContent = '';
    return;
  }
  const query = graphQuery();
  try {
    const payload = await fetchJSON(`/graphs/compare${query}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        from_graph_id: fromGraphID,
        to_graph_id: toGraphID,
      }),
    });
    state.lastCompare = payload;
    renderCompare(payload);
    await refreshCompareHistory();
  } catch (err) {
    compareSummary.textContent = `Compare failed: ${err.message}`;
    compareResults.textContent = '';
    state.lastCompare = null;
  }
}

async function loadSelectedCompare() {
  const compareID = compareHistory.value;
  if (!compareID) {
    compareSummary.textContent = 'No compare history selected.';
    return;
  }
  const payload = await fetchJSON(`/graphs/compare/${encodeURIComponent(compareID)}`);
  state.lastCompare = payload;
  renderCompare(payload);
}

async function deleteSelectedCompare() {
  const compareID = compareHistory.value;
  if (!compareID) {
    compareSummary.textContent = 'No compare history selected.';
    return;
  }
  await fetchJSON(`/graphs/compare/${encodeURIComponent(compareID)}`, {
    method: 'DELETE',
  });
  state.lastCompare = null;
  compareSummary.textContent = `Deleted compare ${compareID}`;
  compareResults.textContent = '';
  await refreshCompareHistory();
}

async function pruneCompareHistory(keepLatest = 20) {
  const payload = await fetchJSON(`/graphs/compare?keep_latest=${encodeURIComponent(String(keepLatest))}`, {
    method: 'DELETE',
  });
  compareSummary.textContent = `Pruned compare history. Deleted ${payload.deleted || 0}, kept latest ${keepLatest}.`;
  compareResults.textContent = '';
  state.lastCompare = null;
  await refreshCompareHistory();
}

function wireEvents() {
  document.getElementById('refreshBtn').addEventListener('click', () => {
    loadGraphsList().catch((err) => {
      summary.textContent = `Failed loading graphs: ${err.message}`;
    });
  });
  document.getElementById('loadGraphBtn').addEventListener('click', () => {
    loadGraph().catch((err) => {
      summary.textContent = `Failed loading graph: ${err.message}`;
    });
  });
  document.getElementById('exportGraphBtn').addEventListener('click', () => {
    if (!state.graph) {
      details.textContent = 'No graph loaded to export.';
      return;
    }
    const data = JSON.stringify(state.graph, null, 2);
    const blob = new Blob([data], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `filtered_graph_${state.graph.graph_id || 'unknown'}.json`;
    a.click();
    URL.revokeObjectURL(url);
  });
  document.getElementById('clearServiceFilterBtn').addEventListener('click', () => {
    serviceFilter.value = '';
    repoFilter.value = '';
    confidenceMin.value = '0';
    confidenceMinLabel.textContent = '0.00';
  });
  document.getElementById('buildForm').addEventListener('submit', buildGraph);
  document.getElementById('compareBtn').addEventListener('click', () => {
    compareGraphs().catch((err) => {
      compareSummary.textContent = `Compare failed: ${err.message}`;
    });
  });
  document.getElementById('refreshCompareHistoryBtn').addEventListener('click', () => {
    refreshCompareHistory(false).catch((err) => {
      compareSummary.textContent = `Compare history refresh failed: ${err.message}`;
    });
  });
  document.getElementById('loadCompareHistoryBtn').addEventListener('click', () => {
    loadSelectedCompare().catch((err) => {
      compareSummary.textContent = `Load compare failed: ${err.message}`;
    });
  });
  document.getElementById('deleteCompareHistoryBtn').addEventListener('click', () => {
    deleteSelectedCompare().catch((err) => {
      compareSummary.textContent = `Delete compare failed: ${err.message}`;
    });
  });
  document.getElementById('loadMoreCompareHistoryBtn').addEventListener('click', () => {
    refreshCompareHistory(true).catch((err) => {
      compareSummary.textContent = `Compare history load more failed: ${err.message}`;
    });
  });
  document.getElementById('pruneCompareHistoryBtn').addEventListener('click', () => {
    pruneCompareHistory(20).catch((err) => {
      compareSummary.textContent = `Compare prune failed: ${err.message}`;
    });
  });
  document.getElementById('exportCompareBtn').addEventListener('click', () => {
    if (!state.lastCompare) {
      compareSummary.textContent = 'Run compare first, then export.';
      return;
    }
    const data = JSON.stringify(state.lastCompare, null, 2);
    const blob = new Blob([data], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `compare_result_${state.lastCompare.from_graph_id || 'from'}_${state.lastCompare.to_graph_id || 'to'}.json`;
    a.click();
    URL.revokeObjectURL(url);
  });
  graphSelect.addEventListener('change', () => {
    loadGraph().catch((err) => {
      summary.textContent = `Failed loading graph: ${err.message}`;
    });
  });
  layoutMode.addEventListener('change', () => {
    state.layout = layoutMode.value;
    renderSVG();
  });
  confidenceMin.addEventListener('input', () => {
    confidenceMinLabel.textContent = Number(confidenceMin.value || '0').toFixed(2);
  });
  document.getElementById('zoomInBtn').addEventListener('click', () => setScale(state.viewport.scale * 1.15));
  document.getElementById('zoomOutBtn').addEventListener('click', () => setScale(state.viewport.scale / 1.15));
  document.getElementById('fitBtn').addEventListener('click', fitView);
  document.getElementById('clearFocusBtn').addEventListener('click', () => {
    state.selectedNodeId = '';
    state.selectedEdgeId = '';
    details.textContent = 'Focus cleared.';
    renderEdges();
    renderSVG();
  });
  graphSvg.addEventListener('wheel', (evt) => {
    evt.preventDefault();
    const delta = evt.deltaY < 0 ? 1.12 : 0.9;
    setScale(state.viewport.scale * delta);
  }, { passive: false });
  graphSvg.addEventListener('mousedown', (evt) => {
    state.dragging = true;
    state.dragStart = svgPoint(evt);
    graphSvg.classList.add('dragging');
  });
  window.addEventListener('mouseup', () => {
    state.dragging = false;
    state.dragStart = null;
    graphSvg.classList.remove('dragging');
  });
  window.addEventListener('mousemove', (evt) => {
    if (!state.dragging || !state.dragStart) return;
    const p = svgPoint(evt);
    state.viewport.tx += p.x - state.dragStart.x;
    state.viewport.ty += p.y - state.dragStart.y;
    state.dragStart = p;
    renderSVG();
  });
}

async function bootstrap() {
  wireEvents();
  confidenceMinLabel.textContent = Number(confidenceMin.value || '0').toFixed(2);
  try {
    await loadDefaults();
  } catch (err) {
    buildStatus.textContent = `Defaults unavailable: ${err.message}`;
  }
  try {
    await loadGraphsList();
  } catch (err) {
    summary.textContent = `Failed loading graphs list: ${err.message}`;
  }
}

bootstrap();
