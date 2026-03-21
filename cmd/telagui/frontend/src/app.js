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

// --- Modes & Tabs ---

var currentMode = 'clients';
var tabBars = { clients: 'tabbar-clients', agents: 'tabbar-agents', hubs: 'tabbar-hubs' };

function switchMode(mode) {
  currentMode = mode;
  document.querySelectorAll('.mode-btn').forEach(function (b) { b.classList.remove('active'); });
  var btn = document.getElementById('mode-' + mode + '-btn');
  if (btn) btn.classList.add('active');

  Object.keys(tabBars).forEach(function (m) {
    document.getElementById(tabBars[m]).classList.toggle('hidden', m !== mode);
  });

  document.querySelectorAll('.tab-pane').forEach(function (el) { el.classList.add('hidden'); });

  var bar = document.getElementById(tabBars[mode]);
  var activeBtn = bar.querySelector('.main-tab.active');
  if (activeBtn) activeBtn.click();
}

function switchTab(name, btn) {
  var tabbar = btn ? btn.parentElement : null;
  if (tabbar) {
    tabbar.querySelectorAll('.main-tab').forEach(function (el) {
      el.classList.remove('active');
    });
  }

  document.querySelectorAll('.tab-pane').forEach(function (el) { el.classList.add('hidden'); });

  var pane = document.getElementById('tab-' + name);
  if (pane) pane.classList.remove('hidden');
  if (btn) btn.classList.add('active');

  // Show/hide profile toolbar
  var profToolbar = document.getElementById('profile-toolbar');
  if (profToolbar) profToolbar.classList.toggle('hidden', name !== 'profile');

  if (name === 'status') refreshStatus();
  if (name === 'profile') showProfileOverview();
  if (name === 'files') refreshFilesTab();
  if (name === 'agents') agentsRefresh();
  if (name === 'hubs') refreshHubsTab();
}

// --- Log Panel ---

function switchLogTab(btn, id) {
  document.querySelectorAll('.log-panel-tab').forEach(function (t) { t.classList.remove('active'); });
  btn.classList.add('active');
  document.querySelectorAll('.log-panel-output').forEach(function (o) { o.classList.add('hidden'); });
  var el = document.getElementById(id);
  if (el) el.classList.remove('hidden');
}

function toggleLogPanel() {
  var panel = document.getElementById('log-panel');
  panel.classList.toggle('collapsed');
  var toggle = panel.querySelector('.log-panel-toggle');
  toggle.innerHTML = panel.classList.contains('collapsed') ? '&#x25B2;' : '&#x25BC;';
}

function tvLog(msg) {
  var el = document.getElementById('log-tv');
  if (!el) return;
  var now = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');
  var line = document.createTextNode(now + ' ' + msg + '\n');
  el.appendChild(line);
  if (!logPanelFrozen) el.scrollTop = el.scrollHeight;
}

function appendTelaLog(text) {
  var el = document.getElementById('log-tela');
  if (!el) return;
  el.textContent += text;
  el.scrollTop = el.scrollHeight;
}

function copyLogPanel() {
  var active = document.querySelector('.log-panel-output:not(.hidden)');
  if (!active || !navigator.clipboard) return;
  // For the command log, copy only the entries list, not the filter bar
  var entries = active.querySelector('.cmd-list');
  navigator.clipboard.writeText((entries || active).textContent);
}

function saveLogPanel() {
  var active = document.querySelector('.log-panel-output:not(.hidden)');
  if (active) {
    goApp.SaveTextToFile(active.textContent, 'log');
  }
}

function clearLogPanel() {
  var active = document.querySelector('.log-panel-output:not(.hidden)');
  if (active) {
    if (active.id === 'log-commands') {
      var list = document.getElementById('cmd-list');
      if (list) list.innerHTML = '';
    } else {
      active.textContent = '';
    }
  }
}

function openAttachDialog() {
  // Placeholder for attach log source dialog
}

// --- Command Log in Panel ---

var activeFilter = 'all';

function addCommandEntry(method, desc, fullCmd) {
  var list = document.getElementById('cmd-list');
  if (!list) return;
  var now = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z').substring(11, 19);
  var methodClass = 'cmd-m-' + method.toLowerCase().replace('delete', 'del');
  var methodLabel = method === 'DELETE' ? 'DEL' : method;

  // One-line version for display: strip backslash-newline continuations
  var oneLine = fullCmd.replace(/ \\\n\s*/g, ' ');

  var line = document.createElement('span');
  line.className = 'cmd-line';
  line.setAttribute('data-cmd', fullCmd);
  line.setAttribute('data-method', method.toLowerCase().replace('delete', 'del'));
  line.onclick = function () { expandCmd(this); };
  line.innerHTML = '<span class="cmd-cp" onclick="event.stopPropagation();cpCmd(this)">&#x2398;</span>'
    + '<span class="cmd-ts">' + now + '</span> '
    + '<span class="' + methodClass + '">' + methodLabel + '</span> '
    + escHtml(oneLine);
  list.appendChild(line);
  list.scrollTop = list.scrollHeight;
  applyFilters();
}

function expandCmd(el) {
  var prev = document.querySelector('.cmd-expanded');
  if (prev) prev.remove();
  if (el.classList.contains('cmd-active')) {
    el.classList.remove('cmd-active');
    return;
  }
  document.querySelectorAll('.cmd-active').forEach(function (e) { e.classList.remove('cmd-active'); });
  el.classList.add('cmd-active');
  var cmd = el.getAttribute('data-cmd');
  var div = document.createElement('span');
  div.className = 'cmd-expanded';
  div.textContent = cmd;
  el.after(div);
}

function cpCmd(span) {
  var line = span.parentElement;
  var cmd = line.getAttribute('data-cmd');
  if (cmd && navigator.clipboard) {
    navigator.clipboard.writeText(cmd);
    span.textContent = '\u2713';
    setTimeout(function () { span.textContent = '\u2398'; }, 1000);
  }
}

function toggleChip(btn, method) {
  document.querySelectorAll('.cmd-filter-chip').forEach(function (c) { c.classList.remove('active'); });
  btn.classList.add('active');
  activeFilter = method;
  applyFilters();
}

function filterCommands(text) {
  applyFilters(text);
}

function applyFilters(text) {
  var input = document.querySelector('.cmd-filter-input');
  var search = (text !== undefined ? text : (input ? input.value : '')).toLowerCase();
  document.querySelectorAll('.cmd-line').forEach(function (entry) {
    var method = entry.getAttribute('data-method') || '';
    var content = entry.textContent.toLowerCase();
    var methodMatch = activeFilter === 'all' || method === activeFilter;
    var textMatch = !search || content.indexOf(search) !== -1;
    entry.style.display = (methodMatch && textMatch) ? '' : 'none';
  });
}

// --- Overlay Panels ---

function toggleAboutOverlay() {
  var el = document.getElementById('about-overlay');
  el.classList.toggle('hidden');
  if (!el.classList.contains('hidden')) refreshAbout();
}

function toggleSettingsOverlay() {
  var el = document.getElementById('settings-overlay');
  el.classList.toggle('hidden');
  if (!el.classList.contains('hidden')) {
    refreshSettings();
    refreshBinStatus();
  }
}

// --- Status Tab ---

function refreshStatus() {
  var profileNameEl = document.getElementById('status-profile-name');
  var badge = document.getElementById('status-conn-state');
  var container = document.getElementById('status-services');
  if (!profileNameEl || !badge || !container) return;

  profileNameEl.textContent = document.getElementById('profile-select')
    ? document.getElementById('profile-select').value || 'telavisor'
    : 'telavisor';

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

    var html = '';

    Object.keys(groups).forEach(function (gk) {
      var g = groups[gk];
      html += '<div class="settings-group">'
        + '<div class="settings-group-header">'
        + escHtml(g.machine.toUpperCase())
        + '<span class="status-hub-label">on ' + escHtml(g.hubName) + '</span>'
        + '</div>';

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

        html += '<div class="settings-row status-svc-row">'
          + '<span class="status-svc-indicator ' + indicatorClass + '"></span>'
          + '<div class="status-svc-name">' + escHtml(svc.service) + '</div>'
          + '<div class="status-svc-remote">' + (svc.servicePort ? ':' + svc.servicePort : '') + '</div>'
          + '<div class="status-svc-local ' + localClass + '">localhost:' + svc.localPort + '</div>'
          + '<div class="status-svc-status">' + statusText + '</div>'
          + '</div>';
      });

      html += '</div>';
    });

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
      // File change events from telad
      if (evt.type === 'file_created' || evt.type === 'file_modified' || evt.type === 'file_deleted' || evt.type === 'file_renamed') {
        if (filesView === 'files' && evt.machine === filesCurrentMachine) {
          var evtDir = (evt.path || '').split('/').slice(0, -1).join('/');
          if (evtDir === filesCurrentPath) {
            filesDebouncedRefresh(); // debounce to avoid flooding on batch changes
          }
        }
      }
    } catch (e) {}
  });

  window.runtime.EventsOn('app:quitting', function () {
    var quitBtn = document.getElementById('quit-btn');
    var connectBtn = document.getElementById('connect-btn');
    if (quitBtn) { quitBtn.classList.add('quitting'); quitBtn.disabled = true; }
    if (connectBtn) { connectBtn.disabled = true; }
    updateConnIcon('disconnecting');
  });

  window.runtime.EventsOn('app:confirm-quit', function () {
    showDisconnectOverlay(function () {
      goApp.QuitApp();
    });
  });

  window.runtime.EventsOn('app:quit-stuck', function () {
    document.getElementById('quit-stuck-overlay').style.display = 'flex';
  });

  window.runtime.EventsOn('tela:tvlog', function (msg) {
    if (msg) tvLog(msg);
  });

  window.runtime.EventsOn('tela:output', function (chunk) {
    if (!chunk) return;
    var el = document.getElementById('log-tela');
    if (!el) return;
    // Clear the "Not connected." placeholder on first output
    if (el.textContent === 'Not connected.') el.textContent = '';
    el.textContent += chunk;
    if (!logPanelFrozen) el.scrollTop = el.scrollHeight;
    // Update tela dot to live
    var dot = document.getElementById('log-tela-dot');
    if (dot) dot.className = 'log-dot log-dot-live';
  });

  window.runtime.EventsOn('app:command', function (entry) {
    if (!entry) return;
    var method = 'CLI';
    var cmd = entry.command || '';
    if (cmd.indexOf('GET ') === 0) method = 'GET';
    else if (cmd.indexOf('POST ') === 0) method = 'POST';
    else if (cmd.indexOf('DELETE ') === 0) method = 'DEL';
    addCommandEntry(method, entry.description || cmd, cmd);
  });
}

// --- Sidebar Resize ---
function initSidebarResize(handleId, sidebarId, minWidth, maxWidth, saveKey) {
  setTimeout(function () {
    var handle = document.getElementById(handleId);
    var sidebar = document.getElementById(sidebarId);
    if (!handle || !sidebar) return;

    // Restore saved width
    if (saveKey) {
      goApp.GetSettings().then(function (s) {
        if (saveKey === 'sidebarWidth' && s.sidebarWidth > 0) {
          sidebar.style.width = s.sidebarWidth + 'px';
        } else if (saveKey === 'hubsSidebarWidth' && s.hubsSidebarWidth > 0) {
          sidebar.style.width = s.hubsSidebarWidth + 'px';
        }
      });
    }

    var dragging = false;

    handle.addEventListener('mousedown', function (e) {
      dragging = true;
      e.preventDefault();
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    });

    document.addEventListener('mousemove', function (e) {
      if (!dragging) return;
      var rect = sidebar.parentElement.getBoundingClientRect();
      var newWidth = e.clientX - rect.left;
      if (newWidth < minWidth) newWidth = minWidth;
      if (newWidth > maxWidth) newWidth = maxWidth;
      sidebar.style.width = newWidth + 'px';
    });

    document.addEventListener('mouseup', function () {
      if (dragging) {
        dragging = false;
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
        var width = parseInt(sidebar.style.width);
        if (width && saveKey === 'sidebarWidth') goApp.SaveSidebarWidth(width);
        if (width && saveKey === 'hubsSidebarWidth') goApp.SaveHubsSidebarWidth(width);
      }
    });
  }, 200);
}
initSidebarResize('sidebar-resize', 'sidebar', 220, 600, 'sidebarWidth');
initSidebarResize('hubs-sidebar-resize', 'hubs-sidebar', 220, 400, 'hubsSidebarWidth');

