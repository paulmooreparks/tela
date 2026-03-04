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
  // /status and /api/status — JSON summary of registered machines, services, and sessions
  if (req.url === '/status' || req.url === '/api/status') {
    const status = [];
    for (const [id, entry] of machines) {
      const ports = entry.ports || [];
      status.push({
        id,
        agentConnected: !!(entry.agentWs && entry.agentWs.readyState === 1),
        hasSession: !!(entry.helperWs && entry.helperWs.readyState === 1),
        registeredAt: entry.registeredAt || null,
        services: ports.map(p => ({ port: p, label: portLabel(p) })),
      });
    }
    res.writeHead(200, {
      'Content-Type': 'application/json',
      'Access-Control-Allow-Origin': '*',
    });
    res.end(JSON.stringify({ machines: status, timestamp: new Date().toISOString() }, null, 2));
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

  let filePath = path.join(WWW_DIR, req.url === '/' ? 'index.html' : req.url);
  filePath = path.normalize(filePath);

  // Prevent path traversal
  if (!filePath.startsWith(WWW_DIR)) {
    res.writeHead(403);
    res.end('Forbidden');
    return;
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

// ── WebSocket server (relay) ───────────────────────────────────────

// machineId -> { agentWs, helperWs, ports, token, registeredAt, udpTokens }
const machines = new Map();

// Well-known port labels for the API and console.
const PORT_LABELS = {
  22: 'SSH', 80: 'HTTP', 443: 'HTTPS', 3389: 'RDP',
  5900: 'VNC', 8080: 'HTTP-Alt', 8443: 'HTTPS-Alt',
};
function portLabel(p) { return PORT_LABELS[p] || `port ${p}`; }

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

httpServer.listen(PORT, '0.0.0.0', () => {
  console.log(`[hub] listening on http+ws://0.0.0.0:${PORT}`);
  console.log(`[hub] static site: ${WWW_DIR}`);
});

wss.on('connection', (ws) => {
  // Store state on the ws object so there are no closure issues
  ws._tela = { role: null, machineId: null, paired: false, peer: null };

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
      handleRegister(ws, msg.machineId, msg.token, msg.ports);
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

function handleRegister(ws, machineId, token, ports) {
  ws._tela.role = 'agent';
  ws._tela.machineId = machineId;

  if (!machines.has(machineId)) {
    machines.set(machineId, { agentWs: null, helperWs: null, ports: [], token: null, registeredAt: null });
  }
  const entry = machines.get(machineId);
  entry.agentWs = ws;
  entry.token = token || null;
  entry.ports = Array.isArray(ports) ? ports : [];
  entry.registeredAt = new Date().toISOString();

  console.log(`[hub] agent registered: ${machineId} ports=[${entry.ports}]${token ? ' (token-protected)' : ''}`);
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
    machines.delete(state.machineId);
  } else if (state.role === 'helper') {
    entry.helperWs = null;
  }
}
