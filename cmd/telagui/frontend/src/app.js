'use strict';

var goApp = window.go && window.go.main && window.go.main.App;
var hubStatusCache = {};
// selectedServices: { "hubURL||machineId||serviceName": { hub, machine, service, servicePort, localPort } }
var selectedServices = {};
var pollIntervalId = null;
var selectedHubURL = null;
var selectedMachineId = null;
var activeTunnels = {}; // "machine:localPort" -> connection count
var verboseMode = false;
var savedFingerprint = ''; // fingerprint of selections at last save/load
var savedServicesJSON = '{}'; // full selectedServices JSON for Undo restore
var savedIncludedHubsJSON = '{}'; // full includedHubs JSON for Undo restore
var profileDirty = false;

// --- Tabs ---

function switchTab(name, btn) {
  document.querySelectorAll('.tab-pane').forEach(function (el) { el.classList.add('hidden'); });
  document.querySelectorAll('.main-tab').forEach(function (el) {
    el.classList.remove('active');
    el.setAttribute('aria-selected', 'false');
  });
  document.getElementById('tab-' + name).classList.remove('hidden');
  btn.classList.add('active');
  btn.setAttribute('aria-selected', 'true');
  if (name === 'status') refreshStatus();
  if (name === 'profile') showProfileOverview();
  if (name === 'terminal') refreshTerminal();
  if (name === 'log') refreshLog();
  if (name === 'about') refreshAbout();
  if (name === 'settings') refreshSettings();
  if (name === 'hubs') refreshHubsTab();
}

// --- Status Tab ---

function refreshStatus() {
  var profileNameEl = document.getElementById('status-profile-name');
  var badge = document.getElementById('status-conn-state');
  var container = document.getElementById('status-services');
  if (!profileNameEl || !badge || !container) return;

  profileNameEl.textContent = document.getElementById('profile-select')
    ? document.getElementById('profile-select').value || 'telagui'
    : 'telagui';

  // Build groups from selectedServices (already in memory, no Go call needed)
  var groups = {};
  Object.keys(selectedServices).forEach(function (key) {
    var sel = selectedServices[key];
    var gk = sel.hub + '||' + sel.machine;
    if (!groups[gk]) {
      groups[gk] = { hub: sel.hub, hubName: hubNameFromURL(sel.hub), machine: sel.machine, services: [] };
    }
    groups[gk].services.push(sel);
  });

  if (Object.keys(groups).length === 0) {
    badge.textContent = 'Disconnected';
    badge.className = 'status-connection-state disconnected';
    container.innerHTML = '<p class="empty-hint">No services selected. Go to <strong>Profiles</strong> to select hubs, machines, and services.</p>';
    return;
  }

  // Single Go call -- not nested inside anything
  goApp.GetConnectionState().then(function (state) {
    if (state.connected) {
      badge.textContent = 'Connected (PID ' + state.pid + ')';
      badge.className = 'status-connection-state connected';
    } else {
      badge.textContent = 'Disconnected';
      badge.className = 'status-connection-state disconnected';
    }

    var html = '<table class="status-service-table"><thead><tr>'
      + '<th style="width:30px"></th>'
      + '<th style="width:22%">Service</th>'
      + '<th style="width:15%">Remote</th>'
      + '<th style="width:22%">Local</th>'
      + '<th style="width:18%">Status</th>'
      + '</tr></thead><tbody>';

    Object.keys(groups).forEach(function (gk) {
      var g = groups[gk];
      html += '<tr class="status-machine-row"><td colspan="5">'
        + '<strong>' + escHtml(g.machine) + '</strong>'
        + '<span class="status-hub-label">on ' + escHtml(g.hubName) + '</span>'
        + '</td></tr>';

      g.services.forEach(function (svc) {
        var indicatorClass = 'unavailable';
        var statusText = 'Not connected';
        var localClass = 'inactive';

        if (state.connected) {
          var portStr = 'localhost:' + svc.localPort;
          var tunnelKey = g.machine + ':' + svc.localPort;
          var tunnelCount = activeTunnels[tunnelKey] || 0;

          if (state.output && state.output.indexOf(portStr) !== -1) {
            if (tunnelCount > 0) {
              indicatorClass = 'available';
              statusText = 'Active (' + tunnelCount + ')';
              localClass = 'active';
            } else {
              indicatorClass = 'available';
              statusText = 'Listening';
              localClass = 'active';
            }
          } else {
            statusText = 'Connecting...';
          }
        }

        html += '<tr>'
          + '<td><span class="status-svc-indicator ' + indicatorClass + '"></span></td>'
          + '<td>' + escHtml(svc.service) + '</td>'
          + '<td><span class="status-remote-port">' + (svc.servicePort ? ':' + svc.servicePort : '') + '</span></td>'
          + '<td><span class="status-local-port ' + localClass + '">localhost:' + svc.localPort + '</span></td>'
          + '<td>' + statusText + '</td>'
          + '</tr>';
      });
    });

    html += '</tbody></table>';
    container.innerHTML = html;
  });
}

