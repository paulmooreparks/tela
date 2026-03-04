/*
  hub.js — Tela POC Hub

  Purpose:
    Combined HTTP + WebSocket server.
    - HTTP requests get the static site from www/.
    - WebSocket connections get the relay that pairs agents with helpers
      and pipes binary data between them bidirectionally.

  Serving both on the same port means Cloudflare Tunnel (or any reverse
  proxy) only needs a single ingress rule.

  How the relay works:
    1. Agent connects via WS, sends: { type: "register", machineId: "..." }
       Hub stores the agent's WebSocket keyed by machineId.
    2. Helper connects via WS, sends: { type: "connect", machineId: "..." }
       Hub looks up the agent, then signals both sides to start.
    3. All subsequent binary messages are relayed between the paired
       agent and helper WebSockets.

  No auth, no TLS, no multiplexing — raw relay for POC validation.
*/

const http = require('http');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const dgram = require('dgram');
const { WebSocketServer } = require('ws');

const PORT = parseInt(process.env.HUB_PORT, 10) || 8080;
const UDP_PORT = parseInt(process.env.HUB_UDP_PORT, 10) || 41820;
const HUB_NAME = process.env.HUB_NAME || '';
const WWW_DIR = path.join(__dirname, 'www');

const MIME = {
  '.html': 'text/html',
  '.css':  'text/css',
  '.js':   'text/javascript',
  '.json': 'application/json',
  '.png':  'image/png',
  '.svg':  'image/svg+xml',
  '.exe':  'application/octet-stream',
};

// ── HTTP server (static site + status API) ─────────────────────────

const httpServer = http.createServer((req, res) => {
  // /api/history — recent session events
  if (req.url === '/api/history') {
    res.writeHead(200, {
      'Content-Type': 'application/json',
      'Access-Control-Allow-Origin': '*',
    });
    res.end(JSON.stringify({ events: sessionHistory, timestamp: new Date().toISOString() }));
    return;
  }

  // /status and /api/status — JSON summary of registered machines, services, and sessions
  if (req.url === '/status' || req.url === '/api/status') {
    const status = [];
    for (const [id, entry] of machines) {
      const services = normalizeServices(entry.ports || [], entry.services || []);
      status.push({
        id,
        displayName: entry.displayName || null,
        hostname: entry.hostname || null,
        os: entry.os || null,
        agentVersion: entry.agentVersion || null,
        tags: Array.isArray(entry.tags) ? entry.tags : [],
        location: entry.location || null,
        owner: entry.owner || null,
        agentConnected: !!(entry.agentWs && entry.agentWs.readyState === 1),
        hasSession: !!(entry.helperWs && entry.helperWs.readyState === 1),
        registeredAt: entry.registeredAt || null,
        lastSeen: entry.lastSeen || entry.registeredAt || null,
        services,
      });
    }
    res.writeHead(200, {
      'Content-Type': 'application/json',
      'Access-Control-Allow-Origin': '*',
    });
    const payload = { machines: status, timestamp: new Date().toISOString() };
    if (HUB_NAME) payload.hubName = HUB_NAME;
    res.end(JSON.stringify(payload, null, 2));
    return;
  }

  // CORS preflight for API endpoints
  if (req.method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET, OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type',
    });
    res.end();
    return;
  }

  // Map URL to file, resolving directory → index.html
  let urlPath = req.url.split('?')[0]; // strip query string
  if (urlPath === '/') urlPath = '/index.html';
  let filePath = path.join(WWW_DIR, urlPath);
  filePath = path.normalize(filePath);

  // Prevent path traversal
  if (!filePath.startsWith(WWW_DIR)) {
    res.writeHead(403);
    res.end('Forbidden');
    return;
  }

  // If path is a directory, look for index.html inside it
  fs.stat(filePath, (statErr, stats) => {
    if (!statErr && stats.isDirectory()) {
      filePath = path.join(filePath, 'index.html');
    }
    fs.readFile(filePath, (err, data) => {
      if (err) {
        res.writeHead(404);
        res.end('Not Found');
        return;
      }
      const ext = path.extname(filePath);
      res.writeHead(200, { 'Content-Type': MIME[ext] || 'application/octet-stream' });
      res.end(data);
    });
  });
});

