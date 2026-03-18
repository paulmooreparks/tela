'use strict';

var goApp = window.go && window.go.main && window.go.main.App;
var hubStatusCache = {};
// selectedServices: { "hubURL||machineId||serviceName": { hub, machine, service, servicePort, localPort } }
var selectedServices = {};
var pollIntervalId = null;
var selectedHubURL = null;
var selectedMachineId = null;
var verboseMode = false;

// --- Tabs ---

function switchTab(name, btn) {
  document.querySelectorAll('.tab-pane').forEach(function (el) { el.classList.add('hidden'); });
  document.querySelectorAll('.main-tab').forEach(function (el) { el.classList.remove('active'); });
  document.getElementById('tab-' + name).classList.remove('hidden');
  btn.classList.add('active');
  if (name === 'terminal') refreshTerminal();
  if (name === 'log') refreshLog();
  if (name === 'about') refreshAbout();
  if (name === 'settings') refreshSettings();
}

// --- Startup ---
refreshVersionDisplay();
refreshProfileList();
loadSavedSelections().then(function () {
  refreshAll();
  // Auto-connect if enabled and there are saved selections
  goApp.ShouldAutoConnect().then(function (should) {
    if (should && Object.keys(selectedServices).length > 0) {
      setTimeout(function () {
        doConnect();
      }, 1500); // delay to let hub status load
    }
  });
});

// Check for updates after a short delay (versions need time to fetch)
setTimeout(function () {
  refreshVersionDisplay();
  checkForUpdate();
}, 4000);

function refreshVersionDisplay() {
  goApp.GetToolVersions().then(function (tv) {
    var el = document.getElementById('app-versions');
    var guiClass = tv.guiBehind ? 'ver-behind' : 'ver-current';
    var cliClass = tv.cliBehind ? 'ver-behind' : 'ver-current';
    var guiTitle = tv.guiBehind ? 'Update available: ' + tv.latest : '';
    var cliTitle = tv.cliBehind ? 'Update available: ' + tv.latest : '';

    el.innerHTML = '<span><span class="ver-label">telagui:</span> '
      + '<span class="' + guiClass + '" title="' + escHtml(guiTitle) + '">' + escHtml(tv.gui || 'dev') + '</span></span>'
      + '<span><span class="ver-label">tela:</span> '
      + '<span class="' + cliClass + '" title="' + escHtml(cliTitle) + '">' + escHtml(tv.cli || '?') + '</span></span>';
  });
}

function checkForUpdate() {
  goApp.HasUpdate().then(function (pending) {
    var btn = document.getElementById('update-btn');
    if (pending) {
      goApp.GetUpdateVersion().then(function (ver) {
        goApp.GetToolVersions().then(function (tv) {
          if (!tv.guiBehind && !tv.cliBehind) return;

          goApp.IsPackageManaged().then(function (managed) {
            if (managed) {
              if (tv.cliBehind) {
                btn.textContent = 'Update CLI (' + ver + ')';
                btn.classList.remove('hidden');
              }
              // Don't offer GUI update for package-managed installs
            } else {
              btn.textContent = 'Restart to Update (' + ver + ')';
              btn.classList.remove('hidden');
            }
          });
        });
      });
    }
  });
}

function restartToUpdate() {
  var btn = document.getElementById('update-btn');
  btn.textContent = 'Updating...';
  btn.disabled = true;
  goApp.RestartToUpdate().then(function () {
    // If we're still here, it was a package-managed install (CLI-only update)
    btn.classList.add('hidden');
    btn.disabled = false;
    refreshVersionDisplay();
  }).catch(function (err) {
    btn.textContent = 'Update failed';
    btn.disabled = false;
    setTimeout(function () { btn.classList.add('hidden'); }, 3000);
  });
}