// --- WebSocket Events ---
if (window.runtime) {
  window.runtime.EventsOn('tela:event', function (eventJSON) {
    try {
      var evt = JSON.parse(eventJSON);
      if (evt.type === 'service_bound' || evt.type === 'connection_state' || evt.type === 'tunnel_activity') {
        if (evt.type === 'service_bound' && evt.machine && evt.name && evt.remote) {
          // Update remote port from tela's actual bound service
          Object.keys(selectedServices).forEach(function (key) {
            var sel = selectedServices[key];
            if (sel.machine === evt.machine && sel.service === evt.name) {
              sel.servicePort = evt.remote;
            }
          });
        }
        if (evt.type === 'tunnel_activity') {
          // Track active tunnels
          var tkey = evt.machine + ':' + evt.localPort;
          if (evt.active) {
            activeTunnels[tkey] = (activeTunnels[tkey] || 0) + 1;
          } else {
            activeTunnels[tkey] = Math.max(0, (activeTunnels[tkey] || 0) - 1);
          }
        }
        refreshStatus();
      }
    } catch (e) {}
  });

  window.runtime.EventsOn('app:quitting', function () {
    var quitBtn = document.getElementById('quit-btn');
    var connectBtn = document.getElementById('connect-btn');
    if (quitBtn) { quitBtn.textContent = 'Quitting...'; quitBtn.className = 'topbar-btn disconnecting-btn'; quitBtn.disabled = true; }
    if (connectBtn) { connectBtn.textContent = 'Disconnecting...'; connectBtn.className = 'topbar-btn disconnecting-btn'; connectBtn.disabled = true; }
  });
}

// --- Sidebar Resize ---
(function () {
  setTimeout(function () {
    var handle = document.getElementById('sidebar-resize');
    var sidebar = document.getElementById('sidebar');
    if (!handle || !sidebar) return;

    // Restore saved width
    goApp.GetSettings().then(function (s) {
      if (s.sidebarWidth > 0) {
        sidebar.style.width = s.sidebarWidth + 'px';
      }
    });

    var dragging = false;

    handle.addEventListener('mousedown', function (e) {
      dragging = true;
      e.preventDefault();
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    });

    document.addEventListener('mousemove', function (e) {
      if (!dragging) return;
      var newWidth = e.clientX;
      if (newWidth < 338) newWidth = 338;
      if (newWidth > 600) newWidth = 600;
      sidebar.style.width = newWidth + 'px';
    });

    document.addEventListener('mouseup', function () {
      if (dragging) {
        dragging = false;
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
        // Save width to settings
        var width = parseInt(sidebar.style.width);
        if (width) goApp.SaveSidebarWidth(width);
      }
    });
  }, 200);
})();

