const state = {
  index: null,
  rawGraph: null,
  rawSummary: null,
  graph: null,
  defaults: null,
  nodeById: {},
  layout: 'service_map',
  viewport: { scale: 1, tx: 0, ty: 0 },
  dragging: false,
  dragStart: null,
  selectedNodeId: '',
  selectedEdgeId: '',
  lastCompare: null,
  compareHistoryNextBefore: '',
  auth: {
    tenant: 'default',
    principal: 'ui-user',
    roles: 'platform_admin,tenant_admin,analyst,compliance_auditor',
    scopes: 'graph:read,graph:write,evidence:read,evidence:raw,sensitive:read,audit:read,audit:export',
  },
};

const graphSelect = document.getElementById('graphSelect');
const includeInferred = document.getElementById('includeInferred');
const edgeTypes = document.getElementById('edgeTypes');
const serviceFilter = document.getElementById('serviceFilter');
const repoFilter = document.getElementById('repoFilter');
const sectionFilter = document.getElementById('sectionFilter');
const classFilter = document.getElementById('classFilter');
const verificationStateFilter = document.getElementById('verificationStateFilter');
const adapterIDFilter = document.getElementById('adapterIDFilter');
const provenanceVersionFilter = document.getElementById('provenanceVersionFilter');
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
const productTemplateID = document.getElementById('productTemplateID');
const productTemplateVars = document.getElementById('productTemplateVars');
const productTemplateSummary = document.getElementById('productTemplateSummary');
const productTemplateResult = document.getElementById('productTemplateResult');
const questionID = document.getElementById('questionID');
const questionVars = document.getElementById('questionVars');
const questionSummary = document.getElementById('questionSummary');
const questionResult = document.getElementById('questionResult');
const runtimeGraphID = document.getElementById('runtimeGraphID');
const runtimeSections = document.getElementById('runtimeSections');
const runtimeIncludeDisputed = document.getElementById('runtimeIncludeDisputed');
const runtimeHistory = document.getElementById('runtimeHistory');
const runtimeCompareTo = document.getElementById('runtimeCompareTo');
const runtimeReportFrom = document.getElementById('runtimeReportFrom');
const runtimeReportTo = document.getElementById('runtimeReportTo');
const runtimeClaims = document.getElementById('runtimeClaims');
const runtimeObservations = document.getElementById('runtimeObservations');
const runtimeSummary = document.getElementById('runtimeSummary');
const runtimeResult = document.getElementById('runtimeResult');
const finalSigners = document.getElementById('finalSigners');
const finalSummary = document.getElementById('finalSummary');
const finalResult = document.getElementById('finalResult');
const edgesBody = document.querySelector('#edgesTable tbody');
const details = document.getElementById('selectionDetails');
const graphSvg = document.getElementById('graphSvg');
const buildStatus = document.getElementById('buildStatus');
const layoutMode = document.getElementById('layoutMode');
const architectureOnly = document.getElementById('architectureOnly');
const collapseByService = document.getElementById('collapseByService');
const showLabels = document.getElementById('showLabels');
const maxNodesInput = document.getElementById('maxNodes');
const maxEdgesInput = document.getElementById('maxEdges');
const graphWarning = document.getElementById('graphWarning');
const authTenant = document.getElementById('authTenant');
const authPrincipal = document.getElementById('authPrincipal');
const authRoles = document.getElementById('authRoles');
const authScopes = document.getElementById('authScopes');
const authStatus = document.getElementById('authStatus');

const edgeColors = {
  service_calls_service: '#264653',
  service_calls_endpoint: '#1d3557',
  service_publishes_queue: '#ef476f',
  queue_delivers_to_service: '#f4a261',
  service_reads_db: '#118ab2',
  service_writes_db: '#2a9d8f',
};

function loadAuth() {
  const tenant = localStorage.getItem('diffmind_auth_tenant');
  const principal = localStorage.getItem('diffmind_auth_principal');
  const roles = localStorage.getItem('diffmind_auth_roles');
  const scopes = localStorage.getItem('diffmind_auth_scopes');
  if (tenant) state.auth.tenant = tenant;
  if (principal) state.auth.principal = principal;
  if (roles) state.auth.roles = roles;
  if (scopes) state.auth.scopes = scopes;
  if (authTenant) authTenant.value = state.auth.tenant;
  if (authPrincipal) authPrincipal.value = state.auth.principal;
  if (authRoles) authRoles.value = state.auth.roles;
  if (authScopes) authScopes.value = state.auth.scopes;
}

function saveAuth() {
  state.auth.tenant = ((authTenant && authTenant.value) || '').trim() || 'default';
  state.auth.principal = ((authPrincipal && authPrincipal.value) || '').trim() || 'ui-user';
  state.auth.roles = ((authRoles && authRoles.value) || '').trim();
  state.auth.scopes = ((authScopes && authScopes.value) || '').trim();
  localStorage.setItem('diffmind_auth_tenant', state.auth.tenant);
  localStorage.setItem('diffmind_auth_principal', state.auth.principal);
  localStorage.setItem('diffmind_auth_roles', state.auth.roles);
  localStorage.setItem('diffmind_auth_scopes', state.auth.scopes);
  if (authStatus) {
    authStatus.textContent = `Auth applied: tenant=${state.auth.tenant}, principal=${state.auth.principal}`;
  }
}

function authHeaders() {
  return {
    'X-DiffMind-Tenant': state.auth.tenant || 'default',
    'X-DiffMind-Principal': state.auth.principal || 'ui-user',
    'X-DiffMind-Roles': state.auth.roles || '',
    'X-DiffMind-Scopes': state.auth.scopes || '',
  };
}

async function fetchJSON(url, options = {}) {
  const req = {
    ...options,
    headers: {
      ...authHeaders(),
      ...(options.headers || {}),
    },
  };
  const res = await fetch(url, req);
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
  if (sectionFilter.value.trim() !== '') {
    params.set('section', sectionFilter.value.trim());
  }
  if (classFilter.value.trim() !== '') {
    params.set('class', classFilter.value.trim());
  }
  if (verificationStateFilter.value.trim() !== '') {
    params.set('verification_state', verificationStateFilter.value.trim());
  }
  if (adapterIDFilter.value.trim() !== '') {
    params.set('adapter_id', adapterIDFilter.value.trim());
  }
  if (provenanceVersionFilter.value.trim() !== '') {
    params.set('provenance_version', provenanceVersionFilter.value.trim());
  }
  const minConfidence = Number(confidenceMin.value || '0');
  if (minConfidence > 0) {
    params.set('confidence_min', minConfidence.toFixed(2));
  }
  const query = params.toString();
  return query ? `?${query}` : '';
}

