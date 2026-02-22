const DEFAULT_AUTH = {
  tenant: 'default',
  principal: 'ui-user',
  roles: 'graph-reader',
  scopes: 'graph:read',
};

function readAuthValue(key, fallback) {
  const raw = localStorage.getItem(key);
  const val = raw == null ? '' : String(raw).trim();
  return val || fallback;
}

const state = {
  index: null,
  rawGraph: null,
  advancedRawGraph: null,
  advancedGraph: null,
  graph: null,
  selectedNodeId: '',
  viewport: { scale: 1, tx: 0, ty: 0 },
  dragging: false,
  dragStart: null,
  dragMoved: false,
  suppressBackgroundClick: false,
  hopDepth: null,
  interacting: false,
  interactionTimer: null,
  nodeTypeVisible: {},
  edgeTypeVisible: {},
  lastPos: new Map(),
  flowOverlayCache: null,
  auth: {
    tenant: readAuthValue('diffmind_auth_tenant', DEFAULT_AUTH.tenant),
    principal: readAuthValue('diffmind_auth_principal', DEFAULT_AUTH.principal),
    roles: readAuthValue('diffmind_auth_roles', DEFAULT_AUTH.roles),
    scopes: readAuthValue('diffmind_auth_scopes', DEFAULT_AUTH.scopes),
  },
};

const graphSelect = document.getElementById('graphSelect');
const nodeSearchInput = document.getElementById('nodeSearchInput');
const refreshBtn = document.getElementById('refreshBtn');
const resetViewBtn = document.getElementById('resetViewBtn');
const hopDepthSelect = document.getElementById('hopDepthSelect');
const nodeTypeFilters = document.getElementById('nodeTypeFilters');
const edgeTypeFilters = document.getElementById('edgeTypeFilters');
const graphSvg = document.getElementById('graphSvg');
const viewport = document.getElementById('viewport');
const selectionDetails = document.getElementById('selectionDetails');
const summary = document.getElementById('summary');

const CANVAS = { width: 1600, height: 900 };
const COL = { exposureX: 240, logicX: 800, dependencyX: 1360 };
const PATH_COLORS = ['#2563eb', '#ef4444', '#059669', '#f59e0b', '#9333ea', '#0ea5e9', '#22c55e', '#f97316'];
const FLOW_EDGE_TYPES = new Set([
  'exposure_reaches_dependency',
  'exposure_invokes_function',
  'function_calls_function',
  'function_calls_dependency',
]);

function authHeaders() {
  return {
    'X-DiffMind-Tenant': state.auth.tenant,
    'X-DiffMind-Principal': state.auth.principal,
    'X-DiffMind-User': state.auth.principal,
    'X-DiffMind-Roles': state.auth.roles,
    'X-DiffMind-Scopes': state.auth.scopes,
  };
}

function ensureAuthDefaults(force = false) {
  let changed = false;
  for (const [field, fallback] of Object.entries(DEFAULT_AUTH)) {
    const val = String((state.auth && state.auth[field]) || '').trim();
    if (!force && val) continue;
    state.auth[field] = fallback;
    localStorage.setItem(`diffmind_auth_${field}`, fallback);
    changed = true;
  }
  return changed;
}

