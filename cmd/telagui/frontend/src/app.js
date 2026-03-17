'use strict';

var goApp = window.go && window.go.main && window.go.main.App;
var hubStatusCache = {};
var selectedHubURL = null;
// selectedServices: { "hubURL||machineId||serviceName": { hub, machine, service, localPort } }
var selectedServices = {};
var pollIntervalId = null;

// --- Startup ---
refreshAll();

// --- Sidebar ---

function refreshAll() {
  var sidebar = document.getElementById('sidebar');
  sidebar.innerHTML = '<p class="loading">Loading hubs...</p>';

  goApp.GetKnownHubs().then(function (hubs) {
    if (!hubs || hubs.length === 0) {
      sidebar.innerHTML = '<div class="sidebar-empty">'
        + '<p>No hubs configured.</p>'
        + '<p class="hint">Click <strong>Add Hub</strong> to get started.</p>'
        + '</div>';
      return;
    }

    sidebar.innerHTML = '';
    hubs.forEach(function (hub) {
      var hubContainer = document.createElement('div');
      hubContainer.className = 'hub-group';

      var hubEl = document.createElement('div');
      hubEl.className = 'hub-item';
      if (selectedHubURL === hub.url) hubEl.classList.add('selected');
      hubEl.innerHTML = '<span class="hub-dot"></span>'
        + '<span class="hub-name">' + escHtml(hub.name) + '</span>'
        + (!hub.hasToken ? '<span class="no-token-badge">no token</span>' : '');
      hubEl.onclick = function () { selectHub(hub, hubEl); };
      hubContainer.appendChild(hubEl);

      sidebar.appendChild(hubContainer);

      // Fetch status if we have a token
      if (hub.hasToken) {
        goApp.GetHubStatus(hub.url).then(function (status) {
          hubStatusCache[hub.url] = status;
          hubEl.querySelector('.hub-dot').className = 'hub-dot ' + (status.online ? 'online' : 'offline');

          if (status.machines) {
            status.machines.forEach(function (m) {
              var mEl = document.createElement('div');
              mEl.className = 'machine-item';
              var dotClass = m.agentConnected ? 'online' : 'offline';
              mEl.innerHTML = '<span class="machine-dot ' + dotClass + '"></span>'
                + escHtml(m.id || m.hostname);
              hubContainer.appendChild(mEl);
            });
          }

          // If this hub is currently selected, re-render the detail pane
          if (selectedHubURL === hub.url) {
            renderHubDetail(hub);
          }
        }).catch(function () {
          hubEl.querySelector('.hub-dot').className = 'hub-dot offline';
        });
      }
    });
  });
}

// --- Detail Pane ---

function selectHub(hub, el) {
  clearSelection();
  el.classList.add('selected');
  selectedHubURL = hub.url;
  renderHubDetail(hub);
}

function renderHubDetail(hub) {
  var pane = document.getElementById('detail-pane');
  var status = hubStatusCache[hub.url] || {};
  var machines = status.machines || [];

  // Check connection state
  goApp.GetConnectionState().then(function (connState) {
    var isConnected = connState.connected;

    var html = '<div class="detail-header">'
      + '<h2>' + escHtml(status.hubName || hub.name) + '</h2>'
      + '<div class="meta">' + escHtml(hub.url) + '</div>'
      + '<div class="detail-actions">'
      + '<button class="btn-secondary btn-sm" onclick="removeHub(\'' + escAttr(hub.url) + '\')">Remove Hub</button>'
      + '</div></div>';

    if (status.error) {
      html += '<div class="connect-error">' + escHtml(status.error) + '</div>';
    } else if (machines.length === 0) {
      html += '<p style="color:var(--text-muted)">No machines registered.</p>';
    } else {
      // Render machines with checkboxes
      machines.forEach(function (m) {
        var machineId = m.id || m.hostname;
        var dot = m.agentConnected
          ? '<span class="hub-dot online" style="display:inline-block"></span>'
          : '<span class="hub-dot offline" style="display:inline-block"></span>';

        html += '<div class="machine-card">'
          + '<div class="machine-card-header">'
          + dot + ' <strong>' + escHtml(machineId) + '</strong>'
          + '<span class="machine-meta">' + (m.agentConnected ? 'Online' : 'Offline') + '</span>'
          + '</div>';

        var services = m.services || [];
        if (services.length === 0) {
          html += '<p class="no-services">No services advertised.</p>';
        } else {
          html += '<div class="service-checklist">';
          services.forEach(function (svc) {
            var key = hub.url + '||' + machineId + '||' + svc.name;
            var checked = selectedServices[key] ? ' checked' : '';
            var disabled = isConnected ? ' disabled' : '';
            var localPort = selectedServices[key]
              ? selectedServices[key].localPort
              : svc.port;

            html += '<label class="service-check-item">'
              + '<input type="checkbox"' + checked + disabled
              + ' onchange="toggleService(\'' + escAttr(hub.url) + '\', \'' + escAttr(machineId) + '\', \'' + escAttr(svc.name) + '\', ' + svc.port + ', this.checked)">'
              + '<span class="service-check-name">' + escHtml(svc.name) + '</span>'
              + '<span class="service-check-port">:' + svc.port + ' ' + escHtml(svc.proto || 'tcp') + '</span>';

            if (selectedServices[key]) {
              html += '<span class="local-port-label">local :' + localPort + '</span>';
            }

            html += '</label>';
          });
          html += '</div>';
        }
        html += '</div>';
      });
    }

    // Action buttons
    if (!isConnected) {
      var hasSelections = Object.keys(selectedServices).length > 0;
      html += '<div class="profile-actions">'
        + '<button class="btn-primary' + (hasSelections ? '' : ' disabled') + '"'
        + ' onclick="saveAndConnect()"'
        + (hasSelections ? '' : ' disabled')
        + '>Save &amp; Connect</button>'
        + '</div>';
    } else {
      // Connected mode: show local port assignments and disconnect
      html += renderConnectedState(connState);
    }

    pane.innerHTML = html;
  });
}