function parseLimit(v, fallback) {
  const n = Number(v);
  if (!Number.isFinite(n)) {
    return fallback;
  }
  if (n <= 0) {
    return 0;
  }
  return Math.floor(n);
}

function architectureNodeTypes() {
  return new Set([
    'service',
    'endpoint',
    'queue',
    'database',
    'topic',
    'table',
    'deployment',
    'runtime_unit',
    'dependency',
    'pipeline_step',
    'build_artifact',
    'environment',
    'owner',
  ]);
}

function aggregateByService(raw) {
  const serviceMeta = new Map();
  for (const s of (raw.meta?.services || [])) {
    serviceMeta.set(s.id, s);
  }

  const serviceNodeByID = new Map();
  for (const n of raw.nodes || []) {
    const sid = n.service_id || (n.type === 'service' ? n.id : '');
    if (!sid) continue;
    if (!serviceNodeByID.has(sid)) {
      const svc = serviceMeta.get(sid);
      serviceNodeByID.set(sid, {
        id: `svc:${sid}`,
        type: 'service',
        label: svc?.name || sid,
        service_id: sid,
        attributes: { collapsed: true },
        confidence: 1,
        inferred: false,
      });
    }
  }

  const nodeToService = new Map();
  for (const n of raw.nodes || []) {
    const sid = n.service_id || (n.type === 'service' ? n.id : '');
    if (sid) nodeToService.set(n.id, sid);
  }

  const edgeAgg = new Map();
  for (const e of raw.edges || []) {
    const srcSid = nodeToService.get(e.source_id);
    const dstSid = nodeToService.get(e.target_id);
    if (!srcSid || !dstSid || srcSid === dstSid) continue;
    const key = `${srcSid}|${dstSid}`;
    const prev = edgeAgg.get(key) || {
      id: `svc_edge:${srcSid}:${dstSid}`,
      type: 'service_calls_service',
      source_id: `svc:${srcSid}`,
      target_id: `svc:${dstSid}`,
      attributes: { collapsed: true, count: 0, edge_types: {} },
      confidence: 0,
      inferred: false,
      evidence_refs: [],
    };
    prev.attributes.count += 1;
    prev.attributes.edge_types[e.type] = (prev.attributes.edge_types[e.type] || 0) + 1;
    if ((e.confidence || 0) > prev.confidence) {
      prev.confidence = e.confidence || 0;
    }
    edgeAgg.set(key, prev);
  }

  const nodes = Array.from(serviceNodeByID.values());
  const edges = Array.from(edgeAgg.values());
  return {
    ...raw,
    nodes,
    edges,
    stats: {
      ...(raw.stats || {}),
      node_count: nodes.length,
      edge_count: edges.length,
      by_node_type: { service: nodes.length },
      by_edge_type: { service_calls_service: edges.length },
    },
  };
}

function applyDisplayFilters(raw) {
  if (!raw) return null;
  const rawNodes = Array.isArray(raw.nodes) ? raw.nodes : [];
  const rawEdges = Array.isArray(raw.edges) ? raw.edges : [];
  const rawServiceIDs = new Set(rawNodes.map((n) => n.service_id).filter((v) => !!v));
  let nodes = Array.isArray(raw.nodes) ? [...raw.nodes] : [];
  let edges = Array.isArray(raw.edges) ? [...raw.edges] : [];

  if (architectureOnly && architectureOnly.checked) {
    const allowed = architectureNodeTypes();
    const keepNode = new Set(nodes.filter((n) => allowed.has(n.type)).map((n) => n.id));
    nodes = nodes.filter((n) => keepNode.has(n.id));
    edges = edges.filter((e) => keepNode.has(e.source_id) && keepNode.has(e.target_id));
  }

  let base = { ...raw, nodes, edges };
  if (collapseByService && collapseByService.checked) {
    base = aggregateByService(base);
    nodes = base.nodes;
    edges = base.edges;
  }

  const maxEdges = parseLimit(maxEdgesInput?.value, 5000);
  if (maxEdges > 0 && edges.length > maxEdges) {
    edges = edges
      .slice()
      .sort((a, b) => (Number(b.confidence || 0) - Number(a.confidence || 0)))
      .slice(0, maxEdges);
  }

  const degree = {};
  for (const n of nodes) degree[n.id] = 0;
  for (const e of edges) {
    degree[e.source_id] = (degree[e.source_id] || 0) + 1;
    degree[e.target_id] = (degree[e.target_id] || 0) + 1;
  }

  const maxNodes = parseLimit(maxNodesInput?.value, 2000);
  if (maxNodes > 0 && nodes.length > maxNodes) {
    const sorted = nodes.slice().sort((a, b) => {
      if (a.type === 'service' && b.type !== 'service') return -1;
      if (b.type === 'service' && a.type !== 'service') return 1;
      const da = degree[a.id] || 0;
      const db = degree[b.id] || 0;
      if (da !== db) return db - da;
      return a.id.localeCompare(b.id);
    });
    const keep = new Set(sorted.slice(0, maxNodes).map((n) => n.id));
    nodes = nodes.filter((n) => keep.has(n.id));
    edges = edges.filter((e) => keep.has(e.source_id) && keep.has(e.target_id));
  }

  const fullNodes = rawNodes.length;
  const fullEdges = rawEdges.length;
  const note = [];
  if (collapseByService && collapseByService.checked && rawServiceIDs.size <= 1) {
    note.push('collapse-by-service on a single-service graph produces one node');
  }
  if (architectureOnly && architectureOnly.checked && nodes.length <= 1 && fullNodes > 1) {
    note.push('architecture-only filter removed most node types');
  }
  if (nodes.length < fullNodes) note.push(`nodes ${nodes.length}/${fullNodes}`);
  if (edges.length < fullEdges) note.push(`edges ${edges.length}/${fullEdges}`);
  graphWarning.textContent = note.length > 0
    ? `View is compressed for readability: ${note.join(', ')}.`
    : '';

  return {
    ...base,
    nodes,
    edges,
    stats: {
      ...(base.stats || {}),
      node_count: nodes.length,
      edge_count: edges.length,
    },
  };
}

