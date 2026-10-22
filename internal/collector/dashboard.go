package collector

import "net/http"

const dashboardPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>NodeWatch · Monitoring</title><style>
*{box-sizing:border-box}body{margin:0;background:#09111f;color:#e5edf9;font:16px system-ui,-apple-system,Segoe UI,sans-serif}main{max-width:1100px;margin:auto;padding:42px 28px}header{display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid #26354d;padding-bottom:25px}h1{margin:0;font-size:31px}.badge{background:#143a2c;color:#8ff0c2;padding:7px 11px;border-radius:999px;font-size:13px}p{color:#9eafc6}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:18px;margin-top:27px}.card{background:#101c2e;border:1px solid #293b56;border-radius:12px;padding:20px}.label{color:#91a3be;font-size:13px;text-transform:uppercase;letter-spacing:.08em}.value{font-size:27px;margin:8px 0}.critical{color:#ff8e8e}.healthy{color:#8ff0c2}table{width:100%;border-collapse:collapse;margin-top:12px}td,th{text-align:left;padding:11px 6px;border-bottom:1px solid #26354d}th{color:#91a3be;font-size:12px;text-transform:uppercase}.notice{margin-top:24px;color:#91a3be;font-size:13px}code{color:#b5d5ff}</style></head>
<body><main><header><div><h1>NodeWatch</h1><p>Real-time infrastructure monitoring</p></div><span class="badge">LIVE API</span></header>
<section class="grid"><article class="card"><div class="label">Known hosts</div><div class="value" id="hostCount">—</div><p>Latest snapshots from the collector.</p></article><article class="card"><div class="label">Firing alerts</div><div class="value critical" id="alertCount">—</div><p>Rules currently requiring attention.</p></article><article class="card"><div class="label">Data source</div><div class="value healthy">Connected</div><p>MySQL history + Redis live state.</p></article></section>
<section class="grid"><article class="card"><div class="label">Hosts</div><table><thead><tr><th>Host</th><th>Status</th><th>Filesystem</th></tr></thead><tbody id="hosts"></tbody></table></article><article class="card"><div class="label">Active alerts</div><table><thead><tr><th>Rule</th><th>Host</th><th>Severity</th></tr></thead><tbody id="alerts"></tbody></table></article></section><p class="notice">Demo data is explicitly simulated. Refreshes every five seconds · <code>/api/v1/hosts</code> · <code>/api/v1/alerts</code></p></main>
<script>const text=v=>document.createTextNode(v);function row(values){const tr=document.createElement('tr');values.forEach(v=>{const td=document.createElement('td');td.append(text(v));tr.append(td)});return tr}async function load(){try{const [h,a]=await Promise.all([fetch('/api/v1/hosts').then(r=>r.json()),fetch('/api/v1/alerts?state=firing').then(r=>r.json())]);document.querySelector('#hostCount').textContent=h.hosts.length;document.querySelector('#alertCount').textContent=a.alerts.length;const hosts=document.querySelector('#hosts');hosts.replaceChildren(...h.hosts.map(x=>row([x.id,x.status,(x.latest.filesystems?.[0]?.used_percent??0)+'%'])));const alerts=document.querySelector('#alerts');alerts.replaceChildren(...a.alerts.map(x=>row([x.rule,x.host_id,x.severity])))}catch(e){document.querySelector('#hostCount').textContent='Unavailable'}}load();setInterval(load,5000)</script></body></html>`

func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(dashboardPage))
}
