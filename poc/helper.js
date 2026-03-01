/*
  helper.js — Tela POC Helper

  Purpose:
    Connects to the Hub via WebSocket, requests a session with a specific
    machine, then binds a local TCP port. When a native client (e.g. mstsc)
    connects to that port, pipes data bidirectionally between the TCP
    socket and the WebSocket tunnel.

  Usage:
    node helper.js [hubUrl] [machineId] [localPort]

  Defaults:
    hubUrl:    ws://localhost:8080
    machineId: my-pc
    localPort: 13389

  Then run:
    mstsc /v:localhost:13389
*/

const WebSocket = require('ws');
const net = require('net');

const HUB_URL = process.argv[2] || process.env.HUB_URL || 'ws://localhost:8080';
const MACHINE_ID = process.argv[3] || process.env.MACHINE_ID || 'my-pc';
const LOCAL_PORT = parseInt(process.argv[4] || process.env.LOCAL_PORT || '13389', 10);

let ws = null;
let tcpServer = null;
let tcpClient = null;
let ready = false;

function start() {
  console.log(`[helper] connecting to hub: ${HUB_URL}`);
  ws = new WebSocket(HUB_URL);

  ws.on('open', () => {
    console.log(`[helper] connected, requesting session for: ${MACHINE_ID}`);
    ws.send(JSON.stringify({ type: 'connect', machineId: MACHINE_ID }));
  });

  ws.on('message', (data, isBinary) => {
    // Binary data → forward to TCP client
    if (isBinary || Buffer.isBuffer(data)) {
      if (tcpClient && !tcpClient.destroyed) {
        tcpClient.write(data);
      }
      return;
    }

    // Text message → JSON control
    let msg;
    try {
      msg = JSON.parse(data.toString());
    } catch {
      return;
    }

    if (msg.type === 'ready') {
      console.log(`[helper] tunnel ready — starting local TCP server`);
      ready = true;
      startLocalServer();
    } else if (msg.type === 'error') {
      console.error(`[helper] hub error: ${msg.message}`);
      process.exit(1);
    }
  });

  ws.on('close', (code, reason) => {
    console.log(`[helper] hub connection closed: ${code} ${reason}`);
    cleanup();
  });

  ws.on('error', (err) => {
    console.error(`[helper] ws error:`, err.message);
  });
}

function startLocalServer() {
  if (tcpServer) return;

  tcpServer = net.createServer((socket) => {
    if (tcpClient) {
      console.log(`[helper] rejecting additional connection (single-session POC)`);
      socket.destroy();
      return;
    }

    console.log(`[helper] native client connected from ${socket.remoteAddress}:${socket.remotePort}`);
    tcpClient = socket;

    socket.on('data', (chunk) => {
      if (ws && ws.readyState === 1) {
        ws.send(chunk, { binary: true });
      }
    });

    socket.on('close', () => {
      console.log(`[helper] native client disconnected`);
      tcpClient = null;
      // Close the WebSocket — session is over
      if (ws && ws.readyState === 1) {
        ws.close(1000, 'Client disconnected');
      }
      cleanup();
    });

    socket.on('error', (err) => {
      console.error(`[helper] TCP client error:`, err.message);
    });
  });

  tcpServer.listen(LOCAL_PORT, '127.0.0.1', () => {
    console.log(`[helper] listening on localhost:${LOCAL_PORT}`);
    console.log(`[helper] >>> Run: mstsc /v:localhost:${LOCAL_PORT}`);
  });

  tcpServer.on('error', (err) => {
    console.error(`[helper] TCP server error:`, err.message);
    process.exit(1);
  });
}

function cleanup() {
  if (tcpClient && !tcpClient.destroyed) {
    tcpClient.destroy();
  }
  tcpClient = null;

  if (tcpServer) {
    tcpServer.close();
    tcpServer = null;
  }
}

// Start
start();