async function loadGraph() {
  const graphID = graphSelect.value;
  if (!graphID) {
    return;
  }

  const query = graphQuery();
  const [graph, metricsPayload, summaryPayload] = await Promise.all([
    fetchJSON(`/graphs/${encodeURIComponent(graphID)}${query}`),
    fetchJSON(`/graphs/${encodeURIComponent(graphID)}/metrics${query}`),
    fetchJSON(`/graphs/${encodeURIComponent(graphID)}/summary${query}`),
  ]);
  state.rawGraph = graph;
  state.rawSummary = summaryPayload;
  state.graph = applyDisplayFilters(graph);
  state.nodeById = {};
  state.selectedNodeId = '';
  state.selectedEdgeId = '';
  for (const n of state.graph.nodes || []) {
    state.nodeById[n.id] = n;
  }

  summary.textContent = `Graph ${graph.graph_id} | mode=${graph.mode} | full_nodes=${summaryPayload.node_count || (graph.nodes || []).length} | full_edges=${summaryPayload.edge_count || (graph.edges || []).length} | visible_nodes=${(state.graph.nodes || []).length} | visible_edges=${(state.graph.edges || []).length}`;
  details.textContent = 'Single click node: focus neighborhood. Double click node: filter by that service.';
  renderLegend(state.graph);
  renderMetrics(metricsPayload);
  renderEdges();
  renderSVG();
  if (runtimeGraphID && runtimeGraphID.value.trim() === '') {
    runtimeGraphID.value = graphID;
  }
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

function parseJSONArrayInput(value, label) {
  const trimmed = String(value || '').trim();
  if (trimmed === '') return [];
  let parsed;
  try {
    parsed = JSON.parse(trimmed);
  } catch (err) {
    throw new Error(`${label} must be valid JSON array: ${err.message}`);
  }
  if (!Array.isArray(parsed)) {
    throw new Error(`${label} must be a JSON array`);
  }
  return parsed;
}

async function loadRuntimePlan() {
  const plan = await fetchJSON('/runtime/plan');
  const signals = Array.isArray(plan.input_signals) ? plan.input_signals.length : 0;
  runtimeSummary.textContent = `Runtime plan loaded: phase=${plan.phase || 'n/a'}, enabled=${Boolean(plan.enabled)}, publish_blocking=${Boolean(plan.publish_blocking)}, signals=${signals}`;
  runtimeResult.textContent = JSON.stringify(plan, null, 2);
}

async function loadRuntimeClaims() {
  const graphID = (runtimeGraphID && runtimeGraphID.value.trim()) || graphSelect.value.trim();
  if (!graphID) {
    runtimeSummary.textContent = 'Runtime claims load requires graph id.';
    return;
  }
  const params = new URLSearchParams();
  const sections = (runtimeSections && runtimeSections.value.trim()) || '';
  if (sections !== '') {
    params.set('sections', sections);
  }
  if (runtimeIncludeDisputed && runtimeIncludeDisputed.checked) {
    params.set('include_disputed', 'true');
  }
  const suffix = params.toString() ? `?${params.toString()}` : '';
  const payload = await fetchJSON(`/runtime/claims/${encodeURIComponent(graphID)}${suffix}`);
  const claims = Array.isArray(payload.claims) ? payload.claims : [];
  runtimeClaims.value = JSON.stringify(claims, null, 2);
  runtimeSummary.textContent = `Loaded ${claims.length} runtime claims from graph ${graphID}.`;
  runtimeResult.textContent = JSON.stringify({
    graph_id: payload.graph_id,
    count: payload.count,
  }, null, 2);
}

async function refreshRuntimeHistory() {
  if (!runtimeHistory) return;
  const payload = await fetchJSON('/runtime/reconcile?limit=20');
  const runs = Array.isArray(payload.runs) ? payload.runs : [];
  runtimeHistory.innerHTML = '';
  if (runtimeCompareTo) runtimeCompareTo.innerHTML = '';
  if (runs.length === 0) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'No runtime runs';
    runtimeHistory.appendChild(option);
    if (runtimeCompareTo) {
      const cmp = document.createElement('option');
      cmp.value = '';
      cmp.textContent = 'No runtime runs';
      runtimeCompareTo.appendChild(cmp);
    }
    return;
  }
  for (const run of runs) {
    const option = document.createElement('option');
    option.value = run.reconcile_id;
    option.textContent = `${run.reconcile_id} (${run.graph_id})`;
    runtimeHistory.appendChild(option);
    if (runtimeCompareTo) {
      const cmp = document.createElement('option');
      cmp.value = run.reconcile_id;
      cmp.textContent = `${run.reconcile_id} (${run.graph_id})`;
      runtimeCompareTo.appendChild(cmp);
    }
  }
  if (runtimeCompareTo && runtimeCompareTo.options.length > 1) {
    runtimeCompareTo.selectedIndex = 1;
  }
}

async function loadSelectedRuntimeRun() {
  if (!runtimeHistory || !runtimeHistory.value) {
    runtimeSummary.textContent = 'No runtime run selected.';
    return;
  }
  const run = await fetchJSON(`/runtime/reconcile/${encodeURIComponent(runtimeHistory.value)}`);
  const req = run.request || {};
  if (runtimeGraphID) runtimeGraphID.value = req.graph_id || run.result?.graph_id || runtimeGraphID.value;
  runtimeClaims.value = JSON.stringify(Array.isArray(req.claims) ? req.claims : [], null, 2);
  runtimeObservations.value = JSON.stringify(Array.isArray(req.observations) ? req.observations : [], null, 2);
  runtimeSummary.textContent = `Loaded runtime run ${run.reconcile_id}.`;
  runtimeResult.textContent = JSON.stringify(run.result || {}, null, 2);
}

