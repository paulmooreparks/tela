'use strict';

var goApp = window.go && window.go.main && window.go.main.App;

// State
var orgData = [];
var hubStatusCache = {};
var hubUrlMap = {};
var hubTokenMap = {};

// --- Startup ---

(function init() {
  // Check for saved portals and load data immediately
  goApp.GetPortals().then(function (portals) {
    if (portals && portals.length > 0) {
      refreshAll();
    }
  });
})();

// --- Portal Management ---

function openAddPortalModal() {
  document.getElementById('add-portal-modal').classList.remove('hidden');
  document.getElementById('portal-email').focus();
}

function closeAddPortalModal() {
  document.getElementById('add-portal-modal').classList.add('hidden');
  document.getElementById('portal-error').classList.add('hidden');
  document.getElementById('add-portal-form').reset();
  document.getElementById('portal-url').value = 'https://awansaya.net';
}

function submitAddPortal(event) {
  event.preventDefault();
  var url = document.getElementById('portal-url').value.trim();
  var email = document.getElementById('portal-email').value.trim();
  var password = document.getElementById('portal-password').value;

  var btn = document.getElementById('portal-submit-btn');
  var errEl = document.getElementById('portal-error');
  btn.disabled = true;
  btn.textContent = 'Connecting...';
  errEl.classList.add('hidden');

  goApp.AddPortal(url, email, password).then(function () {
    closeAddPortalModal();
    btn.disabled = false;
    btn.textContent = 'Connect';
    refreshAll();
  }).catch(function (err) {
    errEl.textContent = err;
    errEl.classList.remove('hidden');
    btn.disabled = false;
    btn.textContent = 'Connect';
  });
}

// --- Command Log ---

function toggleLog() {
  var drawer = document.getElementById('log-drawer');
  drawer.classList.toggle('hidden');
  if (!drawer.classList.contains('hidden')) refreshLog();
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

// --- Main Data ---

function refreshAll() {
  var sidebar = document.getElementById('org-tree');
  sidebar.innerHTML = '<p class="loading">Loading...</p>';

  goApp.GetOrganizations().then(function (orgs) {
    orgData = orgs;
    if (!orgs || orgs.length === 0) {
      sidebar.innerHTML = '<div class="sidebar-empty" id="sidebar-empty">'
        + '<p>No organizations found.</p>'
        + '<p class="hint">Click <strong>Add Portal</strong> to connect.</p></div>';
      return;
    }
    renderSidebar(orgs);
  }).catch(function (err) {
    sidebar.innerHTML = '<div class="sidebar-empty">'
      + '<p>Could not load data.</p>'
      + '<p class="hint">' + escHtml(String(err)) + '</p>'
      + '<button class="btn-primary" onclick="openAddPortalModal()">Add Portal</button></div>';
  });
}

function renderSidebar(orgs) {
  var sidebar = document.getElementById('org-tree');
  sidebar.innerHTML = '';

  orgs.forEach(function (org) {
    var group = document.createElement('div');
    group.className = 'org-group';

    var label = document.createElement('span');
    label.className = 'org-label';
    label.textContent = org.name;
    group.appendChild(label);

    sidebar.appendChild(group);

    goApp.GetOrgHubs(org.id).then(function (hubs) {
      hubs.forEach(function (hub) {
        if (hub.url) hubUrlMap[hub.name] = hub.url;
        if (hub.viewerToken) hubTokenMap[hub.name] = hub.viewerToken;

        var hubContainer = document.createElement('div');
        hubContainer.className = 'hub-group';

        var hubEl = document.createElement('div');
        hubEl.className = 'hub-item';
        hubEl.innerHTML = '<span class="hub-dot"></span>' + escHtml(hub.name);
        hubEl.onclick = function () { selectHub(org, hub, hubEl); };
        hubContainer.appendChild(hubEl);

        group.appendChild(hubContainer);

        goApp.GetHubStatus(hub.name).then(function (status) {
          var machines = status.machines || [];
          var isOnline = machines.length > 0 || !status.error;
          hubEl.querySelector('.hub-dot').className = 'hub-dot ' + (isOnline ? 'online' : 'offline');

          hubStatusCache[hub.name] = status;

          machines.forEach(function (m) {
            var mEl = document.createElement('div');
            mEl.className = 'machine-item';
            var dotClass = m.agentConnected ? 'online' : 'offline';
            mEl.innerHTML = '<span class="machine-dot ' + dotClass + '"></span>' + escHtml(m.id || m.hostname);
            mEl.onclick = function (e) {
              e.stopPropagation();
              selectMachine(org, hub, m, mEl);
            };
            hubContainer.appendChild(mEl);
          });
        }).catch(function () {
          hubEl.querySelector('.hub-dot').className = 'hub-dot offline';
        });
      });
    });
  });
}

// --- Detail Pane ---

function selectHub(org, hub, el) {
  document.querySelectorAll('.hub-item, .machine-item').forEach(function (e) {
    e.classList.remove('selected');
  });
  el.classList.add('selected');

  var pane = document.getElementById('detail-pane');
  var status = hubStatusCache[hub.name] || {};
  var machines = status.machines || [];

  var html = '<div class="detail-header">'
    + '<h2>' + escHtml(hub.name) + '</h2>'
    + '<div class="meta">' + escHtml(hub.url || '') + '</div>'
    + '</div>';

  if (machines.length === 0) {
    html += '<p style="color:var(--text-muted)">No machines registered on this hub.</p>';
  } else {
    html += '<h3 style="margin-bottom:12px">' + machines.length + ' machine(s)</h3>';
    machines.forEach(function (m) {
      var svcCount = (m.services || []).length;
      var dot = m.agentConnected
        ? '<span class="hub-dot online" style="display:inline-block;width:8px;height:8px"></span>'
        : '<span class="hub-dot offline" style="display:inline-block;width:8px;height:8px"></span>';
      html += '<div class="service-card" style="cursor:pointer" onclick="selectMachineByName(\'' + escAttr(hub.name) + '\', \'' + escAttr(m.id || m.hostname) + '\')">'
        + '<div class="service-info">'
        + '<span class="service-name">' + dot + ' ' + escHtml(m.id || m.hostname) + '</span>'
        + '<span class="service-port">' + svcCount + ' service(s)</span>'
        + '</div></div>';
    });
  }

  pane.innerHTML = html;
}

function selectMachine(org, hub, machine, el) {
  document.querySelectorAll('.hub-item, .machine-item').forEach(function (e) {
    e.classList.remove('selected');
  });
  el.classList.add('selected');
  renderMachineDetail(hub, machine);
}

function selectMachineByName(hubName, machineId) {
  var status = hubStatusCache[hubName];
  if (!status) return;
  var machine = (status.machines || []).find(function (m) {
    return (m.id || m.hostname) === machineId;
  });
  if (!machine) return;
  renderMachineDetail({ name: hubName }, machine);
}

function renderMachineDetail(hub, machine) {
  var pane = document.getElementById('detail-pane');
  var services = machine.services || [];
  var dot = machine.agentConnected ? 'Online' : 'Offline';

  var html = '<div class="detail-header">'
    + '<h2>' + escHtml(machine.id || machine.hostname) + '</h2>'
    + '<div class="meta">' + dot + ' on ' + escHtml(hub.name || hub.url || '') + '</div>'
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
        + '<button class="service-connect" onclick="connectService(\'' + escAttr(hub.name) + '\', \'' + escAttr(machine.id || machine.hostname) + '\', \'' + escAttr(svc.name) + '\')">Connect</button>'
        + '</div>';
    });
    html += '</div>';
  }

  html += '<div id="connect-status"></div>';
  pane.innerHTML = html;
}