// ── WebSocket server (relay) ───────────────────────────────────────

// machineId -> { agentWs, helperWs, ports, services, token, registeredAt, lastSeen, metadata..., udpTokens }
const machines = new Map();

// Session history (in-memory ring buffer, most recent first)
const MAX_HISTORY = 100;
const sessionHistory = [];  // [{ machineId, event, timestamp, detail? }]

function recordEvent(machineId, event, detail) {
  sessionHistory.unshift({ machineId, event, timestamp: new Date().toISOString(), detail: detail || null });
  if (sessionHistory.length > MAX_HISTORY) sessionHistory.length = MAX_HISTORY;
}

// Well-known port labels for the API and console.
const PORT_LABELS = {
  22: 'SSH', 80: 'HTTP', 443: 'HTTPS', 3389: 'RDP',
  5900: 'VNC', 8080: 'HTTP-Alt', 8443: 'HTTPS-Alt',
};
function portLabel(p) { return PORT_LABELS[p] || `port ${p}`; }

function normalizeServices(ports, services) {
  // Prefer explicit service descriptors; otherwise derive from ports.
  const fromServices = Array.isArray(services) ? services : [];
  if (fromServices.length > 0) {
    return fromServices
      .filter(s => s && Number.isFinite(s.port))
      .map(s => ({
        port: Number(s.port),
        proto: (s.proto || 'tcp').toString().toLowerCase(),
        name: (s.name || s.label || portLabel(Number(s.port))).toString(),
        description: s.description ? s.description.toString() : '',
      }));
  }
  const fromPorts = Array.isArray(ports) ? ports : [];
  return fromPorts
    .filter(p => Number.isFinite(p))
    .map(p => ({ port: Number(p), proto: 'tcp', name: portLabel(Number(p)), description: '' }));
}

// ── UDP relay ──────────────────────────────────────────────────────

// tokenHex -> { peerTokenHex, peerTokenBuf, peerWs, role, addr: null | {address, port}, machineId }
const udpSessions = new Map();

const udpSocket = dgram.createSocket('udp4');

const TOKEN_LEN = 8;
const PROBE_WORD = 'PROBE';
const READY_WORD = 'READY';

udpSocket.on('message', (msg, rinfo) => {
  if (msg.length < TOKEN_LEN) return; // too short

  const tokenHex = msg.slice(0, TOKEN_LEN).toString('hex');
  const payload = msg.slice(TOKEN_LEN);
  const session = udpSessions.get(tokenHex);
  if (!session) return; // unknown token

  // Record sender's address for this token
  session.addr = { address: rinfo.address, port: rinfo.port };

  // Check if this is a PROBE
  if (payload.toString() === PROBE_WORD) {
    // Send READY back: [same token]["READY"]
    const resp = Buffer.concat([msg.slice(0, TOKEN_LEN), Buffer.from(READY_WORD)]);
    udpSocket.send(resp, rinfo.port, rinfo.address);
    console.log(`[hub] UDP probe OK from ${rinfo.address}:${rinfo.port} (${session.machineId})`);
    return;
  }

  // Relay WG datagram to peer
  const peer = udpSessions.get(session.peerTokenHex);
  if (!peer) return;

  if (peer.addr) {
    // Peer is on UDP — forward via UDP
    const relayBuf = Buffer.concat([Buffer.from(session.peerTokenHex, 'hex'), payload]);
    udpSocket.send(relayBuf, peer.addr.port, peer.addr.address);
  } else if (session.peerWs && session.peerWs.readyState === 1) {
    // Peer hasn't upgraded to UDP — fall back to WebSocket relay
    // session.peerWs is the PEER's WebSocket (stored in the sender's entry)
    session.peerWs.send(payload, { binary: true });
  }
});

udpSocket.on('error', (err) => {
  console.error(`[hub] UDP error:`, err.message);
});

udpSocket.bind(UDP_PORT, '0.0.0.0', () => {
  console.log(`[hub] UDP relay on port ${UDP_PORT}`);
});

const wss = new WebSocketServer({ server: httpServer });