async function deleteSelectedRuntimeRun() {
  if (!runtimeHistory || !runtimeHistory.value) {
    runtimeSummary.textContent = 'No runtime run selected.';
    return;
  }
  const recID = runtimeHistory.value;
  await fetchJSON(`/runtime/reconcile/${encodeURIComponent(recID)}`, { method: 'DELETE' });
  runtimeSummary.textContent = `Deleted runtime run ${recID}.`;
  runtimeResult.textContent = '';
  await refreshRuntimeHistory();
}

async function pruneRuntimeHistory(keepLatest = 20) {
  const payload = await fetchJSON(`/runtime/reconcile?keep_latest=${encodeURIComponent(String(keepLatest))}`, { method: 'DELETE' });
  runtimeSummary.textContent = `Pruned runtime history. Deleted ${payload.deleted || 0}, kept latest ${keepLatest}.`;
  await refreshRuntimeHistory();
}

async function compareSelectedRuntimeRuns() {
  if (!runtimeHistory || !runtimeHistory.value) {
    runtimeSummary.textContent = 'No source runtime run selected.';
    return;
  }
  if (!runtimeCompareTo || !runtimeCompareTo.value) {
    runtimeSummary.textContent = 'No target runtime run selected.';
    return;
  }
  if (runtimeHistory.value === runtimeCompareTo.value) {
    runtimeSummary.textContent = 'Select two different runtime runs to compare.';
    return;
  }
  const payload = await fetchJSON(`/runtime/reconcile/compare?from=${encodeURIComponent(runtimeHistory.value)}&to=${encodeURIComponent(runtimeCompareTo.value)}`);
  const changed =
    (payload.confirmed_added || []).length +
    (payload.confirmed_removed || []).length +
    (payload.contradicted_added || []).length +
    (payload.contradicted_removed || []).length +
    (payload.runtime_only_unmapped_added || []).length +
    (payload.runtime_only_unmapped_removed || []).length +
    (payload.needs_review_added || []).length +
    (payload.needs_review_removed || []).length;
  runtimeSummary.textContent = `Compared runtime runs ${payload.from_reconcile_id} -> ${payload.to_reconcile_id}: changed_items=${changed}`;
  runtimeResult.textContent = JSON.stringify(payload, null, 2);
}

async function loadRuntimeReport() {
  const params = new URLSearchParams();
  const graphID = (runtimeGraphID && runtimeGraphID.value.trim()) || graphSelect.value.trim();
  if (graphID) {
    params.set('graph_id', graphID);
  }
  const from = runtimeReportFrom && runtimeReportFrom.value ? runtimeReportFrom.value.trim() : '';
  const to = runtimeReportTo && runtimeReportTo.value ? runtimeReportTo.value.trim() : '';
  if (from) {
    params.set('from', from);
  }
  if (to) {
    params.set('to', to);
  }
  const suffix = params.toString() ? `?${params.toString()}` : '';
  const payload = await fetchJSON(`/runtime/reconcile/report${suffix}`);
  runtimeSummary.textContent = `Runtime report: runs=${payload.total_runs || 0}, confirmed=${payload.total_confirmed || 0}, contradicted=${payload.total_contradicted || 0}, unmapped=${payload.total_runtime_only_unmapped || 0}, needs_review=${payload.total_needs_review || 0}`;
  runtimeResult.textContent = JSON.stringify(payload, null, 2);
}

async function runRuntimeReconcile() {
  const graphID = (runtimeGraphID && runtimeGraphID.value.trim()) || graphSelect.value.trim();
  if (!graphID) {
    runtimeSummary.textContent = 'Runtime reconcile requires graph id.';
    return;
  }
  const claims = parseJSONArrayInput(runtimeClaims ? runtimeClaims.value : '', 'claims');
  const observations = parseJSONArrayInput(runtimeObservations ? runtimeObservations.value : '', 'observations');

  runtimeSummary.textContent = 'Running runtime reconcile...';
  const payload = await fetchJSON('/runtime/reconcile', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      tenant_id: state.auth.tenant || 'default',
      graph_id: graphID,
      claims,
      observations,
    }),
  });
  const confirmed = Array.isArray(payload.confirmed) ? payload.confirmed.length : 0;
  const contradicted = Array.isArray(payload.contradicted) ? payload.contradicted.length : 0;
  const unmapped = Array.isArray(payload.runtime_only_unmapped) ? payload.runtime_only_unmapped.length : 0;
  const needsReview = Array.isArray(payload.needs_review) ? payload.needs_review.length : 0;
  runtimeSummary.textContent = `Runtime reconcile completed: confirmed=${confirmed}, contradicted=${contradicted}, unmapped=${unmapped}, needs_review=${needsReview}`;
  runtimeResult.textContent = JSON.stringify(payload, null, 2);
  await refreshRuntimeHistory();
}

async function loadProductTemplates() {
  const payload = await fetchJSON('/products/templates');
  const templates = Array.isArray(payload.templates) ? payload.templates : [];
  if (templates.length === 0) {
    productTemplateSummary.textContent = 'No product templates available.';
    productTemplateResult.textContent = JSON.stringify(payload, null, 2);
    return;
  }
  if (productTemplateID && !productTemplateID.value.trim()) {
    productTemplateID.value = templates[0].id || '';
  }
  productTemplateSummary.textContent = `Loaded ${templates.length} product templates from ${payload.path}.`;
  productTemplateResult.textContent = JSON.stringify(payload, null, 2);
}

async function runProductTemplate() {
  const templateID = productTemplateID ? productTemplateID.value.trim() : '';
  if (!templateID) {
    productTemplateSummary.textContent = 'Template ID is required.';
    return;
  }
  let vars = {};
  const rawVars = productTemplateVars ? productTemplateVars.value.trim() : '';
  if (rawVars !== '') {
    try {
      vars = JSON.parse(rawVars);
    } catch (err) {
      productTemplateSummary.textContent = `Template vars must be valid JSON object: ${err.message}`;
      return;
    }
    if (!vars || typeof vars !== 'object' || Array.isArray(vars)) {
      productTemplateSummary.textContent = 'Template vars must be a JSON object.';
      return;
    }
  }
  const payload = await fetchJSON('/products/templates/execute', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      template_id: templateID,
      vars,
      include_result: true,
    }),
  });
  productTemplateSummary.textContent = `Template executed: id=${payload.template_id}, status=${payload.status}, path=${payload.path}`;
  productTemplateResult.textContent = JSON.stringify(payload, null, 2);
}

