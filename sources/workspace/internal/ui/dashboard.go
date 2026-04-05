package ui

const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>DiffMind — Architecture Graph</title>
<script src="https://d3js.org/d3.v7.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/dagre@0.8.5/dist/dagre.min.js"></script>
<style>
:root {
  --bg:#080c15; --panel:#0f1525; --panel2:#151d30; --border:#1a2744; --border2:#243352;
  --text:#dfe6f0; --muted:#7e8fa6; --dim:#4e5d75;
  --blue:#3b9eff; --green:#22c997; --orange:#f5943a; --red:#ef5455; --purple:#9b7cf9; --yellow:#f0c040; --cyan:#22c5d6; --pink:#e36bae;
  --svc-bg:#111b2e; --svc-border:#1e3a66; --ext-bg:#16141e; --ext-border:#3a2a55;
  --queue-bg:#0e1a16; --queue-border:#1a4a36; --db-bg:#1a1510; --db-border:#4a3620;
}
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:"Inter","SF Pro Display",system-ui,sans-serif;background:var(--bg);color:var(--text);display:grid;grid-template-rows:44px 1fr;grid-template-columns:1fr 380px;height:100vh;overflow:hidden}
header{grid-column:1/-1;background:rgba(15,21,37,.94);backdrop-filter:blur(12px);border-bottom:1px solid var(--border);display:flex;align-items:center;gap:14px;padding:0 18px;z-index:10}
header .logo{font-weight:700;font-size:14px;color:var(--blue);letter-spacing:.02em}
header .sep{width:1px;height:18px;background:var(--border)}
header select,header button{background:var(--panel2);color:var(--text);border:1px solid var(--border2);border-radius:5px;padding:4px 10px;font-size:12px;cursor:pointer}
header button:hover{background:#1e2a44}
.stats{display:flex;gap:16px;margin-left:auto}
.stat .n{font-size:16px;font-weight:700;line-height:1}
.stat .l{font-size:9px;color:var(--muted);text-transform:uppercase;letter-spacing:.07em}
#canvas-container{position:relative;overflow:hidden;background:var(--bg)}
svg{width:100%;height:100%}
#sidebar{background:var(--panel);border-left:1px solid var(--border);overflow-y:auto;padding:14px;display:flex;flex-direction:column;gap:10px;font-size:12px}
.sec{background:var(--panel2);border:1px solid var(--border2);border-radius:8px;padding:12px}
.sec h3{font-size:10px;text-transform:uppercase;letter-spacing:.08em;color:var(--muted);margin-bottom:8px}
.sec .row{display:flex;justify-content:space-between;padding:3px 0;border-bottom:1px solid rgba(255,255,255,.03)}
.sec .row:last-child{border-bottom:none}
.sec .k{color:var(--dim);font-size:11px}
.sec .v{color:var(--text);font-size:11px;font-weight:500;text-align:right;max-width:220px;word-break:break-all}
.badge{display:inline-block;padding:1px 6px;border-radius:3px;font-size:10px;font-weight:600}
.badge-http{background:rgba(59,158,255,.15);color:var(--blue)}
.badge-queue{background:rgba(34,201,151,.15);color:var(--green)}
.badge-db{background:rgba(245,148,58,.15);color:var(--orange)}
.badge-cache{background:rgba(239,84,85,.15);color:var(--red)}
.badge-sqs{background:rgba(34,201,151,.12);color:var(--green)}
.badge-sns{background:rgba(240,192,64,.12);color:var(--yellow)}
.badge-kinesis{background:rgba(155,124,249,.12);color:var(--purple)}
.tag{background:rgba(255,255,255,.05);border:1px solid rgba(255,255,255,.07);border-radius:3px;padding:1px 6px;font-size:10px;color:var(--muted);display:inline-block;margin:2px}
.detail-list{max-height:300px;overflow-y:auto}
.detail-item{padding:4px 0;border-bottom:1px solid rgba(255,255,255,.03)}
.detail-item .name{color:var(--text);font-weight:500;font-size:11px}
.detail-item .sub{color:var(--dim);font-size:10px;margin-top:1px}
.legend-box{position:absolute;bottom:12px;left:12px;background:rgba(15,21,37,.9);backdrop-filter:blur(8px);border:1px solid var(--border);border-radius:8px;padding:10px 14px;font-size:10px;z-index:5}
.legend-box .title{font-weight:700;color:var(--muted);text-transform:uppercase;letter-spacing:.06em;margin-bottom:4px;font-size:9px}
.legend-row{display:flex;align-items:center;gap:6px;padding:2px 0}
.legend-swatch{width:12px;height:12px;border-radius:3px}
.legend-line{width:20px;height:2px;border-radius:1px}

/* Node styles in SVG */
.node-service rect{fill:var(--svc-bg);stroke:var(--svc-border);stroke-width:1.5;rx:8}
.node-service.selected rect{stroke:var(--blue);stroke-width:2.5}
.node-service .title-text{fill:var(--text);font-size:11px;font-weight:600}
.node-service .badge-text{fill:var(--muted);font-size:9px}
.node-service .icon-text{font-size:10px}
.node-external rect{fill:var(--ext-bg);stroke:var(--ext-border);stroke-width:1;stroke-dasharray:4,3;rx:8}
.node-external.selected rect{stroke:var(--purple);stroke-width:2}
.node-external .title-text{fill:var(--purple);font-size:10px;font-weight:500}
.node-queue rect{fill:var(--queue-bg);stroke:var(--queue-border);stroke-width:1;rx:12}
.node-queue.selected rect{stroke:var(--green);stroke-width:2}
.node-queue .title-text{fill:var(--green);font-size:9px;font-weight:500}
.node-db rect{fill:var(--db-bg);stroke:var(--db-border);stroke-width:1;rx:4}
.node-db.selected rect{stroke:var(--orange);stroke-width:2}
.node-db .title-text{fill:var(--orange);font-size:9px;font-weight:500}
.node-scheduler rect{fill:#151118;stroke:#3d2a5a;stroke-width:1;rx:10;stroke-dasharray:3,2}
.node-scheduler.selected rect{stroke:var(--yellow);stroke-width:2}
.node-scheduler.hl-neighbor rect{stroke:var(--yellow);stroke-width:2}
.node-scheduler .title-text{fill:var(--yellow);font-size:8px;font-weight:500}
.edge{fill:none;stroke-width:1.5;opacity:.55;transition:opacity .2s,stroke-width .2s}
.edge:hover{opacity:1;stroke-width:2.5}
.edge.type-http{stroke:var(--blue)}
.edge.type-queue_publish{stroke:var(--green)}
.edge.type-queue_consume{stroke:var(--green);stroke-dasharray:5,3}
.edge.type-database{stroke:var(--orange)}
.edge.type-cache{stroke:var(--red)}
.edge.type-scheduler{stroke:var(--yellow);stroke-dasharray:3,2;opacity:.4}
.edge.hl-active{opacity:1;stroke-width:3}
.edge.hl-dimmed{opacity:.1;stroke-width:1}
.edge-label{font-size:8px;fill:var(--dim)}
marker path{fill:var(--dim)}
[class^="node-"]{transition:opacity .2s}
[class^="node-"].hl-neighbor rect{stroke-width:2.5}
.node-service.hl-neighbor rect{stroke:var(--blue)}
.node-external.hl-neighbor rect{stroke:var(--purple)}
.node-queue.hl-neighbor rect{stroke:var(--green)}
.node-db.hl-neighbor rect{stroke:var(--orange)}
[class^="node-"].hl-dimmed{opacity:.15}
</style>
</head>
<body>

<header>
  <span class="logo">DiffMind</span>
  <span class="sep"></span>
  <label style="color:var(--muted);font-size:11px">Run:</label>
  <select id="runSelect"></select>
  <button onclick="loadGraph()">Refresh</button>
  <div class="stats">
    <div class="stat"><div class="n" id="st-svc">-</div><div class="l">Services</div></div>
    <div class="stat"><div class="n" id="st-ext" style="color:var(--purple)">-</div><div class="l">External</div></div>
    <div class="stat"><div class="n" id="st-queue" style="color:var(--green)">-</div><div class="l">Queues</div></div>
    <div class="stat"><div class="n" id="st-db" style="color:var(--orange)">-</div><div class="l">Databases</div></div>
    <div class="stat"><div class="n" id="st-edge" style="color:var(--blue)">-</div><div class="l">Edges</div></div>
  </div>
</header>

<div id="canvas-container">
  <svg id="graph-svg"></svg>
  <div class="legend-box">
    <div class="title">Legend</div>
    <div class="legend-row"><div class="legend-swatch" style="background:var(--svc-bg);border:1.5px solid var(--svc-border)"></div> Known Service</div>
    <div class="legend-row"><div class="legend-swatch" style="background:var(--ext-bg);border:1.5px dashed var(--ext-border)"></div> External / Unknown</div>
    <div class="legend-row"><div class="legend-swatch" style="background:var(--queue-bg);border:1px solid var(--queue-border);border-radius:8px"></div> Queue / Topic</div>
    <div class="legend-row"><div class="legend-swatch" style="background:var(--db-bg);border:1px solid var(--db-border)"></div> Database</div>
    <div class="legend-row"><div class="legend-swatch" style="background:#151118;border:1px dashed #3d2a5a;border-radius:8px"></div> Scheduler</div>
    <div class="legend-row" style="margin-top:3px"><div class="legend-line" style="background:var(--blue)"></div> HTTP call</div>
    <div class="legend-row"><div class="legend-line" style="background:var(--green)"></div> Queue pub/sub</div>
    <div class="legend-row"><div class="legend-line" style="background:var(--orange)"></div> Database</div>
    <div class="legend-row"><div class="legend-line" style="background:var(--red)"></div> Cache</div>
  </div>
</div>

<div id="sidebar">
  <div class="sec" id="sidebar-content">
    <h3>Details</h3>
    <p style="color:var(--dim)">Click any node to inspect its details</p>
  </div>
</div>

<script>
let graphData = null;
let selectedNode = null;

async function fetchJSON(url){const r=await fetch(url);if(!r.ok)throw new Error(await r.text());return r.json()}

async function loadRuns(){
  const d=await fetchJSON('/api/runs');
  const sel=document.getElementById('runSelect');sel.innerHTML='';
  for(const r of(d.runs||[])){const o=document.createElement('option');o.value=r;o.textContent=r;sel.appendChild(o)}
}

async function loadGraph(){
  const id=document.getElementById('runSelect').value||'latest';
  graphData=await fetchJSON('/api/graph/'+encodeURIComponent(id));
  updateStats();
  renderGraph();
  showSidebar(null);
}

function updateStats(){
  document.getElementById('st-svc').textContent=(graphData.services||[]).length;
  document.getElementById('st-ext').textContent=(graphData.external_nodes||[]).length;
  document.getElementById('st-queue').textContent=(graphData.queue_nodes||[]).length;
  document.getElementById('st-db').textContent=(graphData.database_nodes||[]).length;
  document.getElementById('st-edge').textContent=(graphData.edges||[]).length;
}

// ---- DAGRE LAYOUT ----
function renderGraph(){
  const svg=d3.select('#graph-svg');svg.selectAll('*').remove();
  if(!graphData)return;
  const container=document.getElementById('canvas-container');
  const W=container.clientWidth,H=container.clientHeight;

  // Build dagre graph
  const g=new dagre.graphlib.Graph({compound:false,multigraph:true});
  g.setGraph({rankdir:'LR',nodesep:40,ranksep:120,edgesep:25,marginx:60,marginy:60});
  g.setDefaultEdgeLabel(()=>({}));

  const services=graphData.services||[];
  const externals=graphData.external_nodes||[];
  const queues=graphData.queue_nodes||[];
  const databases=graphData.database_nodes||[];
  const schedulers=graphData.scheduler_nodes||[];
  const edges=graphData.edges||[];

  // Measure text width using a hidden SVG text element
  const measurer=svg.append('text').attr('font-family','Inter,SF Pro Display,system-ui,sans-serif').attr('opacity',0);
  function textWidth(str,fontSize){
    measurer.attr('font-size',fontSize+'px').text(str);
    const w=measurer.node().getComputedTextLength();
    return w;
  }

  // Node height constants
  const svcH_base=48;
  const extH=36;
  const queueH=32;
  const dbH=32;
  const padX=32; // horizontal padding inside nodes

  // Add service nodes
  const nodeInfo={};
  services.forEach(s=>{
    const badges=[];
    const rc=(s.http_routes||[]).length;
    const cc=(s.queue_consumers||[]).length;
    const jc=(s.scheduled_jobs||[]).length;
    const wc=(s.webhooks||[]).length;
    const cli=(s.cli_commands||[]).length;
    if(rc)badges.push(rc+'R');
    if(cc)badges.push(cc+'C');
    if(jc)badges.push(jc+'J');
    if(wc)badges.push(wc+'W');
    if(cli)badges.push(cli+'CLI');
    const h=svcH_base+(badges.length>0?8:0);
    const nameW=textWidth(s.name,11)+padX+18; // +18 for icon space
    const w=Math.max(140,nameW);
    g.setNode(s.name,{width:w,height:h,type:'service',data:s,badges});
    nodeInfo[s.name]={type:'service',data:s,badges};
  });

  // Add external nodes
  externals.forEach(e=>{
    const cleanName=cleanLabel(e.name);
    const nameW=textWidth(cleanName,10)+padX;
    const w=Math.max(100,nameW);
    g.setNode(e.name,{width:w,height:extH,type:'external',data:e});
    nodeInfo[e.name]={type:'external',data:e};
  });

  // Add queue nodes
  queues.forEach(q=>{
    const id='queue:'+q.id;
    const cleanName=cleanLabel(q.name);
    const nameW=textWidth(cleanName,9)+padX+16; // +16 for icon
    const w=Math.max(100,nameW);
    g.setNode(id,{width:w,height:queueH,type:'queue',data:q,label:cleanName});
    nodeInfo[id]={type:'queue',data:q};
  });

  // Add database nodes
  databases.forEach(d=>{
    const id='db:'+d.id;
    const cleanName=cleanLabel(d.name);
    const nameW=textWidth(cleanName,9)+padX+18; // +18 for icon
    const w=Math.max(100,nameW);
    g.setNode(id,{width:w,height:dbH,type:'db',data:d,label:cleanName});
    nodeInfo[id]={type:'db',data:d};
  });

  // Add scheduler nodes
  const schedH=26;
  schedulers.forEach(s=>{
    const id='sched:'+s.id;
    const label=cleanLabel(s.name);
    const nameW=textWidth(label,9)+padX+14;
    const w=Math.max(90,nameW);
    g.setNode(id,{width:w,height:schedH,type:'scheduler',data:s,label});
    nodeInfo[id]={type:'scheduler',data:s};
  });

  // Remove measurer
  measurer.remove();

  // Add edges
  edges.forEach((e,i)=>{
    const from=e.from,to=e.to;
    if(g.hasNode(from)&&g.hasNode(to)){
      g.setEdge(from,to,{type:e.type,label:e.label,data:e},'e'+i);
    }
  });

  // Run layout
  dagre.layout(g);

  // Get graph dimensions
  const graphW=g.graph().width||800;
  const graphH=g.graph().height||600;

  // SVG setup with zoom
  const rootG=svg.append('g');
  const zoom=d3.zoom().scaleExtent([0.15,3]).on('zoom',ev=>rootG.attr('transform',ev.transform));
  svg.call(zoom);

  // Defs for arrows
  const defs=svg.append('defs');
  ['http','queue_publish','queue_consume','database','cache','scheduler'].forEach(type=>{
    const colors={http:'#3b9eff',queue_publish:'#22c997',queue_consume:'#22c997',database:'#f5943a',cache:'#ef5455',scheduler:'#f0c040'};
    defs.append('marker').attr('id','arr-'+type).attr('viewBox','0 -4 8 8').attr('refX',8).attr('refY',0)
      .attr('markerWidth',6).attr('markerHeight',6).attr('orient','auto')
      .append('path').attr('d','M0,-3L8,0L0,3').attr('fill',colors[type]||'#4e5d75');
  });

  // Draw edges first (behind nodes)
  const edgeGroup=rootG.append('g').attr('class','edges');
  g.edges().forEach(e=>{
    const ed=g.edge(e);
    const points=ed.points||[];
    if(points.length<2)return;
    const line=d3.line().x(p=>p.x).y(p=>p.y).curve(d3.curveBasis);
    edgeGroup.append('path')
      .attr('class','edge type-'+(ed.type||''))
      .attr('d',line(points))
      .attr('data-from',e.v)
      .attr('data-to',e.w)
      .attr('marker-end','url(#arr-'+(ed.type||'http')+')')
      .style('cursor','pointer')
      .on('click',ev=>{ev.stopPropagation();showEdgeDetail(ed.data)});
    // Edge label at midpoint
    if(points.length>=2 && ed.type==='database'){
      const mid=points[Math.floor(points.length/2)];
      const lbl=(ed.label||'').length>30?(ed.label||'').slice(0,28)+'...':(ed.label||'');
      if(lbl) edgeGroup.append('text').attr('class','edge-label').attr('x',mid.x).attr('y',mid.y-5).attr('text-anchor','middle').text(lbl);
    }
  });

  // Draw nodes
  const nodeGroup=rootG.append('g').attr('class','nodes');
  g.nodes().forEach(nid=>{
    const n=g.node(nid);
    if(!n)return;
    const x=n.x-n.width/2, y=n.y-n.height/2;
    const ng=nodeGroup.append('g')
      .attr('class','node-'+n.type)
      .attr('transform','translate('+x+','+y+')')
      .attr('data-id',nid)
      .style('cursor','pointer')
      .on('click',ev=>{ev.stopPropagation();selectNode(nid,n)});

    ng.append('rect').attr('width',n.width).attr('height',n.height);

    if(n.type==='service'){
      // Service icon + name (full, no truncation)
      drawIcon(ng,'service',8,6,12);
      ng.append('text').attr('class','title-text').attr('x',24).attr('y',17).text(n.data.name);
      // Badges
      if(n.badges&&n.badges.length){
        const badgeG=ng.append('g').attr('transform','translate(10,28)');
        let bx=0;
        n.badges.forEach(b=>{
          badgeG.append('rect').attr('x',bx).attr('y',0).attr('width',b.length*6+8).attr('height',14).attr('rx',3)
            .attr('fill','rgba(59,158,255,.1)').attr('stroke','rgba(59,158,255,.2)').attr('stroke-width',.5);
          badgeG.append('text').attr('x',bx+4).attr('y',10).attr('fill','#7eb8ff').attr('font-size',9).attr('font-weight',500).text(b);
          bx+=b.length*6+12;
        });
      }
    } else if(n.type==='external'){
      drawIcon(ng,'service',n.width/2-6,n.height/2-10,10);
      ng.append('text').attr('class','title-text').attr('x',n.width/2).attr('y',n.height/2+8).attr('text-anchor','middle')
        .text(cleanLabel((nodeInfo[nid]||{}).data?.name||nid));
    } else if(n.type==='queue'){
      const qkind=(n.data&&n.data.kind)||'sqs';
      drawIcon(ng,ICONS[qkind]?qkind:'sqs',4,8,14);
      ng.append('text').attr('class','title-text').attr('x',22).attr('y',20).text(n.label||cleanLabel(n.data?.name||''));
    } else if(n.type==='db'){
      const dbkind=(n.data&&n.data.kind)||'postgresql';
      drawIcon(ng,ICONS[dbkind]?dbkind:'postgresql',4,8,14);
      ng.append('text').attr('class','title-text').attr('x',22).attr('y',20).text(n.label||cleanLabel(n.data?.name||''));
    } else if(n.type==='scheduler'){
      // Clock icon for schedulers
      ng.append('text').attr('x',6).attr('y',17).attr('fill','var(--yellow)').attr('font-size',11).text('\u23F0');
      ng.append('text').attr('class','title-text').attr('x',20).attr('y',17).text(n.label||'');
    }
  });

  // Click background to deselect
  svg.on('click',()=>{selectNode(null,null)});

  // Zoom to fit
  const pad=60;
  const scale=Math.min((W-pad*2)/graphW,(H-pad*2)/graphH,1.2);
  const tx=(W-graphW*scale)/2;
  const ty=(H-graphH*scale)/2;
  svg.call(zoom.transform,d3.zoomIdentity.translate(tx,ty).scale(scale));
}

// SVG icon paths for technology types (rendered inline)
const ICONS={
  // Service type icons (small, 12x12 viewBox)
  service:'<path d="M2,3 L10,3 L10,10 L2,10Z M4,1 L8,1 L8,3 L4,3Z M1,5 L2,5 M10,5 L11,5" stroke="currentColor" fill="none" stroke-width="1"/>',
  // Database icons
  postgresql:'<ellipse cx="7" cy="3" rx="5" ry="2" fill="none" stroke="#336791" stroke-width="1.2"/><path d="M2,3 L2,9 C2,10.1 4.2,11 7,11 C9.8,11 12,10.1 12,9 L12,3" fill="none" stroke="#336791" stroke-width="1.2"/><path d="M2,6 C2,7.1 4.2,8 7,8 C9.8,8 12,7.1 12,6" fill="none" stroke="#336791" stroke-width="1.2"/>',
  dynamodb:'<path d="M3,2 L11,2 L13,7 L11,12 L3,12 L1,7Z" fill="none" stroke="#4053D6" stroke-width="1.2"/><path d="M4,5 L10,5 M4,7 L10,7 M4,9 L10,9" stroke="#4053D6" stroke-width="0.8"/>',
  redis:'<path d="M7,1 L12,4 L7,7 L2,4Z" fill="none" stroke="#DC382D" stroke-width="1.2"/><path d="M2,4 L2,9 L7,12 L12,9 L12,4" fill="none" stroke="#DC382D" stroke-width="1.2"/><path d="M7,7 L7,12" stroke="#DC382D" stroke-width="1"/>',
  athena:'<path d="M7,1 L12,4 L12,10 L7,13 L2,10 L2,4Z" fill="none" stroke="#8C4FFF" stroke-width="1.2"/><circle cx="7" cy="7" r="2" fill="none" stroke="#8C4FFF" stroke-width="1"/>',
  elasticsearch:'<circle cx="7" cy="5" r="3" fill="none" stroke="#FEC514" stroke-width="1.2"/><path d="M4,5 L10,5" stroke="#00BFB3" stroke-width="1.5"/><path d="M9,7.5 L12,11" stroke="#FEC514" stroke-width="1.2"/>',
  mongodb:'<path d="M7,1 C7,1 4,3 4,7 C4,10 6,12 7,13 C8,12 10,10 10,7 C10,3 7,1 7,1Z" fill="none" stroke="#00ED64" stroke-width="1.2"/><path d="M7,5 L7,11" stroke="#00ED64" stroke-width="1"/>',
  // Queue icons
  sqs:'<rect x="2" y="3" width="10" height="8" rx="1" fill="none" stroke="#FF9900" stroke-width="1.2"/><path d="M5,6 L9,6 M5,8 L8,8" stroke="#FF9900" stroke-width="0.8"/><path d="M7,1 L7,3 M7,11 L7,13" stroke="#FF9900" stroke-width="1"/>',
  sns:'<circle cx="7" cy="7" r="4" fill="none" stroke="#FF4F8B" stroke-width="1.2"/><path d="M7,3 L7,2 M3,7 L2,7 M11,7 L12,7 M7,11 L7,12 M4.2,4.2 L3.2,3.2 M9.8,4.2 L10.8,3.2" stroke="#FF4F8B" stroke-width="0.8"/>',
  kinesis:'<path d="M3,2 L8,7 L3,12 M6,2 L11,7 L6,12" fill="none" stroke="#8C4FFF" stroke-width="1.5"/>',
  kafka:'<circle cx="7" cy="4" r="1.5" fill="none" stroke="#231F20" stroke-width="1"/><circle cx="4" cy="9" r="1.5" fill="none" stroke="#231F20" stroke-width="1"/><circle cx="10" cy="9" r="1.5" fill="none" stroke="#231F20" stroke-width="1"/><path d="M7,5.5 L4,7.5 M7,5.5 L10,7.5 M4,9 L10,9" stroke="#231F20" stroke-width="0.8"/>',
};

function drawIcon(parentG, kind, x, y, size){
  const iconSvg=ICONS[kind]||ICONS.service;
  const ig=parentG.append('g').attr('transform','translate('+x+','+y+') scale('+(size/14)+')');
  ig.html(iconSvg);
  return ig;
}

function cleanLabel(s){
  if(!s)return'';
  return s.replace(/\$\{[^}]+\}/g,'').replace(/https?:\/\//,'').trim();
}

function selectNode(nid,n){
  selectedNode=nid;
  // Clear all highlights
  d3.selectAll('[class^="node-"]').classed('selected',false).classed('hl-neighbor',false).classed('hl-dimmed',false);
  d3.selectAll('.edge').classed('hl-active',false).classed('hl-dimmed',false);

  if(nid){
    // Find connected edges and neighbor nodes
    const connEdges=new Set();
    const neighbors=new Set();
    neighbors.add(nid);

    d3.selectAll('.edge').each(function(){
      const el=d3.select(this);
      const from=el.attr('data-from');
      const to=el.attr('data-to');
      if(from===nid||to===nid){
        connEdges.add(this);
        neighbors.add(from);
        neighbors.add(to);
      }
    });

    // Highlight the selected node
    d3.selectAll('[data-id="'+nid+'"]').classed('selected',true);

    // Highlight neighbor nodes
    d3.selectAll('[class^="node-"]').each(function(){
      const el=d3.select(this);
      const id=el.attr('data-id');
      if(id===nid) return; // already selected
      if(neighbors.has(id)){
        el.classed('hl-neighbor',true);
      } else {
        el.classed('hl-dimmed',true);
      }
    });

    // Highlight connected edges, dim the rest
    d3.selectAll('.edge').each(function(){
      if(connEdges.has(this)){
        d3.select(this).classed('hl-active',true);
      } else {
        d3.select(this).classed('hl-dimmed',true);
      }
    });

    // Show sidebar detail
    if(n.type==='service')showServiceDetail(n.data);
    else if(n.type==='external')showExternalDetail(n.data||{name:nid});
    else if(n.type==='queue')showQueueDetail(n.data);
    else if(n.type==='db')showDBDetail(n.data);
    else if(n.type==='scheduler')showSchedulerDetail(n.data);
  } else {
    showSidebar(null);
  }
}

// ---- SIDEBAR RENDERERS ----

function showSidebar(html){
  const sb=document.getElementById('sidebar-content');
  if(!html){
    sb.innerHTML='<h3>Details</h3><p style="color:var(--dim)">Click any node to inspect</p>';
    // Show overview
    if(graphData){
      let ov='<div class="sec"><h3>Services</h3>';
      (graphData.services||[]).forEach(s=>{
        const r=(s.http_routes||[]).length,c=(s.queue_consumers||[]).length,j=(s.scheduled_jobs||[]).length;
        ov+='<div class="detail-item"><div class="name" style="cursor:pointer" onclick="clickService(\''+esc(s.name)+'\')">'+esc(s.name)+'</div>';
        ov+='<div class="sub">'+[r&&r+'routes',c&&c+'consumers',j&&j+'jobs'].filter(Boolean).join(' \u00B7 ')+'</div></div>';
      });
      ov+='</div>';
      document.getElementById('sidebar').innerHTML=sb.outerHTML+ov;
    }
    return;
  }
  document.getElementById('sidebar').innerHTML=html;
}

function clickService(name){
  const svc=(graphData.services||[]).find(s=>s.name===name);
  if(svc) selectNode(name,{type:'service',data:svc});
}

function showServiceDetail(svc){
  let h='<div class="sec"><h3>Service</h3>';
  h+=kv('Name','<span style="color:var(--blue);font-weight:700">'+esc(svc.name)+'</span>');
  h+=kv('Known','<span style="color:var(--green)">Yes \u2713</span>');
  h+='</div>';

  // HTTP Routes - clickable to show connections
  const routes=svc.http_routes||[];
  if(routes.length){
    h+='<div class="sec"><h3>HTTP Endpoints ('+routes.length+')</h3><div class="detail-list">';
    routes.forEach(r=>{
      const d=r.details||{};
      const method=d.method||'';
      const path=d.path||r.name||'';
      const ename=r.name||path;
      h+='<div class="detail-item" style="cursor:pointer" onclick="showExposureFlow(\''+escAttr(svc.name)+'\',\''+escAttr(ename)+'\')">';
      h+='<div class="name"><span class="badge badge-http">'+esc(method)+'</span> '+esc(path)+' <span style="color:var(--dim);font-size:9px">\u25B6</span></div>';
      if(d.controller)h+='<div class="sub">'+esc(d.controller)+'</div>';
      h+='</div>';
    });
    h+='</div></div>';
  }

  // Queue Consumers - clickable
  const cons=svc.queue_consumers||[];
  if(cons.length){
    h+='<div class="sec"><h3>Queue Consumers ('+cons.length+')</h3><div class="detail-list">';
    cons.forEach(c=>{
      const d=c.details||{};
      h+='<div class="detail-item" style="cursor:pointer" onclick="showExposureFlow(\''+escAttr(svc.name)+'\',\''+escAttr(c.name)+'\')">';
      h+='<div class="name"><span class="badge badge-sqs">SQS</span> '+esc(d.queue||c.name)+' <span style="color:var(--dim);font-size:9px">\u25B6</span></div>';
      if(d.production_url)h+='<div class="sub">'+esc(d.production_url)+'</div>';
      h+='</div>';
    });
    h+='</div></div>';
  }

  // Scheduled Jobs - clickable
  const jobs=svc.scheduled_jobs||[];
  if(jobs.length){
    h+='<div class="sec"><h3>Scheduled Jobs ('+jobs.length+')</h3><div class="detail-list">';
    jobs.forEach(j=>{
      const d=j.details||{};
      h+='<div class="detail-item" style="cursor:pointer" onclick="showExposureFlow(\''+escAttr(svc.name)+'\',\''+escAttr(j.name)+'\')">';
      h+='<div class="name">'+esc(j.name)+' <span style="color:var(--dim);font-size:9px">\u25B6</span></div>';
      h+='<div class="sub">'+(d.schedule?'cron: '+esc(d.schedule):'')+(d.spring_profile?' | profile: '+esc(d.spring_profile):'')+'</div></div>';
    });
    h+='</div></div>';
  }

  // CLI Commands - clickable
  const clis=svc.cli_commands||[];
  if(clis.length){
    h+='<div class="sec"><h3>CLI / Lambda ('+clis.length+')</h3><div class="detail-list">';
    clis.forEach(c=>{
      h+='<div class="detail-item" style="cursor:pointer" onclick="showExposureFlow(\''+escAttr(svc.name)+'\',\''+escAttr(c.name)+'\')">';
      h+='<div class="name">'+esc(c.name)+' <span style="color:var(--dim);font-size:9px">\u25B6</span></div>';
      if(c.summary)h+='<div class="sub">'+esc(c.summary)+'</div>';
      h+='</div>';
    });
    h+='</div></div>';
  }

  // Outbound HTTP (dependencies) - clickable
  const outHTTP=(graphData.edges||[]).filter(e=>e.from===svc.name&&e.type==='http');
  if(outHTTP.length){
    h+='<div class="sec"><h3>Outbound HTTP ('+outHTTP.length+')</h3>';
    outHTTP.forEach(e=>{
      const det=(e.details&&e.details[0])||{};
      const dd=det.details||{};
      h+='<div class="detail-item"><div class="name">\u2192 '+esc(e.to)+'</div>';
      if(dd.endpoints_called){
        const eps=Array.isArray(dd.endpoints_called)?dd.endpoints_called:[dd.endpoints_called];
        eps.forEach(ep=>h+='<div class="sub" style="padding-left:8px">'+esc(String(ep))+'</div>');
      }
      h+='</div>';
    });
    h+='</div>';
  }

  // Databases
  const outDB=(graphData.edges||[]).filter(e=>e.from===svc.name&&(e.type==='database'||e.type==='cache'));
  if(outDB.length){
    h+='<div class="sec"><h3>Databases ('+outDB.length+')</h3>';
    outDB.forEach(e=>{
      h+='<div class="detail-item"><div class="name"><span class="badge badge-db">'+esc(e.label)+'</span> '+esc(e.to.replace(/^db:/,''))+'</div></div>';
    });
    h+='</div>';
  }

  showSidebar(h);
}

// Show data flow from a specific exposure (endpoint/scheduler/consumer)
function showExposureFlow(svcName,exposureName){
  const svc=(graphData.services||[]).find(s=>s.name===svcName);
  if(!svc)return;
  const conns=(svc.connections||[]).filter(c=>c.from_name===exposureName);
  
  let h='<div class="sec"><h3>Data Flow</h3>';
  h+=kv('Service','<span style="color:var(--blue)">'+esc(svcName)+'</span>');
  h+=kv('Entry point',esc(exposureName));
  h+='</div>';

  if(conns.length===0){
    h+='<div class="sec"><p style="color:var(--dim)">No downstream connections found for this entry point</p></div>';
  } else {
    // Group connections by target type
    const byType={};
    conns.forEach(c=>{
      const t=c.to_type||'unknown';
      (byType[t]=byType[t]||[]).push(c);
    });

    for(const[type,items]of Object.entries(byType)){
      const typeBadge={outbound_http:'badge-http',db_operation:'badge-db',queue_publish:'badge-queue',cache_operation:'badge-cache'}[type]||'badge-http';
      const typeLabel={outbound_http:'HTTP Calls',db_operation:'Database Ops',queue_publish:'Queue Publish',cache_operation:'Cache Ops'}[type]||type;
      h+='<div class="sec"><h3>'+esc(typeLabel)+' ('+items.length+')</h3><div class="detail-list">';
      items.forEach(c=>{
        h+='<div class="detail-item">';
        h+='<div class="name"><span class="badge '+typeBadge+'">\u2192</span> '+esc(c.to_name)+'</div>';
        h+='<div class="sub">'+esc(c.summary)+'</div>';
        h+='</div>';
      });
      h+='</div></div>';
    }
  }

  // Back button
  h+='<div style="margin-top:8px"><button onclick="clickService(\''+escAttr(svcName)+'\')" style="background:var(--panel2);color:var(--muted);border:1px solid var(--border2);border-radius:5px;padding:4px 12px;cursor:pointer;font-size:11px">\u2190 Back to '+esc(svcName)+'</button></div>';

  showSidebar(h);
}

function showExternalDetail(ext){
  let h='<div class="sec"><h3>External Service</h3>';
  h+=kv('Name','<span style="color:var(--purple)">'+esc(ext.name)+'</span>');
  h+=kv('Known','<span style="color:var(--dim)">No \u2717 (no repo data)</span>');
  h+=kv('Kind',esc(ext.kind||'unknown'));
  h+='</div>';

  // Find all edges pointing to this service
  const inbound=(graphData.edges||[]).filter(e=>e.to===ext.name);
  if(inbound.length){
    h+='<div class="sec"><h3>Called By ('+inbound.length+')</h3>';
    inbound.forEach(e=>{
      h+='<div class="detail-item"><div class="name">'+esc(e.from)+' <span class="badge badge-http">HTTP</span></div></div>';
    });
    h+='</div>';
  }
  showSidebar(h);
}

function showQueueDetail(q){
  let h='<div class="sec"><h3>Queue</h3>';
  h+=kv('Name',esc(q.name));
  h+=kv('Kind','<span class="badge badge-sqs">'+esc(q.kind)+'</span>');
  if(q.fifo)h+=kv('FIFO','Yes');
  h+='</div>';

  const publishers=(graphData.edges||[]).filter(e=>e.to==='queue:'+q.id&&e.type==='queue_publish');
  const consumers=(graphData.edges||[]).filter(e=>e.from==='queue:'+q.id&&e.type==='queue_consume');

  if(publishers.length){
    h+='<div class="sec"><h3>Publishers ('+publishers.length+')</h3>';
    publishers.forEach(e=>h+='<div class="detail-item"><div class="name">'+esc(e.from)+' \u2192</div></div>');
    h+='</div>';
  }
  if(consumers.length){
    h+='<div class="sec"><h3>Consumers ('+consumers.length+')</h3>';
    consumers.forEach(e=>h+='<div class="detail-item"><div class="name">\u2192 '+esc(e.to)+'</div></div>');
    h+='</div>';
  }
  showSidebar(h);
}

function showDBDetail(db){
  let h='<div class="sec"><h3>Database</h3>';
  h+=kv('Name',esc(db.name));
  h+=kv('Type','<span class="badge badge-db">'+esc(db.kind)+'</span>');
  if(db.host) h+=kv('Host',esc(db.host));
  h+='</div>';

  const users=(graphData.edges||[]).filter(e=>e.to==='db:'+db.id);
  if(users.length){
    h+='<div class="sec"><h3>Used By ('+users.length+')</h3>';
    users.forEach(e=>{
      h+='<div class="detail-item"><div class="name">'+esc(e.from)+'</div>';
      h+='<div class="sub">Operations: <span class="badge badge-db">'+esc(e.label||'read/write')+'</span></div></div>';
    });
    h+='</div>';
  }
  showSidebar(h);
}

function showSchedulerDetail(s){
  let h='<div class="sec"><h3>Scheduled Job</h3>';
  h+=kv('Name',esc(s.name));
  h+=kv('Service','<span style="color:var(--blue)">'+esc(s.service)+'</span>');
  if(s.schedule)h+=kv('Schedule','<code style="color:var(--yellow)">'+esc(s.schedule)+'</code>');
  if(s.profile)h+=kv('Profile',esc(s.profile));
  h+='</div>';

  // Show data flow from this scheduler
  const svc=(graphData.services||[]).find(sv=>sv.name===s.service);
  if(svc){
    const conns=(svc.connections||[]).filter(c=>c.from_name===s.name);
    if(conns.length){
      const byType={};
      conns.forEach(c=>{(byType[c.to_type]=byType[c.to_type]||[]).push(c)});
      for(const[type,items]of Object.entries(byType)){
        const typeLabel={outbound_http:'HTTP Calls',db_operation:'Database Ops',queue_publish:'Queue Publish',cache_operation:'Cache Ops'}[type]||type;
        h+='<div class="sec"><h3>'+esc(typeLabel)+' ('+items.length+')</h3><div class="detail-list">';
        items.forEach(c=>{
          h+='<div class="detail-item"><div class="name">\u2192 '+esc(c.to_name)+'</div>';
          h+='<div class="sub">'+esc(c.summary)+'</div></div>';
        });
        h+='</div></div>';
      }
    }
  }
  showSidebar(h);
}

function showEdgeDetail(e){
  if(!e)return;
  let h='<div class="sec"><h3>Connection</h3>';
  h+=kv('From',esc(e.from));
  h+=kv('To',esc(e.to));
  h+=kv('Type','<span class="badge badge-'+({http:'http',queue_publish:'queue',queue_consume:'queue',database:'db',cache:'cache'}[e.type]||'http')+'">'+esc(e.type)+'</span>');
  h+='</div>';
  if(e.details&&e.details.length){
    h+='<div class="sec"><h3>Details</h3>';
    e.details.forEach(d=>{
      h+='<div class="detail-item"><div class="name">'+esc(d.name)+'</div>';
      if(d.summary)h+='<div class="sub">'+esc(d.summary)+'</div>';
      if(d.details){
        const dd=d.details;
        Object.keys(dd).slice(0,8).forEach(k=>{
          h+='<div class="sub"><span style="color:var(--dim)">'+esc(k)+':</span> '+esc(String(dd[k]))+'</div>';
        });
      }
      h+='</div>';
    });
    h+='</div>';
  }
  showSidebar(h);
}

function kv(k,v){return'<div class="row"><span class="k">'+esc(k)+'</span><span class="v">'+v+'</span></div>'}
function esc(s){if(!s)return'';const d=document.createElement('div');d.textContent=String(s);return d.innerHTML}
function escAttr(s){return esc(s).replace(/'/g,'\\&#39;').replace(/"/g,'\\&quot;')}

// ---- INIT ----
(async()=>{await loadRuns();await loadGraph()})();
</script>
</body>
</html>` + ""
