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
.edge{fill:none;stroke-width:1.5;opacity:.55}
.edge:hover{opacity:1;stroke-width:2.5}
.edge.type-http{stroke:var(--blue)}
.edge.type-queue_publish{stroke:var(--green)}
.edge.type-queue_consume{stroke:var(--green);stroke-dasharray:5,3}
.edge.type-database{stroke:var(--orange)}
.edge.type-cache{stroke:var(--red)}
.edge-label{font-size:8px;fill:var(--dim)}
marker path{fill:var(--dim)}
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
  const edges=graphData.edges||[];

  // Node dimensions
  const svcW=180,svcH_base=48;
  const extW=140,extH=36;
  const queueW=160,queueH=32;
  const dbW=140,dbH=32;

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
    g.setNode(s.name,{width:svcW,height:h,type:'service',data:s,badges});
    nodeInfo[s.name]={type:'service',data:s,badges};
  });

  // Add external nodes
  externals.forEach(e=>{
    g.setNode(e.name,{width:extW,height:extH,type:'external',data:e});
    nodeInfo[e.name]={type:'external',data:e};
  });

  // Add queue nodes
  queues.forEach(q=>{
    const id='queue:'+q.id;
    const label=shortLabel(q.name,22);
    g.setNode(id,{width:queueW,height:queueH,type:'queue',data:q,label});
    nodeInfo[id]={type:'queue',data:q};
  });

  // Add database nodes
  databases.forEach(d=>{
    const id='db:'+d.id;
    const label=shortLabel(d.name,20);
    g.setNode(id,{width:dbW,height:dbH,type:'db',data:d,label});
    nodeInfo[id]={type:'db',data:d};
  });

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
  ['http','queue_publish','queue_consume','database','cache'].forEach(type=>{
    const colors={http:'#3b9eff',queue_publish:'#22c997',queue_consume:'#22c997',database:'#f5943a',cache:'#ef5455'};
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
      .attr('marker-end','url(#arr-'+(ed.type||'http')+')')
      .style('cursor','pointer')
      .on('click',ev=>{ev.stopPropagation();showEdgeDetail(ed.data)});
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
      // Service icon + name
      ng.append('text').attr('class','icon-text').attr('x',10).attr('y',18).text(getIcon(n.data));
      ng.append('text').attr('class','title-text').attr('x',26).attr('y',18).text(shortLabel(n.data.name,18));
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
      ng.append('text').attr('class','title-text').attr('x',n.width/2).attr('y',n.height/2+4).attr('text-anchor','middle')
        .text(shortLabel((nodeInfo[nid]||{}).data?.name||nid,18));
    } else if(n.type==='queue'){
      ng.append('text').attr('x',8).attr('y',12).attr('fill','#22c997').attr('font-size',10).text('\u25B6'); // play icon
      ng.append('text').attr('class','title-text').attr('x',22).attr('y',20).text(n.label||'');
    } else if(n.type==='db'){
      ng.append('text').attr('x',6).attr('y',14).attr('fill','#f5943a').attr('font-size',11).text('\u{1F5C4}');
      ng.append('text').attr('class','title-text').attr('x',22).attr('y',20).text(n.label||'');
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

function getIcon(svc){
  const n=svc.name.toLowerCase();
  if(n.includes('api'))return '\u{1F310}';
  if(n.includes('observer')||n.includes('monitor'))return '\u{1F441}';
  if(n.includes('producer')||n.includes('publisher'))return '\u{1F4E4}';
  if(n.includes('translator')||n.includes('transform'))return '\u{1F504}';
  if(n.includes('calculator'))return '\u{1F4CA}';
  if(n.includes('delivery'))return '\u{1F69A}';
  return '\u{2699}';
}

function shortLabel(s,max){
  if(!s)return'';
  s=s.replace(/\$\{[^}]+\}/g,'').replace(/https?:\/\//,'');
  if(s.length<=max)return s;
  return s.slice(0,max-1)+'\u2026';
}

function selectNode(nid,n){
  selectedNode=nid;
  d3.selectAll('[class^="node-"]').classed('selected',false);
  if(nid){
    d3.selectAll('[data-id="'+nid+'"]').classed('selected',true);
    if(n.type==='service')showServiceDetail(n.data);
    else if(n.type==='external')showExternalDetail(n.data||{name:nid});
    else if(n.type==='queue')showQueueDetail(n.data);
    else if(n.type==='db')showDBDetail(n.data);
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
  if(svc){
    selectNode(name,{type:'service',data:svc});
    // Also highlight on graph
    d3.selectAll('[class^="node-"]').classed('selected',false);
    d3.selectAll('[data-id="'+name+'"]').classed('selected',true);
  }
}

function showServiceDetail(svc){
  let h='<div class="sec"><h3>Service</h3>';
  h+=kv('Name','<span style="color:var(--blue);font-weight:700">'+esc(svc.name)+'</span>');
  h+=kv('Known','<span style="color:var(--green)">Yes \u2713</span>');
  h+='</div>';

  // HTTP Routes
  const routes=svc.http_routes||[];
  if(routes.length){
    h+='<div class="sec"><h3>HTTP Endpoints ('+routes.length+')</h3><div class="detail-list">';
    routes.forEach(r=>{
      const d=r.details||{};
      const method=d.method||'';
      const path=d.path||r.name||'';
      h+='<div class="detail-item"><div class="name"><span class="badge badge-http">'+esc(method)+'</span> '+esc(path)+'</div>';
      if(d.controller)h+='<div class="sub">'+esc(d.controller)+'</div>';
      h+='</div>';
    });
    h+='</div></div>';
  }

  // Queue Consumers
  const cons=svc.queue_consumers||[];
  if(cons.length){
    h+='<div class="sec"><h3>Queue Consumers ('+cons.length+')</h3><div class="detail-list">';
    cons.forEach(c=>{
      const d=c.details||{};
      h+='<div class="detail-item"><div class="name"><span class="badge badge-sqs">SQS</span> '+esc(d.queue||c.name)+'</div>';
      if(d.production_url)h+='<div class="sub">'+esc(d.production_url)+'</div>';
      h+='</div>';
    });
    h+='</div></div>';
  }

  // Scheduled Jobs
  const jobs=svc.scheduled_jobs||[];
  if(jobs.length){
    h+='<div class="sec"><h3>Scheduled Jobs ('+jobs.length+')</h3><div class="detail-list">';
    jobs.forEach(j=>{
      const d=j.details||{};
      h+='<div class="detail-item"><div class="name">'+esc(j.name)+'</div>';
      h+='<div class="sub">'+(d.schedule?'cron: '+esc(d.schedule):'')+(d.spring_profile?' | profile: '+esc(d.spring_profile):'')+'</div></div>';
    });
    h+='</div></div>';
  }

  // Connected edges
  const outHTTP=(graphData.edges||[]).filter(e=>e.from===svc.name&&e.type==='http');
  const outQueue=(graphData.edges||[]).filter(e=>e.from===svc.name&&e.type==='queue_publish');
  const inQueue=(graphData.edges||[]).filter(e=>e.to===svc.name&&e.type==='queue_consume');
  const outDB=(graphData.edges||[]).filter(e=>e.from===svc.name&&(e.type==='database'||e.type==='cache'));

  if(outHTTP.length){
    h+='<div class="sec"><h3>Outbound HTTP ('+outHTTP.length+')</h3>';
    outHTTP.forEach(e=>{
      const det=(e.details&&e.details[0])||{};
      const dd=det.details||{};
      h+='<div class="detail-item"><div class="name">\u2192 '+esc(e.to)+'</div>';
      if(dd.endpoints_called)h+='<div class="sub">'+esc(JSON.stringify(dd.endpoints_called))+'</div>';
      h+='</div>';
    });
    h+='</div>';
  }

  if(outDB.length){
    h+='<div class="sec"><h3>Databases ('+outDB.length+')</h3>';
    outDB.forEach(e=>{
      h+='<div class="detail-item"><div class="name"><span class="badge badge-db">'+esc(e.label)+'</span> '+esc(e.to.replace('db:',''))+'</div></div>';
    });
    h+='</div>';
  }

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
  h+=kv('Kind','<span class="badge badge-db">'+esc(db.kind)+'</span>');
  h+='</div>';

  const users=(graphData.edges||[]).filter(e=>e.to==='db:'+db.id);
  if(users.length){
    h+='<div class="sec"><h3>Used By ('+users.length+')</h3>';
    users.forEach(e=>h+='<div class="detail-item"><div class="name">'+esc(e.from)+' <span class="badge badge-db">'+esc(e.label)+'</span></div></div>');
    h+='</div>';
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

// ---- INIT ----
(async()=>{await loadRuns();await loadGraph()})();
</script>
</body>
</html>` + ""