function loadSavedSelections() {
  return goApp.LoadProfile().then(function (connections) {
    if (!connections) return;
    var requests = [];
    connections.forEach(function (conn) {
      var hubURL = conn.hub;
      var machineId = conn.machine;
      (conn.services || []).forEach(function (svc) {
        var key = hubURL + '||' + machineId + '||' + svc.name;
        selectedServices[key] = {
          hub: hubURL,
          machine: machineId,
          service: svc.name,
          servicePort: svc.local,
          localPort: svc.local
        };
        requests.push({ key: key, servicePort: svc.local });
      });
    });
    // Resolve ports to catch any new clashes since last save
    if (requests.length > 0) {
      return goApp.ResolveAllPorts(JSON.stringify(requests)).then(function (assignments) {
        if (assignments) {
          assignments.forEach(function (a) {
            if (selectedServices[a.key]) {
              selectedServices[a.key].localPort = a.localPort;
            }
          });
        }
      });
    }
  }).catch(function () {});
}

function refreshProfileList() {
  goApp.ListProfiles().then(function (profiles) {
    var select = document.getElementById('profile-select');
    goApp.GetCurrentProfile().then(function (current) {
      select.innerHTML = '';
      profiles.forEach(function (name) {
        var opt = document.createElement('option');
        opt.value = name;
        opt.textContent = name;
        if (name === current) opt.selected = true;
        select.appendChild(opt);
      });
    });
  });
}

function switchProfile(name) {
  goApp.SwitchProfile(name).then(function () {
    selectedServices = {};
    loadSavedSelections().then(function () {
      refreshAll();
      updateConnectButton();
    });
  });
}

function newProfile() {
  var name = prompt('New profile name:');
  if (!name) return;
  goApp.CreateProfile(name).then(function () {
    goApp.SwitchProfile(name).then(function () {
      selectedServices = {};
      refreshProfileList();
      refreshAll();
      updateConnectButton();
    });
  }).catch(function (err) {
    alert('Failed: ' + err);
  });
}

function doRefresh() {
  refreshAll();
  goApp.CheckForUpdatesNow().then(function () {
    refreshVersionDisplay();
    checkForUpdate();
  });
}

// --- Sidebar ---

