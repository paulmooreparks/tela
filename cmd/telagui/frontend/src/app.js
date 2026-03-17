'use strict';

var goApp = window.go && window.go.main && window.go.main.App;
var hubStatusCache = {};

// --- Startup: load known hubs immediately ---
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
              mEl.onclick = function (e) {
                e.stopPropagation();
                selectMachine(hub, m, mEl);
              };
              hubContainer.appendChild(mEl);
            });
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

  var pane = document.getElementById('detail-pane');
  var status = hubStatusCache[hub.url] || {};
  var machines = status.machines || [];

  var html = '<div class="detail-header">'
    + '<h2>' + escHtml(hub.name) + '</h2>'
    + '<div class="meta">' + escHtml(hub.url) + '</div>'
    + '<div class="detail-actions">'
    + '<button class="btn-secondary btn-sm" onclick="removeHub(\'' + escAttr(hub.url) + '\')">Remove Hub</button>'
    + '</div></div>';

  if (status.error) {
    html += '<div class="connect-error">' + escHtml(status.error) + '</div>';
  } else if (machines.length === 0) {
    html += '<p style="color:var(--text-muted)">No machines registered.</p>';
  } else {
    machines.forEach(function (m) {
      var svcCount = (m.services || []).length;
      var dot = m.agentConnected
        ? '<span class="hub-dot online" style="display:inline-block"></span>'
        : '<span class="hub-dot offline" style="display:inline-block"></span>';
      html += '<div class="service-card" style="cursor:pointer" onclick="selectMachineByURL(\'' + escAttr(hub.url) + '\', \'' + escAttr(m.id || m.hostname) + '\')">'
        + '<div class="service-info">'
        + '<span class="service-name">' + dot + ' ' + escHtml(m.id || m.hostname) + '</span>'
        + '<span class="service-port">' + svcCount + ' service(s)</span>'
        + '</div></div>';
    });
  }
  pane.innerHTML = html;
}

function selectMachine(hub, machine, el) {
  clearSelection();
  el.classList.add('selected');
  renderMachineDetail(hub, machine);
}

function selectMachineByURL(hubURL, machineId) {
  var status = hubStatusCache[hubURL];
  if (!status || !status.machines) return;
  var machine = status.machines.find(function (m) {
    return (m.id || m.hostname) === machineId;
  });
  if (machine) renderMachineDetail({ url: hubURL, name: hubNameFromURL(hubURL) }, machine);
}

function renderMachineDetail(hub, machine) {
  var pane = document.getElementById('detail-pane');
  var services = machine.services || [];

  var html = '<div class="detail-header">'
    + '<h2>' + escHtml(machine.id || machine.hostname) + '</h2>'
    + '<div class="meta">' + (machine.agentConnected ? 'Online' : 'Offline')
    + ' on ' + escHtml(hub.name || hubNameFromURL(hub.url)) + '</div>'
    + '</div>';

  if (services.length === 0) {
    html += '<p style="color:var(--text-muted)">No services advertised.</p>';
  } else {
    html += '<div class="service-list">';
    services.forEach(function (svc) {
      html += '<div class="service-card">'
        + '<div class="service-info">'
        + '<span class="service-name">' + escHtml(svc.name) + '</span>'
        + '<span class="service-port">:' + svc.port + ' ' + (svc.proto || 'tcp') + '</span>'
        + '</div>'
        + '<button class="service-connect" onclick="connectService(\'' + escAttr(hub.url) + '\', \'' + escAttr(machine.id || machine.hostname) + '\', \'' + escAttr(svc.name) + '\')">Connect</button>'
        + '</div>';
    });
    html += '</div>';
  }

  html += '<div id="connect-status"></div>';
  pane.innerHTML = html;
}

function clearSelection() {
  document.querySelectorAll('.hub-item, .machine-item').forEach(function (e) {
    e.classList.remove('selected');
  });
}

