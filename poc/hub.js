/*
  hub.js — Tela POC Hub

  Purpose:
    WebSocket relay server that pairs agents with helpers and pipes
    binary data between them bidirectionally.

  How it works:
    1. Agent connects, sends: { type: "register", machineId: "..." }
       Hub stores the agent's WebSocket keyed by machineId.
    2. Helper connects, sends: { type: "connect", machineId: "..." }
       Hub looks up the agent, then signals both sides to start.
    3. All subsequent binary messages are relayed between the paired
       agent and helper WebSockets.

  No auth, no TLS, no multiplexing — raw relay for POC validation.
*/

const { WebSocketServer } = require('ws');

const PORT = parseInt(process.env.HUB_PORT, 10) || 8080;

// machineId -> { agentWs, helperWs }
const machines = new Map();

const wss = new WebSocketServer({ port: PORT });

wss.on('listening', () => {
  console.log(`[hub] listening on ws://0.0.0.0:${PORT}`);
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
        peer.send(data, { binary: true });
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
      handleRegister(ws, msg.machineId);
    } else if (msg.type === 'connect') {
      handleConnect(ws, msg.machineId);
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

function handleRegister(ws, machineId) {
  ws._tela.role = 'agent';
  ws._tela.machineId = machineId;

  if (!machines.has(machineId)) {
    machines.set(machineId, { agentWs: null, helperWs: null });
  }
  const entry = machines.get(machineId);
  entry.agentWs = ws;

  console.log(`[hub] agent registered: ${machineId}`);
  ws.send(JSON.stringify({ type: 'registered', machineId }));

  // If a helper is already waiting, pair them
  if (entry.helperWs && entry.helperWs.readyState === 1) {
    pair(machineId);
  }
}

function handleConnect(ws, machineId) {
  ws._tela.role = 'helper';
  ws._tela.machineId = machineId;

  const entry = machines.get(machineId);
  if (!entry || !entry.agentWs || entry.agentWs.readyState !== 1) {
    console.log(`[hub] helper requested ${machineId} — agent not found`);
    ws.send(JSON.stringify({ type: 'error', message: 'Agent not found' }));
    ws.close(1008, 'Agent not found');
    return;
  }

  entry.helperWs = ws;
  console.log(`[hub] helper connected for: ${machineId}`);
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

  // Signal agent to open TCP to local RDP
  agentWs.send(JSON.stringify({ type: 'session-start' }));
  // Signal helper that the tunnel is ready
  helperWs.send(JSON.stringify({ type: 'ready' }));
}

function handleDisconnect(ws) {
  const state = ws._tela;
  if (!state.machineId) return;

  const entry = machines.get(state.machineId);
  if (!entry) return;

  console.log(`[hub] ${state.role} disconnected: ${state.machineId}`);

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