// Keep lastSeen fresh even when idle.
const PING_INTERVAL_MS = 10_000;
setInterval(() => {
  for (const entry of machines.values()) {
    if (entry.agentWs && entry.agentWs.readyState === 1) {
      try { entry.agentWs.ping(); } catch {}
    }
  }
}, PING_INTERVAL_MS);

httpServer.listen(PORT, '0.0.0.0', () => {
  console.log(`[hub] listening on http+ws://0.0.0.0:${PORT}`);
  console.log(`[hub] static site: ${WWW_DIR}`);
});

wss.on('connection', (ws) => {
  // Store state on the ws object so there are no closure issues
  ws._tela = { role: null, machineId: null, paired: false, peer: null };

  ws.on('pong', () => {
    const state = ws._tela;
    if (state.role !== 'agent' || !state.machineId) return;
    const entry = machines.get(state.machineId);
    if (!entry) return;
    entry.lastSeen = new Date().toISOString();
  });

  ws.on('message', (data, isBinary) => {
    const state = ws._tela;

    // If paired, relay binary data to peer
    if (state.paired) {
      const peer = state.peer;
      if (peer && peer.readyState === 1) {
        console.log(`[hub] relay ${state.role}→${state.peer._tela.role} ${data.length}B binary=${isBinary}`);
        peer.send(data, { binary: isBinary });
      }
      return;
    }

    // First message must be JSON
    let msg;
    try {
      msg = JSON.parse(data.toString());
    } catch {
      ws.close(1002, 'Expected JSON for first message');
      return;
    }

    if (msg.type === 'register') {
      handleRegister(ws, msg);
    } else if (msg.type === 'connect') {
      handleConnect(ws, msg.machineId, msg.wgPubKey, msg.token);
    } else {
      ws.close(1002, 'Unknown message type');
    }
  });

  ws.on('close', () => {
    handleDisconnect(ws);
  });

  ws.on('error', (err) => {
    const state = ws._tela;
    console.error(`[hub] ws error (${state.role}/${state.machineId}):`, err.message);
  });
});

function handleRegister(ws, msg) {
  const machineId = msg.machineId;
  const token = msg.token;
  const ports = msg.ports;
  const services = msg.services;

  ws._tela.role = 'agent';
  ws._tela.machineId = machineId;

  if (!machines.has(machineId)) {
    machines.set(machineId, {
      agentWs: null,
      helperWs: null,
      ports: [],
      services: [],
      token: null,
      registeredAt: null,
      lastSeen: null,
      displayName: null,
      hostname: null,
      os: null,
      agentVersion: null,
      tags: [],
      location: null,
      owner: null,
      udpTokens: null,
    });
  }
  const entry = machines.get(machineId);
  entry.agentWs = ws;
  entry.token = token || null;
  entry.ports = Array.isArray(ports) ? ports : [];
  entry.services = Array.isArray(services) ? services : [];
  entry.displayName = msg.displayName || entry.displayName || null;
  entry.hostname = msg.hostname || entry.hostname || null;
  entry.os = msg.os || entry.os || null;
  entry.agentVersion = msg.agentVersion || entry.agentVersion || null;
  entry.tags = Array.isArray(msg.tags) ? msg.tags : (entry.tags || []);
  entry.location = msg.location || entry.location || null;
  entry.owner = msg.owner || entry.owner || null;

  const now = new Date().toISOString();
  entry.lastSeen = now;
  if (!entry.registeredAt) entry.registeredAt = now;

  const normalized = normalizeServices(entry.ports, entry.services);
  const portsForLog = normalized.map(s => s.port);
  console.log(`[hub] agent registered: ${machineId} ports=[${portsForLog}]${token ? ' (token-protected)' : ''}`);
  recordEvent(machineId, 'agent-register', `ports=[${portsForLog}]`);
  ws.send(JSON.stringify({ type: 'registered', machineId }));

  // If a helper is already waiting, pair them
  if (entry.helperWs && entry.helperWs.readyState === 1) {
    pair(machineId);
  }
}