function hubNameFromURL(url) {
  return url.replace(/^wss?:\/\//, '').replace(/^https?:\/\//, '').replace(/\/$/, '');
}

// --- Connect ---

function connectService(hubURL, machineId, serviceName) {
  var statusDiv = document.getElementById('connect-status');

  goApp.GetStoredToken(hubURL).then(function (token) {
    if (token) {
      doConnect(hubURL, machineId, serviceName, token);
    } else {
      promptForToken(hubURL, machineId, serviceName);
    }
  });
}

function promptForToken(hubURL, machineId, serviceName) {
  var statusDiv = document.getElementById('connect-status');
  statusDiv.innerHTML = '<div class="token-prompt">'
    + '<p>A connect token is required for <strong>' + escHtml(hubNameFromURL(hubURL)) + '</strong>.</p>'
    + '<p class="token-hint">Get this from your hub admin, or run: <code>telahubd user show-owner</code></p>'
    + '<div class="form-group"><label>Hub Token</label>'
    + '<input type="password" id="hub-token-input" placeholder="Paste token"></div>'
    + '<button class="btn-primary" id="token-submit-btn">Connect</button>'
    + '<div id="token-error" class="error-msg hidden"></div>'
    + '</div>';

  document.getElementById('token-submit-btn').onclick = function () {
    var token = document.getElementById('hub-token-input').value.trim();
    if (!token) {
      document.getElementById('token-error').textContent = 'Token is required.';
      document.getElementById('token-error').classList.remove('hidden');
      return;
    }
    goApp.AddHub(hubURL, token).then(function () {
      doConnect(hubURL, machineId, serviceName, token);
      refreshAll(); // update sidebar to show token status
    });
  };

  setTimeout(function () { document.getElementById('hub-token-input').focus(); }, 50);
}

function doConnect(hubURL, machineId, serviceName, token) {
  var statusDiv = document.getElementById('connect-status');
  statusDiv.innerHTML = '<div class="connect-msg">Connecting to ' + escHtml(machineId) + '/' + escHtml(serviceName) + '...</div>';

  goApp.Connect(hubURL, machineId, serviceName, token).then(function (msg) {
    statusDiv.innerHTML = '<div class="connect-msg">' + escHtml(msg) + '</div>'
      + '<pre class="connect-output" id="connect-output">Waiting for output...</pre>'
      + '<button class="btn-secondary btn-sm" style="margin-top:8px" onclick="disconnectService(\'' + escAttr(machineId) + '\', \'' + escAttr(serviceName) + '\')">Disconnect</button>';

    var pollId = setInterval(function () {
      goApp.GetConnectionOutput(machineId, serviceName).then(function (output) {
        var el = document.getElementById('connect-output');
        if (el && output) el.textContent = output;
      });
    }, 1000);
    setTimeout(function () { clearInterval(pollId); }, 300000);
  }).catch(function (err) {
    statusDiv.innerHTML = '<div class="connect-error">Connection failed: ' + escHtml(err) + '</div>';
  });
}

function disconnectService(machineId, serviceName) {
  goApp.Disconnect(machineId, serviceName).then(function () {
    var statusDiv = document.getElementById('connect-status');
    if (statusDiv) statusDiv.innerHTML = '<div class="connect-msg">Disconnected.</div>';
  });
}

function removeHub(url) {
  if (!confirm('Remove this hub?')) return;
  goApp.RemoveHub(url).then(function () {
    refreshAll();
    document.getElementById('detail-pane').innerHTML = '<div class="empty-state"><p>Hub removed.</p></div>';
  });
}

// --- Add Hub Modal ---

function openAddHubModal() {
  document.getElementById('add-hub-modal').classList.remove('hidden');
  showAddHubTab('manual');
}

function closeAddHubModal() {
  document.getElementById('add-hub-modal').classList.add('hidden');
}

function showAddHubTab(tab) {
  document.querySelectorAll('.tab-content').forEach(function (el) { el.classList.add('hidden'); });
  document.querySelectorAll('.tab').forEach(function (el) { el.classList.remove('active'); });
  document.getElementById('tab-' + tab).classList.remove('hidden');
  event.target.classList.add('active');

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
  var select = document.getElementById('docker-container');
  select.innerHTML = '<option value="">Loading...</option>';
  goApp.DockerListContainers().then(function (names) {
    select.innerHTML = '';
    if (!names || names.length === 0) {
      select.innerHTML = '<option value="">No containers running</option>';
      return;
    }
    names.forEach(function (name) {
      var opt = document.createElement('option');
      opt.value = name;
      opt.textContent = name;
      select.appendChild(opt);
    });
  }).catch(function (err) {
    select.innerHTML = '<option value="">Docker not available</option>';
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

// --- Portal (optional) ---

function submitPortalSignIn(event) {
  event.preventDefault();
  var url = document.getElementById('portal-url').value.trim();
  var email = document.getElementById('portal-email').value.trim();
  var password = document.getElementById('portal-password').value;
  var errEl = document.getElementById('portal-error');
  var btn = document.getElementById('portal-submit-btn');

  btn.disabled = true;
  btn.textContent = 'Connecting...';
  errEl.classList.add('hidden');

  goApp.PortalSignIn(url, email, password).then(function (name) {
    btn.disabled = false;
    btn.textContent = 'Sign In';
    closeAddHubModal();
    // Portal sign-in succeeded -- TODO: discover hubs from portal and add them
    alert('Signed in as ' + name + '. Portal hub discovery coming soon.');
  }).catch(function (err) {
    errEl.textContent = err;
    errEl.classList.remove('hidden');
    btn.disabled = false;
    btn.textContent = 'Sign In';
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