// --- Log Panel Resize ---
(function () {
  var handle = document.getElementById('log-panel-resize');
  var panel = document.getElementById('log-panel');
  if (!handle || !panel) return;
  var dragging = false;
  handle.addEventListener('mousedown', function (e) {
    dragging = true;
    e.preventDefault();
    document.body.style.cursor = 'row-resize';
    document.body.style.userSelect = 'none';
  });
  document.addEventListener('mousemove', function (e) {
    if (!dragging) return;
    var newHeight = window.innerHeight - e.clientY;
    if (newHeight < 80) newHeight = 80;
    if (newHeight > window.innerHeight * 0.6) newHeight = window.innerHeight * 0.6;
    panel.style.height = newHeight + 'px';
    panel.classList.remove('collapsed');
  });
  document.addEventListener('mouseup', function () {
    if (dragging) {
      dragging = false;
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    }
  });
})();

// --- Startup ---
tvLog('TelaVisor started');
refreshVersionDisplay();
refreshProfileList();
refreshLog();
loadSavedSelections().then(function () {
  tvLog('Profile loaded');
  refreshStatus();
  refreshAll();
  // Re-take snapshot after refreshAll to account for hub status reconciliation
  setTimeout(function () { takeSnapshot(); }, 1000);
  // Auto-connect if enabled and there are saved selections
  goApp.ShouldAutoConnect().then(function (should) {
    if (should && Object.keys(selectedServices).length > 0) {
      setTimeout(function () {
        doConnect();
      }, 1500); // delay to let hub status load
    }
  });
});

// First-run connect tooltip
(function () {
  goApp.GetSettings().then(function (s) {
    if (s.connectTooltipDismissed) {
      dismissConnectTooltip();
    }
    // else: tooltip is visible by default in the HTML
  }).catch(function () {});
})();

function dismissConnectTooltip() {
  var tip = document.getElementById('connect-tooltip');
  if (tip) tip.classList.add('hidden');
}

// Check for updates after a short delay (versions need time to fetch)
setTimeout(function () {
  refreshVersionDisplay();
  checkForUpdate();
}, 4000);

function refreshVersionDisplay() {
  goApp.GetToolVersions().then(function (tv) {
    // Update About overlay version
    var aboutEl = document.getElementById('about-version');
    if (aboutEl) {
      aboutEl.textContent = 'telavisor: ' + (tv.gui || 'dev') + '  |  tela: ' + (tv.cli || 'not installed');
    }
  });
}

var updateInfo = null;
var updateDismissedForSession = false;
var updateSkippedVersion = '';

function checkForUpdate() {
  goApp.GetUpdateInfo().then(function (info) {
    updateInfo = info;
    if (!info.pending || (!info.guiBehind && !info.cliBehind)) {
      document.getElementById('update-btn').disabled = true; document.getElementById('update-btn').title = 'No updates';
      return;
    }
    if (updateDismissedForSession) return;
    if (updateSkippedVersion && updateSkippedVersion === info.version) return;
    document.getElementById('update-btn').disabled = false; document.getElementById('update-btn').title = 'Update available';
  });
}

function refreshSingleBinary(name, btn) {
  btn.classList.add('spinning');
  btn.disabled = true;
  goApp.GetBinStatus().then(function (bins) {
    btn.classList.remove('spinning');
    btn.disabled = false;
    refreshBinStatus();
    checkForUpdate();
  }).catch(function () {
    btn.classList.remove('spinning');
    btn.disabled = false;
  });
}

function toggleUpdateOverlay() {
  var el = document.getElementById('update-overlay');
  el.classList.toggle('hidden');
  if (!el.classList.contains('hidden') && updateInfo) {
    document.getElementById('update-gui-current').textContent = updateInfo.gui || 'dev';
    document.getElementById('update-cli-current').textContent = updateInfo.cli || 'not installed';
    document.getElementById('update-gui-latest').textContent = updateInfo.version || '?';
    document.getElementById('update-cli-latest').textContent = updateInfo.version || '?';

    var notes = [];
    if (updateInfo.guiBehind) notes.push('TelaVisor is out of date.');
    if (updateInfo.cliBehind) notes.push('tela CLI is out of date.');
    if (updateInfo.packageManaged) notes.push('TelaVisor was installed via a package manager. Only the CLI will be updated.');
    document.getElementById('update-note').textContent = notes.join(' ');
  }
}

function applyUpdate() {
  var applyBtn = document.getElementById('update-apply-btn');
  applyBtn.textContent = 'Updating...';
  applyBtn.disabled = true;
  goApp.RestartToUpdate().then(function () {
    // If we're still here, it was a CLI-only update
    document.getElementById('update-btn').disabled = true; document.getElementById('update-btn').title = 'No updates';
    toggleUpdateOverlay();
    applyBtn.textContent = 'Update Now';
    applyBtn.disabled = false;
    refreshVersionDisplay();
  }).catch(function () {
    applyBtn.textContent = 'Update Failed';
    applyBtn.disabled = false;
    setTimeout(function () {
      applyBtn.textContent = 'Update Now';
    }, 3000);
  });
}

function ignoreUpdateSession() {
  updateDismissedForSession = true;
  document.getElementById('update-btn').disabled = true; document.getElementById('update-btn').title = 'No updates';
  toggleUpdateOverlay();
}

function ignoreUpdateForever() {
  if (updateInfo) updateSkippedVersion = updateInfo.version;
  document.getElementById('update-btn').disabled = true; document.getElementById('update-btn').title = 'No updates';
  toggleUpdateOverlay();
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
    pane.innerHTML = '<div class="settings-group yaml-card">'
      + '<div class="settings-group-header">'
      + '<div class="yaml-card-header">'
      + '<span>Profile Preview</span>'
      + '<span class="yaml-card-path" title="Click to copy" onclick="copyProfilePath()" style="cursor:pointer;">' + escHtml(path) + '</span>'
      + '</div></div>'
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
  showPromptDialog('New Profile', '', '', 'Create').then(function (name) {
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
  });
}

function renameCurrentProfile() {
  var sel = document.getElementById('profile-select');
  if (!sel || !sel.value) return;
  var oldName = sel.value;
  showPromptDialog('Rename Profile', 'Rename "' + oldName + '" to:', oldName, 'Rename').then(function (newName) {
    if (!newName || newName === oldName) return;
    goApp.RenameProfile(oldName, newName).then(function () {
      tvLog('Renamed profile "' + oldName + '" to "' + newName + '"');
      refreshProfileList();
      refreshAll();
    }).catch(function (err) {
      showError(err);
    });
  });
}