function refreshAll() {
  var content = document.getElementById('sidebar-content');
  content.innerHTML = '<p class="loading">Loading hubs...</p>';

  goApp.GetKnownHubs().then(function (hubs) {
    if (!hubs || hubs.length === 0) {
      content.innerHTML = '<div class="sidebar-empty">'
        + '<p>No hubs configured.</p>'
        + '<p class="hint">Click <strong>Add Hub</strong> to get started.</p></div>';
      return;
    }

    content.innerHTML = '';
    hubs.forEach(function (hub) {
      var hubContainer = document.createElement('div');
      hubContainer.className = 'hub-group';

      var hubEl = document.createElement('div');
      hubEl.className = 'hub-item';
      if (selectedHubURL === hub.url && !selectedMachineId) hubEl.classList.add('selected');
      hubEl.innerHTML = '<span class="hub-dot"></span>'
        + '<span class="hub-name">' + escHtml(hub.name) + '</span>'
        + (!hub.hasToken ? '<span class="no-token-badge">no token</span>' : '');
      hubEl.onclick = function () { selectHub(hub, hubEl); };
      hubContainer.appendChild(hubEl);

      content.appendChild(hubContainer);

      if (hub.hasToken) {
        goApp.GetHubStatus(hub.url).then(function (status) {
          hubStatusCache[hub.url] = status;
          hubEl.querySelector('.hub-dot').className = 'hub-dot ' + (status.online ? 'online' : 'offline');

          if (status.machines) {
            status.machines.forEach(function (m) {
              var mId = m.id || m.hostname;
              var mEl = document.createElement('div');
              mEl.className = 'machine-item';
              if (selectedHubURL === hub.url && selectedMachineId === mId) mEl.classList.add('selected');
              var dotClass = m.agentConnected ? 'online' : 'offline';
              mEl.innerHTML = '<span class="machine-dot ' + dotClass + '"></span>'
                + escHtml(mId);
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

    updateConnectButton();
  });
}

// --- Detail Pane: Hub View ---

function selectHub(hub, el) {
  clearSelection();
  el.classList.add('selected');
  selectedHubURL = hub.url;
  selectedMachineId = null;
  renderHubDetail(hub);
}

function renderHubDetail(hub) {
  var pane = document.getElementById('detail-pane');
  var status = hubStatusCache[hub.url] || {};
  var machineCount = (status.machines || []).length;
  var onlineCount = (status.machines || []).filter(function (m) { return m.agentConnected; }).length;

  // Count selected services for this hub
  var selectedCount = 0;
  Object.keys(selectedServices).forEach(function (key) {
    if (key.indexOf(hub.url + '||') === 0) selectedCount++;
  });

  var html = '<div class="detail-header">'
    + '<h2>' + escHtml(status.hubName || hub.name) + '</h2>'
    + '<div class="meta">' + escHtml(hub.url) + '</div>'
    + '</div>';

  html += '<div class="hub-stats">'
    + '<div class="stat"><span class="stat-value">' + machineCount + '</span><span class="stat-label">Machines</span></div>'
    + '<div class="stat"><span class="stat-value">' + onlineCount + '</span><span class="stat-label">Online</span></div>'
    + '<div class="stat"><span class="stat-value">' + selectedCount + '</span><span class="stat-label">Selected Services</span></div>'
    + '</div>';

  if (status.error) {
    html += '<div class="connect-error">' + escHtml(status.error) + '</div>';
  }

  html += '<div class="hub-actions">'
    + '<button class="btn-secondary btn-sm" onclick="removeHub(\'' + escAttr(hub.url) + '\')">Remove Hub</button>'
    + '</div>';

  pane.innerHTML = html;
}

// --- Detail Pane: Machine View ---

function selectMachine(hub, machine, el) {
  clearSelection();
  el.classList.add('selected');
  selectedHubURL = hub.url;
  selectedMachineId = machine.id || machine.hostname;
  renderMachineDetail(hub, machine);
}

function renderMachineDetail(hub, machine) {
  var pane = document.getElementById('detail-pane');
  var machineId = machine.id || machine.hostname;
  var services = machine.services || [];

  goApp.GetConnectionState().then(function (connState) {
    var isConnected = connState.connected;

    var html = '<div class="detail-header">'
      + '<h2>' + escHtml(machineId) + '</h2>'
      + '<div class="meta">' + (machine.agentConnected ? 'Online' : 'Offline')
      + ' on ' + escHtml(hub.name || hubNameFromURL(hub.url)) + '</div>'
      + '</div>';

    if (services.length === 0) {
      html += '<p class="no-services">No services advertised.</p>';
    } else {
      html += '<div class="service-checklist">';
      services.forEach(function (svc) {
        var key = hub.url + '||' + machineId + '||' + svc.name;
        var checked = selectedServices[key] ? ' checked' : '';
        var disabled = isConnected ? ' disabled' : '';
        var localPort = selectedServices[key] ? selectedServices[key].localPort : svc.port;

        html += '<label class="service-check-item">'
          + '<input type="checkbox"' + checked + disabled
          + ' onchange="toggleService(\'' + escAttr(hub.url) + '\', \'' + escAttr(machineId) + '\', \'' + escAttr(svc.name) + '\', ' + svc.port + ', this.checked)">'
          + '<span class="service-check-name">' + escHtml(svc.name) + '</span>'
          + '<span class="service-check-port">:' + svc.port + ' ' + escHtml(svc.proto || 'tcp') + '</span>';

        if (selectedServices[key]) {
          html += '<span class="local-port-label">localhost:' + localPort + '</span>';
        }
        html += '</label>';
      });
      html += '</div>';
    }

    // Show connected badge (output is in the Terminal overlay)
    if (isConnected) {
      html += '<div class="connected-panel">'
        + '<div class="connected-header">'
        + '<span class="connected-badge">Connected</span>'
        + '<span class="connected-pid">PID ' + connState.pid + '</span>'
        + '<button class="btn-link" onclick="toggleTerminal()">View Terminal</button>'
        + '</div></div>';
    }

    pane.innerHTML = html;
  });
}

// --- Service Selection ---

function toggleService(hubURL, machineId, serviceName, servicePort, checked) {
  var key = hubURL + '||' + machineId + '||' + serviceName;
  if (checked) {
    selectedServices[key] = {
      hub: hubURL,
      machine: machineId,
      service: serviceName,
      servicePort: servicePort,
      localPort: 0 // will be resolved
    };
  } else {
    delete selectedServices[key];
  }
  resolveAllPortsAndUpdate();
}

function resolveAllPortsAndUpdate() {
  // Build port requests from all selections
  var requests = [];
  Object.keys(selectedServices).forEach(function (key) {
    requests.push({
      key: key,
      servicePort: selectedServices[key].servicePort
    });
  });

  if (requests.length === 0) {
    updateConnectButton();
    persistSelections();
    refreshCurrentPane();
    return;
  }

  goApp.ResolveAllPorts(JSON.stringify(requests)).then(function (assignments) {
    if (assignments) {
      assignments.forEach(function (a) {
        if (selectedServices[a.key]) {
          selectedServices[a.key].localPort = a.localPort;
        }
      });
    }
    updateConnectButton();
    persistSelections();
    refreshCurrentPane();
  });
}

var persistTimer = null;

function persistSelections() {
  if (persistTimer) clearTimeout(persistTimer);
  persistTimer = setTimeout(doPersistSelections, 500);
}

function doPersistSelections() {
  // Build profile and save immediately
  var groups = {};
  Object.keys(selectedServices).forEach(function (key) {
    var sel = selectedServices[key];
    var groupKey = sel.hub + '||' + sel.machine;
    if (!groups[groupKey]) {
      groups[groupKey] = { hub: sel.hub, machine: sel.machine, services: [] };
    }
    groups[groupKey].services.push({ name: sel.service, local: sel.localPort });
  });

  var connections = [];
  Object.keys(groups).forEach(function (k) {
    var g = groups[k];
    connections.push({
      hub: toWSURL(g.hub),
      machine: g.machine,
      services: g.services
    });
  });

  if (connections.length > 0) {
    goApp.SaveProfile(JSON.stringify(connections));
  }
}

function refreshCurrentPane() {
  if (!selectedHubURL) return;
  goApp.GetKnownHubs().then(function (hubs) {
    var hub = hubs.find(function (h) { return h.url === selectedHubURL; });
    if (!hub) return;
    if (selectedMachineId) {
      var status = hubStatusCache[hub.url] || {};
      var machine = (status.machines || []).find(function (m) {
        return (m.id || m.hostname) === selectedMachineId;
      });
      if (machine) renderMachineDetail(hub, machine);
    } else {
      renderHubDetail(hub);
    }
  });
}

// --- Connect/Disconnect Toggle ---

function updateConnectButton() {
  var btn = document.getElementById('connect-btn');
  goApp.GetConnectionState().then(function (state) {
    if (state.connected) {
      btn.textContent = 'Disconnect';
      btn.className = 'topbar-btn disconnect-btn';
    } else {
      var hasSelections = Object.keys(selectedServices).length > 0;
      btn.textContent = 'Connect';
      btn.className = 'topbar-btn connect-btn' + (hasSelections ? '' : ' disabled');
    }
  });
}

function toggleConnection() {
  goApp.GetConnectionState().then(function (state) {
    if (state.connected) {
      doDisconnect();
    } else {
      doConnect();
    }
  });
}

function doConnect() {
  var groups = {};
  Object.keys(selectedServices).forEach(function (key) {
    var sel = selectedServices[key];
    var groupKey = sel.hub + '||' + sel.machine;
    if (!groups[groupKey]) {
      groups[groupKey] = { hub: sel.hub, machine: sel.machine, services: [] };
    }
    groups[groupKey].services.push({ name: sel.service, local: sel.localPort });
  });

  var connections = [];
  Object.keys(groups).forEach(function (k) {
    var g = groups[k];
    connections.push({
      hub: toWSURL(g.hub),
      machine: g.machine,
      services: g.services
    });
  });

  if (connections.length === 0) return;

  goApp.Connect(JSON.stringify(connections)).then(function () {
    startConnectionPoll();
    updateConnectButton();
    refreshCurrentPane();
    // Apply verbose preference (saved toggle or default setting)
    setTimeout(function () {
      if (verboseMode) {
        goApp.SetVerbose(true);
      } else {
        goApp.GetSettings().then(function (s) {
          if (s.verboseDefault) {
            verboseMode = true;
            var btn = document.getElementById('verbose-btn');
            var icon = document.getElementById('verbose-icon');
            if (btn) btn.classList.add('active');
            if (icon) icon.innerHTML = '\u2611';
            goApp.SetVerbose(true);
          }
        });
      }
    }, 2000);
  }).catch(function (err) {
    alert('Connection failed: ' + err);
  });
}

function doDisconnect() {
  // Show "Disconnecting..." state immediately
  var btn = document.getElementById('connect-btn');
  btn.textContent = 'Disconnecting...';
  btn.className = 'topbar-btn disconnecting-btn';
  btn.disabled = true;

  goApp.Disconnect().then(function () {
    stopConnectionPoll();
    btn.disabled = false;
    updateConnectButton();
    refreshCurrentPane();
    refreshTerminal();
  }).catch(function (err) {
    stopConnectionPoll();
    btn.disabled = false;
    updateConnectButton();
    refreshCurrentPane();
  });
}

function startConnectionPoll() {
  stopConnectionPoll();
  pollIntervalId = setInterval(function () {
    goApp.GetConnectionState().then(function (state) {
      // Refresh terminal if visible
      if (!document.getElementById('tab-terminal').classList.contains('hidden')) {
        refreshTerminal();
      }
      if (!state.connected) {
        stopConnectionPoll();
        updateConnectButton();
        refreshCurrentPane();
        // Auto-reconnect if enabled
        goApp.GetSettings().then(function (s) {
          if (s.reconnectOnDrop && Object.keys(selectedServices).length > 0) {
            setTimeout(function () {
              doConnect();
            }, 3000);
          }
        });
      }
    });
  }, 1000);
}

function stopConnectionPoll() {
  if (pollIntervalId) {
    clearInterval(pollIntervalId);
    pollIntervalId = null;
  }
}

// --- Hub Management ---

function removeHub(url) {
  if (!confirm('Remove this hub and all its saved selections?')) return;
  // Remove selections for this hub
  Object.keys(selectedServices).forEach(function (key) {
    if (key.indexOf(url + '||') === 0) delete selectedServices[key];
  });
  goApp.RemoveHub(url).then(function () {
    selectedHubURL = null;
    selectedMachineId = null;
    persistSelections();
    refreshAll();
    document.getElementById('detail-pane').innerHTML = '<div class="empty-state"><p>Hub removed.</p></div>';
  });
}

function clearSelection() {
  document.querySelectorAll('.hub-item, .machine-item').forEach(function (e) {
    e.classList.remove('selected');
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

// Auto-detect credential type as user types
(function () {
  setTimeout(function () {
    var input = document.getElementById('hub-credential');
    if (!input) return;
    input.addEventListener('input', function () {
      var val = this.value.trim();
      var hint = document.getElementById('credential-hint');
      if (!val) {
        hint.textContent = '';
        hint.className = 'field-hint';
        return;
      }
      goApp.DetectCredentialType(val).then(function (type) {
        if (type === 'code') {
          hint.textContent = 'Detected: pairing code';
          hint.className = 'field-hint detected';
        } else if (type === 'token') {
          hint.textContent = 'Detected: hub token';
          hint.className = 'field-hint detected';
        } else {
          hint.textContent = '';
          hint.className = 'field-hint';
        }
      });
    });
  }, 200);
})();

function submitAddHub(event) {
  event.preventDefault();
  var url = document.getElementById('hub-url').value.trim();
  var credential = document.getElementById('hub-credential').value.trim();
  var errEl = document.getElementById('add-hub-error');
  var btn = document.getElementById('add-hub-submit');
  errEl.classList.add('hidden');
  btn.disabled = true;
  btn.textContent = 'Connecting...';

  goApp.DetectCredentialType(credential).then(function (type) {
    if (type === 'code') {
      // Pairing code: exchange via tela pair
      return goApp.PairWithCode(url, credential);
    } else {
      // Raw token: store directly
      return goApp.AddHub(url, credential);
    }
  }).then(function () {
    closeAddHubModal();
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

// --- Docker ---

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

// --- Terminal ---

var terminalAutoScroll = true;
var terminalFrozen = false;

function freezeTerminal() {
  terminalFrozen = true;
  document.getElementById('terminal-output').classList.add('frozen');
}

function unfreezeTerminal() {
  terminalFrozen = false;
  document.getElementById('terminal-output').classList.remove('frozen');
  refreshTerminal();
}

function showCopyToast() {
  var toast = document.getElementById('copy-toast');
  toast.textContent = 'Copied to clipboard';
  toast.classList.add('visible');
  clearTimeout(toast._fadeTimer);
  toast._fadeTimer = setTimeout(function () {
    toast.classList.remove('visible');
  }, 3000);
}

function copyTerminal() {
  freezeTerminal();
  var text = document.getElementById('terminal-output').textContent;
  navigator.clipboard.writeText(text).then(function () {
    showCopyToast();
    setTimeout(unfreezeTerminal, 200);
  });
}

function saveTerminal() {
  freezeTerminal();
  var text = document.getElementById('terminal-output').textContent;
  goApp.SaveTerminalOutput(text).then(function () {
    setTimeout(unfreezeTerminal, 200);
  }).catch(function () {
    unfreezeTerminal();
  });
}

function toggleVerbose() {
  verboseMode = !verboseMode;
  var btn = document.getElementById('verbose-btn');
  var icon = document.getElementById('verbose-icon');
  if (verboseMode) {
    btn.classList.add('active');
    icon.innerHTML = '&#x2611;';
  } else {
    btn.classList.remove('active');
    icon.innerHTML = '&#x2610;';
  }
  goApp.GetConnectionState().then(function (state) {
    if (state.connected) {
      goApp.SetVerbose(verboseMode);
    }
  });
}

function refreshTerminal() {
  if (terminalFrozen) return;

  goApp.GetConnectionState().then(function (state) {
    var output = document.getElementById('terminal-output');
    var status = document.getElementById('terminal-status');

    if (state.connected) {
      status.textContent = 'Connected (PID ' + state.pid + ')';
      status.className = 'terminal-status connected';
    } else {
      status.textContent = 'Disconnected';
      status.className = 'terminal-status disconnected';
    }

    var prevLength = output.textContent.length;
    if (state.output) {
      output.textContent = state.output;
    } else if (!state.connected) {
      output.textContent = 'Not connected.';
    }

    // Auto-scroll to bottom if content changed and user hasn't scrolled up
    if (terminalAutoScroll && output.textContent.length !== prevLength) {
      requestAnimationFrame(function () {
        output.scrollTop = output.scrollHeight;
      });
    }
  });
}

// Set up scroll detection and mouse freeze on the terminal output
(function () {
  setTimeout(function () {
    var el = document.getElementById('terminal-output');
    if (!el) return;

    el.addEventListener('scroll', function () {
      var atBottom = (el.scrollHeight - el.scrollTop - el.clientHeight) < 30;
      terminalAutoScroll = atBottom;
    });

    // Freeze updates on mouse down to allow text selection
    el.addEventListener('mousedown', function () {
      freezeTerminal();
    });

    // On mouse up, auto-copy selection and unfreeze after a short delay
    el.addEventListener('mouseup', function () {
      var sel = window.getSelection();
      if (sel && sel.toString().length > 0) {
        navigator.clipboard.writeText(sel.toString()).then(function () {
          showCopyToast();
        }).catch(function () {});
        // Keep frozen briefly so the selection is visible
        setTimeout(unfreezeTerminal, 1500);
      } else {
        unfreezeTerminal();
      }
    });
  }, 100);
})();


// --- Settings ---

function refreshSettings() {
  goApp.GetSettings().then(function (s) {
    document.getElementById('setting-autoConnect').checked = s.autoConnect;
    document.getElementById('setting-reconnectOnDrop').checked = s.reconnectOnDrop;
    var radios = document.querySelectorAll('input[name="minimizeTo"]');
    radios.forEach(function (r) { r.checked = (r.value === (s.minimizeTo || 'taskbar')); });
    document.getElementById('setting-startMinimized').checked = s.startMinimized;
    document.getElementById('setting-minimizeOnClose').checked = s.minimizeOnClose;
    document.getElementById('setting-autoCheckUpdates').checked = s.autoCheckUpdates;
    document.getElementById('setting-verboseDefault').checked = s.verboseDefault;
  });
  goApp.GetCLIPath().then(function (path) {
    document.getElementById('settings-cli-path').textContent = path;
  });
}

function saveSetting() {
  var s = {
    autoConnect: document.getElementById('setting-autoConnect').checked,
    reconnectOnDrop: document.getElementById('setting-reconnectOnDrop').checked,
    minimizeTo: document.querySelector('input[name="minimizeTo"]:checked').value,
    startMinimized: document.getElementById('setting-startMinimized').checked,
    minimizeOnClose: document.getElementById('setting-minimizeOnClose').checked,
    autoCheckUpdates: document.getElementById('setting-autoCheckUpdates').checked,
    verboseDefault: document.getElementById('setting-verboseDefault').checked
  };
  goApp.SaveSettings(JSON.stringify(s));
}

function clearCredentialStore() {
  if (!confirm('This will delete all stored hub tokens. You will need to re-authenticate with each hub. Continue?')) return;
  goApp.ClearCredentialStore().then(function () {
    refreshAll();
  }).catch(function (err) {
    alert('Failed to clear credential store: ' + err);
  });
}

function importProfile() {
  goApp.ImportProfile().then(function () {
    loadSavedSelections().then(function () {
      refreshAll();
    });
  }).catch(function (err) {
    if (err) alert('Import failed: ' + err);
  });
}

function exportProfile() {
  goApp.ExportProfile().catch(function (err) {
    if (err) alert('Export failed: ' + err);
  });
}

// --- About ---

function refreshAbout() {
  goApp.GetToolVersions().then(function (tv) {
    var el = document.getElementById('about-version');
    el.textContent = 'telagui ' + (tv.gui || 'dev') + '  |  tela ' + (tv.cli || 'not installed');
  });
}

// --- Command Log ---

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

function hubNameFromURL(url) {
  return url.replace(/^wss?:\/\//, '').replace(/^https?:\/\//, '').replace(/\/$/, '');
}

function toWSURL(url) {
  if (url.indexOf('https://') === 0) return 'wss://' + url.substring(8);
  if (url.indexOf('http://') === 0) return 'ws://' + url.substring(7);
  if (url.indexOf('wss://') !== 0 && url.indexOf('ws://') !== 0) return 'wss://' + url;
  return url;
}
