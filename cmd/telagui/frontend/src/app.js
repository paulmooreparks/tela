'use strict';

// Wails runtime bindings
var goApp = window.go && window.go.main && window.go.main.App;

// State
var currentUser = '';
var orgData = [];
var hubStatusCache = {};

function showScreen(id) {
  document.querySelectorAll('.screen').forEach(function (el) {
    el.classList.add('hidden');
  });
  document.getElementById(id).classList.remove('hidden');

  if (id === 'log-screen') refreshLog();
}

// --- Setup ---

var selectedTools = [];

function continueSetup() {
  var wantConnect = document.getElementById('role-connect').checked;
  var wantHost = document.getElementById('role-host').checked;
  var wantHub = document.getElementById('role-hub').checked;

  if (!wantConnect && !wantHost && !wantHub) {
    alert('Select at least one role to continue.');
    return;
  }

  selectedTools = [];
  if (wantConnect) selectedTools.push('tela');
  if (wantHost) selectedTools.push('telad');
  if (wantHub) selectedTools.push('telahubd');

  var btn = document.getElementById('setup-continue');
  btn.disabled = true;
  btn.textContent = 'Checking tools...';

  document.getElementById('tool-status').style.display = '';
  var toolList = document.getElementById('tool-list');
  toolList.innerHTML = '<p class="loading">Checking installed tools...</p>';

  goApp.CheckTools().then(function (statuses) {
    return goApp.LatestRelease().then(function (latestVersion) {
      window._latestVersion = latestVersion;
      return statuses;
    }).catch(function () {
      window._latestVersion = '';
      return statuses;
    });
  }).then(function (statuses) {
    toolList.innerHTML = '';
    var allGood = true;

    selectedTools.forEach(function (name) {
      var status = statuses.find(function (s) { return s.name === name; });
      var installed = status && status.installed;

      var item = document.createElement('div');
      item.className = 'tool-item';
      item.id = 'tool-' + name;

      var badge = installed
        ? '<span class="tool-badge installed">Installed</span>'
        : '<span class="tool-badge missing">Not found</span>';
      var version = (installed && status.version)
        ? '<span class="tool-version">' + escHtml(status.version) + '</span>'
        : '';
      var installBtn = installed
        ? ''
        : ' <button class="btn-small" onclick="installSingleTool(\'' + name + '\')">Install</button>';

      item.innerHTML = '<div><span class="tool-name">' + name + '</span> ' + version + installBtn + '</div>' + badge;
      toolList.appendChild(item);

      if (!installed) allGood = false;
    });

    if (allGood) {
      btn.textContent = 'Sign In';
      btn.disabled = false;
      btn.onclick = function () { showScreen('signin-screen'); };
    } else {
      // Show install all button
      btn.textContent = 'Install Missing Tools';
      btn.disabled = false;
      btn.onclick = function () { installAllMissing(); };
    }
  }).catch(function (err) {
    toolList.innerHTML = '<p class="error-msg">Failed to check tools: ' + escHtml(err) + '</p>';
    btn.textContent = 'Retry';
    btn.disabled = false;
    btn.onclick = function () { continueSetup(); };
  });
}

function installSingleTool(name) {
  var item = document.getElementById('tool-' + name);
  if (!item) return;
  var badge = item.querySelector('.tool-badge');
  badge.className = 'tool-badge installing';
  badge.textContent = 'Installing...';
  // Remove the install button
  var installBtn = item.querySelector('.btn-small');
  if (installBtn) installBtn.remove();

  var version = window._latestVersion || 'latest';
  goApp.InstallTool(name, version).then(function (path) {
    badge.className = 'tool-badge installed';
    badge.textContent = 'Installed';
    // Check if all tools are now installed
    var anyMissing = false;
    selectedTools.forEach(function (t) {
      var el = document.getElementById('tool-' + t);
      if (el && el.querySelector('.tool-badge.missing')) anyMissing = true;
    });
    if (!anyMissing) {
      var btn = document.getElementById('setup-continue');
      btn.textContent = 'Sign In';
      btn.onclick = function () { showScreen('signin-screen'); };
    }
  }).catch(function (err) {
    badge.className = 'tool-badge missing';
    badge.textContent = 'Failed: ' + err;
  });
}

function installAllMissing() {
  var btn = document.getElementById('setup-continue');
  btn.disabled = true;
  btn.textContent = 'Installing...';

  document.getElementById('install-progress').classList.remove('hidden');
  var progress = document.getElementById('progress-fill');
  var statusEl = document.getElementById('install-status');

  var version = window._latestVersion || 'latest';
  var toInstall = [];
  selectedTools.forEach(function (name) {
    var el = document.getElementById('tool-' + name);
    if (el && el.querySelector('.tool-badge.missing')) toInstall.push(name);
  });

  if (toInstall.length === 0) {
    btn.textContent = 'Sign In';
    btn.disabled = false;
    btn.onclick = function () { showScreen('signin-screen'); };
    return;
  }

  var chain = Promise.resolve();
  toInstall.forEach(function (name, idx) {
    chain = chain.then(function () {
      statusEl.textContent = 'Installing ' + name + '...';
      progress.style.width = ((idx / toInstall.length) * 100) + '%';

      var badge = document.querySelector('#tool-' + name + ' .tool-badge');
      if (badge) {
        badge.className = 'tool-badge installing';
        badge.textContent = 'Installing...';
      }

      return goApp.InstallTool(name, version).then(function () {
        if (badge) {
          badge.className = 'tool-badge installed';
          badge.textContent = 'Installed';
        }
      });
    });
  });

  chain.then(function () {
    progress.style.width = '100%';
    statusEl.textContent = 'All tools installed.';
    btn.textContent = 'Sign In';
    btn.disabled = false;
    btn.onclick = function () { showScreen('signin-screen'); };
  }).catch(function (err) {
    statusEl.textContent = 'Install failed: ' + err;
    btn.textContent = 'Retry';
    btn.disabled = false;
    btn.onclick = function () { installAllMissing(); };
  });
}

