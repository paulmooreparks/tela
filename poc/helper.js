/*
  helper.js — Tela POC Helper

  Purpose:
    Connects to the Hub via WebSocket, requests a session with a specific
    machine, then binds a local TCP port. When a client (browser, mstsc,
    ssh, etc.) connects to that port, pipes data bidirectionally between
    the TCP socket and the WebSocket tunnel.

  Usage:
    node helper.js [hubUrl] [machineId] [localPort]

  Defaults:
    hubUrl:    ws://localhost:8080
    machineId: my-pc
    localPort: 8000

  For HTTP test:
    Open http://localhost:8000 in a browser.

  For RDP:
    node helper.js ws://localhost:8080 my-pc 13389
    mstsc /v:localhost:13389
*/

const WebSocket = require('ws');
const net = require('net');

const HUB_URL = process.argv[2] || process.env.HUB_URL || 'ws://localhost:8080';
const MACHINE_ID = process.argv[3] || process.env.MACHINE_ID || 'my-pc';
const LOCAL_PORT = parseInt(process.argv[4] || process.env.LOCAL_PORT || '8000', 10);

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
    if (isBinary) {
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
      console.log(`[helper] already has a connection — queuing new client`);
      // For POC, only one TCP client at a time. Reject extras.
      socket.destroy();
      return;
    }

    console.log(`[helper] client connected from ${socket.remoteAddress}:${socket.remotePort}`);
    tcpClient = socket;

    socket.on('data', (chunk) => {
      if (ws && ws.readyState === 1) {
        ws.send(chunk, { binary: true });
      }
    });

    socket.on('close', () => {
      console.log(`[helper] client disconnected`);
      tcpClient = null;
      // Don't close WebSocket — allow new connections (needed for HTTP)
    });

    socket.on('error', (err) => {
      console.error(`[helper] TCP client error:`, err.message);
    });
  });

  tcpServer.listen(LOCAL_PORT, '127.0.0.1', () => {
    console.log(`[helper] listening on localhost:${LOCAL_PORT}`);
    console.log(`[helper] >>> Open: http://localhost:${LOCAL_PORT}`);
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