function renderConnectedState(connState) {
  var html = '<div class="connected-panel">'
    + '<div class="connected-header">'
    + '<span class="connected-badge">Connected</span>'
    + '<span class="connected-pid">PID ' + connState.pid + '</span>'
    + '</div>';

  if (connState.connections && connState.connections.length > 0) {
    html += '<div class="connected-services">';
    connState.connections.forEach(function (conn) {
      conn.services.forEach(function (svc) {
        html += '<div class="connected-svc-row">'
          + '<span class="connected-svc-name">' + escHtml(conn.machine) + ' / ' + escHtml(svc.name) + '</span>'
          + '<span class="connected-svc-local">localhost:' + svc.local + '</span>'
          + '</div>';
      });
    });
    html += '</div>';
  }

  if (connState.output) {
    html += '<pre class="connect-output">' + escHtml(connState.output) + '</pre>';
  }

  html += '<div class="profile-actions">'
    + '<button class="btn-danger" onclick="doDisconnect()">Disconnect</button>'
    + '</div></div>';

  return html;
}

function toggleService(hubURL, machineId, serviceName, servicePort, checked) {
  var key = hubURL + '||' + machineId + '||' + serviceName;
  if (checked) {
    goApp.AssignLocalPort(servicePort).then(function (localPort) {
      selectedServices[key] = {
        hub: hubURL,
        machine: machineId,
        service: serviceName,
        servicePort: servicePort,
        localPort: localPort
      };
      // Re-render to update the local port display and button state
      refreshDetailPane();
    });
  } else {
    delete selectedServices[key];
    refreshDetailPane();
  }
}

function refreshDetailPane() {
  if (!selectedHubURL) return;
  goApp.GetKnownHubs().then(function (hubs) {
    var hub = null;
    for (var i = 0; i < hubs.length; i++) {
      if (hubs[i].url === selectedHubURL) {
        hub = hubs[i];
        break;
      }
    }
    if (hub) renderHubDetail(hub);
  });
}

function clearSelection() {
  document.querySelectorAll('.hub-item').forEach(function (e) {
    e.classList.remove('selected');
  });
}

// --- Save & Connect ---

function saveAndConnect() {
  // Build profile connections from selected services
  // Group by hub+machine
  var groups = {};
  Object.keys(selectedServices).forEach(function (key) {
    var sel = selectedServices[key];
    var groupKey = sel.hub + '||' + sel.machine;
    if (!groups[groupKey]) {
      groups[groupKey] = {
        hub: sel.hub,
        machine: sel.machine,
        services: []
      };
    }
    groups[groupKey].services.push({
      name: sel.service,
      local: sel.localPort
    });
  });

  var connections = [];
  Object.keys(groups).forEach(function (k) {
    var g = groups[k];
    connections.push({
      hub: toWSURL(g.hub),
      machine: g.machine,
      token: '${TELA_TOKEN}',
      services: g.services
    });
  });

  if (connections.length === 0) return;

  var pane = document.getElementById('detail-pane');
  var statusDiv = document.createElement('div');
  statusDiv.className = 'connect-msg';
  statusDiv.textContent = 'Saving profile and connecting...';
  pane.appendChild(statusDiv);

  goApp.Connect(JSON.stringify(connections)).then(function (msg) {
    // Start polling connection state
    startConnectionPoll();
    refreshDetailPane();
  }).catch(function (err) {
    statusDiv.className = 'connect-error';
    statusDiv.textContent = 'Connection failed: ' + err;
  });
}

function doDisconnect() {
  goApp.Disconnect().then(function () {
    stopConnectionPoll();
    refreshDetailPane();
  }).catch(function (err) {
    alert('Disconnect failed: ' + err);
  });
}

function startConnectionPoll() {
  stopConnectionPoll();
  pollIntervalId = setInterval(function () {
    goApp.GetConnectionState().then(function (state) {
      if (!state.connected) {
        stopConnectionPoll();
        refreshDetailPane();
      }
    });
  }, 2000);
}

function stopConnectionPoll() {
  if (pollIntervalId) {
    clearInterval(pollIntervalId);
    pollIntervalId = null;
  }
}