async function fetchJSON(url, retried = false) {
  const res = await fetch(url, { headers: authHeaders() });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    if (res.status === 401 && !retried) {
      ensureAuthDefaults(true);
      return fetchJSON(url, true);
    }
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

function clipText(v, n = 80) {
  const s = String(v || '');
  return s.length <= n ? s : `${s.slice(0, n - 1)}...`;
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
  return String((node && node.type) || '').toLowerCase();
}

function edgeType(edge) {
  return String((edge && edge.type) || '').toLowerCase();
}

function nodeLabel(node) {
  if (!node) return '';
  return String(node.label || node.id || '').trim() || node.id;
}

function isExposure(node) {
  return nodeType(node) === 'endpoint';
}

function isDependency(node) {
  const t = nodeType(node);
  return t === 'dependency_operation' || t === 'database' || t === 'queue' || t === 'topic' || t === 'table';
}

function isLogic(node) {
  const t = nodeType(node);
  return t === 'function' || t === 'method' || t === 'class' || t === 'interface' || t === 'code_symbol' || t === 'code_function';
}

function laneForNode(node) {
  if (isExposure(node)) return 'exposure';
  if (isDependency(node)) return 'dependency';
  return 'logic';
}

function edgeKey(e) {
  return `${e.source_id}|${e.type}|${e.target_id}`;
}

function isFlowEdge(e) {
  return FLOW_EDGE_TYPES.has(edgeType(e));
}

function parseIntSafe(v, fallback) {
  const n = Number.parseInt(v, 10);
  return Number.isFinite(n) ? n : fallback;
}

function nodeFile(node) {
  const attrs = (node && node.attributes && typeof node.attributes === 'object') ? node.attributes : {};
  const file = String(attrs.file || '').trim();
  return file;
}

function isCodebaseLogic(node) {
  if (!isLogic(node)) return false;
  const file = nodeFile(node);
  return !!file && file !== '<nil>';
}

function nodeOrderHint(node) {
  if (!node) return 1e9;
  const attrs = (node.attributes && typeof node.attributes === 'object') ? node.attributes : {};
  if (typeof attrs.call_index === 'number') return attrs.call_index;
  if (typeof attrs.order === 'number') return attrs.order;
  if (typeof attrs.ordinal === 'number') return attrs.ordinal;
  const line = parseIntSafe(attrs.line, 1e9);
  const col = parseIntSafe(attrs.col, 1e6);
  return line * 1000 + col;
}

function transitionOrderHint(tr, nodeById) {
  const edgeAttrs = (tr.edge && tr.edge.attributes && typeof tr.edge.attributes === 'object') ? tr.edge.attributes : {};
  if (typeof edgeAttrs.call_index === 'number') return edgeAttrs.call_index;
  if (typeof edgeAttrs.order === 'number') return edgeAttrs.order;
  if (typeof edgeAttrs.ordinal === 'number') return edgeAttrs.ordinal;
  return nodeOrderHint(nodeById.get(tr.to));
}

function buildFocusedGraph(raw) {
  const nodes = Array.isArray(raw.nodes) ? raw.nodes : [];
  const edges = Array.isArray(raw.edges) ? raw.edges : [];
  const nodeById = new Map(nodes.map((n) => [n.id, n]));

  const focusedEdges = edges.filter((e) => {
    if (edgeType(e) !== 'exposure_reaches_dependency') return false;
    const s = nodeById.get(e.source_id);
    const t = nodeById.get(e.target_id);
    return !!s && !!t && isExposure(s) && isDependency(t);
  });

  const keep = new Set();
  focusedEdges.forEach((e) => {
    keep.add(e.source_id);
    keep.add(e.target_id);
  });

  const focusedNodes = nodes.filter((n) => keep.has(n.id) || isExposure(n));
  if (focusedNodes.length > 0) {
    return { nodes: focusedNodes, edges: focusedEdges };
  }

  const advanced = buildAdvancedGraph(raw);
  if ((advanced.nodes || []).length > 0) {
    return advanced;
  }

  const cappedNodes = nodes.slice(0, 1800);
  const allowed = new Set(cappedNodes.map((n) => n.id));
  const cappedEdges = edges.filter((e) => allowed.has(e.source_id) && allowed.has(e.target_id)).slice(0, 5000);
  return { nodes: cappedNodes, edges: cappedEdges };
}

function buildAdvancedGraph(raw) {
  const nodes = Array.isArray(raw.nodes) ? raw.nodes : [];
  const edges = Array.isArray(raw.edges) ? raw.edges : [];
  const nodeById = new Map(nodes.map((n) => [n.id, n]));

  const advancedEdges = edges.filter((e) => {
    const t = edgeType(e);
    if (!FLOW_EDGE_TYPES.has(t)) return false;
    const s = nodeById.get(e.source_id);
    const d = nodeById.get(e.target_id);
    if (!s || !d) return false;

    if (t === 'exposure_reaches_dependency') return isExposure(s) && isDependency(d);
    if (t === 'exposure_invokes_function') return isExposure(s) && isLogic(d);
    if (t === 'function_calls_function') return isLogic(s) && isLogic(d);
    if (t === 'function_calls_dependency') return isLogic(s) && isDependency(d);
    return false;
  });

  const keep = new Set();
  advancedEdges.forEach((e) => {
    keep.add(e.source_id);
    keep.add(e.target_id);
  });

  const advancedNodes = nodes.filter((n) => keep.has(n.id) || isExposure(n));
  return { nodes: advancedNodes, edges: advancedEdges };
}

function flowGraph() {
  return state.advancedGraph || state.graph || { nodes: [], edges: [] };
}

function buildAdjacency(graph, reverse = false) {
  const byNode = new Map();
  for (const e of graph.edges || []) {
    if (!isFlowEdge(e)) continue;
    const from = reverse ? e.target_id : e.source_id;
    const to = reverse ? e.source_id : e.target_id;
    if (!byNode.has(from)) byNode.set(from, []);
    byNode.get(from).push({ to, edge: e });
  }
  return byNode;
}

function beginInteraction() {
  state.interacting = true;
  if (state.interactionTimer) {
    clearTimeout(state.interactionTimer);
    state.interactionTimer = null;
  }
  renderGraph();
}

function endInteractionSoon() {
  if (state.interactionTimer) clearTimeout(state.interactionTimer);
  state.interactionTimer = setTimeout(() => {
    state.interacting = false;
    state.interactionTimer = null;
    renderGraph();
  }, 140);
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

function selectedReachability(graph, selectedID) {
  if (!selectedID) return { nodes: new Set(), edges: new Set() };
  const nodes = new Set([selectedID]);
  const edgeKeys = new Set();
  const q = [{ id: selectedID, d: 0 }];
  const seen = new Set([selectedID]);
  const maxDepth = 3;

  while (q.length > 0) {
    const cur = q.shift();
    if (cur.d >= maxDepth) continue;
    for (const e of graph.edges) {
      if (e.source_id === cur.id) {
        nodes.add(e.target_id);
        edgeKeys.add(edgeKey(e));
        if (!seen.has(e.target_id)) {
          seen.add(e.target_id);
          q.push({ id: e.target_id, d: cur.d + 1 });
        }
      } else if (e.target_id === cur.id) {
        nodes.add(e.source_id);
        edgeKeys.add(edgeKey(e));
        if (!seen.has(e.source_id)) {
          seen.add(e.source_id);
          q.push({ id: e.source_id, d: cur.d + 1 });
        }
      }
    }
  }

  const edges = new Set();
  for (let i = 0; i < graph.edges.length; i += 1) {
    if (edgeKeys.has(edgeKey(graph.edges[i]))) edges.add(i);
  }
  return { nodes, edges };
}

function nodesWithinHops(graph, startID, hops) {
  if (!startID || hops == null) return null;
  const visited = new Set();
  const q = [{ id: startID, d: 0 }];

  while (q.length > 0) {
    const cur = q.shift();
    if (visited.has(cur.id)) continue;
    visited.add(cur.id);
    if (cur.d >= hops) continue;

    for (const e of graph.edges) {
      if (e.source_id === cur.id && !visited.has(e.target_id)) q.push({ id: e.target_id, d: cur.d + 1 });
      if (e.target_id === cur.id && !visited.has(e.source_id)) q.push({ id: e.source_id, d: cur.d + 1 });
    }
  }

  return visited;
}

function filteredGraph() {
  const graph = state.graph;
  if (!graph) return { nodes: [], edges: [] };

  const nodes = graph.nodes.filter((n) => state.nodeTypeVisible[nodeType(n)] !== false);
  const nodeIDs = new Set(nodes.map((n) => n.id));

  let edges = graph.edges.filter((e) => {
    if (state.edgeTypeVisible[edgeType(e)] === false) return false;
    return nodeIDs.has(e.source_id) && nodeIDs.has(e.target_id);
  });

  if (state.hopDepth != null && state.selectedNodeId) {
    const tmp = { nodes, edges };
    const inRange = nodesWithinHops(tmp, state.selectedNodeId, state.hopDepth);
    if (inRange && inRange.size > 0) {
      const scopedNodes = nodes.filter((n) => inRange.has(n.id));
      const scopedIDs = new Set(scopedNodes.map((n) => n.id));
      const scopedEdges = edges.filter((e) => scopedIDs.has(e.source_id) && scopedIDs.has(e.target_id));
      return { nodes: scopedNodes, edges: scopedEdges };
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

  return lines;
}

function extractDependencyDetails(node) {
  const attrs = node && node.attributes && typeof node.attributes === 'object' ? node.attributes : {};
  const lines = [];
  const add = (k, v) => {
    if (v === undefined || v === null || v === '') return;
    lines.push(`${k}: ${String(v)}`);
  };
  add('Protocol', attrs.protocol);
  add('Method', attrs.method || attrs.operation_kind);
  add('Target', attrs.target || attrs.operation || nodeLabel(node));
  add('Repository', attrs.repository);
  add('Operation', attrs.operation);
  return lines;
}

function extractLogicDetails(node) {
  const attrs = node && node.attributes && typeof node.attributes === 'object' ? node.attributes : {};
  const lines = [];
  if (attrs.symbol_name) lines.push(`Symbol: ${attrs.symbol_name}`);
  if (attrs.file && attrs.file !== '<nil>') {
    const line = attrs.line ? `:${attrs.line}` : '';
    lines.push(`File: ${attrs.file}${line}`);
  }
  return lines;
}

function nodeDetailsForFlow(node, isRoot) {
  if (!node) return [];
  if (isExposure(node)) {
    const inLines = extractInputDetails(node);
    return isRoot ? inLines.slice(0, 5) : inLines.slice(0, 1);
  }
  if (isDependency(node)) {
    return extractDependencyDetails(node).slice(0, isRoot ? 4 : 2);
  }
  return extractLogicDetails(node).slice(0, isRoot ? 3 : 1);
}

function buildFlowOverlayModel(selectedNode) {
  const graph = flowGraph();
  const nodeById = new Map((graph.nodes || []).map((n) => [n.id, n]));
  if (!selectedNode || !nodeById.has(selectedNode.id)) return null;

  const direction = isDependency(selectedNode) ? 'reverse' : 'forward';
  const reverse = direction === 'reverse';
  const adj = buildAdjacency(graph, reverse);
  const rootKey = 'r';

  const instances = new Map();
  const childrenByKey = new Map();

  const MAX_DEPTH = 24;
  const MAX_INSTANCES = 2800;
  const MAX_TRANSITIONS = 22000;

  let truncated = false;
  let droppedCycles = 0;
  let droppedLibraries = 0;
  let transitions = 0;

  const root = {
    key: rootKey,
    nodeID: selectedNode.id,
    depth: 0,
    parentKey: '',
    edge: null,
    loop: false,
    branchChildren: 0,
  };
  instances.set(rootKey, root);

  const queue = [{ key: rootKey, nodeID: selectedNode.id, depth: 0, path: [selectedNode.id] }];

  while (queue.length > 0) {
    const cur = queue.shift();
    const curInst = instances.get(cur.key);
    if (!curInst) continue;

    if (cur.depth >= MAX_DEPTH) {
      childrenByKey.set(cur.key, []);
      continue;
    }

    const curNode = nodeById.get(cur.nodeID);
    if (!curNode) {
      childrenByKey.set(cur.key, []);
      continue;
    }

    if ((direction === 'forward' && isDependency(curNode)) || (direction === 'reverse' && isExposure(curNode))) {
      childrenByKey.set(cur.key, []);
      continue;
    }

    let next = (adj.get(cur.nodeID) || []).filter((tr) => {
      const target = nodeById.get(tr.to);
      if (!target) return false;
      if (isLogic(target) && !isCodebaseLogic(target)) {
        droppedLibraries += 1;
        return false;
      }
      return true;
    });

    next.sort((a, b) => {
      const oa = transitionOrderHint(a, nodeById);
      const ob = transitionOrderHint(b, nodeById);
      if (oa !== ob) return oa - ob;
      return nodeLabel(nodeById.get(a.to)).localeCompare(nodeLabel(nodeById.get(b.to)));
    });

    const childKeys = [];

    for (let idx = 0; idx < next.length; idx += 1) {
      if (instances.size >= MAX_INSTANCES || transitions >= MAX_TRANSITIONS) {
        truncated = true;
        break;
      }
      transitions += 1;

      const tr = next[idx];
      const targetID = tr.to;
      if (cur.path.includes(targetID)) {
        droppedCycles += 1;
        curInst.loop = true;
        continue;
      }

      const childKey = `${cur.key}.${idx}`;
      childKeys.push(childKey);

      const childInst = {
        key: childKey,
        nodeID: targetID,
        depth: cur.depth + 1,
        parentKey: cur.key,
        edge: tr.edge,
        loop: false,
        branchChildren: 0,
      };
      instances.set(childKey, childInst);

      queue.push({ key: childKey, nodeID: targetID, depth: cur.depth + 1, path: [...cur.path, targetID] });
    }

    curInst.branchChildren = childKeys.length;
    childrenByKey.set(cur.key, childKeys);

    if (truncated) break;
  }

  for (const inst of instances.values()) {
    if (!childrenByKey.has(inst.key)) childrenByKey.set(inst.key, []);
  }

  let leafCount = 0;
  for (const inst of instances.values()) {
    const children = childrenByKey.get(inst.key) || [];
    if (children.length > 0) continue;
    const node = nodeById.get(inst.nodeID);
    if (!node) continue;
    if (direction === 'forward' && isDependency(node)) {
      leafCount += 1;
      continue;
    }
    if (direction === 'reverse' && isExposure(node)) {
      leafCount += 1;
      continue;
    }
    if (!isExposure(node) && !isDependency(node)) leafCount += 1;
  }

  return {
    direction,
    rootKey,
    nodeById,
    instances,
    childrenByKey,
    truncated,
    droppedCycles,
    droppedLibraries,
    leafCount,
  };
}

function layoutFlowOverlay(model, selectedPos) {
  const dirSign = model.direction === 'reverse' ? -1 : 1;
  const columnGap = 228;
  const rowGap = 64;
  const xOffset = 130;

  const yByKey = new Map();
  let nextY = selectedPos.y;

  const assignY = (key) => {
    if (yByKey.has(key)) return yByKey.get(key);
    const children = model.childrenByKey.get(key) || [];
    if (children.length === 0) {
      const y = nextY;
      nextY += rowGap;
      yByKey.set(key, y);
      return y;
    }
    const ys = children.map(assignY);
    const y = ys.reduce((acc, v) => acc + v, 0) / ys.length;
    yByKey.set(key, y);
    return y;
  };

  assignY(model.rootKey);

  const rootY = yByKey.get(model.rootKey) || selectedPos.y;
  const deltaY = selectedPos.y - rootY;

  const boxes = new Map();
  for (const inst of model.instances.values()) {
    const node = model.nodeById.get(inst.nodeID);
    const baseTitle = clipText(nodeLabel(node), 82);
    const extraLines = nodeDetailsForFlow(node, inst.key === model.rootKey);
    const lines = [baseTitle, ...extraLines.map((line) => clipText(line, 92))];

    const maxChars = lines.reduce((acc, line) => Math.max(acc, String(line).length), 18);
    const width = Math.min(420, Math.max(180, Math.round(maxChars * 6.2 + 24)));
    const lineHeight = 13;
    const height = Math.max(36, 12 + lines.length * lineHeight + 10);

    const x = selectedPos.x + dirSign * (xOffset + inst.depth * columnGap);
    const y = (yByKey.get(inst.key) || selectedPos.y) + deltaY;

    boxes.set(inst.key, {
      x,
      y,
      w: width,
      h: height,
      lines,
      node,
      inst,
    });
  }

  return { boxes, dirSign };
}

function renderFlowOverlay(selectedNode, pos) {
  if (!selectedNode || !pos) return null;

  let model = null;
  if (state.flowOverlayCache && state.flowOverlayCache.nodeID === selectedNode.id) {
    model = state.flowOverlayCache.model;
  } else {
    model = buildFlowOverlayModel(selectedNode);
    state.flowOverlayCache = { nodeID: selectedNode.id, model };
  }
  if (!model) return null;

  const layout = layoutFlowOverlay(model, pos.get(selectedNode.id));
  const g = document.createElementNS('http://www.w3.org/2000/svg', 'g');
  g.setAttribute('data-flow-overlay', 'true');

  const mk = (name) => document.createElementNS('http://www.w3.org/2000/svg', name);

  const summaryBox = mk('rect');
  const summaryX = layout.dirSign > 0 ? 470 : 820;
  summaryBox.setAttribute('x', String(summaryX));
  summaryBox.setAttribute('y', '10');
  summaryBox.setAttribute('width', '300');
  summaryBox.setAttribute('height', '54');
  summaryBox.setAttribute('rx', '8');
  summaryBox.setAttribute('fill', '#f8fafc');
  summaryBox.setAttribute('stroke', '#cbd5e1');
  g.appendChild(summaryBox);

  const summaryText = mk('text');
  summaryText.setAttribute('x', String(summaryX + 10));
  summaryText.setAttribute('y', '30');
  summaryText.setAttribute('font-size', '11');
  summaryText.setAttribute('fill', '#0f172a');
  summaryText.textContent = `flow nodes=${model.instances.size} leaves=${model.leafCount} cycles=${model.droppedCycles} hidden_lib_calls=${model.droppedLibraries}`;
  g.appendChild(summaryText);

  if (model.truncated) {
    const warn = mk('text');
    warn.setAttribute('x', String(summaryX + 10));
    warn.setAttribute('y', '46');
    warn.setAttribute('font-size', '11');
    warn.setAttribute('fill', '#b91c1c');
    warn.textContent = 'flow view truncated for safety at high graph size';
    g.appendChild(warn);
  } else {
    const note = mk('text');
    note.setAttribute('x', String(summaryX + 10));
    note.setAttribute('y', '46');
    note.setAttribute('font-size', '11');
    note.setAttribute('fill', '#334155');
    note.textContent = 'branch blocks represent condition/path fan-out; loop badge means cycle edge detected';
    g.appendChild(note);
  }

  for (const inst of model.instances.values()) {
    const children = model.childrenByKey.get(inst.key) || [];
    if (children.length <= 1) continue;

    const childBoxes = children.map((k) => layout.boxes.get(k)).filter(Boolean);
    if (childBoxes.length <= 1) continue;

    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;

    for (const box of childBoxes) {
      minX = Math.min(minX, box.x - box.w / 2);
      maxX = Math.max(maxX, box.x + box.w / 2);
      minY = Math.min(minY, box.y - box.h / 2);
      maxY = Math.max(maxY, box.y + box.h / 2);
    }

    const padX = 14;
    const padY = 10;
    const rect = mk('rect');
    rect.setAttribute('x', String(minX - padX));
    rect.setAttribute('y', String(minY - padY));
    rect.setAttribute('width', String(maxX - minX + padX * 2));
    rect.setAttribute('height', String(maxY - minY + padY * 2));
    rect.setAttribute('rx', '10');
    rect.setAttribute('fill', '#fffbeb');
    rect.setAttribute('fill-opacity', '0.7');
    rect.setAttribute('stroke', '#f59e0b');
    rect.setAttribute('stroke-width', '1.2');
    rect.setAttribute('stroke-dasharray', '4 4');
    g.appendChild(rect);

    const label = mk('text');
    label.setAttribute('x', String(minX - padX + 8));
    label.setAttribute('y', String(minY - padY + 14));
    label.setAttribute('font-size', '10');
    label.setAttribute('font-weight', '700');
    label.setAttribute('fill', '#92400e');
    const parentLoop = inst.loop ? ' + loop' : '';
    label.textContent = `branch (${children.length} paths${parentLoop})`;
    g.appendChild(label);
  }

  const selectedNodePos = pos.get(selectedNode.id);
  const rootBox = layout.boxes.get(model.rootKey);
  if (selectedNodePos && rootBox) {
    const line = mk('line');
    line.setAttribute('x1', String(selectedNodePos.x));
    line.setAttribute('y1', String(selectedNodePos.y));
    line.setAttribute('x2', String(layout.dirSign > 0 ? rootBox.x - rootBox.w / 2 : rootBox.x + rootBox.w / 2));
    line.setAttribute('y2', String(rootBox.y));
    line.setAttribute('stroke', '#0f172a');
    line.setAttribute('stroke-width', '2.2');
    line.setAttribute('stroke-opacity', '0.9');
    g.appendChild(line);
  }

  for (const inst of model.instances.values()) {
    if (inst.key === model.rootKey) continue;
    const parent = layout.boxes.get(inst.parentKey);
    const child = layout.boxes.get(inst.key);
    if (!parent || !child) continue;

    const x1 = layout.dirSign > 0 ? parent.x + parent.w / 2 : parent.x - parent.w / 2;
    const y1 = parent.y;
    const x2 = layout.dirSign > 0 ? child.x - child.w / 2 : child.x + child.w / 2;
    const y2 = child.y;
    const c = (Math.abs(x2 - x1) * 0.45) * layout.dirSign;

    const path = mk('path');
    path.setAttribute('d', `M ${x1} ${y1} C ${x1 + c} ${y1}, ${x2 - c} ${y2}, ${x2} ${y2}`);
    path.setAttribute('fill', 'none');

    const siblings = model.childrenByKey.get(inst.parentKey) || [];
    const childIdx = Math.max(0, siblings.indexOf(inst.key));
    path.setAttribute('stroke', siblings.length > 1 ? PATH_COLORS[childIdx % PATH_COLORS.length] : '#0f172a');
    path.setAttribute('stroke-opacity', '0.86');
    path.setAttribute('stroke-width', siblings.length > 1 ? '1.7' : '1.5');
    g.appendChild(path);

    if (siblings.length > 1) {
      const edgeAttrs = (inst.edge && inst.edge.attributes && typeof inst.edge.attributes === 'object') ? inst.edge.attributes : {};
      const branchLabel = String(edgeAttrs.condition || edgeAttrs.branch || edgeAttrs.case || `path ${childIdx + 1}`);
      const mid = mk('text');
      mid.setAttribute('x', String((x1 + x2) / 2));
      mid.setAttribute('y', String((y1 + y2) / 2 - 4));
      mid.setAttribute('font-size', '9');
      mid.setAttribute('text-anchor', 'middle');
      mid.setAttribute('fill', '#475569');
      mid.textContent = clipText(branchLabel, 22);
      g.appendChild(mid);
    }
  }

  for (const inst of model.instances.values()) {
    const box = layout.boxes.get(inst.key);
    if (!box) continue;

    let fill = '#e2e8f0';
    let stroke = '#475569';
    if (isExposure(box.node)) {
      fill = '#dbeafe';
      stroke = '#1d4ed8';
    } else if (isDependency(box.node)) {
      fill = '#f3e8ff';
      stroke = '#9333ea';
    }

    const rect = mk('rect');
    rect.setAttribute('x', String(box.x - box.w / 2));
    rect.setAttribute('y', String(box.y - box.h / 2));
    rect.setAttribute('width', String(box.w));
    rect.setAttribute('height', String(box.h));
    rect.setAttribute('rx', '9');
    rect.setAttribute('fill', fill);
    rect.setAttribute('fill-opacity', inst.key === model.rootKey ? '1' : '0.95');
    rect.setAttribute('stroke', inst.key === model.rootKey ? '#ef4444' : stroke);
    rect.setAttribute('stroke-width', inst.key === model.rootKey ? '2.2' : '1.4');
    g.appendChild(rect);

    if (inst.loop) {
      const loopBadge = mk('rect');
      loopBadge.setAttribute('x', String(box.x + box.w / 2 - 46));
      loopBadge.setAttribute('y', String(box.y - box.h / 2 + 4));
      loopBadge.setAttribute('width', '38');
      loopBadge.setAttribute('height', '14');
      loopBadge.setAttribute('rx', '7');
      loopBadge.setAttribute('fill', '#fee2e2');
      loopBadge.setAttribute('stroke', '#dc2626');
      g.appendChild(loopBadge);

      const loopText = mk('text');
      loopText.setAttribute('x', String(box.x + box.w / 2 - 27));
      loopText.setAttribute('y', String(box.y - box.h / 2 + 14));
      loopText.setAttribute('font-size', '9');
      loopText.setAttribute('text-anchor', 'middle');
      loopText.setAttribute('font-weight', '700');
      loopText.setAttribute('fill', '#b91c1c');
      loopText.textContent = 'loop';
      g.appendChild(loopText);
    }

    for (let i = 0; i < box.lines.length; i += 1) {
      const txt = mk('text');
      txt.setAttribute('x', String(box.x - box.w / 2 + 8));
      txt.setAttribute('y', String(box.y - box.h / 2 + 16 + i * 13));
      txt.setAttribute('font-size', i === 0 ? '11' : '10');
      txt.setAttribute('font-weight', i === 0 ? '700' : '500');
      txt.setAttribute('fill', '#0f172a');
      txt.textContent = box.lines[i];
      g.appendChild(txt);
    }
  }

  viewport.appendChild(g);
  return model;
}

function layoutGraph(graph) {
  const exposures = graph.nodes.filter(isExposure).sort((a, b) => nodeLabel(a).localeCompare(nodeLabel(b)));
  const logic = graph.nodes
    .filter((n) => !isExposure(n) && !isDependency(n))
    .sort((a, b) => nodeLabel(a).localeCompare(nodeLabel(b)));
  const dependencies = graph.nodes.filter(isDependency).sort((a, b) => nodeLabel(a).localeCompare(nodeLabel(b)));

  const spacingExposure = Math.max(24, Math.floor((CANVAS.height - 130) / Math.max(1, exposures.length)));
  const spacingLogic = Math.max(24, Math.floor((CANVAS.height - 130) / Math.max(1, logic.length)));
  const spacingDependency = Math.max(24, Math.floor((CANVAS.height - 130) / Math.max(1, dependencies.length)));

  const pos = new Map();
  exposures.forEach((n, i) => pos.set(n.id, { x: COL.exposureX, y: 62 + i * spacingExposure }));
  logic.forEach((n, i) => pos.set(n.id, { x: COL.logicX, y: 62 + i * spacingLogic }));
  dependencies.forEach((n, i) => pos.set(n.id, { x: COL.dependencyX, y: 62 + i * spacingDependency }));

  return { exposures, logic, dependencies, pos };
}

function centerOnNode(nodeID) {
  const p = state.lastPos.get(nodeID);
  if (!p) return;
  const sx = state.viewport.scale;
  state.viewport.tx = CANVAS.width / 2 - p.x * sx;
  state.viewport.ty = CANVAS.height / 2 - p.y * sx;
  setTransform();
}

function renderSelection(node, flowModel) {
  const attrs = node.attributes || {};
  const out = {
    id: node.id,
    type: node.type,
    label: nodeLabel(node),
    lane: laneForNode(node),
    attributes: attrs,
  };

  if (flowModel) {
    out.flow = {
      direction: flowModel.direction,
      node_count: flowModel.instances.size,
      leaf_count: flowModel.leafCount,
      cycles: flowModel.droppedCycles,
      hidden_library_calls: flowModel.droppedLibraries,
      truncated: flowModel.truncated,
    };
  }

  selectionDetails.textContent = JSON.stringify(out, null, 2);
}

function renderGraph() {
  clearViewport();
  let graph = filteredGraph();
  if ((!graph || graph.nodes.length === 0) && state.graph && state.graph.nodes.length > 0) {
    for (const t of Object.keys(state.nodeTypeVisible)) state.nodeTypeVisible[t] = true;
    for (const t of Object.keys(state.edgeTypeVisible)) state.edgeTypeVisible[t] = true;
    graph = filteredGraph();
    if (state.graph) renderFilterControls(state.graph);
  }
  if (!graph || graph.nodes.length === 0) {
    summary.textContent = 'No nodes found with the selected filters.';
    selectionDetails.textContent = 'Click a node to see details.';
    return;
  }

  const { exposures, logic, dependencies, pos } = layoutGraph(graph);
  state.lastPos = pos;

  const nodeById = new Map(graph.nodes.map((n) => [n.id, n]));
  const selectedID = state.selectedNodeId;
  const hasSelection = !!selectedID;
  const neighborhood = state.interacting
    ? nodeNeighborhood(graph, selectedID)
    : selectedReachability(graph, selectedID);

  const title = (x, text, color = '#0f172a') => {
    const t = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    t.setAttribute('x', String(x));
    t.setAttribute('y', '28');
    t.setAttribute('font-size', '16');
    t.setAttribute('font-weight', '700');
    t.setAttribute('fill', color);
    t.textContent = text;
    viewport.appendChild(t);
  };

  title(COL.exposureX - 82, 'Exposure', '#1d4ed8');
  title(COL.logicX - 38, 'Logic', '#475569');
  title(COL.dependencyX - 108, 'Dependencies', '#9333ea');

  let edgeStep = 1;
  if (state.interacting && !hasSelection && graph.edges.length > 320) {
    edgeStep = Math.max(1, Math.floor(graph.edges.length / 320));
  }
  if (!hasSelection && graph.edges.length > 2500) {
    edgeStep = Math.max(edgeStep, Math.ceil(graph.edges.length / 2500));
  }

  for (let i = 0; i < graph.edges.length; i += edgeStep) {
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
    line.setAttribute('stroke-opacity', activeEdge ? '0.92' : '0.08');
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
    g.setAttribute('opacity', activeNode ? '1' : '0.1');

    const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
    circle.setAttribute('cx', String(p.x));
    circle.setAttribute('cy', String(p.y));
    circle.setAttribute('r', isSelected ? '7' : '5');
    circle.setAttribute('fill', isSelected ? '#ef4444' : color);
    g.appendChild(circle);

    const text = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    const lane = laneForNode(node);
    const anchor = lane === 'exposure' ? 'end' : lane === 'dependency' ? 'start' : 'middle';
    const textX = lane === 'exposure' ? p.x - 14 : lane === 'dependency' ? p.x + 14 : p.x;
    text.setAttribute('x', String(textX));
    text.setAttribute('y', String(p.y + 4));
    text.setAttribute('font-size', '11');
    text.setAttribute('fill', activeNode ? '#0f172a' : '#64748b');
    text.setAttribute('text-anchor', anchor);
    text.textContent = nodeLabel(node);
    g.appendChild(text);

    g.addEventListener('click', (ev) => {
      ev.stopPropagation();
      state.selectedNodeId = node.id;
      state.flowOverlayCache = null;
      renderGraph();
      centerOnNode(node.id);
    });

    viewport.appendChild(g);

    if (isSelected && isExposure(node)) {
      const lines = extractInputDetails(node);
      if (lines.length > 0) {
        const panel = document.createElementNS('http://www.w3.org/2000/svg', 'g');
        const boxX = p.x + 18;
        const boxY = p.y - 12;
        const lineHeight = 13;
        const boxWidth = 400;
        const shown = lines.slice(0, 8);
        const boxHeight = Math.max(22, 10 + shown.length * lineHeight);

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

        for (let i = 0; i < shown.length; i += 1) {
          const row = document.createElementNS('http://www.w3.org/2000/svg', 'text');
          row.setAttribute('x', String(boxX + 8));
          row.setAttribute('y', String(boxY + 16 + i * lineHeight));
          row.setAttribute('font-size', '11');
          row.setAttribute('fill', '#111827');
          row.setAttribute('text-anchor', 'start');
          row.textContent = shown[i];
          panel.appendChild(row);
        }
        viewport.appendChild(panel);
      }
    }
  };

  exposures.forEach((n) => drawNode(n, '#1d4ed8'));
  logic.forEach((n) => drawNode(n, '#64748b'));
  dependencies.forEach((n) => drawNode(n, '#9333ea'));

  let selectedNode = null;
  let flowModel = null;
  if (state.selectedNodeId) {
    selectedNode = nodeById.get(state.selectedNodeId);
    if (selectedNode) {
      flowModel = renderFlowOverlay(selectedNode, pos);
      renderSelection(selectedNode, flowModel);
    }
  }

  if (!selectedNode) {
    selectionDetails.textContent = 'Click a node to see details.';
  }

  summary.innerHTML = `
    <div><strong>Exposures:</strong> ${exposures.length}</div>
    <div><strong>Logic:</strong> ${logic.length}</div>
    <div><strong>Dependencies:</strong> ${dependencies.length}</div>
    <div><strong>Links:</strong> ${graph.edges.length}${edgeStep > 1 ? ` (thinned x${edgeStep} while moving)` : ''}</div>
    <div><strong>Selection:</strong> ${selectedNode ? escapeHTML(nodeLabel(selectedNode)) : 'none'}</div>
    <div><strong>Branch Meaning:</strong> block around sibling nodes means conditional or parallel fan-out from the parent call.</div>
  `;
}

function ensureFilterDefaults(graph) {
  const nodeTypes = new Set(graph.nodes.map((n) => nodeType(n)));
  const edgeTypes = new Set(graph.edges.map((e) => edgeType(e)));

  for (const t of nodeTypes) {
    if (!(t in state.nodeTypeVisible)) state.nodeTypeVisible[t] = true;
  }
  for (const t of Object.keys(state.nodeTypeVisible)) {
    if (!nodeTypes.has(t)) delete state.nodeTypeVisible[t];
  }

  for (const t of edgeTypes) {
    if (!(t in state.edgeTypeVisible)) state.edgeTypeVisible[t] = true;
  }
  for (const t of Object.keys(state.edgeTypeVisible)) {
    if (!edgeTypes.has(t)) delete state.edgeTypeVisible[t];
  }
}

function renderFilterControls(graph) {
  ensureFilterDefaults(graph);

  const nodeTypes = Object.keys(state.nodeTypeVisible).sort();
  const edgeTypes = Object.keys(state.edgeTypeVisible).sort();

  nodeTypeFilters.innerHTML = '';
  const nodeWrap = document.createElement('div');
  nodeWrap.className = 'filter-grid';
  for (const t of nodeTypes) {
    const row = document.createElement('label');
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.checked = state.nodeTypeVisible[t] !== false;
    cb.addEventListener('change', () => {
      state.nodeTypeVisible[t] = cb.checked;
      renderGraph();
    });
    const text = document.createElement('span');
    text.textContent = t;
    row.appendChild(cb);
    row.appendChild(text);
    nodeWrap.appendChild(row);
  }
  nodeTypeFilters.appendChild(nodeWrap);

  edgeTypeFilters.innerHTML = '';
  const edgeWrap = document.createElement('div');
  edgeWrap.className = 'filter-grid';
  for (const t of edgeTypes) {
    const row = document.createElement('label');
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.checked = state.edgeTypeVisible[t] !== false;
    cb.addEventListener('change', () => {
      state.edgeTypeVisible[t] = cb.checked;
      renderGraph();
    });
    const text = document.createElement('span');
    text.textContent = t;
    row.appendChild(cb);
    row.appendChild(text);
    edgeWrap.appendChild(row);
  }
  edgeTypeFilters.appendChild(edgeWrap);
}

function resetModeState() {
  state.nodeTypeVisible = {};
  state.edgeTypeVisible = {};
  state.hopDepth = null;
  state.flowOverlayCache = null;
  hopDepthSelect.value = 'all';
}

async function loadGraphsList() {
  let index = await fetchJSON('/graphs');
  if ((!index || !Array.isArray(index.graphs) || index.graphs.length === 0) && state.auth.tenant !== DEFAULT_AUTH.tenant) {
    state.auth.tenant = DEFAULT_AUTH.tenant;
    localStorage.setItem('diffmind_auth_tenant', state.auth.tenant);
    index = await fetchJSON('/graphs', true);
  }
  state.index = index;
  const graphs = Array.isArray(index.graphs) ? index.graphs : [];

  graphSelect.innerHTML = '';
  if (graphs.length === 0) {
    const o = document.createElement('option');
    o.value = '';
    o.textContent = 'No graphs available';
    graphSelect.appendChild(o);
    state.rawGraph = null;
    state.advancedRawGraph = null;
    state.advancedGraph = null;
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

  summary.textContent = 'Loading graph...';
  const baseURL = `/graphs/${encodeURIComponent(graphID)}`;
  const raw = await fetchJSON(baseURL);
  state.selectedNodeId = '';
  state.flowOverlayCache = null;
  state.advancedRawGraph = null;
  state.advancedGraph = null;
  state.rawGraph = raw;
  state.graph = buildFocusedGraph(raw);
  if (!state.graph || state.graph.nodes.length === 0) {
    state.graph = raw;
  }
  renderFilterControls(state.graph);
  resetView();
  renderGraph();

  const advURL = `/graphs/${encodeURIComponent(graphID)}?view=advanced`;
  fetchJSON(advURL)
    .then((advancedRaw) => {
      state.advancedRawGraph = advancedRaw;
      state.advancedGraph = buildAdvancedGraph(advancedRaw);
      if ((!state.graph || state.graph.nodes.length === 0) && state.advancedGraph && state.advancedGraph.nodes.length > 0) {
        state.graph = state.advancedGraph;
        renderFilterControls(state.graph);
        resetView();
        renderGraph();
      }
    })
    .catch(() => {
      // Keep base graph rendered; advanced graph is optional for overlay enrichment.
    });
}

function focusNodeByID(nodeID) {
  if (!nodeID || !state.graph) return;
  const n = state.graph.nodes.find((x) => x.id === nodeID);
  if (!n) return;

  const t = nodeType(n);
  state.nodeTypeVisible[t] = true;
  state.selectedNodeId = nodeID;
  state.flowOverlayCache = null;
  renderFilterControls(state.graph);
  renderGraph();
  centerOnNode(nodeID);
}

function findFirstNodeByQuery(query) {
  if (!state.graph || !query) return null;
  const q = query.toLowerCase().trim();
  if (!q) return null;

  const exact = state.graph.nodes.find((n) => nodeLabel(n).toLowerCase() === q);
  if (exact) return exact;

  return state.graph.nodes.find((n) => nodeLabel(n).toLowerCase().includes(q));
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
  graphSvg.addEventListener('click', (ev) => {
    const target = ev.target instanceof Element ? ev.target : null;
    if (target && target.closest('g[data-node-id]')) {
      return;
    }
    if (state.suppressBackgroundClick) {
      state.suppressBackgroundClick = false;
      return;
    }
    state.selectedNodeId = '';
    state.flowOverlayCache = null;
    selectionDetails.textContent = 'Click a node to see details.';
    renderGraph();
  });

  graphSvg.addEventListener('mousedown', (ev) => {
    if (ev.button !== 0) return;
    const target = ev.target instanceof Element ? ev.target : null;
    if (target && target.closest('g[data-node-id]')) {
      return;
    }
    ev.preventDefault();
    state.dragging = true;
    state.dragMoved = false;
    state.dragStart = { x: ev.clientX, y: ev.clientY, tx: state.viewport.tx, ty: state.viewport.ty };
    beginInteraction();
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
    endInteractionSoon();
  });

  graphSvg.addEventListener('wheel', (ev) => {
    ev.preventDefault();
    beginInteraction();
    const step = ev.deltaY < 0 ? 1.06 : 0.94;
    const newScale = Math.max(0.2, Math.min(4, state.viewport.scale * step));
    const mouse = clientPointToSVG(ev.clientX, ev.clientY);
    const worldX = (mouse.x - state.viewport.tx) / state.viewport.scale;
    const worldY = (mouse.y - state.viewport.ty) / state.viewport.scale;

    state.viewport.scale = newScale;
    state.viewport.tx = mouse.x - worldX * newScale;
    state.viewport.ty = mouse.y - worldY * newScale;
    setTransform();
    endInteractionSoon();
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

hopDepthSelect.addEventListener('change', () => {
  const v = hopDepthSelect.value;
  state.hopDepth = v === 'all' ? null : Math.max(1, parseInt(v, 10));
  renderGraph();
});

nodeSearchInput.addEventListener('keydown', (ev) => {
  if (ev.key === 'Enter') {
    ev.preventDefault();
    const found = findFirstNodeByQuery(nodeSearchInput.value);
    if (found) {
      focusNodeByID(found.id);
    }
  }
});

document.addEventListener('keydown', (ev) => {
  if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === 'k') {
    ev.preventDefault();
    nodeSearchInput.focus();
    nodeSearchInput.select();
  }
});

(async function init() {
  wireGraphInteraction();
  selectionDetails.textContent = 'Click a node to see details.';
  try {
    resetModeState();
    await loadGraphsList();
  } catch (err) {
    summary.textContent = escapeHTML(err.message);
  }
})();