// --- Startup ---
refreshVersionDisplay();
refreshProfileList();
loadSavedSelections().then(function () {
  refreshStatus();
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
  goApp.GetUpdateInfo().then(function (info) {
    var btn = document.getElementById('update-btn');
    if (!info.pending || (!info.guiBehind && !info.cliBehind)) return;
    if (info.packageManaged) {
      if (info.cliBehind) {
        btn.textContent = 'Update CLI (' + info.version + ')';
        btn.classList.remove('hidden');
      }
    } else {
      btn.textContent = 'Restart to Update (' + info.version + ')';
      btn.classList.remove('hidden');
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
        var key = makeServiceKey(hubURL, machineId, svc.name);
        selectedServices[key] = {
          hub: hubURL,
          machine: machineId,
          service: svc.name,
          servicePort: svc.remote || 0,
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
  }).then(function () {
    takeSnapshot();
  }).catch(function (err) {
    if (err) console.warn('Failed to load profile:', err);
  });
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

function refreshProfilePath() {
  goApp.GetProfilePath().then(function (path) {
    var el = document.getElementById('profile-path');
    if (el) el.textContent = path;
  });
}

function copyProfilePath() {
  goApp.GetProfilePath().then(function (path) {
    if (navigator.clipboard) navigator.clipboard.writeText(path);
  });
}

function showProfileOverview() {
  selectedHubURL = null;
  selectedMachineId = null;
  clearSelection();
  showProfileYaml();
}

function showProfileYaml() {
  var pane = document.getElementById('detail-pane');
  if (selectedHubURL || selectedMachineId) return;

  var keys = Object.keys(selectedServices);
  if (keys.length === 0) {
    pane.innerHTML = '<div class="empty-state"><p>Select hubs, machines, and services from the sidebar to build your connection profile.</p></div>';
    return;
  }

  var groups = {};
  keys.forEach(function (key) {
    var sel = selectedServices[key];
    var gk = sel.hub + '||' + sel.machine;
    if (!groups[gk]) groups[gk] = { hub: sel.hub, machine: sel.machine, services: [] };
    groups[gk].services.push({ name: sel.service, local: sel.localPort });
  });

  var yaml = 'connections:\n';
  Object.keys(groups).forEach(function (k) {
    var g = groups[k];
    yaml += '  - hub: ' + toWSURL(g.hub) + '\n';
    yaml += '    machine: ' + g.machine + '\n';
    yaml += '    services:\n';
    g.services.forEach(function (s) {
      yaml += '      - name: ' + s.name + '\n';
      yaml += '        local: ' + s.local + '\n';
    });
  });

  goApp.GetProfilePath().then(function (path) {
    pane.innerHTML = '<div class="profile-yaml-preview">'
      + '<div class="profile-yaml-header">'
      + '<h3>Profile Preview</h3>'
      + '<span class="profile-path" title="Click to copy" onclick="copyProfilePath()">' + escHtml(path) + '</span>'
      + '</div>'
      + '<pre class="connect-output">' + escHtml(yaml) + '</pre>'
      + '</div>';
  });
}

function switchProfile(name) {
  goApp.SwitchProfile(name).then(function () {
    selectedServices = {};
    hubStatusCache = {};
    selectedHubURL = null;
    selectedMachineId = null;
    document.getElementById('detail-pane').innerHTML = '<div class="empty-state"><p>Profile switched to ' + escHtml(name) + '. Select a hub or machine.</p></div>';
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
    showError(err);
  });
}

// --- Sidebar ---

// Track which hubs are included in the current profile
var includedHubs = {};

function isHubIncluded(hubURL) {
  // A hub is included if it has any selected services OR is explicitly included
  if (includedHubs[hubURL] !== undefined) return includedHubs[hubURL];
  // Default: included if it has any selected services
  var hasServices = Object.keys(selectedServices).some(function (key) {
    return key.indexOf(hubURL + '||') === 0;
  });
  return hasServices;
}

function toggleHubInclusion(hubURL, included) {
  includedHubs[hubURL] = included;
  if (!included) {
    // Remove all service selections for this hub
    Object.keys(selectedServices).forEach(function (key) {
      if (key.indexOf(hubURL + '||') === 0) delete selectedServices[key];
    });
    // Clear detail pane if showing a machine from this hub
    if (selectedHubURL === hubURL) {
      selectedHubURL = null;
      selectedMachineId = null;
      document.getElementById('detail-pane').innerHTML = '';
    }
    updateConnectButton();
  }
  checkDirty();
  refreshAll();
}

function refreshAll() {
  var content = document.getElementById('sidebar-content');
  content.innerHTML = '<p class="loading">Loading hubs...</p>';

  // Fetch connection state first (flat, not nested) then hubs
  var connectedAtRefresh = false;
  goApp.GetConnectionState().then(function (connState) {
    connectedAtRefresh = connState.connected;
  }).then(function () {
    return goApp.GetKnownHubs();
  }).then(function (hubs) {
    if (!hubs || hubs.length === 0) {
      content.innerHTML = '<div class="sidebar-empty">'
        + '<p>No hubs configured.</p>'
        + '<p class="hint">Go to the <strong>Hubs</strong> tab to add one.</p></div>';
      return;
    }

    content.innerHTML = '';
    hubs.forEach(function (hub) {
      renderHubInSidebar(content, hub, connectedAtRefresh);
    });

    updateConnectButton();
  });
}

function reconcileServicePorts(hubURL, machines) {
  var changed = false;
  machines.forEach(function (m) {
    var mId = m.id || m.hostname;
    (m.services || []).forEach(function (svc) {
      var key = makeServiceKey(hubURL, mId, svc.name);
      if (selectedServices[key] && svc.port) {
        selectedServices[key].servicePort = svc.port;
        changed = true;
      }
    });
  });
  if (changed) refreshStatus();
}

function renderHubInSidebar(content, hub, isConnected) {
  var hubContainer = document.createElement('div');
  var included = isHubIncluded(hub.url);
  hubContainer.className = 'profile-hub-group' + (included ? '' : ' profile-hub-excluded');

  var hubHeader = document.createElement('div');
  hubHeader.className = 'profile-hub-header';
  if (selectedHubURL === hub.url && !selectedMachineId) hubHeader.classList.add('selected');
  var hubDisabled = isConnected ? ' disabled' : '';
  hubHeader.innerHTML = '<input type="checkbox"' + (included ? ' checked' : '') + hubDisabled
    + ' onclick="event.stopPropagation(); toggleHubInclusion(\'' + escAttr(hub.url) + '\', this.checked)">'
    + '<span class="hub-dot"></span>'
    + '<span class="hub-name">' + escHtml(hub.name) + '</span>'
    + (!hub.hasToken ? '<span class="no-token-badge">no token</span>' : '');
  hubHeader.onclick = function (e) {
    if (e.target.tagName === 'INPUT') return;
    selectHub(hub, hubHeader);
  };
  hubContainer.appendChild(hubHeader);
  content.appendChild(hubContainer);

  if (hub.hasToken) {
    goApp.GetHubStatus(hub.url).then(function (status) {
      hubStatusCache[hub.url] = status;
      hubHeader.querySelector('.hub-dot').className = 'hub-dot ' + (status.online ? 'online' : 'offline');

      if (status.machines) {
        reconcileServicePorts(hub.url, status.machines);
      }

      if (status.machines && included) {
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
      hubHeader.querySelector('.hub-dot').className = 'hub-dot offline';
    });
  }
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
        var key = makeServiceKey(hub.url, machineId, svc.name);
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

    pane.innerHTML = html;
  });
}

// --- Service Selection ---

function toggleService(hubURL, machineId, serviceName, servicePort, checked) {
  var key = makeServiceKey(hubURL, machineId, serviceName);
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
    checkDirty();
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
    checkDirty();
    refreshCurrentPane();
  });
}

// Build a stable fingerprint of user intent (which services are selected,
// which hubs are included). Excludes localPort since port assignments are
// unstable across resolve calls and are not user-controlled state.
function selectionFingerprint() {
  var keys = Object.keys(selectedServices).sort();
  var svcPorts = {};
  keys.forEach(function (k) { svcPorts[k] = selectedServices[k].servicePort; });
  var hubs = {};
  Object.keys(includedHubs).forEach(function (k) { hubs[k] = !!includedHubs[k]; });
  return JSON.stringify({ keys: keys, ports: svcPorts, hubs: hubs });
}

function checkDirty() {
  profileDirty = selectionFingerprint() !== savedFingerprint;
  updateSaveButton();
}

function takeSnapshot() {
  savedFingerprint = selectionFingerprint();
  savedServicesJSON = JSON.stringify(selectedServices);
  savedIncludedHubsJSON = JSON.stringify(includedHubs);
  profileDirty = false;
  updateSaveButton();
}

function updateSaveButton() {
  var saveBtn = document.getElementById('save-btn');
  var undoBtn = document.getElementById('undo-btn');
  if (saveBtn) saveBtn.disabled = !profileDirty;
  if (undoBtn) undoBtn.disabled = !profileDirty;
}

function saveSelections() {
  var connections = buildConnections();
  goApp.SaveProfile(JSON.stringify(connections)).then(function () {
    takeSnapshot();
  });
}

function undoSelections() {
  selectedServices = JSON.parse(savedServicesJSON);
  includedHubs = JSON.parse(savedIncludedHubsJSON);
  profileDirty = false;
  updateSaveButton();
  updateConnectButton();
  refreshAll();
  refreshStatus();
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
  var quitBtn = document.getElementById('quit-btn');
  goApp.GetConnectionState().then(function (state) {
    if (state.connected) {
      btn.textContent = 'Disconnect';
      btn.className = 'topbar-btn disconnect-btn';
      if (quitBtn) { quitBtn.className = 'topbar-btn disconnect-btn'; }
    } else {
      var hasSelections = Object.keys(selectedServices).length > 0;
      btn.textContent = 'Connect';
      btn.className = 'topbar-btn connect-btn' + (hasSelections ? '' : ' disabled');
      btn.title = hasSelections ? '' : 'Select services in Profiles first';
      if (quitBtn) { quitBtn.className = 'topbar-btn connect-btn'; quitBtn.textContent = 'Quit'; }
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
  var connections = buildConnections();
  if (connections.length === 0) return;

  goApp.Connect(JSON.stringify(connections)).then(function () {
    // Connect auto-saves the profile; update snapshot
    takeSnapshot();
    startConnectionPoll();
    updateConnectButton();
    refreshAll();
    refreshStatus();
    // Connect WebSocket for real-time events
    setTimeout(function () { goApp.ConnectControlWS(); }, 2000);
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
    showError('Connection failed: ' + err);
  });
}

function doQuit() {
  // Don't change button state here -- OnBeforeClose shows a
  // confirmation dialog when connected. If cancelled, buttons
  // must stay unchanged. The app exits immediately on confirm.
  goApp.QuitApp();
}

function doDisconnect() {
  // Confirm before disconnecting
  goApp.IsConnected().then(function (connected) {
    if (connected && !confirm('Are you sure you want to disconnect?')) return;
    performDisconnect();
  });
}

function performDisconnect() {
  var btn = document.getElementById('connect-btn');
  btn.textContent = 'Disconnecting...';
  btn.className = 'topbar-btn disconnecting-btn';
  btn.disabled = true;

  goApp.Disconnect().then(function () {
    goApp.DisconnectControlWS();
    stopConnectionPoll();
    btn.disabled = false;
    updateConnectButton();
    refreshAll();
    refreshTerminal();
    refreshStatus();
  }).catch(function (err) {
    goApp.DisconnectControlWS();
    stopConnectionPoll();
    btn.disabled = false;
    updateConnectButton();
    refreshAll();
    refreshStatus();
  });
}

var pollInFlight = false;

function startConnectionPoll() {
  stopConnectionPoll();
  pollInFlight = false;
  pollIntervalId = setInterval(function () {
    if (pollInFlight) return;
    pollInFlight = true;
    goApp.GetConnectionState().then(function (state) {
      pollInFlight = false;
      // Refresh terminal if visible
      if (!document.getElementById('tab-terminal').classList.contains('hidden')) {
        refreshTerminal();
      }
      if (!state.connected) {
        stopConnectionPoll();
        updateConnectButton();
        refreshAll();
        // Auto-reconnect if enabled
        goApp.GetSettings().then(function (s) {
          if (s.reconnectOnDrop && Object.keys(selectedServices).length > 0) {
            setTimeout(function () {
              doConnect();
            }, 3000);
          }
        });
      }
    }).catch(function () {
      pollInFlight = false;
    });
  }, 1000);
}

function stopConnectionPoll() {
  if (pollIntervalId) {
    clearInterval(pollIntervalId);
    pollIntervalId = null;
  }
}

// --- Hubs Tab ---

function refreshHubsTab() {
  var list = document.getElementById('hubs-list');
  if (!list) return;
  list.innerHTML = '<p class="loading">Loading hubs...</p>';

  goApp.GetKnownHubs().then(function (hubs) {
    if (!hubs || hubs.length === 0) {
      list.innerHTML = '<div class="sidebar-empty">'
        + '<p>No hubs configured.</p>'
        + '<p class="hint">Click <strong>Add Hub</strong> to get started.</p></div>';
      return;
    }

    list.innerHTML = '';
    hubs.forEach(function (hub) {
      var card = document.createElement('div');
      card.className = 'hub-card';

      var dotClass = 'unknown';
      var status = hubStatusCache[hub.url];
      if (status) {
        dotClass = status.online ? 'online' : 'offline';
      }

      var tokenHtml;
      if (hub.hasToken) {
        tokenHtml = '<span class="token-status-yes">token stored</span>';
      } else {
        tokenHtml = '<span class="token-status-no">no token</span>';
      }

      card.innerHTML = '<span class="hub-card-dot ' + dotClass + '"></span>'
        + '<div class="hub-card-info">'
        + '<div class="hub-card-name">' + escHtml(hub.name) + '</div>'
        + '<div class="hub-card-url">' + escHtml(hub.url) + '</div>'
        + '<div class="hub-card-token">' + tokenHtml + '</div>'
        + '</div>'
        + '<div class="hub-card-actions">'
        + '<button class="btn-danger btn-sm" onclick="removeHub(\'' + escAttr(hub.url) + '\')">Remove</button>'
        + '</div>';

      list.appendChild(card);

      // Fetch status if not cached
      if (!status && hub.hasToken) {
        goApp.GetHubStatus(hub.url).then(function (s) {
          hubStatusCache[hub.url] = s;
          var dot = card.querySelector('.hub-card-dot');
          if (dot) dot.className = 'hub-card-dot ' + (s.online ? 'online' : 'offline');
        }).catch(function () {
          var dot = card.querySelector('.hub-card-dot');
          if (dot) dot.className = 'hub-card-dot offline';
        });
      }
    });
  });
}

// --- Hub Management ---

function removeHub(url) {
  if (!confirm('Remove this hub and all its saved selections?')) return;
  // Remove selections for this hub
  Object.keys(selectedServices).forEach(function (key) {
    if (key.indexOf(url + '||') === 0) delete selectedServices[key];
  });
  delete includedHubs[url];
  goApp.RemoveHub(url).then(function () {
    selectedHubURL = null;
    selectedMachineId = null;
    checkDirty();
    refreshAll();
    refreshHubsTab();
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
    refreshHubsTab();
  }).catch(function (err) {
    errEl.textContent = String(err).replace(/^Error:\s*/i, '');
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
    errEl.textContent = String(err).replace(/^Error:\s*/i, '');
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
    refreshHubsTab();
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
    minimizeTo: 'tray',
    startMinimized: false,
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
    refreshHubsTab();
  }).catch(function (err) {
    showError('Failed to clear credential store: ' + err);
  });
}

function importProfile() {
  goApp.ImportProfile().then(function () {
    loadSavedSelections().then(function () {
      refreshAll();
    });
  }).catch(function (err) {
    if (err) showError('Import failed: ' + err);
  });
}

function exportProfile() {
  goApp.ExportProfile().catch(function (err) {
    if (err) showError('Export failed: ' + err);
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

function makeServiceKey(hub, machine, service) {
  return hub + '||' + machine + '||' + service;
}

function buildConnections() {
  var groups = {};
  Object.keys(selectedServices).forEach(function (key) {
    var sel = selectedServices[key];
    var groupKey = sel.hub + '||' + sel.machine;
    if (!groups[groupKey]) {
      groups[groupKey] = { hub: sel.hub, machine: sel.machine, services: [] };
    }
    groups[groupKey].services.push({ name: sel.service, local: sel.localPort, remote: sel.servicePort });
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
  return connections;
}

function escHtml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function escAttr(s) {
  return String(s).replace(/&/g, '&amp;').replace(/'/g, '&#39;').replace(/"/g, '&quot;');
}

function hubNameFromURL(url) {
  return url.replace(/^wss?:\/\//, '').replace(/^https?:\/\//, '').replace(/\/$/, '');
}

function showError(msg) {
  // Strip Go error prefixes and stack traces for user-facing messages
  var text = String(msg).replace(/^Error:\s*/i, '');
  var el = document.getElementById('error-toast');
  if (el) {
    el.textContent = text;
    el.classList.remove('hidden');
    setTimeout(function () { el.classList.add('hidden'); }, 5000);
  } else {
    alert(text);
  }
}

function toWSURL(url) {
  if (url.indexOf('https://') === 0) return 'wss://' + url.substring(8);
  if (url.indexOf('http://') === 0) return 'ws://' + url.substring(7);
  if (url.indexOf('wss://') !== 0 && url.indexOf('ws://') !== 0) return 'wss://' + url;
  return url;
}