// --- Connect ---

function connectService(hubName, machineId, serviceName) {
  var hubUrl = hubUrlMap[hubName] || hubName;
  var statusDiv = document.getElementById('connect-status');

  goApp.GetStoredToken(hubUrl).then(function (token) {
    if (token) {
      doConnect(hubUrl, hubName, machineId, serviceName, token);
    } else {
      promptForToken(hubUrl, hubName, machineId, serviceName);
    }
  });
}

function promptForToken(hubUrl, hubName, machineId, serviceName) {
  var statusDiv = document.getElementById('connect-status');
  statusDiv.innerHTML = '<div class="token-prompt">'
    + '<p>A connect token is required for <strong>' + escHtml(hubName) + '</strong>.</p>'
    + '<p class="token-hint">Get this from your hub admin, or run: <code>telahubd user show-owner</code></p>'
    + '<div class="form-group"><label for="hub-token-input">Hub Token</label>'
    + '<input type="password" id="hub-token-input" placeholder="Paste your hub token here"></div>'
    + '<div class="form-actions">'
    + '<button class="btn-primary" id="token-submit-btn">Connect</button>'
    + '</div>'
    + '<div id="token-error" class="error-msg hidden"></div>'
    + '</div>';

  document.getElementById('token-submit-btn').onclick = function () {
    var token = document.getElementById('hub-token-input').value.trim();
    if (!token) {
      document.getElementById('token-error').textContent = 'Token is required.';
      document.getElementById('token-error').classList.remove('hidden');
      return;
    }
    goApp.StoreToken(hubUrl, token).then(function () {
      doConnect(hubUrl, hubName, machineId, serviceName, token);
    }).catch(function (err) {
      document.getElementById('token-error').textContent = 'Failed to store token: ' + err;
      document.getElementById('token-error').classList.remove('hidden');
    });
  };

  setTimeout(function () { document.getElementById('hub-token-input').focus(); }, 100);
}

function doConnect(hubUrl, hubName, machineId, serviceName, token) {
  var statusDiv = document.getElementById('connect-status');
  statusDiv.innerHTML = '<div class="connect-msg">Connecting to ' + escHtml(machineId) + '/' + escHtml(serviceName) + '...</div>';

  goApp.Connect(hubUrl, machineId, serviceName, token).then(function (msg) {
    statusDiv.innerHTML = '<div class="connect-msg">' + escHtml(msg) + '</div>'
      + '<pre class="connect-output" id="connect-output">Waiting for output...</pre>';

    var pollId = setInterval(function () {
      goApp.GetConnectionOutput(machineId, serviceName).then(function (output) {
        var el = document.getElementById('connect-output');
        if (el && output) el.textContent = output;
      });
    }, 1000);

    setTimeout(function () { clearInterval(pollId); }, 300000);
  }).catch(function (err) {
    statusDiv.innerHTML = '<div class="connect-msg connect-error">Connection failed: ' + escHtml(err) + '</div>';
  });
}

// --- Utilities ---

function escHtml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function escAttr(s) {
  return String(s).replace(/&/g, '&amp;').replace(/'/g, '&#39;').replace(/"/g, '&quot;');
}
