/*
  agent.js — Tela POC Agent

  Purpose:
    Connects to the Hub via WebSocket, registers a machine ID, and waits.
    When the Hub signals a session, opens a TCP connection to a local
    service and pipes data bidirectionally between the TCP socket and
    the WebSocket.

  Usage:
    node agent.js [hubUrl] [machineId] [targetPort] [targetHost]

  Defaults:
    hubUrl:     ws://localhost:8080
    machineId:  my-pc
    targetPort: 3000  (HTTP — use serve.js for POC testing)
    targetHost: 127.0.0.1

  For RDP:
    node agent.js ws://localhost:8080 my-pc 3389
*/

const WebSocket = require('ws');
const net = require('net');

const HUB_URL = process.argv[2] || process.env.HUB_URL || 'ws://localhost:8080';
const MACHINE_ID = process.argv[3] || process.env.MACHINE_ID || 'my-pc';
const TARGET_PORT = parseInt(process.argv[4] || process.env.TARGET_PORT || '3000', 10);
const TARGET_HOST = process.argv[5] || process.env.TARGET_HOST || '127.0.0.1';

let ws = null;
let tcpSocket = null;

function connect() {
  console.log(`[agent] connecting to hub: ${HUB_URL}`);
  ws = new WebSocket(HUB_URL);

  ws.on('open', () => {
    console.log(`[agent] connected, registering as: ${MACHINE_ID}`);
    ws.send(JSON.stringify({ type: 'register', machineId: MACHINE_ID }));
  });

  ws.on('message', (data, isBinary) => {
    // Binary data → forward to TCP socket
    if (isBinary) {
      if (tcpSocket && !tcpSocket.destroyed) {
        tcpSocket.write(data);
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

    if (msg.type === 'registered') {
      console.log(`[agent] registered as: ${msg.machineId} — waiting for session`);
    } else if (msg.type === 'session-start') {
      console.log(`[agent] session starting — connecting to ${TARGET_HOST}:${TARGET_PORT}`);
      openTcpTunnel();
    } else if (msg.type === 'session-ended') {
      console.log(`[agent] session ended`);
      closeTcp();
    }
  });

  ws.on('close', (code, reason) => {
    console.log(`[agent] hub connection closed: ${code} ${reason}`);
    closeTcp();
    scheduleReconnect();
  });

  ws.on('error', (err) => {
    console.error(`[agent] ws error:`, err.message);
  });
}

function openTcpTunnel() {
  closeTcp();

  tcpSocket = net.createConnection({ host: TARGET_HOST, port: TARGET_PORT }, () => {
    console.log(`[agent] TCP connected to ${TARGET_HOST}:${TARGET_PORT}`);
  });

  tcpSocket.on('data', (chunk) => {
    if (ws && ws.readyState === 1) {
      ws.send(chunk, { binary: true });
    }
  });

  tcpSocket.on('close', () => {
    console.log(`[agent] TCP connection closed`);
    // Don't close the WebSocket — agent stays registered for next session
  });

  tcpSocket.on('error', (err) => {
    console.error(`[agent] TCP error:`, err.message);
  });
}

function closeTcp() {
  if (tcpSocket && !tcpSocket.destroyed) {
    tcpSocket.destroy();
  }
  tcpSocket = null;
}

function scheduleReconnect() {
  console.log(`[agent] reconnecting in 3 seconds...`);
  setTimeout(connect, 3000);
}

// Start
connect();
