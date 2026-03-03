/*
  serve.js — Tela POC Static Web Server

  Purpose:
    A minimal HTTP server that serves the www/ directory on port 3000.
    Used as the "target service" that the agent tunnels to — replacing
    RDP for easy POC validation.

  Usage:
    node serve.js [port]

  Default port: 3000
*/

const http = require('http');
const fs = require('fs');
const path = require('path');

const PORT = parseInt(process.argv[2] || process.env.SERVE_PORT || '3000', 10);
const WWW_DIR = path.join(__dirname, 'www');

const MIME = {
  '.html': 'text/html',
  '.css':  'text/css',
  '.js':   'text/javascript',
  '.json': 'application/json',
  '.png':  'image/png',
  '.svg':  'image/svg+xml',
};

const server = http.createServer((req, res) => {
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

const BIND = process.env.SERVE_BIND || '0.0.0.0';

server.listen(PORT, BIND, () => {
  console.log(`[serve] static web server on http://${BIND}:${PORT}`);
  console.log(`[serve] serving: ${WWW_DIR}`);
});