function removeHub(url) {
  if (!confirm('Remove this hub?')) return;
  goApp.RemoveHub(url).then(function () {
    // Clear selections for this hub
    Object.keys(selectedServices).forEach(function (key) {
      if (key.indexOf(url + '||') === 0) {
        delete selectedServices[key];
      }
    });
    selectedHubURL = null;
    refreshAll();
    document.getElementById('detail-pane').innerHTML = '<div class="empty-state"><p>Hub removed.</p></div>';
  });
}

// --- Add Hub Modal ---

function openAddHubModal() {
  document.getElementById('add-hub-modal').classList.remove('hidden');
  showAddHubTab('manual', document.querySelector('.tab[data-tab="manual"]'));
}

function closeAddHubModal() {
  document.getElementById('add-hub-modal').classList.add('hidden');
}

function showAddHubTab(tab, btn) {
  document.querySelectorAll('.tab-content').forEach(function (el) { el.classList.add('hidden'); });
  document.querySelectorAll('.tab').forEach(function (el) { el.classList.remove('active'); });
  document.getElementById('tab-' + tab).classList.remove('hidden');
  if (btn) btn.classList.add('active');

  if (tab === 'docker') refreshDockerContainers();
}

function submitAddHub(event) {
  event.preventDefault();
  var url = document.getElementById('hub-url').value.trim();
  var token = document.getElementById('hub-token').value.trim();
  var errEl = document.getElementById('add-hub-error');
  errEl.classList.add('hidden');

  goApp.AddHub(url, token).then(function () {
    closeAddHubModal();
    refreshAll();
  }).catch(function (err) {
    errEl.textContent = err;
    errEl.classList.remove('hidden');
  });
}

// --- Docker Integration ---

function refreshDockerContainers() {
  var sel = document.getElementById('docker-container');
  sel.innerHTML = '<option value="">Loading...</option>';
  goApp.DockerListContainers().then(function (names) {
    sel.innerHTML = '';
    if (!names || names.length === 0) {
      sel.innerHTML = '<option value="">No containers running</option>';
      return;
    }
    names.forEach(function (name) {
      var opt = document.createElement('option');
      opt.value = name;
      opt.textContent = name;
      sel.appendChild(opt);
    });
  }).catch(function () {
    sel.innerHTML = '<option value="">Docker not available</option>';
  });
}

function dockerGetOwner() { dockerGetToken('show-owner'); }
function dockerGetViewer() { dockerGetToken('show-viewer'); }

function dockerGetToken(role) {
  var container = document.getElementById('docker-container').value;
  if (!container) return;
  var errEl = document.getElementById('docker-error');
  var resultEl = document.getElementById('docker-result');
  errEl.classList.add('hidden');
  resultEl.classList.add('hidden');

  goApp.DockerGetToken(container, role).then(function (token) {
    document.getElementById('docker-token-value').textContent = token;
    resultEl.classList.remove('hidden');
  }).catch(function (err) {
    errEl.textContent = err;
    errEl.classList.remove('hidden');
  });
}

function dockerAddHub() {
  var url = document.getElementById('docker-hub-url').value.trim();
  var token = document.getElementById('docker-token-value').textContent.trim();
  if (!url) {
    document.getElementById('docker-error').textContent = 'Enter the hub URL first.';
    document.getElementById('docker-error').classList.remove('hidden');
    return;
  }
  goApp.AddHub(url, token).then(function () {
    closeAddHubModal();
    refreshAll();
  });
}

// --- Command Log ---

function toggleLog() {
  document.getElementById('log-drawer').classList.toggle('hidden');
  refreshLog();
}

function refreshLog() {
  goApp.GetCommandLog().then(function (entries) {
    var el = document.getElementById('log-content');
    if (!entries || entries.length === 0) {
      el.innerHTML = '<p class="empty-hint">Actions you take will appear here with their CLI equivalents.</p>';
      return;
    }
    var html = '';
    entries.slice().reverse().forEach(function (entry) {
      html += '<div class="log-entry">'
        + '<div class="log-time">' + escHtml(entry.time) + '</div>'
        + '<div class="log-desc">' + escHtml(entry.description) + '</div>'
        + '<div class="log-cmd-wrap">'
        + '<code class="log-cmd">' + escHtml(entry.command) + '</code>'
        + '<button class="log-copy" onclick="copyText(this, \'' + escAttr(entry.command) + '\')">Copy</button>'
        + '</div></div>';
    });
    el.innerHTML = html;
  });
}

function copyText(btn, text) {
  if (navigator.clipboard) {
    navigator.clipboard.writeText(text).then(function () {
      btn.textContent = 'Copied';
      setTimeout(function () { btn.textContent = 'Copy'; }, 1500);
    });
  }
}

// --- Utilities ---

function escHtml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function escAttr(s) {
  return String(s).replace(/&/g, '&amp;').replace(/'/g, '&#39;').replace(/"/g, '&quot;');
}

function toWSURL(url) {
  if (url.indexOf('https://') === 0) return 'wss://' + url.substring(8);
  if (url.indexOf('http://') === 0) return 'ws://' + url.substring(7);
  if (url.indexOf('wss://') !== 0 && url.indexOf('ws://') !== 0) return 'wss://' + url;
  return url;
}
