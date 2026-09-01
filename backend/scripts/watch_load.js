const WS = globalThis.WebSocket;
if (!WS) throw new Error('Node.js 22 or newer is required');

const SESSION_SECRET = process.env.KANIVET_SESSION_SECRET || process.env.SESSION_SECRET;
if (!SESSION_SECRET) throw new Error('KANIVET_SESSION_SECRET or SESSION_SECRET is required');
const URL = `ws://localhost:53727/api/v1/ws?session_secret=${encodeURIComponent(SESSION_SECRET)}`;
const DURATION_MS = parseInt(process.env.DURATION_MS || '60000', 10);
const CONN_COUNT = parseInt(process.env.CONN_COUNT || '4', 10);

const CLUSTERS = (process.env.CLUSTERS || 'arn:aws:eks:us-east-1:example-account:cluster/example-cluster-a,arn:aws:eks:eu-west-2:example-account:cluster/example-cluster-b').split(',');

const RESOURCES = [
  { group: '', version: 'v1', kind: 'pods', namespace: '' },
  { group: '', version: 'v1', kind: 'services', namespace: '' },
  { group: '', version: 'v1', kind: 'configmaps', namespace: '' },
  { group: '', version: 'v1', kind: 'events', namespace: '' },
  { group: '', version: 'v1', kind: 'secrets', namespace: '' },
  { group: 'apps', version: 'v1', kind: 'deployments', namespace: '' },
  { group: 'apps', version: 'v1', kind: 'statefulsets', namespace: '' },
  { group: 'apps', version: 'v1', kind: 'daemonsets', namespace: '' },
  { group: 'apps', version: 'v1', kind: 'replicasets', namespace: '' },
  { group: 'batch', version: 'v1', kind: 'jobs', namespace: '' },
  { group: 'networking.k8s.io', version: 'v1', kind: 'ingresses', namespace: '' },
];

const stats = { msgs: 0, events: 0, errors: 0, bytes: 0, byKind: {} };

function subscribe(conn, cluster, r) {
  const msg = {
    type: 'subscribe',
    payload: { channel: 'items', cluster, group: r.group, version: r.version, kind: r.kind, namespace: r.namespace },
  };
  conn.send(JSON.stringify(msg));
}

function open(i) {
  return new Promise((resolve) => {
    const conn = new WS(URL);
    conn.addEventListener('open', () => {
      console.log(`[conn ${i}] open`);
      const cluster = CLUSTERS[i % CLUSTERS.length];
      let delay = 0;
      RESOURCES.forEach((r) => {
        setTimeout(() => subscribe(conn, cluster, r), delay);
        delay += 50;
      });
      resolve(conn);
    });
    conn.addEventListener('message', (event) => {
      const data = typeof event.data === 'string' ? event.data : Buffer.from(event.data).toString();
      stats.msgs++;
      stats.bytes += Buffer.byteLength(data);
      try {
        const m = JSON.parse(data);
        if (m.type === 'event' || m.type === 'items') {
          stats.events++;
          const topic = (m.payload && m.payload.topic) || 'unknown';
          stats.byKind[topic] = (stats.byKind[topic] || 0) + 1;
        } else if (m.type === 'error') {
          stats.errors++;
          console.log(`[conn ${i}] err:`, m.error || m.payload);
        }
      } catch {}
    });
    conn.addEventListener('error', (e) => { stats.errors++; console.log(`[conn ${i}] socket err:`, e.message); });
    conn.addEventListener('close', () => console.log(`[conn ${i}] close`));
  });
}

(async () => {
  const conns = [];
  for (let i = 0; i < CONN_COUNT; i++) conns.push(await open(i));
  const t0 = Date.now();
  const interval = setInterval(() => {
    const elapsed = ((Date.now() - t0) / 1000).toFixed(1);
    console.log(`[${elapsed}s] msgs=${stats.msgs} events=${stats.events} errors=${stats.errors} MB=${(stats.bytes / 1048576).toFixed(2)}`);
  }, 5000);
  setTimeout(() => {
    clearInterval(interval);
    console.log('---');
    console.log('FINAL:', JSON.stringify({ msgs: stats.msgs, events: stats.events, errors: stats.errors, bytesMB: (stats.bytes / 1048576).toFixed(2) }));
    const top = Object.entries(stats.byKind).sort((a, b) => b[1] - a[1]).slice(0, 20);
    console.log('Top topics:');
    top.forEach(([k, v]) => console.log(`  ${v}  ${k}`));
    conns.forEach((c) => c.close());
    process.exit(0);
  }, DURATION_MS);
})();