// --- Sign In ---

function doSignIn(event) {
  event.preventDefault();
  var url = document.getElementById('portal-url').value.trim();
  var email = document.getElementById('signin-email').value.trim();
  var password = document.getElementById('signin-password').value;

  var btn = document.getElementById('signin-btn');
  var errEl = document.getElementById('signin-error');
  btn.disabled = true;
  btn.textContent = 'Signing in...';
  errEl.classList.add('hidden');

  goApp.SignIn(url, email, password).then(function (name) {
    currentUser = name;
    document.getElementById('main-user').textContent = name;
    showScreen('main-screen');
    refreshAll();
  }).catch(function (err) {
    errEl.textContent = err;
    errEl.classList.remove('hidden');
    btn.disabled = false;
    btn.textContent = 'Sign In';
  });
}

// --- Main Screen ---

function refreshAll() {
  var sidebar = document.getElementById('org-tree');
  sidebar.innerHTML = '<p class="loading">Loading...</p>';

  goApp.GetOrganizations().then(function (orgs) {
    orgData = orgs;
    renderSidebar(orgs);
  }).catch(function (err) {
    sidebar.innerHTML = '<p class="loading">Error: ' + escHtml(err) + '</p>';
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

    // Fetch hubs for this org
    goApp.GetOrgHubs(org.id).then(function (hubs) {
      hubs.forEach(function (hub) {
        // Store hub URL for connections
        if (hub.url) hubUrlMap[hub.name] = hub.url;

        var hubEl = document.createElement('div');
        hubEl.className = 'hub-item';
        hubEl.innerHTML = '<span class="hub-dot"></span>' + escHtml(hub.name);
        hubEl.onclick = function () { selectHub(org, hub, hubEl); };
        group.appendChild(hubEl);

        // Fetch status for this hub
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
            group.appendChild(mEl);
          });
        }).catch(function () {
          hubEl.querySelector('.hub-dot').className = 'hub-dot offline';
        });
      });
    });
  });
}

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
      var dot = m.agentConnected ? '<span class="hub-dot online" style="display:inline-block;width:8px;height:8px"></span>' : '<span class="hub-dot offline" style="display:inline-block;width:8px;height:8px"></span>';
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

  pane.innerHTML = html;
}

var hubUrlMap = {}; // hub name -> URL

function connectService(hubName, machineId, serviceName) {
  var hubUrl = hubUrlMap[hubName] || hubName;

  goApp.Connect(hubUrl, machineId, serviceName, '').then(function (msg) {
    var pane = document.getElementById('detail-pane');
    var statusDiv = document.getElementById('connect-status');
    if (!statusDiv) {
      statusDiv = document.createElement('div');
      statusDiv.id = 'connect-status';
      statusDiv.className = 'connect-status';
      pane.appendChild(statusDiv);
    }
    statusDiv.innerHTML = '<div class="connect-msg">' + escHtml(msg) + '</div>'
      + '<pre class="connect-output" id="connect-output">Waiting for output...</pre>';

    // Poll for output
    var pollId = setInterval(function () {
      goApp.GetConnectionOutput(machineId, serviceName).then(function (output) {
        var el = document.getElementById('connect-output');
        if (el && output) el.textContent = output;
      });
    }, 1000);

    // Stop polling after 60s
    setTimeout(function () { clearInterval(pollId); }, 60000);
  }).catch(function (err) {
    var pane = document.getElementById('detail-pane');
    var statusDiv = document.getElementById('connect-status');
    if (!statusDiv) {
      statusDiv = document.createElement('div');
      statusDiv.id = 'connect-status';
      statusDiv.className = 'connect-status';
      pane.appendChild(statusDiv);
    }
    statusDiv.innerHTML = '<div class="connect-msg error-msg">Connection failed: ' + escHtml(err) + '</div>';
  });
}

// --- Command Log ---

function refreshLog() {
  goApp.GetCommandLog().then(function (entries) {
    var el = document.getElementById('log-content');
    if (!entries || entries.length === 0) {
      el.innerHTML = '<p class="empty-state">No commands recorded yet. Actions you take in the GUI will appear here with their CLI equivalents.</p>';
      return;
    }

    var html = '';
    entries.slice().reverse().forEach(function (entry) {
      html += '<div class="log-entry">'
        + '<div class="log-time">' + escHtml(entry.time) + '</div>'
        + '<div class="log-desc">' + escHtml(entry.description) + '</div>'
        + '<div class="log-cmd-wrap">'
        + '<code class="log-cmd">' + escHtml(entry.command) + '</code>'
        + '<button class="log-copy" onclick="copyText(this, ' + escAttr(JSON.stringify(entry.command)) + ')">Copy</button>'
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