function handleConnect(ws, machineId, wgPubKey, token) {
  ws._tela.role = 'helper';
  ws._tela.machineId = machineId;
  ws._tela.wgPubKey = wgPubKey || null;

  const entry = machines.get(machineId);
  if (!entry || !entry.agentWs || entry.agentWs.readyState !== 1) {
    console.log(`[hub] helper requested ${machineId} — agent not found`);
    ws.send(JSON.stringify({ type: 'error', message: 'Agent not found' }));
    ws.close(1008, 'Agent not found');
    return;
  }

  // Validate token — if the agent registered with a token, the client must match
  if (entry.token && entry.token !== (token || '')) {
    console.log(`[hub] helper token mismatch for ${machineId}`);
    ws.send(JSON.stringify({ type: 'error', message: 'Invalid token' }));
    ws.close(1008, 'Invalid token');
    return;
  }

  entry.helperWs = ws;
  console.log(`[hub] helper connected for: ${machineId}${wgPubKey ? ' (WG)' : ''}`);
  pair(machineId);
}

function pair(machineId) {
  const entry = machines.get(machineId);
  if (!entry || !entry.agentWs || !entry.helperWs) return;

  const agentWs = entry.agentWs;
  const helperWs = entry.helperWs;

  // Cross-link peers
  agentWs._tela.paired = true;
  agentWs._tela.peer = helperWs;

  helperWs._tela.paired = true;
  helperWs._tela.peer = agentWs;

  console.log(`[hub] paired agent <-> helper for: ${machineId}`);
  recordEvent(machineId, 'session-start', 'Client connected');

  // Forward helper's WG public key (if present) to agent in session-start
  const helperWgPubKey = helperWs._tela.wgPubKey || undefined;
  agentWs.send(JSON.stringify({ type: 'session-start', wgPubKey: helperWgPubKey }));
  // Signal helper that the tunnel is ready
  helperWs.send(JSON.stringify({ type: 'ready' }));

  // Generate UDP relay tokens and send udp-offer to both sides
  const agentToken = crypto.randomBytes(TOKEN_LEN);
  const helperToken = crypto.randomBytes(TOKEN_LEN);
  const agentTokenHex = agentToken.toString('hex');
  const helperTokenHex = helperToken.toString('hex');

  // Register UDP session entries (cross-linked)
  // peerWs points to the PEER's WebSocket (for fallback when peer hasn't upgraded)
  udpSessions.set(agentTokenHex, {
    peerTokenHex: helperTokenHex,
    peerTokenBuf: helperToken,
    peerWs: helperWs,
    role: 'agent',
    addr: null,
    machineId,
  });
  udpSessions.set(helperTokenHex, {
    peerTokenHex: agentTokenHex,
    peerTokenBuf: agentToken,
    peerWs: agentWs,
    role: 'helper',
    addr: null,
    machineId,
  });

  // Store token hex values on machine entry for cleanup
  entry.udpTokens = [agentTokenHex, helperTokenHex];

  // Send UDP offers — each side gets its own token and the hub's UDP port
  agentWs.send(JSON.stringify({ type: 'udp-offer', token: agentTokenHex, port: UDP_PORT }));
  helperWs.send(JSON.stringify({ type: 'udp-offer', token: helperTokenHex, port: UDP_PORT }));
  console.log(`[hub] sent udp-offer to both sides for: ${machineId} (port ${UDP_PORT})`);
}

function handleDisconnect(ws) {
  const state = ws._tela;
  if (!state.machineId) return;

  const entry = machines.get(state.machineId);
  if (!entry) return;

  console.log(`[hub] ${state.role} disconnected: ${state.machineId}`);
  recordEvent(state.machineId, state.role + '-disconnect', state.role + ' disconnected');

  // Clean up UDP session tokens
  if (entry.udpTokens) {
    for (const tokenHex of entry.udpTokens) {
      udpSessions.delete(tokenHex);
    }
    entry.udpTokens = null;
    console.log(`[hub] cleaned up UDP sessions for: ${state.machineId}`);
  }

  // Close peer
  const peer = state.peer;
  if (peer && peer.readyState === 1) {
    peer.close(1001, `${state.role} disconnected`);
  }

  if (state.role === 'agent') {
    entry.agentWs = null;
    entry.lastSeen = new Date().toISOString();
  } else if (state.role === 'helper') {
    entry.helperWs = null;
  }
}