async function loadProductQuestions() {
  const payload = await fetchJSON('/products/questions');
  const questions = Array.isArray(payload.questions) ? payload.questions : [];
  if (questions.length === 0) {
    questionSummary.textContent = 'No questions available.';
    questionResult.textContent = JSON.stringify(payload, null, 2);
    return;
  }
  if (questionID && !questionID.value.trim()) {
    questionID.value = questions[0].id || '';
  }
  questionSummary.textContent = `Loaded ${questions.length} questions from ${payload.path}.`;
  questionResult.textContent = JSON.stringify(payload, null, 2);
}

async function runQuestion() {
  const qid = questionID ? questionID.value.trim() : '';
  if (!qid) {
    questionSummary.textContent = 'Question ID is required.';
    return;
  }
  let vars = {};
  const rawVars = questionVars ? questionVars.value.trim() : '';
  if (rawVars !== '') {
    try {
      vars = JSON.parse(rawVars);
    } catch (err) {
      questionSummary.textContent = `Question vars must be valid JSON object: ${err.message}`;
      return;
    }
    if (!vars || typeof vars !== 'object' || Array.isArray(vars)) {
      questionSummary.textContent = 'Question vars must be a JSON object.';
      return;
    }
  }
  const payload = await fetchJSON('/products/questions/execute', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      question_id: qid,
      vars,
    }),
  });
  questionSummary.textContent = `Question executed: id=${payload.question_id}, status=${payload.status}, endpoint=${payload.question_endpoint}`;
  questionResult.textContent = JSON.stringify(payload, null, 2);
}

async function loadQuestionCoverage() {
  const payload = await fetchJSON('/products/questions/coverage');
  questionSummary.textContent = `Question coverage: covered=${payload.covered || 0}/${payload.total || 0}, ratio=${Number(payload.coverage_ratio || 0).toFixed(2)}`;
  questionResult.textContent = JSON.stringify(payload, null, 2);
}

async function runQuestionCatalog() {
  let vars = {};
  const rawVars = questionVars ? questionVars.value.trim() : '';
  if (rawVars !== '') {
    try {
      vars = JSON.parse(rawVars);
    } catch (err) {
      questionSummary.textContent = `Question vars must be valid JSON object: ${err.message}`;
      return;
    }
    if (!vars || typeof vars !== 'object' || Array.isArray(vars)) {
      questionSummary.textContent = 'Question vars must be a JSON object.';
      return;
    }
  }
  const payload = await fetchJSON('/products/questions/run', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ vars }),
  });
  questionSummary.textContent = `Question catalog run: succeeded=${payload.succeeded || 0}/${payload.total || 0}, failed=${payload.failed || 0}, overall_passed=${Boolean(payload.overall_passed)}`;
  questionResult.textContent = JSON.stringify(payload, null, 2);
}

async function runFinalGateAttestation() {
  const signers = (finalSigners && finalSigners.value ? finalSigners.value : '')
    .split(',')
    .map((v) => v.trim())
    .filter((v) => v !== '');
  finalSummary.textContent = 'Running final gate attestation...';
  const payload = await fetchJSON('/final/attest', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ signers }),
  });
  const report = payload.readiness_report || {};
  const passed = typeof report.overall_passed === 'boolean' ? report.overall_passed : Boolean(payload.overall_passed);
  finalSummary.textContent = `Final gate completed: overall_passed=${passed}`;
  finalResult.textContent = JSON.stringify(payload, null, 2);
}

async function loadFinalReadiness() {
  const payload = await fetchJSON('/final/readiness');
  const report = payload.report || {};
  finalSummary.textContent = `Loaded final readiness from ${payload.path}: overall_passed=${Boolean(report.overall_passed)}`;
  finalResult.textContent = JSON.stringify(payload, null, 2);
}

