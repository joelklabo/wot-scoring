package main

import (
	"fmt"
	"net/http"
)

func handleDemo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, demoPageHTML)
}

const demoPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>WoT Explorer — NIP-85 Trust Dashboard</title>
<style>
  :root {
    --bg: #0d1117;
    --surface: #161b22;
    --border: #30363d;
    --text: #e6edf3;
    --muted: #8b949e;
    --accent: #58a6ff;
    --green: #3fb950;
    --yellow: #d29922;
    --red: #f85149;
    --purple: #bc8cff;
  }
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif;
    background: var(--bg);
    color: var(--text);
    min-height: 100vh;
  }
  .header {
    text-align: center;
    padding: 2rem 1rem 1rem;
    border-bottom: 1px solid var(--border);
  }
  .header h1 { font-size: 1.5rem; font-weight: 600; }
  .header h1 span { color: var(--accent); }
  .header p { color: var(--muted); font-size: 0.85rem; margin-top: 0.3rem; }
  .search-bar {
    display: flex;
    justify-content: center;
    padding: 1.5rem 1rem;
    gap: 0.5rem;
  }
  .search-bar input {
    width: 480px;
    max-width: 70vw;
    padding: 0.6rem 1rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--text);
    font-size: 0.9rem;
    outline: none;
  }
  .search-bar input:focus { border-color: var(--accent); }
  .search-bar button {
    padding: 0.6rem 1.2rem;
    border: none;
    border-radius: 6px;
    background: var(--accent);
    color: #fff;
    font-weight: 600;
    cursor: pointer;
    font-size: 0.9rem;
  }
  .search-bar button:hover { opacity: 0.9; }
  .search-bar button:disabled { opacity: 0.5; cursor: wait; }
  #status { text-align: center; color: var(--muted); font-size: 0.85rem; padding: 0.5rem; }
  #status.error { color: var(--red); }
  .dashboard {
    display: none;
    max-width: 1100px;
    margin: 0 auto;
    padding: 1rem;
    gap: 1rem;
  }
  .dashboard.visible { display: grid; grid-template-columns: 1fr 1fr; }
  @media (max-width: 700px) { .dashboard.visible { grid-template-columns: 1fr; } }
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 1.2rem;
  }
  .card h2 { font-size: 0.9rem; color: var(--muted); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 0.8rem; }
  .card.full { grid-column: 1 / -1; }

  /* Trust Score Gauge */
  .gauge-container { display: flex; align-items: center; gap: 1.5rem; }
  .gauge { position: relative; width: 120px; height: 120px; }
  .gauge svg { transform: rotate(-90deg); }
  .gauge circle {
    fill: none;
    stroke-width: 10;
    stroke-linecap: round;
  }
  .gauge .bg { stroke: var(--border); }
  .gauge .fill { stroke: var(--accent); transition: stroke-dashoffset 0.8s ease; }
  .gauge .value {
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%) rotate(0deg);
    font-size: 2rem;
    font-weight: 700;
  }
  .gauge-details { flex: 1; }
  .gauge-details .row { display: flex; justify-content: space-between; padding: 0.3rem 0; border-bottom: 1px solid var(--border); font-size: 0.85rem; }
  .gauge-details .row:last-child { border-bottom: none; }
  .gauge-details .label { color: var(--muted); }

  /* Role Badge */
  .role-badge {
    display: inline-block;
    padding: 0.2rem 0.6rem;
    border-radius: 12px;
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
  }
  .role-hub { background: rgba(88,166,255,0.15); color: var(--accent); }
  .role-authority { background: rgba(188,140,255,0.15); color: var(--purple); }
  .role-connector { background: rgba(63,185,80,0.15); color: var(--green); }
  .role-participant { background: rgba(210,153,34,0.15); color: var(--yellow); }
  .role-consumer, .role-observer, .role-isolated { background: rgba(139,148,158,0.15); color: var(--muted); }

  /* Reputation Bars */
  .rep-row { margin-bottom: 0.6rem; }
  .rep-label { display: flex; justify-content: space-between; font-size: 0.8rem; margin-bottom: 0.2rem; }
  .rep-label .grade { font-weight: 700; }
  .rep-bar { height: 6px; border-radius: 3px; background: var(--border); overflow: hidden; }
  .rep-bar .fill { height: 100%; border-radius: 3px; transition: width 0.6s ease; }
  .grade-a .fill { background: var(--green); }
  .grade-b .fill { background: #3fb990; }
  .grade-c .fill { background: var(--yellow); }
  .grade-d .fill { background: #d97706; }
  .grade-f .fill { background: var(--red); }

  /* Sybil Indicator */
  .sybil-score { font-size: 2.5rem; font-weight: 700; }
  .sybil-label { font-size: 0.85rem; color: var(--muted); margin-top: 0.3rem; }
  .sybil-signals { margin-top: 0.8rem; }
  .sybil-signals .signal { display: flex; justify-content: space-between; font-size: 0.8rem; padding: 0.25rem 0; }

  /* Trust Circle */
  .circle-stats { display: flex; gap: 1rem; margin-bottom: 0.8rem; flex-wrap: wrap; }
  .circle-stat { text-align: center; }
  .circle-stat .num { font-size: 1.3rem; font-weight: 700; color: var(--accent); }
  .circle-stat .lbl { font-size: 0.7rem; color: var(--muted); text-transform: uppercase; }
  .member-list { max-height: 260px; overflow-y: auto; }
  .member {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.4rem 0;
    border-bottom: 1px solid var(--border);
    font-size: 0.8rem;
  }
  .member:last-child { border-bottom: none; }
  .member .rank { color: var(--muted); width: 2ch; text-align: right; }
  .member .pk { font-family: monospace; color: var(--text); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .member .score { font-weight: 600; width: 3ch; text-align: right; }

  /* Influence Simulation */
  .sim-row {
    display: flex;
    gap: 0.5rem;
    margin-bottom: 0.8rem;
    align-items: center;
    flex-wrap: wrap;
  }
  .sim-row input {
    flex: 1;
    min-width: 200px;
    padding: 0.5rem 0.8rem;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--text);
    font-size: 0.85rem;
    font-family: monospace;
    outline: none;
  }
  .sim-row input:focus { border-color: var(--accent); }
  .sim-row button {
    padding: 0.5rem 1rem;
    border: none;
    border-radius: 6px;
    background: var(--purple);
    color: #fff;
    font-weight: 600;
    cursor: pointer;
    font-size: 0.85rem;
    white-space: nowrap;
  }
  .sim-row button:hover { opacity: 0.9; }
  .sim-row button:disabled { opacity: 0.5; cursor: wait; }
  .sim-result { display: none; }
  .sim-result.visible { display: block; }
  .sim-stats { display: flex; gap: 1.2rem; margin-bottom: 0.8rem; flex-wrap: wrap; }
  .sim-stat { text-align: center; }
  .sim-stat .num { font-size: 1.5rem; font-weight: 700; }
  .sim-stat .lbl { font-size: 0.7rem; color: var(--muted); text-transform: uppercase; }
  .sim-affected { max-height: 200px; overflow-y: auto; }
  .sim-affected .aff {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.3rem 0;
    border-bottom: 1px solid var(--border);
    font-size: 0.8rem;
  }
  .sim-affected .aff:last-child { border-bottom: none; }
  .aff .pk { font-family: monospace; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .aff .delta { font-weight: 600; width: 6ch; text-align: right; }
  .aff .dir { width: 1.5ch; text-align: center; }

  /* Follow Quality */
  .quality-header { display: flex; align-items: center; gap: 1rem; margin-bottom: 0.8rem; }
  .quality-score { font-size: 2.2rem; font-weight: 700; }
  .quality-class { font-size: 0.85rem; color: var(--muted); }
  .quality-bars { margin-bottom: 0.8rem; }
  .quality-bar-row { margin-bottom: 0.5rem; }
  .quality-bar-label { display: flex; justify-content: space-between; font-size: 0.75rem; margin-bottom: 0.15rem; }
  .quality-bar-label .val { color: var(--muted); }
  .quality-bar { height: 5px; border-radius: 3px; background: var(--border); overflow: hidden; }
  .quality-bar .fill { height: 100%; border-radius: 3px; transition: width 0.6s ease; }
  .quality-cats { display: flex; gap: 0.8rem; flex-wrap: wrap; margin-bottom: 0.6rem; }
  .quality-cat { text-align: center; flex: 1; min-width: 60px; padding: 0.4rem; border-radius: 6px; background: rgba(255,255,255,0.03); }
  .quality-cat .num { font-size: 1.2rem; font-weight: 700; }
  .quality-cat .lbl { font-size: 0.65rem; color: var(--muted); text-transform: uppercase; }
  .suggestions { max-height: 120px; overflow-y: auto; }
  .suggestion { display: flex; align-items: center; gap: 0.5rem; padding: 0.25rem 0; border-bottom: 1px solid var(--border); font-size: 0.75rem; }
  .suggestion:last-child { border-bottom: none; }
  .suggestion .pk { font-family: monospace; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .suggestion .reason { color: var(--muted); font-size: 0.7rem; }

  /* Compare */
  .compat-score { font-size: 2.5rem; font-weight: 700; text-align: center; }
  .compat-class { font-size: 0.85rem; color: var(--muted); text-align: center; margin-top: 0.2rem; margin-bottom: 0.8rem; }
  .overlap-list { max-height: 200px; overflow-y: auto; }
  .overlap-section { margin-bottom: 0.8rem; }
  .overlap-section h3 { font-size: 0.8rem; color: var(--muted); margin-bottom: 0.4rem; }

  /* Network Health */
  .health-grid { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 0.6rem; margin-bottom: 0.8rem; }
  @media (max-width: 700px) { .health-grid { grid-template-columns: 1fr 1fr; } }
  .health-cell { text-align: center; padding: 0.5rem; border-radius: 6px; background: rgba(255,255,255,0.03); }
  .health-cell .num { font-size: 1.1rem; font-weight: 700; }
  .health-cell .lbl { font-size: 0.65rem; color: var(--muted); text-transform: uppercase; }
  .health-banner {
    display: none;
    max-width: 1100px;
    margin: 0 auto;
    padding: 0 1rem 0.5rem;
  }
  .health-banner.visible { display: block; }

  /* Spam Detection */
  .spam-prob { font-size: 2.2rem; font-weight: 700; }
  .spam-label { font-size: 0.85rem; color: var(--muted); margin-top: 0.2rem; margin-bottom: 0.6rem; }
  .spam-signals { }
  .spam-signal { display: flex; justify-content: space-between; align-items: center; font-size: 0.8rem; padding: 0.25rem 0; border-bottom: 1px solid var(--border); }
  .spam-signal:last-child { border-bottom: none; }
  .spam-signal .bar-bg { width: 60px; height: 5px; background: var(--border); border-radius: 3px; overflow: hidden; }
  .spam-signal .bar-fill { height: 100%; border-radius: 3px; }

  /* Trust Path */
  .path-container { }
  .path-item { margin-bottom: 0.8rem; padding: 0.8rem; border: 1px solid var(--border); border-radius: 6px; background: rgba(255,255,255,0.02); }
  .path-header { display: flex; justify-content: space-between; font-size: 0.8rem; margin-bottom: 0.5rem; }
  .path-hops { display: flex; align-items: center; gap: 0.3rem; font-size: 0.75rem; flex-wrap: wrap; }
  .path-node { padding: 0.2rem 0.4rem; border-radius: 4px; background: rgba(88,166,255,0.1); font-family: monospace; }
  .path-arrow { color: var(--muted); }
  .path-node.mutual { border: 1px solid var(--green); }

  /* Footer */
  .footer { text-align: center; padding: 2rem; color: var(--muted); font-size: 0.75rem; }
  .footer a { color: var(--accent); text-decoration: none; }
</style>
</head>
<body>

<div class="header">
  <h1><span>WoT</span> Explorer</h1>
  <p>NIP-85 Trust Dashboard — Enter a Nostr pubkey to explore their Web of Trust profile</p>
</div>

<div class="search-bar">
  <input type="text" id="pubkeyInput" placeholder="npub1... or hex pubkey" autofocus>
  <button id="searchBtn" onclick="doSearch()">Explore</button>
</div>
<div id="status"></div>

<div class="health-banner" id="healthBanner">
  <div class="card">
    <h2>Network Health</h2>
    <div id="healthContent"><span style="color:var(--muted);font-size:0.85rem;">Loading network stats...</span></div>
  </div>
</div>

<div class="dashboard" id="dashboard">
  <div class="card" id="scoreCard">
    <h2>Trust Score</h2>
    <div class="gauge-container">
      <div class="gauge">
        <svg viewBox="0 0 120 120">
          <circle class="bg" cx="60" cy="60" r="50"></circle>
          <circle class="fill" id="gaugeCircle" cx="60" cy="60" r="50"
            stroke-dasharray="314.16" stroke-dashoffset="314.16"></circle>
        </svg>
        <div class="value" id="scoreValue">—</div>
      </div>
      <div class="gauge-details" id="scoreDetails"></div>
    </div>
  </div>

  <div class="card" id="sybilCard">
    <h2>Sybil Resistance</h2>
    <div id="sybilContent"></div>
  </div>

  <div class="card" id="reputationCard">
    <h2>Reputation</h2>
    <div id="reputationContent"></div>
  </div>

  <div class="card" id="circleCard">
    <h2>Trust Circle</h2>
    <div id="circleContent"></div>
  </div>

  <div class="card" id="qualityCard">
    <h2>Follow Quality</h2>
    <div id="qualityContent"></div>
  </div>

  <div class="card" id="spamCard">
    <h2>Spam Detection</h2>
    <div id="spamContent"></div>
  </div>

  <div class="card" id="anomalyCard">
    <h2>Anomaly Detection</h2>
    <div id="anomalyContent"></div>
  </div>

  <div class="card" id="auditCard">
    <h2>Score Audit — Why This Score?</h2>
    <div id="auditContent"></div>
  </div>

  <div class="card" id="recommendCard">
    <h2>Recommended Follows</h2>
    <div id="recommendContent"></div>
  </div>

  <div class="card full" id="compareCard">
    <h2>Trust Circle Compare</h2>
    <p style="font-size:0.8rem;color:var(--muted);margin-bottom:0.8rem;">
      Compare trust circles with another pubkey to see compatibility and shared connections.
    </p>
    <div class="sim-row">
      <input type="text" id="compareTarget" placeholder="Compare with... (npub1... or hex pubkey)">
      <button id="compareBtn" onclick="runCompare()" style="background:var(--green);">Compare Circles</button>
    </div>
    <div id="compareStatus" style="font-size:0.8rem;color:var(--muted);margin-bottom:0.5rem;"></div>
    <div class="sim-result" id="compareResult">
      <div class="sim-stats" id="compareStats"></div>
      <div id="compareDetails"></div>
    </div>
  </div>

  <div class="card full" id="influenceCard">
    <h2>Influence Simulation — What If?</h2>
    <p style="font-size:0.8rem;color:var(--muted);margin-bottom:0.8rem;">
      Simulate an unfollow and see how trust scores ripple across the network.
    </p>
    <div class="sim-row">
      <input type="text" id="simTarget" placeholder="Target pubkey to unfollow (npub1... or hex)">
      <button id="simBtn" onclick="runSimulation()">Simulate Unfollow</button>
    </div>
    <div id="simStatus" style="font-size:0.8rem;color:var(--muted);margin-bottom:0.5rem;"></div>
    <div class="sim-result" id="simResult">
      <div class="sim-stats" id="simStats"></div>
      <div class="sim-affected" id="simAffected"></div>
    </div>
  </div>

  <div class="card full" id="pathCard">
    <h2>Trust Path Finder</h2>
    <p style="font-size:0.8rem;color:var(--muted);margin-bottom:0.8rem;">
      Discover how trust flows between two identities through the network.
    </p>
    <div class="sim-row">
      <input type="text" id="pathTarget" placeholder="Target pubkey (npub1... or hex)">
      <button id="pathBtn" onclick="runPathFinder()" style="background:#d29922;">Find Trust Paths</button>
    </div>
    <div id="pathStatus" style="font-size:0.8rem;color:var(--muted);margin-bottom:0.5rem;"></div>
    <div class="sim-result" id="pathResult">
      <div class="sim-stats" id="pathStats"></div>
      <div class="path-container" id="pathPaths"></div>
    </div>
  </div>

  <div class="card full" id="predictCard">
    <h2>Link Prediction — Will They Follow?</h2>
    <p style="font-size:0.8rem;color:var(--muted);margin-bottom:0.8rem;">
      Predict the likelihood of a follow relationship between two pubkeys using graph topology signals.
    </p>
    <div class="sim-row">
      <input type="text" id="predictTarget" placeholder="Target pubkey (npub1... or hex)">
      <button id="predictBtn" onclick="runPredict()" style="background:var(--accent);">Predict</button>
    </div>
    <div id="predictStatus" style="font-size:0.8rem;color:var(--muted);margin-bottom:0.5rem;"></div>
    <div class="sim-result" id="predictResult">
      <div class="sim-stats" id="predictStats"></div>
      <div id="predictDetails"></div>
    </div>
  </div>
</div>

<div class="footer">
  Powered by <a href="/">WoT Scoring API</a> — NIP-85 Trusted Assertions
  · <a href="/docs">API Docs</a> · <a href="/swagger">Swagger</a> · <a href="/openapi.json">OpenAPI</a>
</div>

<script>
const $ = s => document.querySelector(s);
const input = $('#pubkeyInput');
const status = $('#status');
const dashboard = $('#dashboard');
const btn = $('#searchBtn');

input.addEventListener('keydown', e => { if (e.key === 'Enter') doSearch(); });

async function doSearch() {
  const raw = input.value.trim();
  if (!raw) return;
  btn.disabled = true;
  status.className = '';
  status.textContent = 'Loading...';
  dashboard.classList.remove('visible');

  try {
    const base = window.location.origin;
    const pk = encodeURIComponent(raw);

    const [scoreRes, sybilRes, repRes, circleRes, influenceRes, qualityRes, spamRes, anomalyRes, auditRes, recommendRes] = await Promise.all([
      fetch(base + '/score?pubkey=' + pk).then(r => r.json()),
      fetch(base + '/sybil?pubkey=' + pk).then(r => r.json()),
      fetch(base + '/reputation?pubkey=' + pk).then(r => r.json()),
      fetch(base + '/trust-circle?pubkey=' + pk).then(r => r.json()),
      fetch(base + '/influence/batch', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({pubkeys: [raw]})
      }).then(r => r.json()),
      fetch(base + '/follow-quality?pubkey=' + pk + '&suggestions=5').then(r => r.json()),
      fetch(base + '/spam?pubkey=' + pk).then(r => r.json()),
      fetch(base + '/anomalies?pubkey=' + pk).then(r => r.json()),
      fetch(base + '/audit?pubkey=' + pk).then(r => r.json()),
      fetch(base + '/recommend?pubkey=' + pk + '&limit=8').then(r => r.json())
    ]);

    if (scoreRes.error) throw new Error(scoreRes.error);

    currentPubkey = raw;
    renderScore(scoreRes, influenceRes);
    renderSybil(sybilRes);
    renderReputation(repRes);
    renderCircle(circleRes);
    renderQuality(qualityRes);
    renderSpam(spamRes);
    renderAnomalies(anomalyRes);
    renderAudit(auditRes);
    renderRecommend(recommendRes);

    dashboard.classList.add('visible');
    status.textContent = '';
  } catch (e) {
    status.className = 'error';
    status.textContent = 'Error: ' + e.message;
  } finally {
    btn.disabled = false;
  }
}

function scoreColor(score) {
  if (score >= 70) return 'var(--green)';
  if (score >= 40) return 'var(--yellow)';
  if (score >= 20) return 'var(--accent)';
  return 'var(--red)';
}

function renderScore(data, influence) {
  const score = data.score ?? data.normalized_score ?? 0;
  const pct = score / 100;
  const circ = 314.16;
  const circle = $('#gaugeCircle');
  circle.style.strokeDashoffset = circ * (1 - pct);
  circle.style.stroke = scoreColor(score);
  $('#scoreValue').textContent = score;
  $('#scoreValue').style.color = scoreColor(score);

  const inf = influence.results?.[0] || {};
  const role = inf.classification || '—';
  const roleCls = 'role-' + role;

  let details = '';
  details += row('Rank', '#' + (data.rank || inf.rank || '—'));
  details += row('Percentile', ((data.percentile || inf.percentile || 0) * 100).toFixed(1) + '%');
  details += row('Followers', inf.followers || '—');
  details += row('Following', inf.follows || '—');
  details += row('Mutuals', inf.mutual_count || '—');
  details += row('Role', '<span class="role-badge ' + roleCls + '">' + role + '</span>');
  $('#scoreDetails').innerHTML = details;
}

function row(label, value) {
  return '<div class="row"><span class="label">' + label + '</span><span>' + value + '</span></div>';
}

function renderSybil(data) {
  const score = data.sybil_score ?? 0;
  const cls = data.classification || '—';
  let color = 'var(--green)';
  if (score < 50) color = 'var(--yellow)';
  if (score < 25) color = 'var(--red)';

  let html = '<div class="sybil-score" style="color:' + color + '">' + score + '</div>';
  html += '<div class="sybil-label">' + cls + '</div>';

  if (data.signals) {
    html += '<div class="sybil-signals">';
    for (const [key, val] of Object.entries(data.signals)) {
      const label = key.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
      html += '<div class="signal"><span>' + label + '</span><span>' + (typeof val === 'number' ? val.toFixed(2) : val) + '</span></div>';
    }
    html += '</div>';
  }
  $('#sybilContent').innerHTML = html;
}

function renderReputation(data) {
  const components = data.components || [];
  let html = '';
  for (const c of components) {
    const pct = ((c.score || 0) * 100).toFixed(0);
    const grade = c.grade || '?';
    const gCls = 'grade-' + grade.toLowerCase();
    html += '<div class="rep-row ' + gCls + '">';
    html += '<div class="rep-label"><span>' + c.name + '</span><span class="grade">' + grade + ' (' + pct + '%)</span></div>';
    html += '<div class="rep-bar"><div class="fill" style="width:' + pct + '%"></div></div>';
    html += '</div>';
  }
  const total = data.reputation_score ?? 0;
  html += '<div style="margin-top:0.8rem;font-size:0.9rem;">Overall: <strong>' + total + '/100</strong> — ' + (data.grade || '') + '</div>';
  $('#reputationContent').innerHTML = html;
}

function renderCircle(data) {
  const members = data.members || [];
  const inner = data.inner_circle || [];
  const metrics = data.metrics || {};

  let html = '<div class="circle-stats">';
  html += stat(data.circle_size || 0, 'Members');
  html += stat((metrics.avg_trust_score || 0).toFixed(0), 'Avg Score');
  html += stat(((metrics.cohesion || 0) * 100).toFixed(0) + '%', 'Cohesion');
  html += stat(((metrics.density || 0) * 100).toFixed(0) + '%', 'Density');
  html += '</div>';

  html += '<div class="member-list">';
  const show = inner.length > 0 ? inner : members.slice(0, 10);
  show.forEach((m, i) => {
    const pk = m.pubkey.slice(0, 8) + '...' + m.pubkey.slice(-6);
    const roleCls = 'role-' + m.classification;
    html += '<div class="member">';
    html += '<span class="rank">' + (i + 1) + '</span>';
    html += '<span class="pk">' + pk + '</span>';
    html += '<span class="role-badge ' + roleCls + '">' + m.classification + '</span>';
    html += '<span class="score" style="color:' + scoreColor(m.trust_score) + '">' + m.trust_score + '</span>';
    html += '</div>';
  });
  html += '</div>';

  if (metrics.role_counts) {
    html += '<div style="margin-top:0.6rem;font-size:0.75rem;color:var(--muted)">';
    html += Object.entries(metrics.role_counts).map(([r,c]) => r + ': ' + c).join(' · ');
    html += '</div>';
  }

  $('#circleContent').innerHTML = html;
}

function stat(num, label) {
  return '<div class="circle-stat"><div class="num">' + num + '</div><div class="lbl">' + label + '</div></div>';
}

function qualityColor(score) {
  if (score >= 75) return 'var(--green)';
  if (score >= 50) return 'var(--yellow)';
  if (score >= 25) return 'var(--accent)';
  return 'var(--red)';
}

function renderQuality(data) {
  const qs = data.quality_score ?? 0;
  const cls = data.classification || '—';

  let html = '<div class="quality-header">';
  html += '<div class="quality-score" style="color:' + qualityColor(qs) + '">' + qs + '</div>';
  html += '<div><div style="font-size:0.9rem;font-weight:600;">' + cls + '</div>';
  html += '<div class="quality-class">' + (data.follow_count || 0) + ' follows analyzed</div></div>';
  html += '</div>';

  const bd = data.breakdown || {};
  html += '<div class="quality-bars">';
  html += qualBar('Avg Trust', bd.avg_trust_score, 100);
  html += qualBar('Reciprocity', bd.reciprocity * 100, 100);
  html += qualBar('Diversity', bd.diversity * 100, 100);
  html += qualBar('Signal Ratio', bd.signal_ratio * 100, 100);
  html += '</div>';

  const cats = data.categories || {};
  html += '<div class="quality-cats">';
  html += '<div class="quality-cat"><div class="num" style="color:var(--green)">' + (cats.strong || 0) + '</div><div class="lbl">Strong</div></div>';
  html += '<div class="quality-cat"><div class="num" style="color:var(--yellow)">' + (cats.moderate || 0) + '</div><div class="lbl">Moderate</div></div>';
  html += '<div class="quality-cat"><div class="num" style="color:var(--red)">' + (cats.weak || 0) + '</div><div class="lbl">Weak</div></div>';
  html += '<div class="quality-cat"><div class="num" style="color:var(--muted)">' + (cats.unknown || 0) + '</div><div class="lbl">Unknown</div></div>';
  html += '</div>';

  const sugg = data.suggestions || [];
  if (sugg.length > 0) {
    html += '<div style="font-size:0.75rem;color:var(--muted);margin-bottom:0.3rem;">Follows to reconsider:</div>';
    html += '<div class="suggestions">';
    sugg.forEach(s => {
      const pk = s.pubkey.slice(0, 8) + '...' + s.pubkey.slice(-6);
      html += '<div class="suggestion">';
      html += '<span class="pk">' + pk + '</span>';
      html += '<span class="score" style="color:var(--red)">' + s.trust_score + '</span>';
      html += '<span class="reason">' + s.reason + '</span>';
      html += '</div>';
    });
    html += '</div>';
  }

  $('#qualityContent').innerHTML = html;
}

function qualBar(label, value, max) {
  const pct = Math.min((value / max) * 100, 100).toFixed(0);
  const color = value >= 60 ? 'var(--green)' : value >= 30 ? 'var(--yellow)' : 'var(--red)';
  return '<div class="quality-bar-row">' +
    '<div class="quality-bar-label"><span>' + label + '</span><span class="val">' + (typeof value === 'number' ? value.toFixed(1) : value) + '</span></div>' +
    '<div class="quality-bar"><div class="fill" style="width:' + pct + '%;background:' + color + '"></div></div>' +
    '</div>';
}

let currentPubkey = '';

// Auto-load network health on page open
(async function loadHealth() {
  try {
    const base = window.location.origin;
    const data = await fetch(base + '/network-health').then(r => r.json());
    if (data.error) throw new Error(data.error);

    const hs = data.health_score ?? 0;
    let color = 'var(--green)';
    if (hs < 60) color = 'var(--yellow)';
    if (hs < 30) color = 'var(--red)';

    const ds = data.degree_stats || {};
    const sd = data.score_distribution || {};
    const cn = data.connectivity || {};

    let html = '<div class="health-grid">';
    html += hCell(hs, 'Health Score', color);
    html += hCell((data.graph_size || 0).toLocaleString(), 'Nodes');
    html += hCell((data.edge_count || 0).toLocaleString(), 'Edges');
    html += hCell(((data.reciprocity || 0) * 100).toFixed(1) + '%', 'Reciprocity');
    html += hCell(sd.gini_coefficient?.toFixed(3) || '—', 'Gini Coeff');
    html += hCell(sd.centralization || '—', 'Centralization');
    html += hCell(ds.power_law_alpha?.toFixed(2) || '—', 'Power Law α');
    html += hCell((cn.largest_component_percent || 0).toFixed(1) + '%', 'Largest Component');
    html += hCell(data.classification || '—', 'Classification');
    html += '</div>';

    const hubs = data.top_hubs || [];
    if (hubs.length > 0) {
      html += '<div style="font-size:0.75rem;color:var(--muted);margin-bottom:0.3rem;">Top Hubs</div>';
      html += '<div class="member-list" style="max-height:120px;">';
      hubs.slice(0, 5).forEach((h, i) => {
        const pk = h.pubkey.slice(0, 8) + '...' + h.pubkey.slice(-6);
        html += '<div class="member"><span class="rank">' + (i+1) + '</span><span class="pk">' + pk + '</span><span class="score" style="color:var(--accent)">' + h.score + '</span></div>';
      });
      html += '</div>';
    }

    $('#healthContent').innerHTML = html;
    $('#healthBanner').classList.add('visible');
  } catch (e) {
    $('#healthContent').innerHTML = '<span style="color:var(--red);font-size:0.8rem;">Failed to load: ' + e.message + '</span>';
    $('#healthBanner').classList.add('visible');
  }
})();

function hCell(num, label, color) {
  const style = color ? ' style="color:' + color + '"' : '';
  return '<div class="health-cell"><div class="num"' + style + '>' + num + '</div><div class="lbl">' + label + '</div></div>';
}

function spamColor(prob) {
  if (prob <= 0.3) return 'var(--green)';
  if (prob <= 0.6) return 'var(--yellow)';
  return 'var(--red)';
}

function renderSpam(data) {
  if (data.error) { $('#spamContent').innerHTML = '<span style="color:var(--muted);font-size:0.8rem;">' + data.error + '</span>'; return; }
  const prob = data.spam_probability ?? 0;
  const cls = data.classification || '—';
  const color = spamColor(prob);

  let html = '<div class="spam-prob" style="color:' + color + '">' + (prob * 100).toFixed(0) + '%</div>';
  html += '<div class="spam-label">' + cls + '</div>';

  const signals = data.signals || [];
  if (signals.length > 0) {
    html += '<div class="spam-signals">';
    signals.forEach(s => {
      const pct = Math.min((s.score || 0) * 100, 100).toFixed(0);
      const barColor = s.score <= 0.3 ? 'var(--green)' : s.score <= 0.6 ? 'var(--yellow)' : 'var(--red)';
      html += '<div class="spam-signal">';
      html += '<span>' + (s.name || '').replace(/_/g, ' ') + '</span>';
      html += '<div class="bar-bg"><div class="bar-fill" style="width:' + pct + '%;background:' + barColor + '"></div></div>';
      html += '</div>';
    });
    html += '</div>';
  }

  if (data.summary) {
    html += '<div style="font-size:0.75rem;color:var(--muted);margin-top:0.5rem;">' + data.summary + '</div>';
  }

  $('#spamContent').innerHTML = html;
}

function renderAnomalies(data) {
  if (data.error) { $('#anomalyContent').innerHTML = '<span style="color:var(--muted);font-size:0.8rem;">' + data.error + '</span>'; return; }
  const risk = data.risk_level || 'clean';
  const count = data.anomaly_count || 0;
  let rColor = 'var(--green)';
  if (risk === 'medium') rColor = 'var(--yellow)';
  if (risk === 'high') rColor = 'var(--red)';
  if (risk === 'low') rColor = 'var(--accent)';

  let html = '<div style="display:flex;align-items:center;gap:1rem;margin-bottom:0.8rem;">';
  html += '<div class="spam-prob" style="color:' + rColor + '">' + count + '</div>';
  html += '<div><div style="font-size:0.9rem;font-weight:600;">' + risk.toUpperCase() + ' Risk</div>';
  html += '<div style="font-size:0.8rem;color:var(--muted);">' + (data.followers || 0) + ' followers · ' + (data.follows || 0) + ' following</div></div>';
  html += '</div>';

  const anomalies = data.anomalies || [];
  if (anomalies.length > 0) {
    anomalies.forEach(a => {
      let sevColor = 'var(--accent)';
      if (a.severity === 'medium') sevColor = 'var(--yellow)';
      if (a.severity === 'high') sevColor = 'var(--red)';
      html += '<div style="padding:0.4rem 0;border-bottom:1px solid var(--border);font-size:0.8rem;">';
      html += '<span style="color:' + sevColor + ';font-weight:600;text-transform:uppercase;font-size:0.7rem;">' + a.severity + '</span> ';
      html += '<span>' + (a.type || '').replace(/_/g, ' ') + '</span>';
      html += '<div style="color:var(--muted);font-size:0.75rem;">' + (a.description || '') + '</div>';
      html += '</div>';
    });
  } else {
    html += '<div style="color:var(--green);font-size:0.8rem;">No anomalies detected — account looks clean.</div>';
  }

  const gbr = data.ghost_ratio ?? 0;
  const fbr = data.follow_back_ratio ?? 0;
  html += '<div style="font-size:0.7rem;color:var(--muted);margin-top:0.5rem;">Ghost followers: ' + ((gbr*100).toFixed(1)) + '% · Follow-back ratio: ' + ((fbr*100).toFixed(1)) + '%</div>';

  $('#anomalyContent').innerHTML = html;
}

async function runCompare() {
  const target = $('#compareTarget').value.trim();
  if (!target || !currentPubkey) return;
  const cmpBtn = $('#compareBtn');
  const cmpStatus = $('#compareStatus');
  const cmpResult = $('#compareResult');
  cmpBtn.disabled = true;
  cmpStatus.textContent = 'Comparing trust circles...';
  cmpResult.classList.remove('visible');

  try {
    const base = window.location.origin;
    const res = await fetch(base + '/trust-circle/compare?pubkey1=' + encodeURIComponent(currentPubkey) + '&pubkey2=' + encodeURIComponent(target));
    const data = await res.json();
    if (data.error) throw new Error(data.error);

    const compat = data.compatibility || {};
    const cScore = compat.score ?? 0;
    let color = 'var(--green)';
    if (cScore < 50) color = 'var(--yellow)';
    if (cScore < 25) color = 'var(--red)';

    let statsHtml = '';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:' + color + '">' + cScore + '</div><div class="lbl">Compatibility</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:var(--accent)">' + (compat.overlap_count || 0) + '</div><div class="lbl">Shared Trusted</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num">' + (data.circle_size_1 || 0) + '</div><div class="lbl">Circle 1</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num">' + (data.circle_size_2 || 0) + '</div><div class="lbl">Circle 2</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:var(--muted)">' + ((compat.overlap_ratio || 0) * 100).toFixed(1) + '%</div><div class="lbl">Jaccard</div></div>';
    $('#compareStats').innerHTML = statsHtml;

    let detHtml = '';
    const overlap = data.overlap || [];
    if (overlap.length > 0) {
      detHtml += '<div class="overlap-section"><h3>Shared Trusted Connections (' + overlap.length + ')</h3>';
      detHtml += '<div class="overlap-list">';
      overlap.slice(0, 15).forEach(m => {
        const pk = m.pubkey.slice(0, 8) + '...' + m.pubkey.slice(-6);
        detHtml += '<div class="member">';
        detHtml += '<span class="pk">' + pk + '</span>';
        detHtml += '<span class="score" style="color:' + scoreColor(m.trust_score) + '">' + m.trust_score + '</span>';
        detHtml += '</div>';
      });
      if (overlap.length > 15) detHtml += '<div style="font-size:0.75rem;color:var(--muted);padding:0.3rem 0;">+ ' + (overlap.length - 15) + ' more</div>';
      detHtml += '</div></div>';
    } else {
      detHtml += '<div style="color:var(--muted);font-size:0.8rem;">No shared trusted connections found.</div>';
    }

    const cls = compat.classification || '';
    detHtml += '<div style="font-size:0.75rem;color:var(--muted);margin-top:0.5rem;">Compatibility: ' + cls + ' — ' + (compat.shared_follows || 0) + ' shared follows (' + ((compat.shared_ratio || 0) * 100).toFixed(1) + '% overlap)</div>';

    $('#compareDetails').innerHTML = detHtml;
    cmpResult.classList.add('visible');
    cmpStatus.textContent = 'Comparison complete — ' + (compat.overlap_count || 0) + ' shared trusted connections.';
  } catch (e) {
    cmpStatus.textContent = 'Error: ' + e.message;
    cmpStatus.style.color = 'var(--red)';
  } finally {
    cmpBtn.disabled = false;
  }
}

async function runSimulation() {
  const target = $('#simTarget').value.trim();
  if (!target || !currentPubkey) return;
  const simBtn = $('#simBtn');
  const simStatus = $('#simStatus');
  const simResult = $('#simResult');
  simBtn.disabled = true;
  simStatus.textContent = 'Running PageRank simulation...';
  simResult.classList.remove('visible');

  try {
    const base = window.location.origin;
    const res = await fetch(base + '/influence?pubkey=' + encodeURIComponent(currentPubkey) + '&other=' + encodeURIComponent(target) + '&action=unfollow');
    const data = await res.json();
    if (data.error) throw new Error(data.error);

    let statsHtml = '';
    const ac = data.affected_count || 0;
    statsHtml += '<div class="sim-stat"><div class="num" style="color:var(--purple)">' + ac.toLocaleString() + '</div><div class="lbl">Nodes Affected</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:var(--accent)">' + (data.summary?.influence_radius || '—') + '</div><div class="lbl">Radius</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:var(--green)">' + (data.summary?.total_positive || 0).toLocaleString() + '</div><div class="lbl">Score Increases</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:var(--red)">' + (data.summary?.total_negative || 0).toLocaleString() + '</div><div class="lbl">Score Decreases</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num">' + (data.summary?.classification || '—') + '</div><div class="lbl">Impact</div></div>';
    $('#simStats').innerHTML = statsHtml;

    let affHtml = '';
    const top = data.top_affected || [];
    top.forEach(a => {
      const pk = a.pubkey.slice(0, 8) + '...' + a.pubkey.slice(-6);
      const arrow = a.direction === 'increase' ? '↑' : '↓';
      const color = a.direction === 'increase' ? 'var(--green)' : 'var(--red)';
      const d = a.raw_delta ? a.raw_delta.toExponential(1) : '0';
      affHtml += '<div class="aff">';
      affHtml += '<span class="dir" style="color:' + color + '">' + arrow + '</span>';
      affHtml += '<span class="pk">' + pk + '</span>';
      affHtml += '<span class="delta" style="color:' + color + '">' + d + '</span>';
      affHtml += '</div>';
    });
    if (top.length === 0) {
      affHtml = '<div style="color:var(--muted);font-size:0.8rem;">No significant changes detected.</div>';
    }
    $('#simAffected').innerHTML = affHtml;

    simResult.classList.add('visible');
    simStatus.textContent = 'Simulation complete — ' + ac.toLocaleString() + ' nodes affected across ' + (data.graph_size || 0).toLocaleString() + ' total.';
  } catch (e) {
    simStatus.textContent = 'Error: ' + e.message;
    simStatus.style.color = 'var(--red)';
  } finally {
    simBtn.disabled = false;
  }
}

async function runPathFinder() {
  const target = $('#pathTarget').value.trim();
  if (!target || !currentPubkey) return;
  const pathBtn = $('#pathBtn');
  const pathStatus = $('#pathStatus');
  const pathResult = $('#pathResult');
  pathBtn.disabled = true;
  pathStatus.textContent = 'Finding trust paths...';
  pathResult.classList.remove('visible');

  try {
    const base = window.location.origin;
    const res = await fetch(base + '/trust-path?from=' + encodeURIComponent(currentPubkey) + '&to=' + encodeURIComponent(target) + '&max_paths=3');
    const data = await res.json();
    if (data.error) throw new Error(data.error);

    let color = 'var(--green)';
    const bt = data.best_trust || 0;
    if (bt < 0.3) color = 'var(--yellow)';
    if (bt < 0.1) color = 'var(--red)';

    let statsHtml = '';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:' + color + '">' + (bt * 100).toFixed(0) + '%</div><div class="lbl">Best Trust</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:var(--accent)">' + (data.path_diversity || 0) + '</div><div class="lbl">Paths Found</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num">' + (data.overall_trust ? (data.overall_trust * 100).toFixed(0) + '%' : '—') + '</div><div class="lbl">Overall Trust</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num">' + (data.classification || '—') + '</div><div class="lbl">Strength</div></div>';
    $('#pathStats').innerHTML = statsHtml;

    let pathsHtml = '';
    const paths = data.paths || [];
    if (paths.length > 0) {
      paths.forEach((p, i) => {
        pathsHtml += '<div class="path-item">';
        pathsHtml += '<div class="path-header"><span>Path ' + (i+1) + ' (' + p.length + ' hops)</span><span style="color:var(--accent)">' + (p.trust_score * 100).toFixed(1) + '% trust</span></div>';
        pathsHtml += '<div class="path-hops">';
        const hops = p.hops || [];
        hops.forEach((h, j) => {
          const pk = h.pubkey.slice(0, 6) + '..' + h.pubkey.slice(-4);
          const mutualCls = h.is_mutual ? ' mutual' : '';
          pathsHtml += '<span class="path-node' + mutualCls + '" title="WoT: ' + h.wot_score + (h.is_mutual ? ' (mutual)' : '') + '">' + pk + ' <small style="color:var(--muted)">' + h.wot_score + '</small></span>';
          if (j < hops.length - 1) pathsHtml += '<span class="path-arrow">→</span>';
        });
        pathsHtml += '</div></div>';
      });
    } else if (!data.connected) {
      pathsHtml = '<div style="color:var(--muted);font-size:0.8rem;">No trust path found — these identities are not connected in the graph.</div>';
    }
    $('#pathPaths').innerHTML = pathsHtml;

    pathResult.classList.add('visible');
    pathStatus.textContent = data.connected ? 'Found ' + (data.path_diversity || 0) + ' trust path(s) — ' + (data.classification || '') + ' connection.' : 'No connection found.';
  } catch (e) {
    pathStatus.textContent = 'Error: ' + e.message;
    pathStatus.style.color = 'var(--red)';
  } finally {
    pathBtn.disabled = false;
  }
}

async function runPredict() {
  const target = $('#predictTarget').value.trim();
  if (!target || !currentPubkey) return;
  const predBtn = $('#predictBtn');
  const predStatus = $('#predictStatus');
  const predResult = $('#predictResult');
  predBtn.disabled = true;
  predStatus.textContent = 'Analyzing graph topology...';
  predResult.classList.remove('visible');

  try {
    const base = window.location.origin;
    const res = await fetch(base + '/predict?source=' + encodeURIComponent(currentPubkey) + '&target=' + encodeURIComponent(target));
    const data = await res.json();
    if (data.error) throw new Error(data.error);

    const pred = data.prediction ?? 0;
    let color = 'var(--green)';
    if (pred < 0.5) color = 'var(--yellow)';
    if (pred < 0.3) color = 'var(--muted)';
    if (pred < 0.1) color = 'var(--red)';

    let statsHtml = '';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:' + color + '">' + (pred * 100).toFixed(0) + '%</div><div class="lbl">Prediction</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num">' + ((data.confidence || 0) * 100).toFixed(0) + '%</div><div class="lbl">Confidence</div></div>';
    statsHtml += '<div class="sim-stat"><div class="num" style="color:var(--accent)">' + (data.classification || '—') + '</div><div class="lbl">Likelihood</div></div>';
    if (data.already_follows) {
      statsHtml += '<div class="sim-stat"><div class="num" style="color:var(--green)">Yes</div><div class="lbl">Already Follows</div></div>';
    }
    $('#predictStats').innerHTML = statsHtml;

    let detHtml = '';
    const signals = data.signals || [];
    if (signals.length > 0) {
      detHtml += '<div style="font-size:0.75rem;color:var(--muted);margin-bottom:0.4rem;">Topology Signals</div>';
      signals.forEach(s => {
        const pct = Math.min((s.normalized || 0) * 100, 100).toFixed(0);
        const barColor = s.normalized >= 0.5 ? 'var(--green)' : s.normalized >= 0.2 ? 'var(--yellow)' : 'var(--muted)';
        detHtml += '<div class="spam-signal">';
        detHtml += '<span>' + (s.name || '').replace(/_/g, ' ') + ' <small style="color:var(--muted)">(' + ((s.weight || 0) * 100).toFixed(0) + '%)</small></span>';
        detHtml += '<div class="bar-bg"><div class="bar-fill" style="width:' + pct + '%;background:' + barColor + '"></div></div>';
        detHtml += '</div>';
      });
    }

    const mutuals = data.top_mutuals || [];
    if (mutuals.length > 0) {
      detHtml += '<div style="font-size:0.75rem;color:var(--muted);margin-top:0.6rem;margin-bottom:0.3rem;">Shared Connections (' + mutuals.length + ')</div>';
      detHtml += '<div class="member-list" style="max-height:120px;">';
      mutuals.slice(0, 8).forEach(m => {
        const pk = m.pubkey.slice(0, 8) + '...' + m.pubkey.slice(-6);
        detHtml += '<div class="member"><span class="pk">' + pk + '</span><span class="score" style="color:' + scoreColor(m.wot_score) + '">' + m.wot_score + '</span></div>';
      });
      detHtml += '</div>';
    }

    $('#predictDetails').innerHTML = detHtml;
    predResult.classList.add('visible');
    predStatus.textContent = 'Prediction: ' + (data.classification || '') + ' (' + (pred * 100).toFixed(0) + '% probability)';
  } catch (e) {
    predStatus.textContent = 'Error: ' + e.message;
    predStatus.style.color = 'var(--red)';
  } finally {
    predBtn.disabled = false;
  }
}

function renderAudit(data) {
  if (data.error || data.status === 'payment_required') {
    $('#auditContent').innerHTML = '<span style="color:var(--muted);font-size:0.8rem;">' + (data.error || data.message || 'Unavailable') + '</span>';
    return;
  }
  const pr = data.pagerank || {};
  const eng = data.engagement || {};
  const comp = data.composite || {};
  const finalScore = comp.final_score ?? data.final_score ?? pr.normalized_score ?? 0;

  let html = '<div style="display:flex;align-items:center;gap:1rem;margin-bottom:0.8rem;">';
  html += '<div class="spam-prob" style="color:' + scoreColor(finalScore) + '">' + finalScore + '</div>';
  html += '<div><div style="font-size:0.85rem;font-weight:600;">Final Score</div>';
  html += '<div style="font-size:0.75rem;color:var(--muted);">Rank #' + (pr.rank || '?') + ' · Top ' + ((pr.percentile || 0) * 100).toFixed(1) + '%</div></div>';
  html += '</div>';

  html += '<div style="font-size:0.75rem;color:var(--muted);margin-bottom:0.4rem;">PageRank Breakdown</div>';
  html += '<div style="display:grid;grid-template-columns:1fr 1fr;gap:0.3rem 1rem;font-size:0.8rem;">';
  html += '<div>Raw Score: <span style="color:var(--accent)">' + (pr.raw_score ? pr.raw_score.toExponential(2) : '—') + '</span></div>';
  html += '<div>Normalized: <span style="color:' + scoreColor(pr.normalized_score || 0) + '">' + (pr.normalized_score || 0) + '/100</span></div>';
  html += '<div>Followers: <span style="color:var(--accent)">' + (pr.follower_count || 0).toLocaleString() + '</span></div>';
  html += '<div>Following: <span>' + (pr.following_count || 0).toLocaleString() + '</span></div>';
  html += '</div>';

  if (comp.internal_weight) {
    html += '<div style="font-size:0.75rem;color:var(--muted);margin-top:0.6rem;margin-bottom:0.3rem;">Composite Scoring</div>';
    html += '<div style="font-size:0.8rem;">Internal (' + (comp.internal_weight * 100) + '%): <span style="color:' + scoreColor(comp.internal_score || 0) + '">' + (comp.internal_score || 0) + '</span>';
    html += ' · External (' + (comp.external_weight * 100) + '%): <span style="color:var(--accent)">' + ((comp.external_average || 0).toFixed(1)) + '</span></div>';
  }

  const tf = data.top_followers || [];
  if (tf.length > 0) {
    html += '<div style="font-size:0.75rem;color:var(--muted);margin-top:0.6rem;margin-bottom:0.3rem;">Top Followers by WoT</div>';
    html += '<div class="member-list" style="max-height:100px;">';
    tf.forEach(f => {
      const pk = f.pubkey.slice(0, 8) + '...' + f.pubkey.slice(-6);
      html += '<div class="member"><span class="pk">' + pk + '</span><span class="score" style="color:' + scoreColor(f.score) + '">' + f.score + '</span></div>';
    });
    html += '</div>';
  }

  const posts = eng.posts || 0;
  const replies = eng.replies || 0;
  const zaps = eng.zaps_received_count || 0;
  const zapSats = eng.zaps_received_sats || 0;
  if (posts > 0 || replies > 0 || zaps > 0) {
    html += '<div style="font-size:0.75rem;color:var(--muted);margin-top:0.5rem;">';
    html += 'Activity: ' + posts + ' posts · ' + replies + ' replies';
    if (zaps > 0) html += ' · ' + zaps + ' zaps (' + zapSats.toLocaleString() + ' sats)';
    html += '</div>';
  }

  $('#auditContent').innerHTML = html;
}

function renderRecommend(data) {
  if (data.error || data.status === 'payment_required') {
    $('#recommendContent').innerHTML = '<span style="color:var(--muted);font-size:0.8rem;">' + (data.error || data.message || 'Unavailable') + '</span>';
    return;
  }
  const recs = data.recommendations || [];
  if (recs.length === 0) {
    $('#recommendContent').innerHTML = '<span style="color:var(--muted);font-size:0.8rem;">No recommendations found — this pubkey may have too few follows.</span>';
    return;
  }

  let html = '<div style="font-size:0.75rem;color:var(--muted);margin-bottom:0.5rem;">' + data.total_found + ' candidates from ' + (data.follows_count || 0) + ' follows — top ' + recs.length + ' shown</div>';
  html += '<div class="member-list" style="max-height:200px;">';
  recs.forEach(r => {
    const pk = r.pubkey.slice(0, 8) + '...' + r.pubkey.slice(-6);
    const mutualPct = ((r.mutual_ratio || 0) * 100).toFixed(0);
    html += '<div class="member" style="display:flex;justify-content:space-between;align-items:center;">';
    html += '<span class="pk">' + pk + '</span>';
    html += '<span style="font-size:0.75rem;">';
    html += '<span style="color:var(--accent)">' + (r.mutual_follows || 0) + ' mutual</span>';
    html += ' <span style="color:var(--muted)">(' + mutualPct + '%)</span>';
    html += ' · <span style="color:' + scoreColor(r.wot_score) + '">' + r.wot_score + '</span>';
    html += '</span></div>';
  });
  html += '</div>';

  $('#recommendContent').innerHTML = html;
}
</script>
</body>
</html>`