function deleteCurrentProfile() {
  var sel = document.getElementById('profile-select');
  if (!sel || !sel.value) return;
  var name = sel.value;
  goApp.ConfirmDialog('Delete Profile', 'Delete profile "' + name + '"? This cannot be undone.').then(function (yes) {
    if (!yes) return;
    goApp.DeleteProfile(name).then(function () {
      tvLog('Deleted profile "' + name + '"');
      refreshProfileList();
      refreshAll();
    }).catch(function (err) {
      showError(err);
    });
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
    var hubName = hub.name || hubNameFromURL(hub.url);

    var html = '<h2 style="font-size:18px;font-weight:700;margin-bottom:4px;">' + escHtml(machineId) + '</h2>'
      + '<p class="section-desc">' + (machine.agentConnected ? 'Online' : 'Offline')
      + ' on ' + escHtml(hubName) + '</p>';

    if (services.length === 0) {
      html += '<p class="empty-hint">No services advertised.</p>';
    } else {
      html += '<div class="settings-group"><div class="settings-group-header">Services</div>';
      services.forEach(function (svc) {
        var key = makeServiceKey(hub.url, machineId, svc.name);
        var checked = selectedServices[key] ? ' checked' : '';
        var disabled = isConnected ? ' disabled' : '';
        var localPort = selectedServices[key] ? selectedServices[key].localPort : '';

        html += '<div class="profile-svc-row">'
          + '<input type="checkbox"' + checked + disabled
          + ' onchange="toggleService(\'' + escAttr(hub.url) + '\', \'' + escAttr(machineId) + '\', \'' + escAttr(svc.name) + '\', ' + svc.port + ', this.checked)">'
          + '<div class="profile-svc-name">' + escHtml(svc.name) + '</div>'
          + '<div class="profile-svc-port">:' + svc.port + '</div>'
          + '<div class="profile-svc-proto">' + escHtml(svc.proto || 'tcp') + '</div>';

        if (localPort) {
          html += '<div class="profile-svc-local">localhost:' + localPort + '</div>';
        }
        html += '</div>';
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
  goApp.GetConnectionState().then(function (state) {
    if (state.connected) {
      updateConnIcon('connected');
    } else {
      updateConnIcon('disconnected');
    }
  });
}

function updateConnIcon(state) {
  var btn = document.getElementById('connect-btn');
  if (!btn) return;

  // Remove all state classes
  btn.classList.remove('connected', 'connecting');

  if (state === 'connected') {
    btn.classList.add('connected');
    btn.title = 'Disconnect';
  } else if (state === 'connecting') {
    btn.classList.add('connecting');
    btn.title = 'Connecting...';
  } else if (state === 'disconnecting') {
    btn.classList.add('connecting');
    btn.title = 'Disconnecting...';
  } else {
    btn.title = 'Connect';
  }

  // Dismiss the first-run tooltip on any state change
  dismissConnectTooltip();
}

function goToStatus() {
  switchMode('clients');
  var statusBtn = document.querySelector('#tabbar-clients .main-tab');
  if (statusBtn) switchTab('status', statusBtn);
}

function toggleConnection() {
  // Dismiss the first-run tooltip permanently
  dismissConnectTooltip();
  goApp.GetSettings().then(function (s) {
    if (!s.connectTooltipDismissed) {
      s.connectTooltipDismissed = true;
      goApp.SaveSettings(s);
    }
  }).catch(function () {});

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

  tvLog('Connecting...');
  updateConnIcon('connecting');
  goApp.Connect(JSON.stringify(connections)).then(function () {
    tvLog('Connected');
    // Connect auto-saves the profile; update snapshot
    takeSnapshot();
    startConnectionPoll();
    updateConnectButton();
    refreshAll();
    refreshStatus();
    refreshFilesTab();
    agentsRefresh();
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
  goApp.GetConnectionState().then(function (state) {
    if (state.connected) {
      goApp.GetSettings().then(function (s) {
        if (s.confirmDisconnect) {
          showDisconnectOverlay(function () {
            goApp.QuitApp();
          });
        } else {
          goApp.QuitApp();
        }
      });
    } else {
      goApp.QuitApp();
    }
  });
}

var disconnectCallback = null;

function doDisconnect() {
  goApp.GetSettings().then(function (s) {
    if (!s.confirmDisconnect) {
      performDisconnect();
      return;
    }
    showDisconnectOverlay(performDisconnect);
  });
}

function showDisconnectOverlay(callback) {
  disconnectCallback = callback;
  document.getElementById('disconnect-overlay').classList.remove('hidden');
}

function confirmDisconnect() {
  document.getElementById('disconnect-overlay').classList.add('hidden');
  if (disconnectCallback) {
    disconnectCallback();
    disconnectCallback = null;
  }
}

function cancelDisconnect() {
  document.getElementById('disconnect-overlay').classList.add('hidden');
  disconnectCallback = null;
}

function performDisconnect() {
  var btn = document.getElementById('connect-btn');
  btn.disabled = true;
  tvLog('Disconnecting...');
  updateConnIcon('disconnecting');

  goApp.Disconnect().then(function () {
    tvLog('Disconnected');
    goApp.DisconnectControlWS();
    stopConnectionPoll();
    btn.disabled = false;
    updateConnectButton();
    refreshAll();
    refreshTerminal();
    refreshStatus();
    filesShowMachineList();
    agentsRefresh();
  }).catch(function (err) {
    goApp.DisconnectControlWS();
    stopConnectionPoll();
    btn.disabled = false;
    updateConnectButton();
    refreshAll();
    filesShowMachineList();
    agentsRefresh();
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
      // Always refresh tela log (it's in the persistent panel)
      refreshTerminal();
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

// --- Agents Mode ---

var agentsData = [];
var agentsSelectedId = '';

function agentsRefresh() {
  goApp.GetConnectionState().then(function (state) {
    document.getElementById('agents-pair-btn').disabled = !state.connected;
    if (!state.connected) {
      document.getElementById('agents-sidebar-list').innerHTML = '<p class="empty-hint" style="padding:16px;">Connect to view agents.</p>';
      document.getElementById('agents-detail').innerHTML = '<div class="agents-detail-empty">Connect to view agents.</div>';
      agentsData = [];
      return;
    }
    goApp.GetAgentList().then(function (agents) {
      agentsData = agents || [];
      agentsRenderSidebar();
      if (agentsSelectedId) {
        var found = agentsData.find(function (a) { return a.id === agentsSelectedId; });
        if (found) agentsShowDetail(found);
        else document.getElementById('agents-detail').innerHTML = '<div class="agents-detail-empty">Select an agent to view details.</div>';
      }
    }).catch(function () {
      document.getElementById('agents-sidebar-list').innerHTML = '<p class="empty-hint" style="padding:16px;">Failed to load agents.</p>';
    });
  }).catch(function () {});
}

function agentsRenderSidebar() {
  var html = '';
  agentsData.forEach(function (a) {
    var active = a.id === agentsSelectedId ? ' active' : '';
    var dotClass = a.online ? 'online' : 'offline';
    var ver = a.version || 'unknown';
    html += '<div class="agents-sidebar-item' + active + '" onclick="agentsSelect(\'' + escAttr(a.id) + '\')">'
      + '<span class="machine-status-dot ' + dotClass + '"></span>'
      + '<div>'
      + '<div class="agents-sidebar-name">' + escHtml(a.id) + '</div>'
      + '<div class="agents-sidebar-version">' + escHtml(ver) + '</div>'
      + '</div>'
      + '</div>';
  });
  if (agentsData.length === 0) {
    html = '<p class="empty-hint" style="padding:16px;">No agents registered.</p>';
  }
  document.getElementById('agents-sidebar-list').innerHTML = html;
}

function agentsSelect(id) {
  agentsSelectedId = id;
  agentsRenderSidebar();
  var agent = agentsData.find(function (a) { return a.id === id; });
  if (agent) agentsShowDetail(agent);
}

function agentsShowDetail(a) {
  var isOnline = a.online;
  var statusClass = isOnline ? 'online' : 'offline';
  var statusText = isOnline ? 'Online' : 'Offline';
  var dis = isOnline ? '' : ' disabled';

  var html = '<div class="agent-detail-header">'
    + '<div class="agent-detail-title">' + escHtml(a.id) + '</div>'
    + '<span class="agent-detail-status ' + statusClass + '">' + statusText + '</span>'
    + '</div>';

  // Agent Info card
  html += agentCard('Agent Info',
    agentRow('Version', agentMono(a.version || 'unknown'))
    + agentRow('Hub', escHtml(a.hub))
    + agentRow('Hostname', agentMono(a.hostname || '-'))
    + agentRow('Platform', agentMono(a.os || '-'))
    + agentRow('Last seen', agentMono(a.lastSeen ? agentFormatDate(a.lastSeen) : '-'))
    + agentRow('Active sessions', agentMono(a.sessionCount))
  );

  // Services card
  var svcHtml = '<div class="agent-svc-tags">';
  if (a.services && a.services.length > 0) {
    a.services.forEach(function (s) {
      var label = (s.name || '') + ' :' + (s.port || '');
      svcHtml += '<span class="agent-svc-tag">' + escHtml(label) + '</span>';
    });
  } else {
    svcHtml += '<span style="padding:0 16px;color:var(--text-muted);font-size:12px;">No services</span>';
  }
  svcHtml += '</div>';
  html += agentCard('Services', svcHtml);

  // File Share card
  var fs = a.capabilities && a.capabilities.fileShare;
  if (fs && fs.enabled) {
    var badgeCls = fs.writable ? 'rw' : 'ro';
    var badgeText = fs.writable ? 'read-write' : 'read-only';
    html += agentCard('File Share',
      agentRow('Status', '<span class="agent-fs-badge ' + badgeCls + '">' + badgeText + '</span>')
      + (fs.maxFileSize ? agentRow('Max file size', agentMono(agentFormatBytes(fs.maxFileSize))) : '')
    );
  } else {
    html += agentCard('File Share',
      agentRow('Status', '<span style="color:var(--text-muted)">Not configured</span>')
    );
  }

  // Update card
  var latestVersion = '';
  if (typeof updateInfo === 'object' && updateInfo && updateInfo.version) {
    latestVersion = updateInfo.version;
  }
  var isOutdated = latestVersion && a.version && a.version !== latestVersion;
  var updateContent = '<div class="agent-update-row">'
    + '<span class="agent-update-current">' + escHtml(a.version || '?') + '</span>';
  if (isOutdated) {
    updateContent += '<span class="agent-update-arrow">&#x2192;</span>'
      + '<span class="agent-update-latest">' + escHtml(latestVersion) + '</span>'
      + '<span class="agent-update-note">Update available</span>'
      + '<button type="button" class="tb-btn" disabled title="Agent update not yet implemented">Update</button>';
  } else {
    updateContent += '<span class="agent-update-note">Up to date</span>';
  }
  updateContent += '</div>';
  html += agentCard('Update', updateContent);

  // Management card (actions not yet implemented)
  html += agentCard('Management',
    agentRow('Configuration', '<span class="danger-desc">Pull or push the agent config file through the tunnel.</span>'
      + '<button type="button" class="tb-btn" disabled title="Coming soon">Pull</button>'
      + '<button type="button" class="tb-btn" disabled title="Coming soon">Push</button>')
    + agentRow('Agent log', '<span class="danger-desc">View or download the agent log output.</span>'
      + '<button type="button" class="tb-btn" disabled title="Coming soon">View Log</button>')
    + agentRow('Restart', '<span class="danger-desc">Restart the telad service on this machine.</span>'
      + '<button type="button" class="tb-btn" disabled title="Coming soon">Restart</button>')
  );

  // Danger Zone (matches Hubs mode style)
  html += '<div class="settings-group danger-zone"><div class="settings-group-header">Danger Zone</div>'
    + '<div class="settings-row"><div class="settings-label">Stop agent</div>'
    + '<div class="settings-value danger-value">'
    + '<span class="danger-desc">Stop the telad service on this machine. Users will lose connectivity.</span>'
    + '<button class="btn-danger btn-sm" disabled title="Coming soon">Stop Agent</button>'
    + '</div></div>'
    + '<div class="settings-row"><div class="settings-label">Unregister</div>'
    + '<div class="settings-value danger-value">'
    + '<span class="danger-desc">Remove this machine from the hub. The agent will need to re-register.</span>'
    + '<button class="btn-danger btn-sm" disabled title="Coming soon">Unregister</button>'
    + '</div></div></div>';

  document.getElementById('agents-detail').innerHTML = html;
}

function agentsPairAgent() {
  // Find the first hub from the profile
  var hub = '';
  for (var key in selectedServices) {
    var svc = selectedServices[key];
    if (svc.hub) { hub = svc.hub; break; }
  }
  if (!hub) { showError('No hub available'); return; }

  showPromptDialog('Pair Agent', 'Enter the machine name for the new agent:', '', 'Generate Code').then(function (machine) {
    if (!machine) return;
    var wsHub = toWSURL(hub);
    goApp.AdminAPICall(wsHub, 'POST', '/api/admin/pair-code',
      JSON.stringify({type: 'register', machines: [machine]}))
      .then(function (resp) {
        try { var data = JSON.parse(resp); } catch (e) { showError('Invalid response'); return; }
        if (data.code) {
          tvLog('Pairing code for ' + machine + ': ' + data.code);
          showPromptDialog('Pairing Code', 'Give this code to the agent operator. It expires in ' + (data.expires || '10 minutes') + '.', data.code, 'Close');
        } else if (data.error) {
          showError('Pair failed: ' + data.error);
        }
      }).catch(function (err) { showError('Pair failed: ' + err); });
  }).catch(function () {});
}

function agentCard(title, content) {
  return '<div class="settings-group"><div class="settings-group-header">' + title + '</div>' + content + '</div>';
}

function agentRow(label, value) {
  return '<div class="settings-row"><div class="settings-label">' + label + '</div><div class="settings-value">' + value + '</div></div>';
}

function agentMono(v) {
  return '<span style="font-family:var(--mono);font-size:12px;">' + escHtml(String(v)) + '</span>';
}

function agentFormatDate(iso) {
  try {
    var d = new Date(iso);
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'});
  } catch (e) { return iso; }
}

function agentFormatBytes(n) {
  if (n >= 1073741824) return (n / 1073741824).toFixed(1) + ' GB';
  if (n >= 1048576) return (n / 1048576).toFixed(0) + ' MB';
  if (n >= 1024) return (n / 1024).toFixed(0) + ' KB';
  return n + ' B';
}

// --- Files Tab ---

var filesView = 'machines'; // 'machines' | 'files'
var filesCurrentMachine = '';
var filesCurrentPath = '';
var filesCurrentWritable = false;
var filesNavHistory = [];
var filesSelectedIndices = new Set();
var filesLastClickedIndex = -1;
var filesCurrentEntries = [];
var filesListGeneration = 0;      // guards against stale async responses
var filesRefreshTimer = null;     // debounce timer for live events
var filesMachineCapabilities = {}; // cached capabilities from hub status

function refreshFilesTab() {
  if (filesView === 'files' && filesCurrentMachine) {
    filesListDir(filesCurrentMachine, filesCurrentPath);
  } else {
    filesShowMachineList();
  }
}

// ── Machine list view ──

function filesShowMachineList() {
  filesView = 'machines';
  filesCurrentMachine = '';
  filesCurrentPath = '';
  filesCurrentWritable = false;
  filesNavHistory = [];
  filesCurrentEntries = [];
  filesClearSelection();
  document.getElementById('files-header').style.display = 'none';
  document.getElementById('files-actionbar').style.display = 'none';
  document.getElementById('files-btn-back').disabled = true;
  document.getElementById('files-btn-up').disabled = true;
  filesRenderPath();

  goApp.GetConnectionState().then(function (state) {
    var machines = {};
    Object.keys(selectedServices).forEach(function (key) {
      var svc = selectedServices[key];
      if (svc.machine && !machines[svc.machine]) {
        machines[svc.machine] = { name: svc.machine, hub: hubNameFromURL(svc.hub) };
      }
    });

    var machineList = Object.keys(machines).map(function (k) { return machines[k]; });
    if (machineList.length === 0) {
      document.getElementById('files-content').innerHTML = '<div class="files-empty">No machines in profile.</div>';
      document.getElementById('files-status-counts').textContent = '';
      return;
    }

    // Use cached capabilities if available, fetch only if cache is empty
    var capabilitiesPromise;
    if (state.connected && Object.keys(filesMachineCapabilities).length > 0) {
      capabilitiesPromise = Promise.resolve(filesMachineCapabilities);
    } else if (state.connected) {
      capabilitiesPromise = goApp.GetMachineCapabilities().catch(function (err) { tvLog('Capabilities fetch failed: ' + err); return {}; });
    } else {
      filesMachineCapabilities = {};
      capabilitiesPromise = Promise.resolve({});
    }
    capabilitiesPromise.then(function (caps) {
      filesMachineCapabilities = caps || {};
      var html = '<div class="files-machine-list">';
      machineList.forEach(function (m) {
        var statusClass = state.connected ? 'online' : 'disconnected';
        var badge, disabled;

        if (!state.connected) {
          badge = '<span class="files-machine-badge off">disconnected</span>';
          disabled = true;
        } else {
          var mc = caps[m.name];
          var fs = mc && mc.fileShare;
          if (fs && fs.enabled) {
            badge = fs.writable
              ? '<span class="files-machine-badge">read-write</span>'
              : '<span class="files-machine-badge ro">read-only</span>';
            disabled = false;
          } else {
            badge = '<span class="files-machine-badge none">no file share</span>';
            disabled = true;
          }
        }

        var onclick = disabled ? '' : ' onclick="filesOpenMachine(\'' + escAttr(m.name) + '\')"';
        var cls = disabled ? ' disabled' : '';

        html += '<div class="files-machine-card' + cls + '"' + onclick + '>'
          + '<span class="files-machine-status ' + statusClass + '"></span>'
          + '<span class="fe-icon-machine"></span>'
          + '<div>'
          + '<div class="files-machine-name">' + escHtml(m.name) + '</div>'
          + '<div class="files-machine-meta">' + escHtml(m.hub) + '</div>'
          + '</div>'
          + badge
          + '</div>';
      });
      html += '</div>';

      document.getElementById('files-content').innerHTML = html;
      document.getElementById('files-status-counts').textContent = machineList.length + ' machine' + (machineList.length !== 1 ? 's' : '');
    }).catch(function () {
      // Capabilities unavailable -- show all machines as clickable
      // with unknown status. Better than blocking the entire view.
      var fallbackHtml = '<div class="files-machine-list">';
      machineList.forEach(function (m) {
        fallbackHtml += '<div class="files-machine-card" onclick="filesOpenMachine(\'' + escAttr(m.name) + '\')">'
          + '<span class="files-machine-status online"></span>'
          + '<span class="fe-icon-machine"></span>'
          + '<div><div class="files-machine-name">' + escHtml(m.name) + '</div>'
          + '<div class="files-machine-meta">' + escHtml(m.hub) + '</div></div>'
          + '<span class="files-machine-badge none">unknown</span>'
          + '</div>';
      });
      fallbackHtml += '</div>';
      document.getElementById('files-content').innerHTML = fallbackHtml;
      document.getElementById('files-status-counts').textContent = machineList.length + ' machine' + (machineList.length !== 1 ? 's' : '');
    });
  }).catch(function () {
    document.getElementById('files-content').innerHTML = '<div class="files-empty">Failed to get connection state.</div>';
  });
}

function filesOpenMachine(name) {
  filesCurrentMachine = name;
  filesCurrentPath = '';
  filesNavHistory = ['machines'];
  filesClearSelection();

  // Determine writable from cached capabilities
  var mc = filesMachineCapabilities[name];
  var fs = mc && mc.fileShare;
  filesCurrentWritable = !!(fs && fs.enabled && fs.writable);

  filesListDir(name, '');
}

// ── File list view ──

function filesListDir(machine, path) {
  filesView = 'files';
  var gen = ++filesListGeneration; // guard against stale responses

  document.getElementById('files-header').style.display = 'flex';
  document.getElementById('files-actionbar').style.display = 'flex';
  document.getElementById('files-btn-back').disabled = (filesNavHistory.length === 0);
  document.getElementById('files-btn-up').disabled = false;
  filesRenderPath();
  filesUpdateActionButtons();

  var listEl = document.getElementById('files-content');
  listEl.innerHTML = '<div class="files-empty">Loading...</div>';

  var req = JSON.stringify({op: 'list', path: path});
  goApp.FileShareRequest(machine, req).then(function (respJSON) {
    if (gen !== filesListGeneration) return; // stale response, discard
    try {
      var resp = JSON.parse(respJSON);
    } catch (e) {
      listEl.innerHTML = '<div class="files-empty">Invalid response from server.</div>';
      return;
    }
    if (!resp.ok) {
      listEl.innerHTML = '<div class="files-empty">' + escHtml(resp.error) + '</div>';
      return;
    }

    filesCurrentPath = path;
    filesCurrentEntries = (resp.entries || []).slice();
    filesCurrentEntries.sort(function (a, b) {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      return a.name.localeCompare(b.name);
    });

    filesRenderEntries();
    filesUpdateStatusBar();
    filesUpdateActionButtons();
  }).catch(function (err) {
    if (gen !== filesListGeneration) return;
    listEl.innerHTML = '<div class="files-empty">' + escHtml(String(err)) + '</div>';
  });
}

function filesRenderEntries() {
  var canDrag = filesCurrentWritable;
  var html = '';

  // Parent directory row (drop target for moving items up)
  if (filesCurrentPath) {
    var parentDropAttr = canDrag
      ? ' ondragover="filesDragOver(event)" ondragenter="filesDragEnter(event)" ondragleave="filesDragLeave(event)" ondrop="filesDropParent(event)"'
      : '';
    html += '<div class="files-entry files-entry-dir" data-idx="-1"'
      + parentDropAttr
      + ' ondblclick="filesGoUp()">'
      + '<span class="fe-name"><span class="fe-icon-dir"></span><span class="fe-label">..</span></span>'
      + '<span class="fe-modified"></span>'
      + '<span class="fe-type">Parent folder</span>'
      + '<span class="fe-size"></span>'
      + '</div>';
  }

  filesCurrentEntries.forEach(function (entry, idx) {
    var selClass = filesSelectedIndices.has(idx) ? ' selected' : '';
    var dirClass = entry.isDir ? ' files-entry-dir' : '';
    var dragAttr = canDrag ? ' draggable="true" ondragstart="filesDragStart(event, ' + idx + ')"' : '';
    var dropAttr = entry.isDir ? ' ondragover="filesDragOver(event)" ondragenter="filesDragEnter(event)" ondragleave="filesDragLeave(event)" ondrop="filesDrop(event, ' + idx + ')"' : '';

    html += '<div class="files-entry' + dirClass + selClass + '" data-idx="' + idx + '"'
      + dragAttr + dropAttr
      + ' onclick="filesOnEntryClick(event, ' + idx + ')"'
      + ' ondblclick="filesOnEntryDblClick(' + idx + ')">';

    if (entry.isDir) {
      html += '<span class="fe-name"><span class="fe-icon-dir"></span><span class="fe-label">' + escHtml(entry.name) + '</span></span>'
        + '<span class="fe-modified">' + escHtml(formatFileDate(entry.modTime)) + '</span>'
        + '<span class="fe-type">File folder</span>'
        + '<span class="fe-size"></span>';
    } else {
      html += '<span class="fe-name"><span class="fe-icon-file"></span><span class="fe-label">' + escHtml(entry.name) + '</span></span>'
        + '<span class="fe-modified">' + escHtml(formatFileDate(entry.modTime)) + '</span>'
        + '<span class="fe-type"></span>'
        + '<span class="fe-size">' + escHtml(formatFileSize(entry.size)) + '</span>';
    }
    html += '</div>';
  });

  if (filesCurrentEntries.length === 0) {
    html += '<div class="files-empty">This folder is empty</div>';
  }

  document.getElementById('files-content').innerHTML = html;
}

// ── Selection ──

function filesClearSelection() {
  filesSelectedIndices.clear();
  filesLastClickedIndex = -1;
  filesUpdateActionButtons();
}

function filesOnEntryClick(event, idx) {
  event.stopPropagation();
  if (event.ctrlKey || event.metaKey) {
    if (filesSelectedIndices.has(idx)) filesSelectedIndices.delete(idx);
    else filesSelectedIndices.add(idx);
    filesLastClickedIndex = idx;
  } else if (event.shiftKey && filesLastClickedIndex >= 0) {
    var start = Math.min(filesLastClickedIndex, idx);
    var end = Math.max(filesLastClickedIndex, idx);
    for (var i = start; i <= end; i++) filesSelectedIndices.add(i);
  } else {
    filesSelectedIndices.clear();
    filesSelectedIndices.add(idx);
    filesLastClickedIndex = idx;
  }
  document.querySelectorAll('.files-entry').forEach(function (el) {
    var i = parseInt(el.getAttribute('data-idx'));
    el.classList.toggle('selected', filesSelectedIndices.has(i));
  });
  filesUpdateActionButtons();
}

function filesOnEntryDblClick(idx) {
  filesSelectedIndices.clear();
  filesLastClickedIndex = -1;
  var entry = filesCurrentEntries[idx];
  if (!entry) return;

  if (entry.isDir) {
    var path = filesCurrentPath ? filesCurrentPath + '/' + entry.name : entry.name;
    filesNavigateTo(path);
  } else {
    filesDownloadFile(entry.name, filesCurrentPath ? filesCurrentPath + '/' + entry.name : entry.name);
  }
}

function filesUpdateActionButtons() {
  var hasSelection = filesSelectedIndices.size > 0;
  var singleSelection = filesSelectedIndices.size === 1;
  var w = filesCurrentWritable;

  document.getElementById('files-btn-download').disabled = !hasSelection;
  document.getElementById('files-btn-delete').disabled = !hasSelection || !w;
  document.getElementById('files-btn-rename').disabled = !singleSelection || !w;
  document.getElementById('files-btn-newfolder').disabled = !w;
  document.getElementById('files-btn-upload').disabled = !w;

  var info = document.getElementById('files-selection-info');
  if (hasSelection && info) {
    var fc = 0, dc = 0, ts = 0;
    filesSelectedIndices.forEach(function (i) {
      var e = filesCurrentEntries[i];
      if (e) { if (e.isDir) dc++; else { fc++; ts += (e.size || 0); } }
    });
    var parts = [];
    if (fc) parts.push(fc + ' file' + (fc !== 1 ? 's' : ''));
    if (dc) parts.push(dc + ' folder' + (dc !== 1 ? 's' : ''));
    info.textContent = parts.join(', ') + ' selected' + (ts > 0 ? ' (' + formatFileSize(ts) + ')' : '');
  } else if (info) {
    info.textContent = '';
  }
}

// ── Drag and Drop ──

function filesDragStart(event, idx) {
  // If dragged item is in selection, drag all selected; otherwise drag just this one
  var items = [];
  if (filesSelectedIndices.has(idx)) {
    filesSelectedIndices.forEach(function (i) {
      var e = filesCurrentEntries[i];
      if (e) items.push(e.name);
    });
  } else {
    var e = filesCurrentEntries[idx];
    if (e) items.push(e.name);
  }
  event.dataTransfer.setData('text/plain', JSON.stringify(items));
  event.dataTransfer.effectAllowed = 'move';
}

function filesDragOver(event) {
  event.preventDefault();
  event.dataTransfer.dropEffect = 'move';
}

function filesDragEnter(event) {
  event.preventDefault();
  var row = event.target.closest('.files-entry-dir');
  if (row) row.classList.add('drag-over');
}

function filesDragLeave(event) {
  var row = event.target.closest('.files-entry-dir');
  if (row && !row.contains(event.relatedTarget)) {
    row.classList.remove('drag-over');
  }
}

function filesDrop(event, targetIdx) {
  event.preventDefault();
  var row = event.target.closest('.files-entry-dir');
  if (row) row.classList.remove('drag-over');

  var targetEntry = filesCurrentEntries[targetIdx];
  if (!targetEntry || !targetEntry.isDir) return;

  var data = event.dataTransfer.getData('text/plain');
  var items;
  try { items = JSON.parse(data); } catch (e) { return; }
  if (!items || items.length === 0) return;

  // Cannot drop a folder into itself
  if (items.indexOf(targetEntry.name) >= 0) return;

  var machine = filesCurrentMachine;
  var path = filesCurrentPath;
  var targetDir = path ? path + '/' + targetEntry.name : targetEntry.name;

  // Move items sequentially
  var idx = 0;
  function moveNext() {
    if (idx >= items.length) {
      filesClearSelection();
      filesRefresh();
      return;
    }
    var name = items[idx++];
    var srcPath = path ? path + '/' + name : name;
    var dstPath = targetDir + '/' + name;
    var req = JSON.stringify({op: 'move', path: srcPath, newPath: dstPath});
    goApp.FileShareRequest(machine, req).then(function (respJSON) {
      try { var resp = JSON.parse(respJSON); } catch (e) { /* skip */ }
      if (resp && resp.ok) tvLog('Moved ' + name + ' to ' + targetEntry.name + '/');
      else tvLog('Move failed (' + name + '): ' + (resp ? resp.error : 'unknown'));
      moveNext();
    }).catch(function () { moveNext(); });
  }
  moveNext();
}

function filesDropParent(event) {
  event.preventDefault();
  var row = event.target.closest('.files-entry-dir');
  if (row) row.classList.remove('drag-over');

  var data = event.dataTransfer.getData('text/plain');
  var items;
  try { items = JSON.parse(data); } catch (e) { return; }
  if (!items || items.length === 0) return;

  var machine = filesCurrentMachine;
  var path = filesCurrentPath;
  // Parent path
  var parentParts = path.split('/');
  parentParts.pop();
  var parentDir = parentParts.join('/');

  var idx = 0;
  function moveNext() {
    if (idx >= items.length) {
      filesClearSelection();
      filesRefresh();
      return;
    }
    var name = items[idx++];
    var srcPath = path ? path + '/' + name : name;
    var dstPath = parentDir ? parentDir + '/' + name : name;
    var req = JSON.stringify({op: 'move', path: srcPath, newPath: dstPath});
    goApp.FileShareRequest(machine, req).then(function (respJSON) {
      try { var resp = JSON.parse(respJSON); } catch (e) { /* skip */ }
      if (resp && resp.ok) tvLog('Moved ' + name + ' to parent');
      else tvLog('Move failed (' + name + '): ' + (resp ? resp.error : 'unknown'));
      moveNext();
    }).catch(function () { moveNext(); });
  }
  moveNext();
}

function filesDropToBreadcrumb(event, targetPath) {
  event.preventDefault();
  event.target.classList.remove('drag-over');

  var data = event.dataTransfer.getData('text/plain');
  var items;
  try { items = JSON.parse(data); } catch (e) { return; }
  if (!items || items.length === 0) return;

  var machine = filesCurrentMachine;
  var path = filesCurrentPath;

  // Don't move to the same directory
  if (targetPath === path) return;

  var idx = 0;
  function moveNext() {
    if (idx >= items.length) {
      filesClearSelection();
      filesRefresh();
      return;
    }
    var name = items[idx++];
    var srcPath = path ? path + '/' + name : name;
    var dstPath = targetPath ? targetPath + '/' + name : name;
    var req = JSON.stringify({op: 'move', path: srcPath, newPath: dstPath});
    goApp.FileShareRequest(machine, req).then(function (respJSON) {
      try { var resp = JSON.parse(respJSON); } catch (e) { /* skip */ }
      if (resp && resp.ok) tvLog('Moved ' + name + ' to /' + (targetPath || ''));
      else tvLog('Move failed (' + name + '): ' + (resp ? resp.error : 'unknown'));
      moveNext();
    }).catch(function () { moveNext(); });
  }
  moveNext();
}

// Click empty area clears selection
document.addEventListener('click', function (e) {
  if (filesView !== 'files') return;
  if (!e.target.closest('.files-entry') && !e.target.closest('.files-actionbar') && !e.target.closest('.files-header')) {
    filesClearSelection();
    document.querySelectorAll('.files-entry.selected').forEach(function (el) { el.classList.remove('selected'); });
  }
});

// ── Navigation ──

function filesNavigateTo(path) {
  filesNavHistory.push(filesCurrentMachine + ':' + filesCurrentPath);
  filesCurrentPath = path;
  filesClearSelection();
  filesListDir(filesCurrentMachine, path);
}

function filesGoBack() {
  if (filesNavHistory.length === 0) return;
  var prev = filesNavHistory.pop();
  if (prev === 'machines') { filesShowMachineList(); return; }
  var colonIdx = prev.indexOf(':');
  filesCurrentMachine = colonIdx >= 0 ? prev.substring(0, colonIdx) : prev;
  filesCurrentPath = colonIdx >= 0 ? prev.substring(colonIdx + 1) : '';
  filesClearSelection();
  filesListDir(filesCurrentMachine, filesCurrentPath);
}

function filesGoUp() {
  if (!filesCurrentMachine) return;
  if (!filesCurrentPath) { filesShowMachineList(); return; }
  filesNavHistory.push(filesCurrentMachine + ':' + filesCurrentPath);
  var parts = filesCurrentPath.split('/');
  parts.pop();
  filesCurrentPath = parts.join('/');
  filesClearSelection();
  filesListDir(filesCurrentMachine, filesCurrentPath);
}

function filesRenderPath() {
  var el = document.getElementById('files-path');
  if (!el) return;
  var canDrop = filesCurrentWritable && filesView === 'files';
  var dropAttrs = function (targetPath) {
    if (!canDrop) return '';
    return ' ondragover="filesDragOver(event)" ondragenter="event.preventDefault(); this.classList.add(\'drag-over\')"'
      + ' ondragleave="this.classList.remove(\'drag-over\')"'
      + ' ondrop="filesDropToBreadcrumb(event, \'' + escAttr(targetPath) + '\')"';
  };

  if (filesView === 'machines') {
    el.innerHTML = '<span class="files-path-seg root">Machines</span>';
    return;
  }
  // Machine root segment (drop target = machine root '')
  var html = '<span class="files-path-seg root" onclick="filesShowMachineList()">Machines</span>';
  html += '<span class="files-path-sep">&#x203A;</span>';
  html += '<span class="files-path-seg"' + dropAttrs('') + ' onclick="filesNavHistory.push(filesCurrentMachine+\':\'+filesCurrentPath); filesCurrentPath=\'\'; filesClearSelection(); filesListDir(filesCurrentMachine,\'\');">' + escHtml(filesCurrentMachine) + '</span>';
  if (filesCurrentPath) {
    var parts = filesCurrentPath.split('/');
    var acc = '';
    parts.forEach(function (p) {
      acc = acc ? acc + '/' + p : p;
      var accCopy = acc;
      html += '<span class="files-path-sep">&#x203A;</span>';
      html += '<span class="files-path-seg"' + dropAttrs(accCopy) + ' onclick="filesNavHistory.push(filesCurrentMachine+\':\'+filesCurrentPath); filesCurrentPath=\'' + escAttr(accCopy) + '\'; filesClearSelection(); filesListDir(filesCurrentMachine,\'' + escAttr(accCopy) + '\');">' + escHtml(p) + '</span>';
    });
  }
  el.innerHTML = html;
}

function filesRefresh() {
  if (filesView === 'files' && filesCurrentMachine) {
    filesListDir(filesCurrentMachine, filesCurrentPath);
  } else {
    filesShowMachineList();
  }
}

// Debounced refresh for live file events (prevents flooding on batch changes)
function filesDebouncedRefresh() {
  if (filesRefreshTimer) clearTimeout(filesRefreshTimer);
  filesRefreshTimer = setTimeout(function () {
    filesRefreshTimer = null;
    filesRefresh();
  }, 300);
}

// ── Actions ──

function filesDownloadFile(fileName, remotePath) {
  var machine = filesCurrentMachine; // capture for closure
  goApp.SaveFileDialog(fileName).then(function (localPath) {
    if (!localPath) return;
    tvLog('Downloading ' + remotePath + '...');
    goApp.FileShareDownload(machine, remotePath, localPath).then(function (respJSON) {
      try { var resp = JSON.parse(respJSON); } catch (e) { showError('Download failed: invalid response'); return; }
      if (resp.ok) {
        tvLog('Downloaded ' + remotePath + ' (' + formatFileSize(resp.size) + ')');
      } else {
        tvLog('Download failed: ' + resp.error);
        showError('Download failed: ' + resp.error);
      }
    }).catch(function (err) {
      tvLog('Download failed: ' + err);
      showError('Download failed: ' + err);
    });
  }).catch(function (err) { showError('Save dialog failed: ' + err); });
}

function filesDownloadSelected() {
  if (filesSelectedIndices.size === 0) return;
  // Serialize downloads to avoid multiple save dialogs at once
  var items = [];
  filesSelectedIndices.forEach(function (i) {
    var e = filesCurrentEntries[i];
    if (e && !e.isDir) {
      items.push({ name: e.name, path: filesCurrentPath ? filesCurrentPath + '/' + e.name : e.name });
    }
  });
  if (items.length === 0) {
    showError('Directories cannot be downloaded individually.');
    return;
  }
  var idx = 0;
  function downloadNext() {
    if (idx >= items.length) return;
    var item = items[idx++];
    filesDownloadFile(item.name, item.path);
    // Note: filesDownloadFile is async but we don't chain here because
    // each one opens a save dialog. Sequential is better UX.
  }
  downloadNext();
}

function filesUpload() {
  if (!filesCurrentMachine) return;
  var machine = filesCurrentMachine; // capture for closure
  var path = filesCurrentPath;
  goApp.OpenFileDialog().then(function (localPath) {
    if (!localPath) return;
    var fileName = localPath.split(/[\\/]/).pop();
    var remoteName = path ? path + '/' + fileName : fileName;
    tvLog('Uploading ' + fileName + '...');
    goApp.FileShareUpload(machine, localPath, remoteName).then(function (respJSON) {
      try { var resp = JSON.parse(respJSON); } catch (e) { showError('Upload failed: invalid response'); return; }
      if (resp.ok) {
        tvLog('Uploaded ' + fileName + ' (' + formatFileSize(resp.size) + ')');
        filesRefresh();
      } else {
        tvLog('Upload failed: ' + resp.error);
        showError('Upload failed: ' + resp.error);
      }
    }).catch(function (err) {
      tvLog('Upload failed: ' + err);
      showError('Upload failed: ' + err);
    });
  }).catch(function (err) { showError('Open dialog failed: ' + err); });
}

function filesDeleteSelected() {
  var names = [];
  filesSelectedIndices.forEach(function (i) {
    var e = filesCurrentEntries[i];
    if (e) names.push(e.name);
  });
  if (names.length === 0) return;
  var machine = filesCurrentMachine; // capture for closure
  var path = filesCurrentPath;
  showConfirmDialog('Delete', 'Delete ' + names.join(', ') + '?', 'Delete').then(function (yes) {
    if (!yes) return;
    var idx = 0;
    function deleteNext() {
      if (idx >= names.length) {
        filesClearSelection();
        filesRefresh();
        return;
      }
      var name = names[idx++];
      var remotePath = path ? path + '/' + name : name;
      var req = JSON.stringify({op: 'delete', path: remotePath});
      goApp.FileShareRequest(machine, req).then(function (respJSON) {
        try { var resp = JSON.parse(respJSON); } catch (e) { /* ignore parse errors */ }
        if (resp && resp.ok) tvLog('Deleted ' + remotePath);
        else tvLog('Delete failed (' + name + '): ' + (resp ? resp.error : 'unknown'));
        deleteNext();
      }).catch(function () { deleteNext(); });
    }
    deleteNext();
  }).catch(function () {});
}

function filesNewFolder() {
  if (!filesCurrentMachine) return;
  var machine = filesCurrentMachine;
  var path = filesCurrentPath;
  showPromptDialog('New Folder', '', '', 'Create').then(function (name) {
    if (!name) return;
    var fullPath = path ? path + '/' + name : name;
    var req = JSON.stringify({op: 'mkdir', path: fullPath});
    goApp.FileShareRequest(machine, req).then(function (respJSON) {
      try { var resp = JSON.parse(respJSON); } catch (e) { showError('New folder failed: invalid response'); return; }
      if (resp.ok) {
        tvLog('Created folder ' + name);
        filesRefresh();
      } else {
        tvLog('New folder failed: ' + resp.error);
        showError('New folder failed: ' + resp.error);
      }
    }).catch(function (err) { showError('New folder failed: ' + err); });
  }).catch(function () {});
}

function filesRenameSelected() {
  if (filesSelectedIndices.size !== 1) return;
  var idx = filesSelectedIndices.values().next().value;
  var entry = filesCurrentEntries[idx];
  if (!entry) return;
  var machine = filesCurrentMachine;
  var path = filesCurrentPath;

  showPromptDialog('Rename', 'Rename "' + entry.name + '" to:', entry.name, 'Rename').then(function (newName) {
    if (!newName || newName === entry.name) return;
    var oldPath = path ? path + '/' + entry.name : entry.name;
    var req = JSON.stringify({op: 'rename', path: oldPath, newName: newName});
    goApp.FileShareRequest(machine, req).then(function (respJSON) {
      try { var resp = JSON.parse(respJSON); } catch (e) { showError('Rename failed: invalid response'); return; }
      if (resp.ok) {
        tvLog('Renamed ' + entry.name + ' to ' + newName);
        filesClearSelection();
        filesRefresh();
      } else {
        tvLog('Rename failed: ' + resp.error);
        showError('Rename failed: ' + resp.error);
      }
    }).catch(function (err) { showError('Rename failed: ' + err); });
  }).catch(function () {});
}

// ── Status bar ──

function filesUpdateStatusBar() {
  var dirs = 0, files = 0, totalSize = 0;
  filesCurrentEntries.forEach(function (e) {
    if (e.isDir) dirs++; else { files++; totalSize += (e.size || 0); }
  });
  var parts = [];
  if (files) parts.push(files + ' file' + (files !== 1 ? 's' : ''));
  if (dirs) parts.push(dirs + ' folder' + (dirs !== 1 ? 's' : ''));
  var text = parts.join(', ') || 'Empty';
  var modeClass = filesCurrentWritable ? 'rw' : 'ro';
  var modeText = filesCurrentWritable ? 'read-write' : 'read-only';
  document.getElementById('files-status').innerHTML = '<span>' + text + '</span>'
    + (totalSize > 0 ? '<span class="files-status-sep"></span><span>' + formatFileSize(totalSize) + '</span>' : '')
    + '<span class="files-status-mode"><span class="files-status-dot ' + modeClass + '"></span>' + modeText + '</span>';
}

// ── Formatters ──

function formatFileSize(bytes) {
  if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(1) + ' GB';
  if (bytes >= 1048576) return (bytes / 1048576).toFixed(1) + ' MB';
  if (bytes >= 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return bytes + ' B';
}

function formatFileDate(iso) {
  if (!iso) return '';
  try {
    var d = new Date(iso);
    return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'});
  } catch (e) {
    return iso;
  }
}

// --- Hubs Tab (Admin Layout) ---

var currentAdminHub = '';
var currentAdminView = 'hub-settings';

function refreshHubsTab() {
  var select = document.getElementById('hub-admin-select');
  if (!select) return;

  goApp.GetKnownHubs().then(function (hubs) {
    var prev = select.value;
    select.innerHTML = '';
    if (!hubs || hubs.length === 0) {
      select.innerHTML = '<option value="">No hubs configured</option>';
      currentAdminHub = '';
      renderHubAdminDetail();
      return;
    }
    hubs.forEach(function (hub) {
      var opt = document.createElement('option');
      opt.value = hub.url;
      opt.textContent = hub.name;
      select.appendChild(opt);
    });
    // Restore previous selection or use first
    if (prev && hubs.some(function (h) { return h.url === prev; })) {
      select.value = prev;
    }
    currentAdminHub = select.value;
    renderHubAdminDetail();
  });
}

function onHubAdminSelect() {
  currentAdminHub = document.getElementById('hub-admin-select').value;
  renderHubAdminDetail();
}

function showHubAdminView(view) {
  currentAdminView = view;
  var items = document.querySelectorAll('.hubs-admin-nav-item');
  items.forEach(function (el) {
    el.classList.toggle('active', el.getAttribute('data-view') === view);
  });
  renderHubAdminDetail();
}

function renderHubAdminDetail() {
  var pane = document.getElementById('hubs-admin-detail');
  if (!pane) return;
  if (!currentAdminHub) {
    pane.innerHTML = '<p class="empty-hint">Add a hub to get started.</p>';
    return;
  }
  switch (currentAdminView) {
    case 'hub-settings': renderHubSettings(pane); break;
    case 'machines': renderHubMachines(pane); break;
    case 'tokens': renderHubTokens(pane); break;
    case 'acls': renderHubACLs(pane); break;
    default: pane.innerHTML = '';
  }
}

// --- Hub Settings View ---

function renderHubSettings(pane) {
  var hub = currentAdminHub;
  var hubName = hubNameFromUrl(hub);
  pane.innerHTML = '<h2>Hub Settings</h2>'
    + '<p class="section-desc">Connection and configuration for <strong>' + escHtml(hubName) + '</strong></p>'
    + '<p class="loading">Loading...</p>';

  goApp.LogAdminGET(hub, '/api/status');
  goApp.LogAdminGET(hub, '/api/admin/portals');

  // Fetch hub info and portals in parallel (flat, no nesting)
  var hubInfoData = null;
  var portalsData = null;
  var tokenRole = 'unknown';
  var done = 0;

  function tryRender() {
    done++;
    if (done < 3) return;
    var consoleUrl = hub.replace('wss://', 'https://').replace('ws://', 'http://').replace(/\/$/, '') + '/';

    var html = '<h2>Hub Settings</h2>'
      + '<p class="section-desc">Connection and configuration for <strong>' + escHtml(hubName) + '</strong></p>';

    // Connection group
    html += '<div class="settings-group"><div class="settings-group-header">Connection</div>';
    html += '<div class="settings-row"><div class="settings-label">URL</div><div class="settings-value">' + escHtml(hub) + '</div></div>';

    var statusDot = hubInfoData ? '<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:var(--accent);margin-right:6px;vertical-align:middle;"></span>Online'
      : '<span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:#95a5a6;margin-right:6px;vertical-align:middle;"></span>Offline';
    html += '<div class="settings-row"><div class="settings-label">Status</div><div class="settings-value" style="font-family:var(--font)">' + statusDot + '</div></div>';

    html += '<div class="settings-row"><div class="settings-label">Your role</div><div class="settings-value" style="font-family:var(--font)"><span class="role-badge role-' + tokenRole + '">' + tokenRole + '</span></div></div>';
    html += '<div class="settings-row"><div class="settings-label">Console</div><div class="settings-value"><a href="' + escAttr(consoleUrl) + '" target="_blank">' + escHtml(consoleUrl) + '</a></div></div>';
    html += '</div>';

    // Hub info group
    if (hubInfoData) {
      var hi = hubInfoData.hub || {};
      html += '<div class="settings-group"><div class="settings-group-header">Hub Info</div>';
      if (hubInfoData.hubName) html += '<div class="settings-row"><div class="settings-label">Hub name</div><div class="settings-value">' + escHtml(hubInfoData.hubName) + '</div></div>';
      if (hi.hostname) html += '<div class="settings-row"><div class="settings-label">Hostname</div><div class="settings-value">' + escHtml(hi.hostname) + '</div></div>';
      if (hi.os && hi.arch) html += '<div class="settings-row"><div class="settings-label">Platform</div><div class="settings-value">' + escHtml(hi.os + '/' + hi.arch) + '</div></div>';
      if (hi.goVersion) html += '<div class="settings-row"><div class="settings-label">Go version</div><div class="settings-value">' + escHtml(hi.goVersion) + '</div></div>';
      if (hi.uptime) {
        var secs = parseInt(hi.uptime, 10);
        var uptimeStr = formatUptime(secs);
        html += '<div class="settings-row"><div class="settings-label">Uptime</div><div class="settings-value">' + escHtml(uptimeStr) + '</div></div>';
      }
      html += '</div>';
    }

    // Portals group
    html += '<div class="settings-group"><div class="settings-group-header">Portals</div>';
    if (portalsData && portalsData.portals && portalsData.portals.length > 0) {
      portalsData.portals.forEach(function (p) {
        var syncLabel = p.hasSyncToken ? '<span style="color:var(--accent);font-family:var(--font);font-size:11px;">sync token set</span>' : '';
        html += '<div class="settings-row"><div class="settings-label">' + escHtml(p.name) + '</div>'
          + '<div class="settings-value" style="display:flex;align-items:center;gap:12px;">'
          + '<a href="' + escAttr(p.url) + '" target="_blank">' + escHtml(p.url) + '</a>'
          + syncLabel + '</div></div>';
      });
    } else {
      html += '<div class="settings-row"><div class="settings-label">None</div><div class="settings-value" style="font-family:var(--font);color:var(--text-muted)">No portal registrations</div></div>';
    }
    html += '</div>';

    // Danger zone
    html += '<div class="settings-group danger-zone"><div class="settings-group-header">Danger Zone</div>'
      + '<div class="settings-row"><div class="settings-label">Remove hub</div>'
      + '<div class="settings-value danger-value">'
      + '<span class="danger-desc">Remove this hub from TelaVisor. Does not affect the hub itself.</span>'
      + '<button class="btn-danger btn-sm" onclick="removeHub(\'' + escAttr(hub) + '\')">Remove Hub</button>'
      + '</div></div>'
      + '<div class="settings-row"><div class="settings-label">Clear credentials</div>'
      + '<div class="settings-value danger-value">'
      + '<span class="danger-desc">Delete all stored hub tokens. You will need to re-add hubs.</span>'
      + '<button class="btn-danger btn-sm" onclick="clearCredentialStore()">Clear All</button>'
      + '</div></div></div>';

    pane.innerHTML = html;
  }

  goApp.GetHubInfo(hub).then(function (raw) {
    try { hubInfoData = JSON.parse(raw); } catch (e) { hubInfoData = null; }
    tryRender();
  }).catch(function () { tryRender(); });

  goApp.AdminListPortals(hub).then(function (raw) {
    try { portalsData = JSON.parse(raw); } catch (e) { portalsData = null; }
    tryRender();
  }).catch(function () { tryRender(); });

  goApp.GetTokenRole(hub).then(function (role) {
    tokenRole = role || 'unknown';
    tryRender();
  }).catch(function () { tryRender(); });
}

function formatUptime(secs) {
  if (isNaN(secs) || secs < 0) return 'unknown';
  var d = Math.floor(secs / 86400);
  var h = Math.floor((secs % 86400) / 3600);
  var m = Math.floor((secs % 3600) / 60);
  var parts = [];
  if (d > 0) parts.push(d + 'd');
  if (h > 0) parts.push(h + 'h');
  parts.push(m + 'm');
  return parts.join(' ');
}

// --- Machines View ---

function renderHubMachines(pane) {
  var hub = currentAdminHub;
  pane.innerHTML = '<h2>Machines</h2>'
    + '<p class="section-desc">Registered machines on <strong>' + escHtml(hubNameFromUrl(hub)) + '</strong></p>'
    + '<p class="loading">Loading...</p>';

  goApp.LogAdminGET(hub, '/api/status');
  goApp.GetHubStatus(hub).then(function (status) {
    if (!status.online) {
      pane.innerHTML = '<h2>Machines</h2><p class="section-desc">Hub is offline or unreachable.</p>';
      return;
    }
    var html = '<h2>Machines</h2>'
      + '<p class="section-desc">Registered machines on <strong>' + escHtml(hubNameFromUrl(hub)) + '</strong></p>';

    if (!status.machines || status.machines.length === 0) {
      html += '<p class="empty-hint">No machines registered.</p>';
    } else {
      status.machines.forEach(function (m) {
        var dotClass = m.agentConnected ? 'online' : 'offline';
        var sessionsHtml = m.sessionCount > 0
          ? '<span style="font-size:12px;color:var(--accent);font-weight:600;">' + m.sessionCount + ' active session' + (m.sessionCount > 1 ? 's' : '') + '</span>'
          : '<span style="font-size:12px;color:var(--text-muted);">No active sessions</span>';
        var servicesHtml = '';
        if (m.services && m.services.length > 0) {
          m.services.forEach(function (s) {
            servicesHtml += '<span class="service-tag">' + escHtml(s.name) + ' :' + s.port + '</span>';
          });
        }
        html += '<div class="machine-card">'
          + '<div class="machine-status-dot ' + dotClass + '"></div>'
          + '<div class="machine-info">'
          + '<div class="machine-name">' + escHtml(m.id) + '</div>'
          + '<div class="machine-detail">' + (m.agentConnected ? 'Online' : 'Offline') + (m.lastSeen ? ' &middot; Last seen: ' + escHtml(m.lastSeen) : '') + '</div>'
          + '<div class="machine-services">' + servicesHtml + '</div>'
          + '</div>'
          + '<div style="text-align:right;">' + sessionsHtml + '</div>'
          + '</div>';
      });
    }
    pane.innerHTML = html;
  });
}

// --- Tokens View ---

function renderHubTokens(pane) {
  var hub = currentAdminHub;
  pane.innerHTML = '<h2>Tokens</h2>'
    + '<p class="section-desc">Manage authentication tokens for <strong>' + escHtml(hubNameFromUrl(hub)) + '</strong></p>'
    + '<p class="loading">Loading...</p>';

  goApp.LogAdminGET(hub, '/api/admin/tokens');
  goApp.AdminListTokens(hub).then(function (raw) {
    var data;
    try { data = JSON.parse(raw); } catch (e) { data = {}; }
    if (data.error) {
      pane.innerHTML = '<h2>Tokens</h2><p class="section-desc">' + escHtml(data.error) + '</p>';
      return;
    }
    var tokens = data.tokens || [];

    var html = '<h2>Tokens</h2>'
      + '<p class="section-desc">Manage authentication tokens for <strong>' + escHtml(hubNameFromUrl(hub)) + '</strong></p>';

    html += '<div class="toolbar">'
      + '<button class="btn-primary btn-sm" onclick="promptCreateToken()">Add Token</button>'
      + '<button class="btn-primary btn-sm" style="background:#3498db;border-color:#3498db;" onclick="promptPairCode()">Generate Pairing Code</button>'
      + '</div>';

    html += '<p style="font-size:11px;color:var(--text-muted);margin-bottom:12px;">Full tokens are only shown at creation or after rotation.</p>';

    html += '<table class="admin-table"><thead><tr>'
      + '<th>Identity</th><th>Role</th><th>Token Preview</th><th>Actions</th>'
      + '</tr></thead><tbody>';

    tokens.forEach(function (t) {
      var isOwner = t.role === 'owner';
      html += '<tr><td><strong>' + escHtml(t.id) + '</strong></td>'
        + '<td><span class="role-badge role-' + t.role + '">' + t.role + '</span></td>'
        + '<td><span class="token-preview">' + escHtml(t.tokenPreview) + '</span></td>'
        + '<td><div class="action-btns">'
        + '<button class="icon-btn" onclick="rotateToken(\'' + escAttr(t.id) + '\')">Rotate</button>';
      if (!isOwner) {
        html += '<button class="icon-btn danger" onclick="deleteToken(\'' + escAttr(t.id) + '\')">Delete</button>';
      }
      html += '</div></td></tr>';
    });

    html += '</tbody></table>';
    html += '<p style="font-size:11px;color:var(--text-muted);margin-top:8px;">To change a token\'s role, delete it and create a new one with the desired role.</p>';

    pane.innerHTML = html;
  });
}

// --- Modal Helpers ---

function closeModal(id) {
  var el = document.getElementById(id);
  if (el) el.classList.add('hidden');
}

function showResultModal(title, message, value, hint) {
  document.getElementById('result-modal-title').textContent = title;
  document.getElementById('result-modal-message').textContent = message;
  document.getElementById('result-modal-value').value = value;
  document.getElementById('result-modal-hint').textContent = hint || '';
  document.getElementById('result-modal').classList.remove('hidden');
  // Auto-select for easy copy
  document.getElementById('result-modal-value').select();
}

function copyResultAndClose() {
  var input = document.getElementById('result-modal-value');
  input.select();
  document.execCommand('copy');
  closeModal('result-modal');
}

// --- Token Actions ---

function promptCreateToken() {
  document.getElementById('new-token-id').value = '';
  document.getElementById('new-token-role').value = 'user';
  var errEl = document.getElementById('create-token-error');
  errEl.classList.add('hidden');
  errEl.textContent = '';
  document.getElementById('create-token-modal').classList.remove('hidden');
  document.getElementById('new-token-id').focus();
}

function submitCreateToken(event) {
  event.preventDefault();
  var id = document.getElementById('new-token-id').value.trim();
  var role = document.getElementById('new-token-role').value;
  if (!id) return;

  goApp.AdminCreateToken(currentAdminHub, id, role).then(function (raw) {
    var data;
    try { data = JSON.parse(raw); } catch (e) { data = {}; }
    if (data.error) {
      var errEl = document.getElementById('create-token-error');
      errEl.textContent = data.error;
      errEl.classList.remove('hidden');
      return;
    }
    closeModal('create-token-modal');
    if (data.token) {
      showResultModal('Token Created', 'Copy this token now. It will not be shown again.', data.token, 'Identity: ' + id + ' | Role: ' + role);
    }
    renderHubTokens(document.getElementById('hubs-admin-detail'));
  });
}

function deleteToken(id) {
  goApp.ConfirmDialog('Delete Token', 'Delete identity "' + id + '"? This removes the token and all its ACL entries.').then(function (yes) {
    if (!yes) return;
    goApp.AdminDeleteToken(currentAdminHub, id).then(function () {
      renderHubTokens(document.getElementById('hubs-admin-detail'));
    });
  });
}

function rotateToken(id) {
  goApp.ConfirmDialog('Rotate Token', 'Rotate token for "' + id + '"? The old token will stop working immediately.').then(function (yes) {
    if (!yes) return;
    goApp.AdminRotateToken(currentAdminHub, id).then(function (raw) {
      var data;
      try { data = JSON.parse(raw); } catch (e) { data = {}; }
      if (data.error) {
        showError(data.error);
        return;
      }
      if (data.token) {
        showResultModal('Token Rotated', 'New token for "' + id + '". Copy now, it will not be shown again.', data.token);
      }
      renderHubTokens(document.getElementById('hubs-admin-detail'));
    });
  });
}

function promptPairCode() {
  document.getElementById('pair-role').value = 'user';
  document.getElementById('pair-expires').value = '1h';
  var errEl = document.getElementById('pair-code-error');
  errEl.classList.add('hidden');
  errEl.textContent = '';
  document.getElementById('pair-code-modal').classList.remove('hidden');
}

function submitPairCode(event) {
  event.preventDefault();
  var role = document.getElementById('pair-role').value;
  var expires = document.getElementById('pair-expires').value;

  goApp.AdminGeneratePairCode(currentAdminHub, 'connect', role, '', expires).then(function (raw) {
    var data;
    try { data = JSON.parse(raw); } catch (e) { data = {}; }
    if (data.error) {
      var errEl = document.getElementById('pair-code-error');
      errEl.textContent = data.error;
      errEl.classList.remove('hidden');
      return;
    }
    closeModal('pair-code-modal');
    showResultModal('Pairing Code', 'Share this code with the user. It can only be used once.', data.code, 'Expires: ' + (data.expiresAt || 'soon'));
  });
}

// --- ACLs View ---

function renderHubACLs(pane) {
  var hub = currentAdminHub;
  pane.innerHTML = '<h2>Access Control</h2>'
    + '<p class="section-desc">Manage per-machine permissions for <strong>' + escHtml(hubNameFromUrl(hub)) + '</strong></p>'
    + '<p class="loading">Loading...</p>';

  goApp.LogAdminGET(hub, '/api/admin/acls');
  goApp.AdminListACLs(hub).then(function (raw) {
    var data;
    try { data = JSON.parse(raw); } catch (e) { data = {}; }
    if (data.error) {
      pane.innerHTML = '<h2>Access Control</h2><p class="section-desc">' + escHtml(data.error) + '</p>';
      return;
    }
    var acls = data.acls || [];

    var html = '<h2>Access Control</h2>'
      + '<p class="section-desc">Manage per-machine permissions for <strong>' + escHtml(hubNameFromUrl(hub)) + '</strong></p>';

    html += '<div class="toolbar">'
      + '<button class="btn-primary btn-sm" onclick="promptGrantAccess()">Grant Access</button>'
      + '</div>';

    // Check for wildcard
    var hasWildcard = acls.some(function (a) { return a.machineId === '*'; });
    if (hasWildcard) {
      html += '<div class="wildcard-note"><strong>*</strong> Wildcard ACL is active: identities listed under <strong>*</strong> can connect to all machines.</div>';
    }

    if (acls.length === 0) {
      html += '<p class="empty-hint">No explicit ACL rules configured.</p>';
    } else {
      acls.forEach(function (acl) {
        var ruleCount = (acl.registerId ? 1 : 0) + (acl.connectIds ? acl.connectIds.length : 0);
        var metaLabel = acl.machineId === '*' ? 'applies to all machines' : ruleCount + ' rule' + (ruleCount !== 1 ? 's' : '');

        html += '<div class="acl-card"><div class="acl-card-header"><div>'
          + '<span class="acl-machine-name">' + escHtml(acl.machineId) + '</span>'
          + '<span class="acl-machine-meta">&nbsp;&nbsp;' + metaLabel + '</span>'
          + '</div></div><div class="acl-card-body">';

        // Header row
        html += '<div class="acl-perm-row" style="padding-bottom:8px;border-bottom:1px solid var(--border);">'
          + '<div style="flex:1"></div>'
          + '<div class="acl-checks">'
          + '<span style="width:70px;text-align:center;font-weight:600;font-size:12px;color:var(--text-muted);">Register</span>'
          + '<span style="width:70px;text-align:center;font-weight:600;font-size:12px;color:var(--text-muted);">Connect</span>'
          + '</div>'
          + '<div style="width:70px"></div></div>';

        // Register identity
        if (acl.registerId) {
          html += '<div class="acl-perm-row">'
            + '<div class="acl-identity">' + escHtml(acl.registerId) + '</div>'
            + '<div class="acl-checks">'
            + '<div style="width:70px;text-align:center;">&#x2705;</div>'
            + '<div style="width:70px;text-align:center;"></div>'
            + '</div>'
            + '<div style="width:70px;text-align:right;"><button class="icon-btn danger" onclick="revokeRegister(\'' + escAttr(acl.registerId) + '\',\'' + escAttr(acl.machineId) + '\')">Revoke</button></div>'
            + '</div>';
        }

        // Connect identities
        var connectIds = acl.connectIds || [];
        connectIds.forEach(function (id) {
          html += '<div class="acl-perm-row">'
            + '<div class="acl-identity">' + escHtml(id) + '</div>'
            + '<div class="acl-checks">'
            + '<div style="width:70px;text-align:center;"></div>'
            + '<div style="width:70px;text-align:center;">&#x2705;</div>'
            + '</div>'
            + '<div style="width:70px;text-align:right;"><button class="icon-btn danger" onclick="revokeConnect(\'' + escAttr(id) + '\',\'' + escAttr(acl.machineId) + '\')">Revoke</button></div>'
            + '</div>';
        });

        if (!acl.registerId && connectIds.length === 0) {
          html += '<p style="font-size:13px;color:var(--text-muted);padding:8px 0;">No explicit rules.</p>';
        }

        html += '</div></div>';
      });
    }

    html += '<p style="font-size:11px;color:var(--text-muted);margin-top:12px;">Register is single-assignment: only one identity can register a given machine.</p>';
    pane.innerHTML = html;
  });
}

function promptGrantAccess() {
  // Populate identity dropdown from token list
  var identitySelect = document.getElementById('grant-identity');
  identitySelect.innerHTML = '<option value="">Loading...</option>';

  // Populate machine dropdown from hub status
  var machineSelect = document.getElementById('grant-machine');
  machineSelect.innerHTML = '<option value="*">* (all machines)</option>';

  goApp.AdminListTokens(currentAdminHub).then(function (raw) {
    var data;
    try { data = JSON.parse(raw); } catch (e) { data = {}; }
    identitySelect.innerHTML = '<option value="">Select identity...</option>';
    var tokens = data.tokens || [];
    tokens.forEach(function (t) {
      var opt = document.createElement('option');
      opt.value = t.id;
      opt.textContent = t.id + ' (' + t.role + ')';
      identitySelect.appendChild(opt);
    });
  });

  goApp.GetHubStatus(currentAdminHub).then(function (status) {
    if (status.machines) {
      status.machines.forEach(function (m) {
        var opt = document.createElement('option');
        opt.value = m.id;
        opt.textContent = m.id;
        machineSelect.appendChild(opt);
      });
    }
  });

  document.getElementById('grant-type').value = 'connect';
  var errEl = document.getElementById('grant-access-error');
  errEl.classList.add('hidden');
  errEl.textContent = '';
  document.getElementById('grant-access-modal').classList.remove('hidden');
}

function submitGrantAccess(event) {
  event.preventDefault();
  var id = document.getElementById('grant-identity').value;
  var machine = document.getElementById('grant-machine').value;
  var type = document.getElementById('grant-type').value;
  if (!id) return;

  var call = type === 'register'
    ? goApp.AdminGrantRegister(currentAdminHub, id, machine)
    : goApp.AdminGrantConnect(currentAdminHub, id, machine);

  call.then(function (raw) {
    var data;
    try { data = JSON.parse(raw); } catch (e) { data = {}; }
    if (data.error) {
      var errEl = document.getElementById('grant-access-error');
      errEl.textContent = data.error;
      errEl.classList.remove('hidden');
      return;
    }
    closeModal('grant-access-modal');
    renderHubACLs(document.getElementById('hubs-admin-detail'));
  });
}

function revokeConnect(id, machineId) {
  goApp.ConfirmDialog('Revoke Access', 'Revoke connect access for "' + id + '" on "' + machineId + '"?').then(function (yes) {
    if (!yes) return;
    goApp.AdminRevokeConnect(currentAdminHub, id, machineId).then(function () {
      renderHubACLs(document.getElementById('hubs-admin-detail'));
    });
  });
}

function revokeRegister(id, machineId) {
  goApp.ConfirmDialog('Revoke Access', 'Revoke register access for "' + id + '" on "' + machineId + '"?').then(function (yes) {
    if (!yes) return;
    goApp.AdminRevokeRegister(currentAdminHub, id, machineId).then(function () {
      renderHubACLs(document.getElementById('hubs-admin-detail'));
    });
  });
}

// --- Hub Management ---

function removeHub(url) {
  goApp.ConfirmDialog('Remove Hub', 'Remove this hub and all its saved selections?').then(function (yes) {
    if (!yes) return;
    Object.keys(selectedServices).forEach(function (key) {
      if (key.indexOf(url + '||') === 0) delete selectedServices[key];
    });
    delete includedHubs[url];
    goApp.RemoveHub(url).then(function () {
      selectedHubURL = null;
      selectedMachineId = null;
      currentAdminHub = '';
      checkDirty();
      refreshAll();
      refreshHubsTab();
      document.getElementById('detail-pane').innerHTML = '<div class="empty-state"><p>Hub removed.</p></div>';
    });
  });
}

function hubNameFromUrl(url) {
  return (url || '').replace(/^wss?:\/\//, '').replace(/^https?:\/\//, '').replace(/\/$/, '');
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

var logPanelFrozen = false;

function freezeLogPanel() {
  logPanelFrozen = true;
}

function unfreezeLogPanel() {
  logPanelFrozen = false;
}

function copyTerminal() {
  var text = document.getElementById('log-tela').textContent;
  if (navigator.clipboard) navigator.clipboard.writeText(text);
}

function saveTerminal() {
  var text = document.getElementById('log-tela').textContent;
  goApp.SaveTerminalOutput(text);
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
  goApp.GetConnectionState().then(function (state) {
    var dot = document.getElementById('log-tela-dot');
    if (dot) {
      dot.className = state.connected ? 'log-dot log-dot-live' : 'log-dot log-dot-idle';
    }
    if (!state.connected) {
      var el = document.getElementById('log-tela');
      if (el && el.textContent !== 'Not connected.' && !el.textContent.trim()) {
        el.textContent = 'Not connected.';
      }
    }
  });
}

// Set up scroll freeze and auto-copy on all log panel outputs
(function () {
  setTimeout(function () {
    var outputs = document.querySelectorAll('.log-panel-output, .cmd-list');
    outputs.forEach(function (el) {
      el.addEventListener('mousedown', function () {
        freezeLogPanel();
      });

      el.addEventListener('mouseup', function () {
        var sel = window.getSelection();
        if (sel && sel.toString().length > 0) {
          navigator.clipboard.writeText(sel.toString()).catch(function () {});
          // Keep frozen briefly so the selection remains visible
          setTimeout(unfreezeLogPanel, 1500);
        } else {
          unfreezeLogPanel();
        }
      });
    });
  }, 200);
})();


// --- Settings ---

function refreshSettings() {
  goApp.GetSettings().then(function (s) {
    document.getElementById('setting-autoConnect').checked = s.autoConnect;
    document.getElementById('setting-reconnectOnDrop').checked = s.reconnectOnDrop;
    document.getElementById('setting-confirmDisconnect').checked = s.confirmDisconnect;
    document.getElementById('setting-minimizeOnClose').checked = s.minimizeOnClose;
    document.getElementById('setting-autoCheckUpdates').checked = s.autoCheckUpdates;
    document.getElementById('setting-verboseDefault').checked = s.verboseDefault;

    // Populate binary path (show default if empty)
    var binPathInput = document.getElementById('setting-binPath');
    if (binPathInput) {
      if (s.binPath) {
        binPathInput.value = s.binPath;
      } else {
        goApp.GetDefaultBinPath().then(function (p) {
          binPathInput.value = p;
          binPathInput.setAttribute('data-default', p);
        });
      }
    }

    // Populate default profile dropdown
    var sel = document.getElementById('setting-defaultProfile');
    if (sel) {
      goApp.ListProfiles().then(function (profiles) {
        sel.innerHTML = '';
        (profiles || []).forEach(function (p) {
          var opt = document.createElement('option');
          opt.value = p;
          opt.textContent = p;
          if (p === (s.defaultProfile || '')) opt.selected = true;
          sel.appendChild(opt);
        });
      });
    }
  });
}

function gatherSettings() {
  return {
    autoConnect: document.getElementById('setting-autoConnect').checked,
    reconnectOnDrop: document.getElementById('setting-reconnectOnDrop').checked,
    confirmDisconnect: document.getElementById('setting-confirmDisconnect').checked,
    minimizeTo: 'tray',
    startMinimized: false,
    minimizeOnClose: document.getElementById('setting-minimizeOnClose').checked,
    autoCheckUpdates: document.getElementById('setting-autoCheckUpdates').checked,
    verboseDefault: document.getElementById('setting-verboseDefault').checked,
    defaultProfile: document.getElementById('setting-defaultProfile') ? document.getElementById('setting-defaultProfile').value : '',
    binPath: getBinPathValue()
  };
}

function saveSettingsWithValidation() {
  var s = gatherSettings();
  var errEl = document.getElementById('settings-error');
  var pathInput = document.getElementById('setting-binPath');
  errEl.classList.add('hidden');
  errEl.textContent = '';
  if (pathInput) pathInput.classList.remove('invalid');

  // Validate bin path
  goApp.ValidateBinPath(s.binPath).then(function (warning) {
    if (warning && warning.indexOf('does not exist') === -1) {
      // Hard errors (not a directory, cannot access)
      errEl.textContent = warning;
      errEl.classList.remove('hidden');
      if (pathInput) pathInput.classList.add('invalid');
      return;
    }
    // Valid (or just a "will be created" warning, which is OK)
    goApp.SaveSettings(JSON.stringify(s)).then(function () {
      settingsDirty = false;
      var btn = document.getElementById('settings-save-btn');
      if (btn) btn.disabled = true;
      if (pathInput) pathInput.classList.remove('invalid');
      // Clear update warning and re-check with new settings
      document.getElementById('update-btn').disabled = true; document.getElementById('update-btn').title = 'No updates';
      updateDismissedForSession = false;
      updateSkippedVersion = '';
      checkForUpdate();
      refreshVersionDisplay();
    });
  });
}

var settingsDirty = false;

function markSettingsDirty() {
  settingsDirty = true;
  var btn = document.getElementById('settings-save-btn');
  if (btn) btn.disabled = false;
}

function closeSettings() {
  if (settingsDirty) {
    goApp.ConfirmDialog('Unsaved Changes', 'You have unsaved settings changes. Discard them?').then(function (yes) {
      if (!yes) return;
      discardAndClose();
    });
  } else {
    toggleSettingsOverlay();
  }
}

function discardAndClose() {
  refreshSettings();
  settingsDirty = false;
  var btn = document.getElementById('settings-save-btn');
  if (btn) btn.disabled = true;
  var errEl = document.getElementById('settings-error');
  if (errEl) { errEl.classList.add('hidden'); errEl.textContent = ''; }
  var pathInput = document.getElementById('setting-binPath');
  if (pathInput) pathInput.classList.remove('invalid');
  toggleSettingsOverlay();
}

// Keep saveSetting for backward compat (browse/restore call it)
function saveSetting() {
  saveSettingsWithValidation();
}

function getBinPathValue() {
  var input = document.getElementById('setting-binPath');
  if (!input) return '';
  var val = input.value.trim();
  var def = input.getAttribute('data-default') || '';
  // If the value equals the default, save as empty (use default)
  return val === def ? '' : val;
}

function refreshBinStatus() {
  var container = document.getElementById('bin-status');
  if (!container) return;

  container.innerHTML = '<span style="font-size:11px;color:var(--text-muted);">Checking...</span>';

  goApp.GetBinStatus().then(function (bins) {
    // Get path validation warning
    var pathInput = document.getElementById('setting-binPath');
    var pathVal = pathInput ? pathInput.value.trim() : '';
    var defVal = pathInput ? (pathInput.getAttribute('data-default') || '') : '';
    var checkPath = (pathVal === defVal) ? '' : pathVal;

    goApp.ValidateBinPath(checkPath).then(function (warning) {
      var warningHtml = warning ? '<div style="font-size:11px;color:#f39c12;margin-bottom:4px;">' + escHtml(warning) + '</div>' : '';
      renderBinStatus(container, bins, warningHtml);
    });
  });
}

function renderBinStatus(container, bins, warningHtml) {
    if (!bins || bins.length === 0) {
      container.innerHTML = warningHtml;
      return;
    }
    var html = warningHtml;
    bins.forEach(function (b) {
      var dotClass = 'missing';
      var verText = 'not found';
      var action = '';

      if (b.found && b.upToDate) {
        dotClass = 'found';
        verText = b.version;
      } else if (b.found && !b.upToDate) {
        dotClass = 'outdated';
        verText = b.version + ' (latest: ' + (b.latest || '?') + ')';
        action = '<button class="bin-status-action" onclick="installBinary(\'' + escAttr(b.name) + '\')">Update</button>';
      } else {
        dotClass = 'missing';
        verText = 'not found';
        action = '<button class="bin-status-action" onclick="installBinary(\'' + escAttr(b.name) + '\')">Install</button>';
      }

      html += '<div class="bin-status-item">'
        + '<span class="bin-status-dot ' + dotClass + '"></span>'
        + '<span class="bin-status-name">' + escHtml(b.name) + '</span>'
        + '<span class="bin-status-ver">' + escHtml(verText) + '</span>'
        + action
        + '<button type="button" class="bin-refresh-btn" onclick="refreshSingleBinary(\'' + escAttr(b.name) + '\', this)" title="Check for updates">&#x21BB;</button>'
        + '</div>';
    });
    container.innerHTML = html;
}

function installBinary(name) {
  var container = document.getElementById('bin-status');
  goApp.InstallBinary(name).then(function () {
    tvLog('Installed ' + name);
    refreshBinStatus();
    refreshVersionDisplay();
    checkForUpdate();
  }).catch(function (err) {
    tvLog('Failed to install ' + name + ': ' + err);
  });
}

function browseBinPath() {
  goApp.BrowseBinPath().then(function (dir) {
    if (dir) {
      document.getElementById('setting-binPath').value = dir;
      saveSetting();
      refreshBinStatus();
    }
  });
}

function restoreDefaultBinPath() {
  goApp.GetDefaultBinPath().then(function (p) {
    var input = document.getElementById('setting-binPath');
    input.value = p;
    input.setAttribute('data-default', p);
    saveSetting();
    refreshBinStatus();
  });
}

function clearCredentialStore() {
  showConfirmDialog('Clear Credentials', 'This will delete all stored hub tokens. You will need to re-authenticate with each hub.', 'Delete All').then(function (yes) {
    if (!yes) return;
    goApp.ClearCredentialStore().then(function () {
      refreshAll();
      refreshHubsTab();
    }).catch(function (err) {
      showError('Failed to clear credential store: ' + err);
    });
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
    if (el) el.textContent = 'telavisor: ' + (tv.gui || 'dev') + '  |  tela: ' + (tv.cli || 'not installed');
  });
  goApp.GetCLIPath().then(function (path) {
    var el = document.getElementById('settings-cli-path');
    if (el) el.textContent = path;
  });
}

// --- Command Log ---

function refreshLog() {
  goApp.GetCommandLog().then(function (entries) {
    var list = document.getElementById('cmd-list');
    if (!list || !entries) return;
    list.innerHTML = '';
    entries.forEach(function (entry) {
      var method = 'CLI';
      var desc = entry.command;
      if (entry.description.indexOf('GET ') === 0) { method = 'GET'; desc = entry.command; }
      else if (entry.description.indexOf('POST ') === 0) { method = 'POST'; desc = entry.command; }
      else if (entry.description.indexOf('DELETE ') === 0) { method = 'DELETE'; desc = entry.command; }
      addCommandEntry(method, entry.description + '  ' + desc.substring(0, 80), entry.command);
    });
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
  var text = String(msg).replace(/^Error:\s*/i, '');
  var el = document.getElementById('error-toast');
  if (el) {
    el.textContent = text;
    el.classList.remove('hidden');
    setTimeout(function () { el.classList.add('hidden'); }, 5000);
  }
}

// ── Themed dialog system (replaces alert/confirm/prompt) ──

var _dialogResolve = null;
var _dialogMode = ''; // 'confirm' or 'prompt'

function showConfirmDialog(title, message, okLabel) {
  return new Promise(function (resolve) {
    _dialogResolve = resolve;
    _dialogMode = 'confirm';
    document.getElementById('generic-dialog-title').textContent = title;
    document.getElementById('generic-dialog-message').textContent = message;
    document.getElementById('generic-dialog-input').classList.add('hidden');
    var okBtn = document.getElementById('generic-dialog-ok');
    okBtn.textContent = okLabel || 'OK';
    okBtn.className = (okLabel && okLabel.toLowerCase().indexOf('delete') >= 0) ? 'btn-danger' : 'btn-primary';
    document.getElementById('generic-dialog-overlay').classList.remove('hidden');
  });
}

function showPromptDialog(title, message, defaultValue, okLabel) {
  return new Promise(function (resolve) {
    _dialogResolve = resolve;
    _dialogMode = 'prompt';
    document.getElementById('generic-dialog-title').textContent = title;
    document.getElementById('generic-dialog-message').textContent = message || '';
    if (!message) document.getElementById('generic-dialog-message').style.display = 'none';
    else document.getElementById('generic-dialog-message').style.display = '';
    var input = document.getElementById('generic-dialog-input');
    input.classList.remove('hidden');
    input.value = defaultValue || '';
    input.placeholder = '';
    var okBtn = document.getElementById('generic-dialog-ok');
    okBtn.textContent = okLabel || 'OK';
    okBtn.className = 'btn-primary';
    document.getElementById('generic-dialog-overlay').classList.remove('hidden');
    setTimeout(function () { input.focus(); input.select(); }, 50);
  });
}

function genericDialogOk() {
  document.getElementById('generic-dialog-overlay').classList.add('hidden');
  if (_dialogResolve) {
    if (_dialogMode === 'prompt') {
      _dialogResolve(document.getElementById('generic-dialog-input').value);
    } else {
      _dialogResolve(true);
    }
    _dialogResolve = null;
  }
}

function genericDialogCancel() {
  document.getElementById('generic-dialog-overlay').classList.add('hidden');
  if (_dialogResolve) {
    if (_dialogMode === 'prompt') {
      _dialogResolve(null);
    } else {
      _dialogResolve(false);
    }
    _dialogResolve = null;
  }
}

// Allow Enter to submit and Escape to cancel
document.addEventListener('keydown', function (e) {
  var overlay = document.getElementById('generic-dialog-overlay');
  if (!overlay || overlay.classList.contains('hidden')) return;
  if (e.key === 'Enter') { e.preventDefault(); genericDialogOk(); }
  if (e.key === 'Escape') { e.preventDefault(); genericDialogCancel(); }
});

function toWSURL(url) {
  if (url.indexOf('https://') === 0) return 'wss://' + url.substring(8);
  if (url.indexOf('http://') === 0) return 'ws://' + url.substring(7);
  if (url.indexOf('wss://') !== 0 && url.indexOf('ws://') !== 0) return 'wss://' + url;
  return url;
}