async function loadFinalDecision() {
  const payload = await fetchJSON('/final/decision');
  const text = payload.content || '';
  finalSummary.textContent = `Loaded final gate decision from ${payload.path}`;
  finalResult.textContent = text;
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
    dependency: '#7b2cbf',
    pipeline_step: '#3a86ff',
    config_key: '#6c757d',
    deployment: '#2f9e44',
    runtime_unit: '#495057',
    verification_decision: '#6a4c93',
    conflict: '#d62828',
    dependency_risk: '#c1121f',
    build_artifact: '#8d99ae',
    sensitive_surface: '#ff006e',
    environment: '#457b9d',
    owner: '#2d6a4f',
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

function sectionTint(section) {
  if (section === 'exposure') return '#e3f2fd';
  if (section === 'logic') return '#edf7ed';
  if (section === 'dependencies') return '#fff3e0';
  return '#f1f5f9';
}

function canonicalNodeSection(node) {
  const section = String(node.section || '').trim().toLowerCase();
  if (section === 'exposure' || section === 'logic' || section === 'dependencies') {
    return section;
  }
  const cls = String(node.class || '').trim().toLowerCase();
  if (cls.startsWith('exposure_')) return 'exposure';
  if (cls.startsWith('dependency_')) return 'dependencies';
  if (cls.startsWith('logic_')) return 'logic';

  // Compatibility fallback for legacy graphs without ontology-v2 metadata.
  const t = (node.type || '').toLowerCase();
  if (t === 'endpoint') return 'exposure';
  if (t === 'queue' || t === 'topic') return 'dependencies';
  if (t === 'database' || t === 'table' || t === 'dependency' || t === 'build_artifact') return 'dependencies';
  return 'logic';
}

function toGroupLabel(value) {
  const raw = String(value || '').trim();
  if (raw === '') return '';
  return raw
    .replace(/[_\-]+/g, ' ')
    .replace(/([a-z])([A-Z])/g, '$1 $2')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

function classifyNodeGroup(section, node) {
  if (String(node.class || '').trim() !== '') {
    return toGroupLabel(node.class);
  }
  const t = (node.type || '').toLowerCase();

  if (section === 'exposure') {
    if (t === 'endpoint') return 'API Endpoints';
    if (t === 'queue' || t === 'topic') return 'Queue/Topic Inputs';
    if (t === 'sensitive_surface') return 'Sensitive Inputs';
    if (t === 'runtime_unit') return 'Ingress/Runtime Entry';
    return 'Other Inputs';
  }

  if (section === 'dependencies') {
    if (t === 'dependency') return 'External Services & APIs';
    if (t === 'database' || t === 'table') return 'Databases & Storage';
    if (t === 'queue' || t === 'topic') return 'Queue/Topic Publishes';
    if (t === 'build_artifact') return 'Build/Infra Artifacts';
    return 'Other Dependencies';
  }

  if (t === 'service') return 'Service Core';
  if (t === 'runtime_unit') return 'Runtime Units';
  if (t === 'config_key' || t === 'environment') return 'Configuration';
  if (t === 'pipeline_step' || t === 'deployment') return 'Build & Delivery';
  if (t === 'verification_decision' || t === 'conflict' || t === 'dependency_risk' || t === 'sensitive_surface') return 'Quality & Risk';
  if (t === 'owner') return 'Ownership';
  return 'Domain Logic';
}

function computeServiceMapLayout(graph) {
  const nodes = Array.isArray(graph.nodes) ? graph.nodes : [];
  const sectionOrder = ['exposure', 'logic', 'dependencies'];
  const sectionTitle = {
    exposure: '1. Exposure',
    logic: '2. Logic',
    dependencies: '3. Dependencies',
  };
  const sectionBuckets = new Map(sectionOrder.map((s) => [s, new Map()]));

  for (const node of nodes) {
    const section = canonicalNodeSection(node);
    const group = classifyNodeGroup(section, node);
    const groups = sectionBuckets.get(section) || new Map();
    if (!groups.has(group)) groups.set(group, []);
    groups.get(group).push(node);
    sectionBuckets.set(section, groups);
  }

  const canvasWidth = 2600;
  const padX = 45;
  const padY = 48;
  const sectionGap = 26;
  const sectionWidth = Math.floor((canvasWidth - padX * 2 - sectionGap * 2) / 3);
  const nodeStepX = 135;
  const nodeStepY = 42;
  const groupTitleH = 24;
  const groupInnerPad = 16;
  const groupGap = 14;
  const sectionMinHeight = 620;

  const position = {};
  const sections = [];
  let globalMaxY = padY + sectionMinHeight;

  sectionOrder.forEach((section, idx) => {
    const x = padX + idx * (sectionWidth + sectionGap);
    const groupsMap = sectionBuckets.get(section) || new Map();
    const groupNames = Array.from(groupsMap.keys()).sort((a, b) => {
      if (a === 'Service Core') return -1;
      if (b === 'Service Core') return 1;
      return a.localeCompare(b);
    });

    let y = padY + 38;
    const renderedGroups = [];
    for (const groupName of groupNames) {
      const members = (groupsMap.get(groupName) || [])
        .slice()
        .sort((a, b) => (a.type + a.id).localeCompare(b.type + b.id));
      const usableWidth = Math.max(120, sectionWidth - groupInnerPad * 2);
      const cols = Math.max(1, Math.floor(usableWidth / nodeStepX));
      const rows = Math.max(1, Math.ceil(members.length / cols));
      const groupHeight = groupTitleH + groupInnerPad + rows * nodeStepY + 10;

      members.forEach((node, i) => {
        const col = i % cols;
        const row = Math.floor(i / cols);
        position[node.id] = {
          x: x + groupInnerPad + 18 + col * nodeStepX,
          y: y + groupTitleH + groupInnerPad + row * nodeStepY,
        };
      });

      renderedGroups.push({
        name: groupName,
        x: x + 8,
        y,
        w: sectionWidth - 16,
        h: groupHeight,
      });
      y += groupHeight + groupGap;
    }

    const sectionHeight = Math.max(sectionMinHeight, y - padY + 20);
    globalMaxY = Math.max(globalMaxY, padY + sectionHeight + 40);
    sections.push({
      key: section,
      title: sectionTitle[section],
      x,
      y: padY,
      w: sectionWidth,
      h: sectionHeight,
      groups: renderedGroups,
    });
  });

  return {
    w: canvasWidth,
    h: globalMaxY,
    position,
    sections,
  };
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

  let w = 1200;
  let h = 520;
  const nodes = graph.nodes;
  let position = {};
  let serviceMapLayout = null;
  if (state.layout === 'service_map') {
    serviceMapLayout = computeServiceMapLayout(graph);
    w = serviceMapLayout.w;
    h = serviceMapLayout.h;
    position = serviceMapLayout.position;
  } else if (state.layout === 'service_lanes') {
    position = computeServiceLanePositions(nodes, w, h);
  } else {
    position = computeGridPositions(nodes, w, h);
  }
  graphSvg.setAttribute('viewBox', `0 0 ${w} ${h}`);

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

  if (serviceMapLayout && Array.isArray(serviceMapLayout.sections)) {
    for (const section of serviceMapLayout.sections) {
      const sectionRect = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
      sectionRect.setAttribute('x', section.x);
      sectionRect.setAttribute('y', section.y);
      sectionRect.setAttribute('width', section.w);
      sectionRect.setAttribute('height', section.h);
      sectionRect.setAttribute('rx', 14);
      sectionRect.setAttribute('fill', sectionTint(section.key));
      sectionRect.setAttribute('fill-opacity', '0.65');
      sectionRect.setAttribute('stroke', '#94a3b8');
      sectionRect.setAttribute('stroke-width', '1.2');
      root.appendChild(sectionRect);

      const sectionLabel = document.createElementNS('http://www.w3.org/2000/svg', 'text');
      sectionLabel.setAttribute('x', section.x + 12);
      sectionLabel.setAttribute('y', section.y + 24);
      sectionLabel.setAttribute('font-size', '16');
      sectionLabel.setAttribute('font-weight', '700');
      sectionLabel.setAttribute('fill', '#0f172a');
      sectionLabel.textContent = section.title;
      root.appendChild(sectionLabel);

      for (const group of section.groups || []) {
        const groupRect = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
        groupRect.setAttribute('x', group.x);
        groupRect.setAttribute('y', group.y);
        groupRect.setAttribute('width', group.w);
        groupRect.setAttribute('height', group.h);
        groupRect.setAttribute('rx', 10);
        groupRect.setAttribute('fill', '#ffffff');
        groupRect.setAttribute('fill-opacity', '0.8');
        groupRect.setAttribute('stroke', '#cbd5e1');
        groupRect.setAttribute('stroke-width', '1');
        root.appendChild(groupRect);

        const groupLabel = document.createElementNS('http://www.w3.org/2000/svg', 'text');
        groupLabel.setAttribute('x', group.x + 10);
        groupLabel.setAttribute('y', group.y + 17);
        groupLabel.setAttribute('font-size', '12');
        groupLabel.setAttribute('font-weight', '600');
        groupLabel.setAttribute('fill', '#334155');
        groupLabel.textContent = group.name;
        root.appendChild(groupLabel);
      }
    }
  }

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
    if (!p) continue;
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
    const shouldShowLabel = (showLabels && showLabels.checked) || node.id === selectedNode || (selectedNode && neighborhood.has(node.id)) || state.viewport.scale >= 1.35;
    if (!shouldShowLabel) {
      label.setAttribute('display', 'none');
    }

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
  setScaleAt(nextScale, { x: graphSvg.clientWidth / 2, y: graphSvg.clientHeight / 2 });
}

function setScaleAt(nextScale, anchor) {
  const oldScale = state.viewport.scale;
  const clamped = Math.max(0.2, Math.min(3, nextScale));
  const a = anchor || { x: graphSvg.clientWidth / 2, y: graphSvg.clientHeight / 2 };
  const worldX = (a.x - state.viewport.tx) / oldScale;
  const worldY = (a.y - state.viewport.ty) / oldScale;
  state.viewport.scale = clamped;
  state.viewport.tx = a.x - worldX * clamped;
  state.viewport.ty = a.y - worldY * clamped;
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
  const loadProductTemplatesBtn = document.getElementById('loadProductTemplatesBtn');
  if (loadProductTemplatesBtn) {
    loadProductTemplatesBtn.addEventListener('click', () => {
      loadProductTemplates().catch((err) => {
        productTemplateSummary.textContent = `Load templates failed: ${err.message}`;
      });
    });
  }
  const runProductTemplateBtn = document.getElementById('runProductTemplateBtn');
  if (runProductTemplateBtn) {
    runProductTemplateBtn.addEventListener('click', () => {
      runProductTemplate().catch((err) => {
        productTemplateSummary.textContent = `Run template failed: ${err.message}`;
      });
    });
  }
  const loadQuestionsBtn = document.getElementById('loadQuestionsBtn');
  if (loadQuestionsBtn) {
    loadQuestionsBtn.addEventListener('click', () => {
      loadProductQuestions().catch((err) => {
        questionSummary.textContent = `Load questions failed: ${err.message}`;
      });
    });
  }
  const runQuestionBtn = document.getElementById('runQuestionBtn');
  if (runQuestionBtn) {
    runQuestionBtn.addEventListener('click', () => {
      runQuestion().catch((err) => {
        questionSummary.textContent = `Run question failed: ${err.message}`;
      });
    });
  }
  const loadQuestionCoverageBtn = document.getElementById('loadQuestionCoverageBtn');
  if (loadQuestionCoverageBtn) {
    loadQuestionCoverageBtn.addEventListener('click', () => {
      loadQuestionCoverage().catch((err) => {
        questionSummary.textContent = `Question coverage failed: ${err.message}`;
      });
    });
  }
  const runQuestionCatalogBtn = document.getElementById('runQuestionCatalogBtn');
  if (runQuestionCatalogBtn) {
    runQuestionCatalogBtn.addEventListener('click', () => {
      runQuestionCatalog().catch((err) => {
        questionSummary.textContent = `Run question catalog failed: ${err.message}`;
      });
    });
  }
  const loadRuntimePlanBtn = document.getElementById('loadRuntimePlanBtn');
  if (loadRuntimePlanBtn) {
    loadRuntimePlanBtn.addEventListener('click', () => {
      loadRuntimePlan().catch((err) => {
        runtimeSummary.textContent = `Runtime plan load failed: ${err.message}`;
      });
    });
  }
  const loadRuntimeClaimsBtn = document.getElementById('loadRuntimeClaimsBtn');
  if (loadRuntimeClaimsBtn) {
    loadRuntimeClaimsBtn.addEventListener('click', () => {
      loadRuntimeClaims().catch((err) => {
        runtimeSummary.textContent = `Runtime claims load failed: ${err.message}`;
      });
    });
  }
  const runRuntimeReconcileBtn = document.getElementById('runRuntimeReconcileBtn');
  if (runRuntimeReconcileBtn) {
    runRuntimeReconcileBtn.addEventListener('click', () => {
      runRuntimeReconcile().catch((err) => {
        runtimeSummary.textContent = `Runtime reconcile failed: ${err.message}`;
      });
    });
  }
  const refreshRuntimeHistoryBtn = document.getElementById('refreshRuntimeHistoryBtn');
  if (refreshRuntimeHistoryBtn) {
    refreshRuntimeHistoryBtn.addEventListener('click', () => {
      refreshRuntimeHistory().catch((err) => {
        runtimeSummary.textContent = `Runtime history refresh failed: ${err.message}`;
      });
    });
  }
  const loadRuntimeHistoryBtn = document.getElementById('loadRuntimeHistoryBtn');
  if (loadRuntimeHistoryBtn) {
    loadRuntimeHistoryBtn.addEventListener('click', () => {
      loadSelectedRuntimeRun().catch((err) => {
        runtimeSummary.textContent = `Runtime run load failed: ${err.message}`;
      });
    });
  }
  const compareRuntimeHistoryBtn = document.getElementById('compareRuntimeHistoryBtn');
  if (compareRuntimeHistoryBtn) {
    compareRuntimeHistoryBtn.addEventListener('click', () => {
      compareSelectedRuntimeRuns().catch((err) => {
        runtimeSummary.textContent = `Runtime compare failed: ${err.message}`;
      });
    });
  }
  const deleteRuntimeHistoryBtn = document.getElementById('deleteRuntimeHistoryBtn');
  if (deleteRuntimeHistoryBtn) {
    deleteRuntimeHistoryBtn.addEventListener('click', () => {
      deleteSelectedRuntimeRun().catch((err) => {
        runtimeSummary.textContent = `Runtime run delete failed: ${err.message}`;
      });
    });
  }
  const pruneRuntimeHistoryBtn = document.getElementById('pruneRuntimeHistoryBtn');
  if (pruneRuntimeHistoryBtn) {
    pruneRuntimeHistoryBtn.addEventListener('click', () => {
      pruneRuntimeHistory(20).catch((err) => {
        runtimeSummary.textContent = `Runtime history prune failed: ${err.message}`;
      });
    });
  }
  const loadRuntimeReportBtn = document.getElementById('loadRuntimeReportBtn');
  if (loadRuntimeReportBtn) {
    loadRuntimeReportBtn.addEventListener('click', () => {
      loadRuntimeReport().catch((err) => {
        runtimeSummary.textContent = `Runtime report failed: ${err.message}`;
      });
    });
  }
  const runFinalGateBtn = document.getElementById('runFinalGateBtn');
  if (runFinalGateBtn) {
    runFinalGateBtn.addEventListener('click', () => {
      runFinalGateAttestation().catch((err) => {
        finalSummary.textContent = `Final gate attestation failed: ${err.message}`;
      });
    });
  }
  const loadFinalReadinessBtn = document.getElementById('loadFinalReadinessBtn');
  if (loadFinalReadinessBtn) {
    loadFinalReadinessBtn.addEventListener('click', () => {
      loadFinalReadiness().catch((err) => {
        finalSummary.textContent = `Load final readiness failed: ${err.message}`;
      });
    });
  }
  const loadFinalDecisionBtn = document.getElementById('loadFinalDecisionBtn');
  if (loadFinalDecisionBtn) {
    loadFinalDecisionBtn.addEventListener('click', () => {
      loadFinalDecision().catch((err) => {
        finalSummary.textContent = `Load final decision failed: ${err.message}`;
      });
    });
  }
  graphSelect.addEventListener('change', () => {
    if (runtimeGraphID && runtimeGraphID.value.trim() === '') {
      runtimeGraphID.value = graphSelect.value;
    }
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
  document.getElementById('zoomInBtn').addEventListener('click', () => setScale(state.viewport.scale * 1.1));
  document.getElementById('zoomOutBtn').addEventListener('click', () => setScale(state.viewport.scale / 1.1));
  document.getElementById('fitBtn').addEventListener('click', fitView);
  document.getElementById('resetViewBtn').addEventListener('click', fitView);
  document.getElementById('clearFocusBtn').addEventListener('click', () => {
    state.selectedNodeId = '';
    state.selectedEdgeId = '';
    details.textContent = 'Focus cleared.';
    renderEdges();
    renderSVG();
  });
  document.getElementById('applyViewBtn').addEventListener('click', () => {
    if (!state.rawGraph) return;
    state.graph = applyDisplayFilters(state.rawGraph);
    state.nodeById = {};
    for (const n of state.graph.nodes || []) {
      state.nodeById[n.id] = n;
    }
    renderLegend(state.graph);
    renderEdges();
    renderSVG();
  });
  architectureOnly.addEventListener('change', () => document.getElementById('applyViewBtn').click());
  collapseByService.addEventListener('change', () => document.getElementById('applyViewBtn').click());
  showLabels.addEventListener('change', () => renderSVG());
  maxNodesInput.addEventListener('change', () => document.getElementById('applyViewBtn').click());
  maxEdgesInput.addEventListener('change', () => document.getElementById('applyViewBtn').click());
  graphSvg.addEventListener('wheel', (evt) => {
    evt.preventDefault();
    const anchor = svgPoint(evt);
    const factor = Math.exp(-evt.deltaY * 0.001);
    setScaleAt(state.viewport.scale * factor, anchor);
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
    const panFactor = 0.72;
    state.viewport.tx += (p.x - state.dragStart.x) * panFactor;
    state.viewport.ty += (p.y - state.dragStart.y) * panFactor;
    state.dragStart = p;
    renderSVG();
  });
  const applyAuthBtn = document.getElementById('applyAuthBtn');
  if (applyAuthBtn) {
    applyAuthBtn.addEventListener('click', () => {
      saveAuth();
      loadGraphsList().catch((err) => {
        summary.textContent = `Auth applied but graph load failed: ${err.message}`;
      });
    });
  }
}

async function bootstrap() {
  loadAuth();
  saveAuth();
  wireEvents();
  confidenceMinLabel.textContent = Number(confidenceMin.value || '0').toFixed(2);
  try {
    await loadDefaults();
  } catch (err) {
    buildStatus.textContent = `Defaults unavailable: ${err.message}`;
  }
  try {
    await loadGraphsList();
    if (runtimeGraphID && runtimeGraphID.value.trim() === '' && graphSelect.value) {
      runtimeGraphID.value = graphSelect.value;
    }
    if (runtimeClaims && runtimeClaims.value.trim() === '') {
      runtimeClaims.value = JSON.stringify([{ graph_id: graphSelect.value || 'g1', edge_id: 'e1' }], null, 2);
    }
    if (runtimeSections && runtimeSections.value.trim() === '') {
      runtimeSections.value = 'exposure,dependencies';
    }
    if (productTemplateVars && productTemplateVars.value.trim() === '') {
      productTemplateVars.value = JSON.stringify({
        graph_id: graphSelect.value || 'g1',
        service_id: 'a',
        node_id: 'svc:a',
      }, null, 2);
    }
    if (questionVars && questionVars.value.trim() === '') {
      questionVars.value = JSON.stringify({
        graph_id: graphSelect.value || 'g1',
        service_id: 'a',
        node_id: 'svc:a',
      }, null, 2);
    }
    if (runtimeObservations && runtimeObservations.value.trim() === '') {
      runtimeObservations.value = JSON.stringify([{ source_system: 'gateway', signal_type: 'http', attributes: { edge_id: 'e1' } }], null, 2);
    }
    if (finalSigners && finalSigners.value.trim() === '') {
      finalSigners.value = 'engineering,platform,security';
    }
    await loadProductTemplates();
    await loadProductQuestions();
    await refreshRuntimeHistory();
  } catch (err) {
    summary.textContent = `Failed loading graphs list: ${err.message}`;
  }
}

bootstrap();
