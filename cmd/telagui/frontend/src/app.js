'use strict';

var goApp = window.go && window.go.main && window.go.main.App;
var hubStatusCache = {};
// selectedServices: { "hubURL||machineId||serviceName": { hub, machine, service, servicePort, localPort } }
var selectedServices = {};
var pollIntervalId = null;
var selectedHubURL = null;
var selectedMachineId = null;
var currentDetailView = 'profile'; // 'profile', 'machine', 'hub', 'preview'
var activeTunnels = {}; // "machine:localPort" -> connection count
var verboseMode = false;
var savedFingerprint = ''; // fingerprint of selections at last save/load
var savedServicesJSON = '{}'; // full selectedServices JSON for Undo restore
var savedIncludedHubsJSON = '{}'; // full includedHubs JSON for Undo restore
var profileDirty = false;

// --- Modes & Tabs ---

var currentMode = 'clients';
var tabBars = { clients: 'tabbar-clients', infra: 'tabbar-infra' };

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
  if (name === 'remotes') refreshRemotesList();
  if (name === 'credentials') refreshCredentialsList();
  if (name === 'client-settings') refreshClientSettings();
}

// --- Log Panel ---

function switchLogTab(btn, id) {
  document.querySelectorAll('.log-panel-tab').forEach(function (t) { t.classList.remove('active'); });
  btn.classList.add('active');
  document.querySelectorAll('.log-panel-output').forEach(function (o) { o.classList.add('hidden'); });
  var el = document.getElementById(id);
  if (el) {
    el.classList.remove('hidden');
    // Scroll to bottom on tab switch unless user has explicitly scrolled up
    if (!logUserScrolledUp[id]) {
      el.scrollTop = el.scrollHeight;
    }
  }
}

function toggleLogPanel() {
  var panel = document.getElementById('log-panel');
  panel.classList.toggle('collapsed');
  var collapsed = panel.classList.contains('collapsed');
  var toggle = panel.querySelector('.log-panel-toggle');
  toggle.innerHTML = collapsed ? '&#x25B2;' : '&#x25BC;';
  goApp.GetSettings().then(function (s) {
    s.logPanelCollapsed = collapsed;
    goApp.SaveSettings(JSON.stringify(s));
  });
}

// Track whether user has scrolled up in each pane (keyed by element ID).
// When true, auto-scroll is suppressed so the user can read history.
var logUserScrolledUp = {};
var logMaxLines = 5000; // default; overridden from settings on load

// Detect user scroll: if scrolled to bottom (within 5px), auto-scroll is active.
// If scrolled up, suppress auto-scroll until user scrolls back to bottom.
function initLogScrollTracking(id) {
  var el = document.getElementById(id);
  if (!el) return;
  logUserScrolledUp[id] = false;
  el.addEventListener('scroll', function () {
    var atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 5;
    logUserScrolledUp[id] = !atBottom;
  });
}

function logAutoScroll(el) {
  if (!logUserScrolledUp[el.id]) {
    el.scrollTop = el.scrollHeight;
  }
}

// Trim a <pre> element to at most maxLines text nodes.
function trimLogPre(el, maxLines) {
  if (maxLines <= 0) return;
  while (el.childNodes.length > maxLines) {
    el.removeChild(el.firstChild);
  }
}

// Trim a text-content-based element by newline count.
function trimLogText(el, maxLines) {
  if (maxLines <= 0) return;
  var text = el.textContent;
  var lines = text.split('\n');
  if (lines.length > maxLines + 1) {
    el.textContent = lines.slice(lines.length - maxLines - 1).join('\n');
  }
}

function tvLog(msg) {
  var el = document.getElementById('log-tv');
  if (!el) return;
  var now = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');
  var line = document.createTextNode(now + ' ' + msg + '\n');
  el.appendChild(line);
  trimLogPre(el, logMaxLines);
  logAutoScroll(el);
}

function appendTelaLog(text) {
  var el = document.getElementById('log-tela');
  if (!el) return;
  el.textContent += text;
  trimLogText(el, logMaxLines);
  logAutoScroll(el);
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
  if (!active) return;
  var entries = active.querySelector('.cmd-list');
  var text = (entries || active).textContent;
  goApp.SaveTerminalOutput(text).then(function (path) {
    if (path) tvLog('Saved log to ' + path);
  }).catch(function (err) {
    showError('Save failed: ' + err);
  });
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
  var existing = document.getElementById('attach-log-popover');
  if (existing) { existing.remove(); return; }

  var pop = document.createElement('div');
  pop.id = 'attach-log-popover';
  pop.className = 'attach-log-popover';
  pop.innerHTML = '<div class="attach-log-loading">Loading sources...</div>';

  // Anchor to the header (not inside the scrollable tabs container).
  var addBtn = document.querySelector('.log-panel-tab-add');
  var rect = addBtn.getBoundingClientRect();
  pop.style.position = 'fixed';
  pop.style.left = rect.left + 'px';
  pop.style.bottom = (window.innerHeight - rect.top) + 'px';
  document.body.appendChild(pop);

  // Close on click outside
  function closePopover(e) {
    if (!pop.contains(e.target) && e.target !== addBtn) {
      pop.remove();
      document.removeEventListener('mousedown', closePopover);
    }
  }
  setTimeout(function () { document.addEventListener('mousedown', closePopover); }, 0);

  goApp.GetAgentList().then(function (agents) {
    agents = agents || [];
    // Extract unique hubs
    var hubSet = {};
    agents.forEach(function (a) { if (a.hub) hubSet[a.hub] = true; });
    var hubs = Object.keys(hubSet);

    var html = '';
    if (hubs.length > 0) {
      html += '<div class="attach-log-section">Hubs</div>';
      hubs.forEach(function (h) {
        html += '<button class="attach-log-item" onclick="hubViewLogs(\'' + escAttr(h) + '\');this.closest(\'.attach-log-popover\').remove()">'
          + '<span class="log-dot log-dot-live"></span>' + escHtml(h) + '</button>';
      });
    }
    if (agents.length > 0) {
      html += '<div class="attach-log-section">Agents</div>';
      agents.forEach(function (a) {
        var dotClass = a.online ? 'log-dot-live' : 'log-dot-idle';
        html += '<button class="attach-log-item" onclick="agentsViewLogs(\'' + escAttr(a.id) + '\',\'' + escAttr(a.hub) + '\');this.closest(\'.attach-log-popover\').remove()">'
          + '<span class="log-dot ' + dotClass + '"></span>' + escHtml(a.id) + '</button>';
      });
    }
    if (!html) html = '<div class="attach-log-empty">No log sources available.</div>';
    pop.innerHTML = html;
  }).catch(function () {
    pop.innerHTML = '<div class="attach-log-empty">Failed to load sources.</div>';
  });
}

function hubViewLogs(hubName) {
  var paneId = 'log-hub-' + hubName.replace(/[^a-zA-Z0-9]/g, '-');
  var existing = document.getElementById(paneId);

  if (!existing) {
    var tabBar = document.querySelector('.log-panel-tabs');
    var addBtn = tabBar.querySelector('.log-panel-tab-add');
    var tab = document.createElement('button');
    tab.className = 'log-panel-tab';
    tab.onclick = function () { switchLogTab(tab, paneId); };
    tab.innerHTML = '<span class="log-dot log-dot-idle" id="' + paneId + '-dot"></span>'
      + escHtml(hubName)
      + '<span class="log-tab-close" onclick="event.stopPropagation();removeAgentLogTab(\'' + escAttr(paneId) + '\',this.parentNode)" title="Close">&times;</span>';
    tabBar.insertBefore(tab, addBtn);

    var pre = document.createElement('pre');
    pre.className = 'log-panel-output hidden';
    pre.id = paneId;
    pre.setAttribute('data-hub', hubName);
    pre.textContent = 'Loading logs for ' + hubName + '...\n';
    document.getElementById('log-panel').appendChild(pre);
    initLogScrollTracking(paneId);
    saveLogTabs();
  }

  var tabBtn = document.querySelector('.log-panel-tabs').querySelector('[onclick*="' + paneId + '"]')
    || document.querySelector('.log-panel-tab:last-of-type');
  if (tabBtn) switchLogTab(tabBtn, paneId);

  var panel = document.getElementById('log-panel');
  if (panel.classList.contains('collapsed')) toggleLogPanel();

  var wsHub = toWSURL(hubName);
  var dot = document.getElementById(paneId + '-dot');
  if (dot) dot.className = 'log-dot log-dot-warn';

  goApp.GetHubLogs(wsHub, 500).then(function (resp) {
    try { var data = JSON.parse(resp); } catch (e) {
      document.getElementById(paneId).textContent = 'Error: invalid response\n';
      if (dot) dot.className = 'log-dot log-dot-idle';
      return;
    }
    if (data.error) {
      document.getElementById(paneId).textContent = 'Error: ' + data.error + '\n';
      if (dot) dot.className = 'log-dot log-dot-idle';
      return;
    }
    var lines = data.lines || [];
    var el = document.getElementById(paneId);
    el.textContent = lines.join('\n') + '\n';
    logAutoScroll(el);
    if (dot) dot.className = 'log-dot log-dot-live';
  }).catch(function (err) {
    document.getElementById(paneId).textContent = 'Failed: ' + err + '\n';
    if (dot) dot.className = 'log-dot log-dot-idle';
  });
}

function hubUpdateStatus(msg) {
  var el = document.getElementById('hub-update-status');
  if (el) el.textContent = msg;
}

// ── Release channels ──────────────────────────────────────────────

// renderChannelSelect emits the HTML for a channel dropdown with the
// given id. selected is the channel name to pre-select ('' leaves it on
// dev so the UI is never blank before load completes).
function renderChannelSelect(id, selected) {
  var channels = ['dev', 'beta', 'stable'];
  var sel = selected || 'dev';
  var html = '<select id="' + escAttr(id) + '" class="tb-select">';
  for (var i = 0; i < channels.length; i++) {
    var c = channels[i];
    html += '<option value="' + c + '"' + (c === sel ? ' selected' : '') + '>' + c + '</option>';
  }
  html += '</select>';
  return html;
}

// formatChannelStatus builds the trailing status string next to a
// channel dropdown. info is the parsed response shape shared by hub
// GET /api/admin/update and agent update-status.
function formatChannelStatus(info) {
  if (!info) return '';
  if (info.error) return 'error: ' + info.error;
  var cur = info.currentVersion || '?';
  var latest = info.latestVersion || '';
  if (!latest) return 'currently ' + cur;
  if (info.updateAvailable) return 'currently ' + cur + ' (latest ' + latest + ')';
  return cur + ' (latest)';
}

// preChannelMarker is the placeholder we leave on the channel row when a
// hub or agent is on a pre-channel build (no GET /api/admin/update). The
// caller can use the same marker to disable the Software button so the
// two controls never disagree.
var preChannelMarker = 'pre-channel build (update first via legacy path)';

// applySoftwareButton updates a Software card button so its label, title,
// disabled state, and trailing status text reflect the channel-aware
// info that loadHubChannel / loadAgentChannel just fetched. Pass the
// info object as returned by GetHubChannelInfo / GetAgentChannelInfo,
// or null if the call failed.
function applySoftwareButton(btnId, statusId, info, fallbackVer) {
  var btn = document.getElementById(btnId);
  var statusEl = document.getElementById(statusId);
  if (!btn || !statusEl) return;
  if (!info) {
    btn.textContent = 'Update';
    btn.disabled = true;
    statusEl.textContent = '';
    return;
  }
  if (info.error === 'method not allowed') {
    btn.textContent = 'Update';
    btn.disabled = true;
    statusEl.textContent = preChannelMarker;
    return;
  }
  var cur = info.currentVersion || fallbackVer || '';
  var latest = info.latestVersion || '';
  if (!latest) {
    btn.textContent = 'Update';
    btn.disabled = true;
    statusEl.textContent = cur ? 'currently ' + cur : '';
    return;
  }
  if (info.updateAvailable) {
    btn.textContent = 'Update to ' + latest;
    btn.disabled = false;
    statusEl.textContent = 'currently running ' + cur;
  } else {
    btn.textContent = 'Up to date';
    btn.disabled = true;
    statusEl.textContent = cur + ' (latest)';
  }
}

function loadHubChannel(hubURL) {
  var sel = document.getElementById('hub-channel-select');
  var status = document.getElementById('hub-channel-status');
  if (!sel || !status) return;
  goApp.GetHubChannelInfo(hubURL).then(function (raw) {
    var info = {};
    try { info = JSON.parse(raw); } catch (e) { }
    if (info.error === 'method not allowed') {
      // Hub predates the channel system. Hide the dropdown row and
      // leave a clear hint. The Software button will fall through to
      // the legacy update path; applySoftwareButton handles labeling.
      var row = sel.closest('.settings-row') || sel.closest('tr');
      if (row) row.style.display = 'none';
      applySoftwareButton('hub-update-btn', 'hub-update-status', info);
      return;
    }
    if (info.channel) sel.value = info.channel;
    status.textContent = formatChannelStatus(info);
    applySoftwareButton('hub-update-btn', 'hub-update-status', info);
    sel.onchange = function () {
      var newCh = sel.value;
      showConfirmDialog('Switch Channel', 'Switch this hub to the ' + newCh + ' channel? New updates will follow the ' + newCh + ' release line.', 'Switch').then(function (yes) {
        if (!yes) { sel.value = info.channel || 'dev'; return; }
        status.textContent = 'switching to ' + newCh + '...';
        goApp.SetHubChannel(hubURL, newCh).then(function (r) {
          var res = {};
          try { res = JSON.parse(r); } catch (e) { }
          if (res.error) { status.textContent = 'error: ' + res.error; return; }
          loadHubChannel(hubURL);
        });
      });
    };
  }).catch(function (err) {
    status.textContent = 'unavailable';
    applySoftwareButton('hub-update-btn', 'hub-update-status', null);
  });
}

function loadAgentChannel(hubURL, machineID) {
  var sel = document.getElementById('agent-channel-select');
  var status = document.getElementById('agent-channel-status');
  if (!sel || !status) return;
  goApp.GetAgentChannelInfo(hubURL, machineID).then(function (raw) {
    var info = {};
    try { info = JSON.parse(raw); } catch (e) { }
    // Agent mgmt responses arrive wrapped in {ok, payload:{...}, message?}.
    // A pre-channel agent rejects the unknown action with ok=false and a
    // 'unknown action' message; treat that the same as a 405 for the UI.
    if (info && info.payload && typeof info.payload === 'object') info = info.payload;
    if (info && info.ok === false && /unknown action/i.test(info.message || '')) {
      info = { error: 'method not allowed' };
    }
    if (info.error === 'method not allowed') {
      var row = sel.closest('tr') || sel.closest('.settings-row');
      if (row) row.style.display = 'none';
      applySoftwareButton('agent-update-btn', 'agent-update-status', info);
      return;
    }
    if (info.channel) sel.value = info.channel;
    status.textContent = formatChannelStatus(info);
    applySoftwareButton('agent-update-btn', 'agent-update-status', info);
    sel.onchange = function () {
      var newCh = sel.value;
      showConfirmDialog('Switch Agent Channel', 'Switch ' + machineID + ' to the ' + newCh + ' channel?', 'Switch').then(function (yes) {
        if (!yes) { sel.value = info.channel || 'dev'; return; }
        status.textContent = 'switching to ' + newCh + '...';
        goApp.SetAgentChannel(hubURL, machineID, newCh).then(function (r) {
          var res = {};
          try { res = JSON.parse(r); } catch (e) { }
          if (res.error) { status.textContent = 'error: ' + res.error; return; }
          loadAgentChannel(hubURL, machineID);
        });
      });
    };
  }).catch(function () {
    status.textContent = 'unavailable';
    applySoftwareButton('agent-update-btn', 'agent-update-status', null);
  });
}

function loadClientChannel() {
  var sel = document.getElementById('client-channel-select');
  var status = document.getElementById('client-channel-status');
  if (!sel || !status) return;
  goApp.GetClientChannel().then(function (info) {
    if (info && info.channel) sel.value = info.channel;
    status.textContent = info && info.manifestUrl ? info.manifestUrl : '';
    sel.onchange = function () {
      var newCh = sel.value;
      goApp.SetClientChannel(newCh).then(function (r) {
        var res = {};
        try { res = JSON.parse(r); } catch (e) { }
        if (res.error) { status.textContent = 'error: ' + res.error; return; }
        status.textContent = res.manifestUrl || '';
      });
    };
  });
}

function hubUpdate(hubURL, hubName) {
  showConfirmDialog('Update Hub', 'Download and install the latest telahubd on ' + hubName + '? The hub will restart after updating.', 'Update').then(function (yes) {
    if (!yes) return;
    var btn = document.getElementById('hub-update-btn');
    if (btn) btn.disabled = true;
    hubUpdateStatus('Sending update request...');
    tvLog('Updating telahubd on ' + hubName + '...');

    goApp.UpdateHub(hubURL, '').then(function (resp) {
      try { var data = JSON.parse(resp); } catch (e) {}
      if (data && data.error) {
        hubUpdateStatus('');
        if (btn) btn.disabled = false;
        showError('Update failed: ' + data.error);
        return;
      }
      var msg = (data && data.message) || '';
      tvLog('Update: ' + (msg || 'requested for ' + hubName));

      if (msg.indexOf('already running') === 0) {
        hubUpdateStatus(msg);
        if (btn) btn.disabled = false;
        return;
      }

      // Extract target version from "updating to v0.4.0".
      var targetVer = '';
      var match = msg.match(/updating to (\S+)/);
      if (match) targetVer = match[1];

      // Hub is downloading and will restart. Poll until it comes back.
      hubUpdateStatus('Hub is downloading update and restarting...');
      pollHubOnline(hubURL, hubName, targetVer, 0);
    }).catch(function (err) {
      hubUpdateStatus('');
      if (btn) btn.disabled = false;
      showError('Update failed: ' + err);
    });
  });
}

function pollHubOnline(hubURL, hubName, targetVer, attempt) {
  if (attempt > 30) {
    hubUpdateStatus('Hub did not come back online.');
    tvLog(hubName + ': hub did not come back after update');
    var btn = document.getElementById('hub-update-btn');
    if (btn) btn.disabled = false;
    return;
  }
  setTimeout(function () {
    goApp.GetHubInfo(hubURL).then(function (raw) {
      try { var info = JSON.parse(raw); } catch (e) {}
      var ver = (info && info.hub && info.hub.version) || '';
      // If we know the target version, wait for it specifically.
      // Otherwise, accept any version response as success.
      if (ver && (!targetVer || ver === targetVer)) {
        hubUpdateStatus('Updated to ' + ver);
        tvLog(hubName + ': updated to ' + ver);
        var btn = document.getElementById('hub-update-btn');
        if (btn) btn.disabled = false;
        // Re-render hub settings with fresh data.
        var settingsPane = document.getElementById('hubs-admin-detail');
        if (settingsPane && currentAdminView === 'hub-settings') renderHubSettings(settingsPane);
      } else {
        hubUpdateStatus('Waiting for hub to restart... (' + (attempt + 1) + ')');
        pollHubOnline(hubURL, hubName, targetVer, attempt + 1);
      }
    }).catch(function () {
      hubUpdateStatus('Waiting for hub to restart... (' + (attempt + 1) + ')');
      pollHubOnline(hubURL, hubName, targetVer, attempt + 1);
    });
  }, 2000);
}

function hubRestart(hubURL, hubName) {
  showConfirmDialog('Restart Hub', 'Restart telahubd on ' + hubName + '? All active sessions will be interrupted.', 'Restart').then(function (yes) {
    if (!yes) return;
    tvLog('Restarting telahubd on ' + hubName + '...');
    goApp.RestartHub(hubURL).then(function (resp) {
      try { var data = JSON.parse(resp); } catch (e) {}
      if (data && data.error) { showError('Restart failed: ' + data.error); return; }
      tvLog('Restart: ' + (data && data.message ? data.message : 'requested for ' + hubName));
    }).catch(function (err) { showError('Restart failed: ' + err); });
  });
}

// --- Command Log in Panel ---

var activeFilter = 'all';

function addCommandEntry(method, desc, fullCmd) {
  var list = document.getElementById('cmd-list');
  if (!list) return;
  var now = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z').substring(11, 19);
  var methodClass = 'cmd-m-' + method.toLowerCase();
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
  // Trim old entries
  if (logMaxLines > 0) {
    while (list.children.length > logMaxLines) {
      list.removeChild(list.firstChild);
    }
  }
  logAutoScroll(list);
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
    if (state.connected && state.attached) {
      badge.textContent = 'Attached (external tela)';
      badge.className = 'status-connection-state connected';
    } else if (state.connected) {
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
          var portFound = state.output && state.output.indexOf(portStr) !== -1;

          // In attached mode, we don't have stdout output.
          // Check if the service appears in the bound services list.
          if (!portFound && state.attached && boundServicesCache && boundServicesCache.length > 0) {
            for (var si = 0; si < boundServicesCache.length; si++) {
              var bs = boundServicesCache[si];
              if (parseInt(bs.local) === parseInt(svc.localPort) && bs.machine === g.machine) {
                portFound = true;
                break;
              }
            }
            // If no exact match, try matching by machine only (port may differ from profile)
            if (!portFound) {
              for (var si2 = 0; si2 < boundServicesCache.length; si2++) {
                if (boundServicesCache[si2].machine === g.machine) {
                  portFound = true;
                  break;
                }
              }
            }
          }

          if (portFound) {
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

        // Clickable link for HTTP services (gateway, http) when listening
        var svcNameLower = (svc.service || '').toLowerCase();
        var isHttpService = svcNameLower === 'gateway' || svcNameLower === 'http' || svcNameLower === 'web';
        var localDisplay = 'localhost:' + svc.localPort;
        if (isHttpService && portFound) {
          localDisplay = '<a href="http://localhost:' + svc.localPort + '" target="_blank" rel="noopener" class="status-svc-link">localhost:' + svc.localPort + '</a>';
        }

        html += '<div class="settings-row status-svc-row">'
          + '<span class="status-svc-indicator ' + indicatorClass + '"></span>'
          + '<div class="status-svc-name">' + escHtml(svc.service) + '</div>'
          + '<div class="status-svc-remote">' + (svc.servicePort ? ':' + svc.servicePort : '') + '</div>'
          + '<div class="status-svc-local ' + localClass + '">' + localDisplay + '</div>'
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
          // Service confirmed bound. Transition connecting -> connected.
          if (connPhase === 'connecting') {
            tvLog('Connected');
            setConnPhase('connected');
            onConnectionChanged();
          }
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
    setConnPhase('disconnecting');
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
    trimLogText(el, logMaxLines);
    logAutoScroll(el);
    // Update tela dot to live
    var dot = document.getElementById('log-tela-dot');
    if (dot) dot.className = 'log-dot log-dot-live';
  });

  window.runtime.EventsOn('tela:exited', function () {
    if (connPhase === 'connected' || connPhase === 'connecting') {
      tvLog('tela process exited unexpectedly');
      stopConnectionPoll();
      goApp.DisconnectControlWS();
      setConnPhase('disconnected');
      connAttached = false;
      boundServicesCache = null;
      onConnectionChanged();
    }
  });

  window.runtime.EventsOn('tela:attached', function () {
    tvLog('Attached to running tela');
    connAttached = true;
    setConnPhase('connected');

    // Update tela log tab immediately
    var logEl = document.getElementById('log-tela');
    if (logEl) logEl.textContent = 'Attached to external tela. Log output is in the terminal where tela is running.';
    var logDot = document.getElementById('log-tela-dot');
    if (logDot) logDot.className = 'log-dot log-dot-live';

    // Fetch bound services, then refresh UI
    function fetchAndRefresh() {
      goApp.GetControlServices().then(function (svcs) {
        boundServicesCache = svcs || [];
        onConnectionChanged();
      }).catch(function () {});
    }

    // Connect WebSocket for live events, then fetch services
    goApp.ConnectControlWS().then(function () {
      fetchAndRefresh();
    }).catch(function () {
      // WebSocket failed, still try fetching services via HTTP
      fetchAndRefresh();
    });

    // Also fetch immediately in case WS takes time
    fetchAndRefresh();
  });

  window.runtime.EventsOn('tela:disconnected', function () {
    tvLog('External tela disconnected');
    connAttached = false;
    boundServicesCache = null;
    setConnPhase('disconnected');
    onConnectionChanged();
    // Auto-stop mount when tela disconnects
    goApp.IsMountRunning().then(function (running) {
      if (running) {
        goApp.StopMount();
        tvLog('Mount stopped (tela disconnected)');
        refreshMountStatus();
      }
    });
  });

  window.runtime.EventsOn('app:command', function (entry) {
    if (!entry) return;
    var badge = (entry.method === 'CLI' || !entry.method) ? 'CLI' : 'API';
    addCommandEntry(badge, entry.description || entry.command, entry.command || '');
  });

  // Now that all event listeners are registered, check for running tela
  goApp.TryAttach();
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
      // Save log panel height
      goApp.GetSettings().then(function (s) {
        s.logPanelHeight = parseInt(panel.style.height) || 0;
        goApp.SaveSettings(JSON.stringify(s));
      });
    }
  });

  // Restore saved height and collapsed state
  goApp.GetSettings().then(function (s) {
    if (s.logPanelHeight > 0) {
      panel.style.height = s.logPanelHeight + 'px';
    }
    if (s.logPanelCollapsed) {
      panel.classList.add('collapsed');
      var toggle = panel.querySelector('.log-panel-toggle');
      if (toggle) toggle.innerHTML = '&#x25B2;';
    }
  });
})();

// --- Log scroll tracking init ---
initLogScrollTracking('log-tv');
initLogScrollTracking('log-tela');
initLogScrollTracking('cmd-list');

// Load log max lines from settings
goApp.GetSettings().then(function (s) {
  if (s.logMaxLines && s.logMaxLines > 0) logMaxLines = s.logMaxLines;
  restoreLogTabs(s.openLogTabs || []);
});

// --- Startup ---
tvLog('TelaVisor started');
refreshVersionDisplay();
refreshProfileList();
refreshLog();
loadSavedSelections().then(function () {
  tvLog('Profile loaded');
  refreshStatus();
  refreshAll();
  refreshProfileMTU();
  // Re-take snapshot after refreshAll to account for hub status reconciliation
  setTimeout(function () { takeSnapshot(); }, 1000);
  // Auto-connect if enabled and there are saved selections
  goApp.ShouldAutoConnect().then(function (should) {
    if (should && Object.keys(selectedServices).length > 0 && connPhase === 'disconnected') {
      setTimeout(function () {
        if (connPhase === 'disconnected') doConnect();
      }, 1500); // delay to let hub status load
    }
  });
});

// First-run connect tooltip + theme
(function () {
  goApp.GetSettings().then(function (s) {
    if (s.connectTooltipDismissed) {
      dismissConnectTooltip();
    }
    // Apply saved theme
    applyTelaTheme(s.theme || 'system');
    // Restore dotfiles preference
    if (s.hideDotfiles === false) {
      filesHideDotfiles = false;
      var cb = document.getElementById('files-hide-dotfiles');
      if (cb) cb.checked = false;
    }
  }).catch(function () {});

  // Version stamp
  goApp.GetVersion().then(function (v) {
    var el = document.getElementById('app-version');
    if (el && v) el.textContent = v;
  }).catch(function () {});
})();

function dismissConnectTooltip() {
  var tip = document.getElementById('connect-tooltip');
  if (tip) tip.classList.add('hidden');
}

// Check for updates after a short delay (versions need time to fetch)
// Fetch latest version early so all views have it.
checkForUpdate();
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
var latestVersion = '';

// Format a version with an up-to-date or outdated indicator.
// Uses a span with class="version-badge" and data-ver so we can
// refresh all badges when latestVersion arrives asynchronously.
function versionBadge(ver) {
  if (!ver) return '<span class="version-badge" data-ver="">unknown</span>';
  return '<span class="version-badge" data-ver="' + escAttr(ver) + '">' + formatVersionBadge(ver) + '</span>';
}

function formatVersionBadge(ver) {
  if (!latestVersion) {
    return escHtml(ver) + ' <span class="tools-service-label">(checking...)</span>';
  }
  if (ver === latestVersion) {
    return '<span class="tools-status-ok">' + escHtml(ver) + '</span>'
      + ' <span class="tools-service-label">(latest: ' + escHtml(latestVersion) + ')</span>';
  }
  return '<span class="tools-status-warn">' + escHtml(ver) + '</span>'
    + ' <a href="#" onclick="event.preventDefault();scrollToManagement();" class="tools-service-label" style="text-decoration:underline;cursor:pointer;">update available: ' + escHtml(latestVersion) + '</a>';
}

// Scroll to the Management card on the active settings page (hub or agent).
function scrollToManagement() {
  var el = document.getElementById('hub-management-card') || document.getElementById('agent-management-card');
  if (el) {
    el.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
}

// Refresh all version badges on screen (called when latestVersion is populated).
function refreshVersionBadges() {
  document.querySelectorAll('.version-badge').forEach(function (el) {
    var ver = el.getAttribute('data-ver');
    if (ver) el.innerHTML = formatVersionBadge(ver);
  });
  // Sidebar version labels: just color them.
  document.querySelectorAll('.version-badge-sidebar').forEach(function (el) {
    var ver = el.getAttribute('data-ver');
    if (latestVersion && ver && ver !== latestVersion) {
      el.style.color = '#f39c12';
    } else {
      el.style.color = '';
    }
  });
  // Re-render the agent detail and hub settings so Management button labels update.
  if (document.getElementById('agent-management-card')) {
    var agent = (agentsData || []).find(function (a) { return a.id === agentsSelectedId; });
    if (agent) agentsShowDetail(agent);
  }
  if (document.getElementById('hub-management-card')) {
    var pane = document.getElementById('hubs-admin-detail');
    if (pane && currentAdminView === 'hub-settings') renderHubSettings(pane);
  }
}
var updateDismissedForSession = false;
var updateSkippedVersion = '';

function checkForUpdate() {
  goApp.GetUpdateInfo().then(function (info) {
    updateInfo = info;
    if (info.version) {
      latestVersion = info.version;
      refreshVersionBadges();
    } else {
      // updateVersion not yet populated; fetch directly from bin status.
      goApp.GetBinStatus().then(function (bins) {
        if (bins && bins.length > 0 && bins[0].latest) {
          latestVersion = bins[0].latest;
          refreshVersionBadges();
        }
      });
    }
    if (!info.pending || (!info.guiBehind && !info.cliBehind)) {
      document.getElementById('update-btn').disabled = true; document.getElementById('update-btn').title = 'No updates';
      return;
    }
    if (updateDismissedForSession) return;
    if (updateSkippedVersion && updateSkippedVersion === info.version) return;
    document.getElementById('update-btn').disabled = false; document.getElementById('update-btn').title = 'Update available';
  });
}

// Shared: install or update a single binary and refresh all tool displays.
function refreshSingleBinary(name, btn) {
  btn.disabled = true;
  btn.textContent = 'Installing...';
  goApp.InstallBinary(name).then(function () {
    btn.textContent = 'Done';
    refreshBinStatus();
    refreshClientToolVersions();
    refreshUpdateTable();
    checkForUpdate();
  }).catch(function (err) {
    btn.disabled = false;
    btn.textContent = 'Retry';
    tvLog('Install ' + name + ' failed: ' + err);
  });
}

function updateServiceAgent(btn) {
  btn.disabled = true;
  btn.textContent = 'Updating...';
  tvLog('Sending update command to local telad service...');
  goApp.UpdateServiceAgent().then(function (resp) {
    try { var data = JSON.parse(resp); } catch (e) {}
    if (data && data.error) {
      btn.disabled = false;
      btn.textContent = 'Retry';
      showError('Service update failed: ' + data.error);
      return;
    }
    btn.textContent = 'Restarting...';
    tvLog('Update: ' + (data && data.message ? data.message : 'update sent'));
    // Wait for the service to restart, then refresh.
    setTimeout(function () {
      btn.textContent = 'Done';
      refreshBinStatus();
      refreshUpdateTable();
    }, 10000);
  }).catch(function (err) {
    btn.disabled = false;
    btn.textContent = 'Retry';
    showError('Service update failed: ' + err);
  });
}

// Shared: render a tools table with optional per-binary action buttons.
// container: DOM element to fill. showActions: if true, show Update/Install buttons.
// onDone: optional callback after rendering.
function renderToolsTable(container, showActions, onDone) {
  goApp.GetBinStatus().then(function (bins) {
    goApp.GetVersion().then(function (guiVer) {
      var latest = (bins && bins.length > 0 && bins[0].latest) ? bins[0].latest : '';
      if (updateInfo && updateInfo.version) latest = updateInfo.version;
      var tvUpToDate = !latest || guiVer === latest;

      var html = '<table class="tools-table"><thead><tr><th>Tool</th><th>Installed</th><th>Available</th>';
      if (showActions) html += '<th></th>';
      html += '</tr></thead><tbody>';

      // TelaVisor
      html += '<tr><td>' + (showActions ? '<span class="bin-dot bin-dot-ok"></span>' : '') + 'TelaVisor</td>'
        + '<td class="tools-version">' + escHtml(guiVer || 'dev') + '</td>'
        + '<td class="tools-version ' + (tvUpToDate ? 'tools-status-ok' : 'tools-status-warn') + '">' + escHtml(latest || guiVer || 'dev') + '</td>';
      if (showActions) {
        var tvAction = tvUpToDate ? '' : '<button class="tb-btn tools-action-btn" onclick="restartToUpdate(this)">Update &amp; Restart</button>';
        html += '<td>' + tvAction + '</td>';
      }
      html += '</tr>';

      // Managed binaries
      if (bins) {
        bins.forEach(function (b) {
          var svcOnly = !b.found && b.serviceInstalled;

          // Main row version: use service version when no local copy exists.
          var mainVer = b.found ? (b.version || 'unknown') : (svcOnly ? (b.serviceVersion || 'unknown') : 'not installed');
          var mainUpToDate = svcOnly ? (b.serviceVersion === b.latest) : b.upToDate;
          var dotClass = (b.found || svcOnly) ? 'bin-dot-ok' : 'bin-dot-missing';
          var availClass = (b.found || svcOnly) ? (mainUpToDate ? 'tools-status-ok' : 'tools-status-warn') : '';

          // Label: show "service (running/stopped)" when the service is the only installation.
          var nameLabel = escHtml(b.name);
          if (svcOnly) {
            var svcStatus = b.serviceRunning ? 'running' : 'stopped';
            nameLabel += ' <span class="tools-service-label">service (' + svcStatus + ')</span>';
          }

          html += '<tr><td>' + (showActions ? '<span class="bin-dot ' + dotClass + '"></span>' : '') + nameLabel + '</td>'
            + '<td class="tools-version">' + escHtml(mainVer) + '</td>'
            + '<td class="tools-version ' + availClass + '">' + escHtml(b.latest || '?') + '</td>';
          if (showActions) {
            var action = '';
            if (b.found) {
              action = b.upToDate ? '' : '<button class="tb-btn tools-action-btn" onclick="refreshSingleBinary(\'' + escAttr(b.name) + '\', this)">Update</button>';
            } else if (svcOnly && !mainUpToDate) {
              action = '<button class="tb-btn tools-action-btn" onclick="updateServiceAgent(this)">Update</button>';
            } else if (!svcOnly) {
              action = '<button class="tb-btn tools-action-btn" onclick="refreshSingleBinary(\'' + escAttr(b.name) + '\', this)">Install</button>';
            }
            html += '<td>' + action + '</td>';
          }
          html += '</tr>';

          // Show a service sub-row only when BOTH a local copy and a service exist.
          if (b.found && b.serviceInstalled) {
            var svcVer = b.serviceVersion || 'unknown';
            var svcState = b.serviceRunning ? 'running' : 'stopped';
            var svcUpToDate = b.serviceVersion === b.latest;
            var svcAvailClass = svcUpToDate ? 'tools-status-ok' : 'tools-status-warn';
            html += '<tr class="tools-service-row"><td>' + (showActions ? '<span class="bin-dot ' + (b.serviceRunning ? 'bin-dot-ok' : 'bin-dot-missing') + '"></span>' : '')
              + '<span class="tools-service-label">service (' + svcState + ')</span></td>'
              + '<td class="tools-version">' + escHtml(svcVer) + '</td>'
              + '<td class="tools-version ' + svcAvailClass + '">' + escHtml(b.latest || '?') + '</td>';
            if (showActions) html += '<td></td>';
            html += '</tr>';
          }
        });
      }
      html += '</tbody></table>';
      container.innerHTML = html;
      if (onDone) onDone();
    });
  });
}

function toggleUpdateOverlay() {
  var el = document.getElementById('update-overlay');
  el.classList.toggle('hidden');
  if (!el.classList.contains('hidden') && updateInfo) {
    refreshUpdateTable();
    var notes = [];
    if (updateInfo.guiBehind) notes.push('TelaVisor is out of date.');
    if (updateInfo.cliBehind) notes.push('tela CLI is out of date.');
    if (updateInfo.packageManaged) notes.push('TelaVisor was installed via a package manager. Only the CLI will be updated.');
    document.getElementById('update-note').textContent = notes.join(' ');
  }
}

function refreshUpdateTable() {
  var container = document.getElementById('update-table-container');
  if (container) renderToolsTable(container, true);
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

function refreshProfileMTU() {
  // Called on profile load; no visible UI to update (dialog reads on open)
}

function openMTUDialog() {
  goApp.GetProfileMTU().then(function (mtu) {
    var isDefault = !mtu || mtu === 0;
    var input = document.getElementById('mtu-value');
    var checkbox = document.getElementById('mtu-use-default');
    if (input) {
      input.value = isDefault ? 1100 : mtu;
      input.disabled = isDefault;
    }
    if (checkbox) checkbox.checked = isDefault;
    document.getElementById('mtu-dialog').classList.remove('hidden');
  });
}

function closeMTUDialog() {
  document.getElementById('mtu-dialog').classList.add('hidden');
}

function onMTUDefaultToggle(checked) {
  var input = document.getElementById('mtu-value');
  if (input) input.disabled = checked;
}

function saveMTUDialog() {
  var checkbox = document.getElementById('mtu-use-default');
  var input = document.getElementById('mtu-value');
  var mtu = checkbox && checkbox.checked ? 0 : (parseInt(input.value) || 1100);
  goApp.SetProfileMTU(mtu).then(function () {
    closeMTUDialog();
  }).catch(function (err) {
    tvLog('Set MTU failed: ' + err);
  });
}

function onMountEnableToggle(machine, checked) {
  var fields = ['mc-point-', 'mc-port-', 'mc-auto-'];
  fields.forEach(function (prefix) {
    var el = document.getElementById(prefix + machine);
    if (el) el.disabled = !checked;
  });
  if (!checked) {
    clearFieldError('mc-point-' + machine);
    goApp.SetMountConfig(machine, {mount: '', port: 0}).then(function () {
      updateMountButtonState();
    });
  } else {
    // Validate immediately so the user sees the required field
    validateMountPoint(machine);
  }
  markProfileDirty();
}

function validateMountPoint(machine) {
  var enabledEl = document.getElementById('mc-enabled-' + machine);
  var pointId = 'mc-point-' + machine;
  var point = (document.getElementById(pointId).value || '').trim();
  if (enabledEl && enabledEl.checked && !point) {
    setFieldError(pointId, 'Mount point is required when mount is enabled');
  } else {
    clearFieldError(pointId);
  }
}

function onMountFieldChange(machine) {
  var point = (document.getElementById('mc-point-' + machine).value || '').trim();
  var port = parseInt(document.getElementById('mc-port-' + machine).value) || 18080;
  clearFieldError('mc-point-' + machine);
  goApp.SetMountConfig(machine, {mount: point, port: port}).then(function () {
    updateMountButtonState();
  });
  markProfileDirty();
}

// Update mount button enabled state based on connection + any mount config.
function updateMountButtonState() {
  var mountBtn = document.getElementById('mount-btn');
  if (!mountBtn) return;

  if (connPhase !== 'connected') {
    mountBtn.disabled = true;
    mountBtn.title = 'Mount file shares (not connected)';
    return;
  }

  goApp.GetMountConfig().then(function (mc) {
    goApp.IsMountRunning().then(function (running) {
      if (running) {
        mountBtn.disabled = false;
        mountBtn.classList.add('mounted');
        mountBtn.title = 'Unmount file shares';
      } else if (mc && mc.mount) {
        mountBtn.disabled = false;
        mountBtn.classList.remove('mounted');
        mountBtn.title = 'Mount file shares (' + mc.mount + ')';
      } else {
        mountBtn.disabled = true;
        mountBtn.classList.remove('mounted');
        mountBtn.title = 'No mount configured';
      }
    });
  });
}

function copyProfileCLI() {
  goApp.GetProfilePath().then(function (path) {
    if (!path) return;
    var cmd = 'tela connect -profile "' + path + '"';
    var mtuInput = document.getElementById('profile-mtu');
    var mtu = mtuInput ? parseInt(mtuInput.value) : 0;
    if (mtu > 0) {
      cmd += ' -mtu ' + mtu;
    }
    navigator.clipboard.writeText(cmd).then(function () {
      tvLog('Copied: ' + cmd);
    });
  });
}

function showProfileOverview() {
  selectedHubURL = null;
  selectedMachineId = null;
  currentDetailView = 'profile';
  refreshAll();
  renderProfileSettings();
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
      refreshProfileMTU();
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
  showConfirmDialog('Delete Profile', 'Delete profile "' + name + '"? This cannot be undone.', 'Delete').then(function (yes) {
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

    // Profile Settings root node
    var rootNode = document.createElement('div');
    rootNode.className = 'profile-root' + (!selectedHubURL && !selectedMachineId && currentDetailView === 'profile' ? ' selected' : '');
    rootNode.innerHTML = '<span class="profile-root-icon">&#x2699;</span> Profile Settings';
    rootNode.onclick = function () { selectProfileSettings(rootNode); };
    content.appendChild(rootNode);

    hubs.forEach(function (hub) {
      renderHubInSidebar(content, hub, connectedAtRefresh);
    });

    // Preview node
    var previewNode = document.createElement('div');
    previewNode.className = 'profile-root' + (currentDetailView === 'preview' ? ' selected' : '');
    previewNode.innerHTML = '<span class="profile-root-icon">&#x2B9A;</span> Preview';
    previewNode.onclick = function () { selectPreviewNode(previewNode); };
    content.appendChild(previewNode);

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

      if (included) {
        var renderedMachines = {};
        if (status.machines) {
          status.machines.forEach(function (m) {
            var mId = m.id || m.hostname;
            renderedMachines[mId] = true;
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
        // Show machines in the profile that aren't in the status response
        Object.keys(selectedServices).forEach(function (key) {
          var sel = selectedServices[key];
          if (sel.hub !== hub.url) return;
          if (renderedMachines[sel.machine]) return;
          renderedMachines[sel.machine] = true;
          var mEl = document.createElement('div');
          mEl.className = 'machine-item machine-unreachable';
          if (selectedHubURL === hub.url && selectedMachineId === sel.machine) mEl.classList.add('selected');
          mEl.innerHTML = '<span class="machine-dot unreachable"></span>'
            + escHtml(sel.machine)
            + '<span class="unreachable-badge">unreachable</span>';
          mEl.onclick = function (e) {
            e.stopPropagation();
            selectMachine(hub, { id: sel.machine, services: [], agentConnected: false, unreachable: true }, mEl);
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

function selectProfileSettings(el) {
  clearSelection();
  el.classList.add('selected');
  selectedHubURL = null;
  selectedMachineId = null;
  currentDetailView = 'profile';
  renderProfileSettings();
}

function selectPreviewNode(el) {
  clearSelection();
  el.classList.add('selected');
  currentDetailView = 'preview';
  renderPreview();
}

function selectHub(hub, el) {
  clearSelection();
  el.classList.add('selected');
  selectedHubURL = hub.url;
  selectedMachineId = null;
  currentDetailView = 'hub';
  renderHubDetail(hub);
}

function renderProfileSettings() {
  var pane = document.getElementById('detail-pane');
  var locked = connPhase === 'connected' || connPhase === 'connecting';

  goApp.GetMountConfig().then(function (mc) {
    goApp.GetProfileMTU().then(function (mtu) {
      var isDefault = !mtu || mtu === 0;
      var mtuVal = isDefault ? 1100 : mtu;

      goApp.GetSettings().then(function (s) {
        var profileName = s.defaultProfile || '';

        var mountEnabled = !!mc.mount;
        // Mount fields disabled if: locked OR mount not enabled
        var mountFieldDis = (locked || !mountEnabled) ? ' disabled' : '';
        var allDis = locked ? ' disabled' : '';

        var html = '<div class="settings-group">'
          + '<div class="settings-group-header">Name</div>'
          + '<div class="settings-group-body">'
          + '<input type="text" id="ps-name" class="form-input full-width" value="' + escAttr(profileName) + '" title="Profile name"' + allDis + ' onchange="onProfileNameChange()">'
          + '</div></div>';

        html += '<div class="settings-group">'
          + '<div class="settings-group-header">Mount</div>'
          + '<div class="settings-group-body"><div class="mount-config-form">'
          + '<div class="mount-config-row"><label class="mount-config-check"><input type="checkbox" id="ps-mount-enable"' + (mountEnabled ? ' checked' : '') + allDis + ' onchange="onPsMountEnableToggle(this.checked)"> Enable</label></div>'
          + '<div class="mount-config-row"><label class="mount-config-label" for="ps-mount-point">Mount point:</label><input type="text" id="ps-mount-point" class="form-input mono" value="' + escAttr(mc.mount || '') + '" data-required="Mount point is required when mount is enabled"' + mountFieldDis + ' onchange="onPsMountChange()" onblur="validatePsMount()"></div>'
          + '<div class="mount-config-row"><label class="mount-config-label" for="ps-mount-port">Port:</label><input type="number" id="ps-mount-port" class="form-input mono mount-config-port" min="1024" max="65535" value="' + (mc.port || 18080) + '"' + mountFieldDis + ' onchange="onPsMountChange()"></div>'
          + '<div class="mount-config-row"><label class="mount-config-check"><input type="checkbox" id="ps-mount-auto"' + (mc.auto ? ' checked' : '') + mountFieldDis + ' onchange="onPsMountChange()"> Auto-mount on connect</label></div>'
          + '</div></div></div>';

        html += '<div class="settings-group">'
          + '<div class="settings-group-header">MTU</div>'
          + '<div class="settings-group-body"><div class="mount-config-form">'
          + '<div class="mount-config-row"><input type="number" id="ps-mtu" class="form-input mono mount-config-port" value="' + mtuVal + '" title="Tunnel MTU"' + ((isDefault || locked) ? ' disabled' : '') + ' onchange="onPsMtuChange()"></div>'
          + '<div class="mount-config-row"><label class="mount-config-check"><input type="checkbox" id="ps-mtu-default"' + (isDefault ? ' checked' : '') + allDis + ' onchange="onPsMtuDefaultToggle(this.checked)"> Use default (1100)</label></div>'
          + '</div></div></div>';

        pane.innerHTML = html;
      });
    });
  });
}

function renderPreview() {
  var pane = document.getElementById('detail-pane');
  goApp.GetProfilePath().then(function (path) {
    goApp.GetSettings().then(function (s) {
      var profileName = s.defaultProfile || '';
      var cli = 'tela connect -profile "' + profileName + '"';

      var html = '<div class="settings-group">'
        + '<div class="settings-group-header">Profile</div>'
        + '<div class="preview-info-row">'
        + '<span class="preview-info-label">File:</span>'
        + '<code class="preview-info-value" id="preview-path">' + escHtml(path) + '</code>'
        + '<button type="button" class="copy-btn" onclick="copyToClipboard(document.getElementById(\'preview-path\').textContent)" title="Copy path">&#x2398;</button>'
        + '</div>'
        + '<div class="preview-info-row">'
        + '<span class="preview-info-label">CLI:</span>'
        + '<code class="preview-info-value" id="preview-cli">' + escHtml(cli) + '</code>'
        + '<button type="button" class="copy-btn" onclick="copyToClipboard(document.getElementById(\'preview-cli\').textContent)" title="Copy command">&#x2398;</button>'
        + '</div>'
        + '</div>';

      html += '<div class="settings-group">'
        + '<div class="settings-group-header">YAML</div>'
        + '<pre class="yaml-preview" id="preview-yaml">Loading...</pre>'
        + '</div>';

      pane.innerHTML = html;

      // Load YAML content
      goApp.GetProfileYAML().then(function (yaml) {
        var el = document.getElementById('preview-yaml');
        if (el) el.textContent = yaml || '(empty)';
      });
    });
  });
}

// --- Profile Settings handlers ---

function onProfileNameChange() {
  var newName = (document.getElementById('ps-name').value || '').trim();
  if (!newName) return;
  goApp.GetCurrentProfile().then(function (oldName) {
    if (oldName === newName) return;
    goApp.RenameProfile(oldName, newName).then(function () {
      refreshAll();
    }).catch(function (err) {
      tvLog('Rename failed: ' + err);
    });
  });
}

function onPsMountEnableToggle(checked) {
  ['ps-mount-point', 'ps-mount-port', 'ps-mount-auto'].forEach(function (id) {
    var el = document.getElementById(id);
    if (el) el.disabled = !checked;
  });
  if (!checked) {
    clearFieldError('ps-mount-point');
    goApp.SetMountConfig({mount: '', port: 0, auto: false}).then(function () {
      updateMountButtonState();
    });
  }
  markProfileDirty();
}

function validatePsMount() {
  var enabled = document.getElementById('ps-mount-enable');
  var point = (document.getElementById('ps-mount-point').value || '').trim();
  if (enabled && enabled.checked && !point) {
    setFieldError('ps-mount-point', 'Mount point is required when mount is enabled');
  } else {
    clearFieldError('ps-mount-point');
  }
}

function onPsMountChange() {
  var point = (document.getElementById('ps-mount-point').value || '').trim();
  var port = parseInt(document.getElementById('ps-mount-port').value) || 18080;
  var auto = document.getElementById('ps-mount-auto').checked;
  clearFieldError('ps-mount-point');
  goApp.SetMountConfig({mount: point, port: port, auto: auto}).then(function () {
    updateMountButtonState();
  });
  markProfileDirty();
}

function onPsMtuDefaultToggle(checked) {
  document.getElementById('ps-mtu').disabled = checked;
  if (checked) {
    goApp.SetProfileMTU(0);
  }
  markProfileDirty();
}

function onPsMtuChange() {
  var mtu = parseInt(document.getElementById('ps-mtu').value) || 1100;
  goApp.SetProfileMTU(mtu);
  markProfileDirty();
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
  currentDetailView = 'machine';
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
      + '<p class="section-desc" style="margin-bottom:16px;">' + (machine.agentConnected ? 'Online' : 'Offline')
      + ' on ' + escHtml(hubName) + '</p>';

    if (machine.unreachable) {
      html += '<p class="empty-hint" style="margin-bottom:12px;">This machine is not currently registered on the hub.</p>';
      // Show services from the profile so they can be unchecked
      var profileServices = [];
      Object.keys(selectedServices).forEach(function (key) {
        var sel = selectedServices[key];
        if (sel.hub === hub.url && sel.machine === machineId) {
          profileServices.push({ name: sel.service, port: sel.servicePort || 0, proto: 'tcp', key: key });
        }
      });
      if (profileServices.length > 0) {
        html += '<div class="settings-group"><div class="settings-group-header">Services</div>';
        profileServices.forEach(function (svc) {
          var localPort = selectedServices[svc.key] ? selectedServices[svc.key].localPort : '';
          html += '<div class="profile-svc-row">'
            + '<input type="checkbox" checked' + (isConnected ? ' disabled' : '')
            + ' onchange="toggleService(\'' + escAttr(hub.url) + '\', \'' + escAttr(machineId) + '\', \'' + escAttr(svc.name) + '\', ' + svc.port + ', this.checked)">'
            + '<div class="profile-svc-name">' + escHtml(svc.name) + '</div>'
            + '<div class="profile-svc-port">' + (svc.port ? ':' + svc.port : '') + '</div>'
            + '<div class="profile-svc-proto">' + escHtml(svc.proto) + '</div>';
          if (localPort) {
            html += '<div class="profile-svc-local">localhost:' + localPort + '</div>';
          }
          html += '</div>';
        });
        html += '</div>';
      } else {
        html += '<p class="empty-hint">No services in profile.</p>';
      }
      pane.innerHTML = html;
      return;
    }

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

function removeUnreachableMachine(hubURL, machineId) {
  var keysToRemove = [];
  Object.keys(selectedServices).forEach(function (key) {
    var sel = selectedServices[key];
    if (sel.hub === hubURL && sel.machine === machineId) {
      keysToRemove.push(key);
    }
  });
  keysToRemove.forEach(function (key) { delete selectedServices[key]; });
  markProfileDirty();
  refreshAll();
}

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

function markProfileDirty() {
  profileDirty = true;
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
  // Validate all required fields in the detail pane
  var detailPane = document.getElementById('detail-pane');
  if (detailPane && !validateRequiredFields(detailPane)) {
    showError('Fix the highlighted fields before saving');
    return;
  }
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

// ── Connection State Management ──
// Single source of truth. State transitions are event-driven.
// The poll exists only to detect unexpected process death and
// to refresh the terminal log. It does not drive state transitions.

var connPhase = 'disconnected'; // 'disconnected' | 'connecting' | 'connected' | 'disconnecting'
var connAttached = false; // true when attached to external tela
var boundServicesCache = null; // cached bound services from control API

function setConnPhase(phase) {
  connPhase = phase;
  applyConnectionUI();
}

function applyConnectionUI() {
  var btn = document.getElementById('connect-btn');
  if (btn) {
    btn.classList.remove('connected', 'connecting');
    btn.disabled = (connPhase === 'disconnecting');
    if (connPhase === 'connected') {
      btn.classList.add('connected');
      btn.title = connAttached ? 'Detach' : 'Disconnect';
    } else if (connPhase === 'connecting' || connPhase === 'disconnecting') {
      btn.classList.add('connecting');
      btn.title = connPhase === 'connecting' ? 'Connecting...' : 'Disconnecting...';
    } else {
      btn.title = 'Connect';
    }
  }

  // Mount button: check connection + config
  updateMountButtonState();

  // Tela log dot
  var dot = document.getElementById('log-tela-dot');
  if (dot) dot.className = (connPhase === 'connected' || connPhase === 'connecting') ? 'log-dot log-dot-live' : 'log-dot log-dot-idle';

  // Agents pair button
  var pairBtn = document.getElementById('agents-pair-btn');
  if (pairBtn) pairBtn.disabled = (connPhase !== 'connected');

  dismissConnectTooltip();
}

// Called when all tabs should reflect the current connection state.
function onConnectionChanged() {
  applyConnectionUI();
  refreshAll();
  refreshStatus();
  agentsRefresh();
  if (connPhase === 'connected') {
    refreshFilesTab();
    // Re-check mount state after auto-mount has had time to start
    setTimeout(updateMountButtonState, 3000);
  } else {
    filesShowMachineList();
  }
  // Re-render current detail pane to update disabled states
  if (currentDetailView === 'profile') {
    renderProfileSettings();
  } else if (currentDetailView === 'preview') {
    renderPreview();
  }
}

// Legacy compatibility: called from places that used to call updateConnectButton.
// Now just applies UI from the current phase.
function updateConnectButton() {
  applyConnectionUI();
}

function goToStatus() {
  switchMode('clients');
  var statusBtn = document.querySelector('#tabbar-clients .main-tab');
  if (statusBtn) switchTab('status', statusBtn);
}

function toggleConnection() {
  dismissConnectTooltip();
  goApp.GetSettings().then(function (s) {
    if (!s.connectTooltipDismissed) {
      s.connectTooltipDismissed = true;
      goApp.SaveSettings(s);
    }
  }).catch(function () {});

  if (connPhase === 'connected' || connPhase === 'connecting') {
    doDisconnect();
  } else if (connPhase === 'disconnected') {
    doConnect();
  }
  // If disconnecting, ignore clicks
}

function doConnect() {
  var connections = buildConnections();
  if (connections.length === 0) return;

  // Only reset if not already connected/attached
  if (connPhase !== 'disconnected') return;

  connAttached = false;
  boundServicesCache = null;
  tvLog('Connecting...');
  setConnPhase('connecting');

  goApp.Connect(JSON.stringify(connections)).then(function () {
    // Process started. Stay in 'connecting' until first service_bound event.
    tvLog('Process started, waiting for tunnels...');
    takeSnapshot();
    startConnectionPoll();

    // Connect WebSocket immediately, retry until control API is ready.
    // After connecting, check if services are already bound (events
    // may have fired before the WebSocket was listening).
    (function connectWS() {
      goApp.ConnectControlWS().then(function () {
        // WebSocket connected. Check if services are already bound.
        if (connPhase === 'connecting') {
          goApp.GetControlServices().then(function (services) {
            if (services && services.length > 0 && connPhase === 'connecting') {
              tvLog('Connected');
              setConnPhase('connected');
              onConnectionChanged();
            }
          });
        }
      }).catch(function () {
        if (connPhase === 'connecting' || connPhase === 'connected') {
          setTimeout(connectWS, 500);
        }
      });
    })();

    // Refresh tabs (they can show partial state while tunnels establish)
    refreshAll();
    refreshStatus();
    refreshFilesTab();
    agentsRefresh();

    // Apply verbose preference once WebSocket is likely ready
    setTimeout(function () {
      if (verboseMode) {
        goApp.SetVerbose(true);
      } else {
        goApp.GetSettings().then(function (s) {
          if (s.verboseDefault) {
            verboseMode = true;
            var vbtn = document.getElementById('verbose-btn');
            var vicon = document.getElementById('verbose-icon');
            if (vbtn) vbtn.classList.add('active');
            if (vicon) vicon.innerHTML = '\u2611';
            goApp.SetVerbose(true);
          }
        });
      }
    }, 2000);
  }).catch(function (err) {
    tvLog('Connection failed: ' + err);
    showError('Connection failed: ' + err);
    setConnPhase('disconnected');
  });
}

function doQuit() {
  if (connPhase === 'connected' || connPhase === 'connecting') {
    // Attached mode: just quit (detach is non-destructive)
    if (connAttached) {
      goApp.QuitApp();
      return;
    }
    goApp.GetSettings().then(function (s) {
      if (s.confirmDisconnect) {
        showDisconnectOverlay(function () { goApp.QuitApp(); });
      } else {
        goApp.QuitApp();
      }
    });
  } else {
    goApp.QuitApp();
  }
}

var disconnectCallback = null;

function doDisconnect() {
  // Attached mode: detach without confirmation (non-destructive)
  goApp.GetConnectionState().then(function (state) {
    if (state.attached) {
      performDisconnect();
      return;
    }
    goApp.GetSettings().then(function (s) {
      if (!s.confirmDisconnect) {
        performDisconnect();
        return;
      }
      showDisconnectOverlay(performDisconnect);
    });
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
  tvLog(connAttached ? 'Detaching...' : 'Disconnecting...');
  setConnPhase('disconnecting');

  // Stop mount before disconnecting
  goApp.IsMountRunning().then(function (running) {
    if (running) goApp.StopMount();
  });

  goApp.Disconnect().then(function () {
    tvLog(connAttached ? 'Detached' : 'Disconnected');
  }).catch(function () {
    tvLog('Disconnect error (cleaning up)');
  }).finally(function () {
    connAttached = false;
    boundServicesCache = null;
    goApp.DisconnectControlWS();
    stopConnectionPoll();
    setConnPhase('disconnected');
    onConnectionChanged();
    refreshTerminal();
  });
}

// The poll exists to detect unexpected process death and refresh
// the terminal log. It does NOT drive state transitions.
var pollInFlight = false;

function startConnectionPoll() {
  stopConnectionPoll();
  pollInFlight = false;
  pollIntervalId = setInterval(function () {
    if (pollInFlight) return;
    pollInFlight = true;
    goApp.GetConnectionState().then(function (state) {
      pollInFlight = false;
      refreshTerminal();

      // Detect unexpected process death
      if (!state.connected && connPhase === 'connected') {
        tvLog('Connection lost');
        stopConnectionPoll();
        goApp.DisconnectControlWS();
        setConnPhase('disconnected');
        onConnectionChanged();
        refreshTerminal();

        // Auto-reconnect if enabled
        goApp.GetSettings().then(function (s) {
          if (s.reconnectOnDrop && Object.keys(selectedServices).length > 0) {
            tvLog('Reconnecting in 3 seconds...');
            setTimeout(function () { doConnect(); }, 3000);
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
var agentsSelectedHub = '';

function agentsRefresh() {
  goApp.GetConnectionState().then(function (state) {
    document.getElementById('agents-pair-btn').disabled = !state.connected;
  }).catch(function () {
    document.getElementById('agents-pair-btn').disabled = true;
  });
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
      + '<div class="agents-sidebar-version version-badge-sidebar" data-ver="' + escAttr(ver) + '"' + (latestVersion && ver !== latestVersion ? ' style="color:#f39c12"' : '') + '>' + escHtml(ver) + '</div>'
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

var agentsConfigDirty = false;

function agentsMarkDirty() {
  agentsConfigDirty = true;
  document.getElementById('agents-undo-btn').disabled = false;
  document.getElementById('agents-save-btn').disabled = false;
}

function agentsShowDetail(a) {
  var isOnline = a.online;
  var hasMgmt = a.capabilities && a.capabilities.management;
  var canManage = isOnline && hasMgmt;
  var eid = escAttr(a.id);
  var ehub = escAttr(a.hub);

  agentsConfigDirty = false;
  document.getElementById('agents-undo-btn').disabled = true;
  document.getElementById('agents-save-btn').disabled = true;
  document.getElementById('agents-restart-btn').disabled = !canManage;
  document.getElementById('agents-logs-btn').disabled = !canManage;
  agentsSelectedHub = a.hub;

  var html = '<div class="detail-header">'
    + '<div class="detail-title">' + escHtml(a.id)
    + ' <span class="' + (isOnline ? 'badge-online' : 'badge-offline') + '">'
    + (isOnline ? 'Online' : 'Offline') + '</span></div>'
    + '<div class="detail-subtitle">On ' + escHtml(a.hub) + '</div>'
    + '</div>';

  // Agent Info (read-only kv-table)
  html += '<div class="setting-card"><div class="setting-card-title">Agent Info</div>'
    + '<div class="setting-card-desc">Read-only metadata reported by the agent at registration.</div>'
    + '<table class="kv-table">'
    + '<tr><td>Version</td><td>' + versionBadge(a.version) + '</td></tr>'
    + '<tr><td>Hub</td><td>' + escHtml(a.hub) + '</td></tr>'
    + '<tr><td>Hostname</td><td>' + escHtml(a.hostname || '-') + '</td></tr>'
    + '<tr><td>Platform</td><td>' + escHtml(a.os || '-') + '</td></tr>'
    + '<tr><td>Last seen</td><td>' + escHtml(a.lastSeen ? agentFormatDate(a.lastSeen) : '-') + '</td></tr>'
    + '<tr><td>Active sessions</td><td>' + String(a.sessionCount || 0) + '</td></tr>'
    + '</table></div>';

  // Display Name (editable)
  var displayName = a.displayName || '';
  html += '<div class="setting-card"><div class="setting-card-title">Display Name</div>'
    + '<div class="setting-card-desc">Human-readable name shown in dashboards and portals.</div>'
    + '<div class="setting-field">'
    + '<input class="setting-input" type="text" id="agent-displayName" value="' + escAttr(displayName) + '" onchange="agentsMarkDirty()">'
    + '</div></div>';

  // Tags (editable)
  var tags = (a.tags && a.tags.length) ? a.tags.join(', ') : '';
  html += '<div class="setting-card"><div class="setting-card-title">Tags</div>'
    + '<div class="setting-card-desc">Comma-separated metadata tags for filtering and organization.</div>'
    + '<div class="setting-field">'
    + '<input class="setting-input" type="text" id="agent-tags" value="' + escAttr(tags) + '" placeholder="e.g. production, us-east" onchange="agentsMarkDirty()">'
    + '</div></div>';

  // Location (editable)
  var location = a.location || '';
  html += '<div class="setting-card"><div class="setting-card-title">Location</div>'
    + '<div class="setting-card-desc">Physical or logical location of this machine.</div>'
    + '<div class="setting-field">'
    + '<input class="setting-input" type="text" id="agent-location" value="' + escAttr(location) + '" placeholder="e.g. Building A, Rack 12" onchange="agentsMarkDirty()">'
    + '</div></div>';

  // Services
  html += '<div class="setting-card"><div class="setting-card-title">Services</div>'
    + '<div class="setting-card-desc">TCP services exposed through the tunnel.</div>';
  if (a.services && a.services.length > 0) {
    a.services.forEach(function (s) {
      html += '<div class="setting-list-item"><div>'
        + '<strong>' + escHtml(s.name || 'unnamed') + '</strong> '
        + '<span class="chip">:' + (s.port || '') + '</span>'
        + (s.proto ? ' <span class="chip">' + escHtml(s.proto) + '</span>' : '')
        + '</div></div>';
    });
  } else {
    html += '<div class="setting-card-desc">No services configured.</div>';
  }
  html += '</div>';

  // File Share (editable when management supported)
  var fs = a.capabilities && a.capabilities.fileShare;
  html += '<div class="setting-card"><div class="setting-card-title">File Share</div>'
    + '<div class="setting-card-desc">Sandboxed directory access through the encrypted tunnel.</div>';
  if (fs && fs.enabled) {
    html += '<div class="setting-check"><input type="checkbox" id="agent-fs-enabled" checked onchange="agentsMarkDirty()"> Enabled</div>'
      + '<div class="setting-check"><input type="checkbox" id="agent-fs-writable"' + (fs.writable ? ' checked' : '') + ' onchange="agentsMarkDirty()"> Writable</div>'
      + '<div class="setting-check"><input type="checkbox" id="agent-fs-allowDelete"' + (fs.allowDelete ? ' checked' : '') + ' onchange="agentsMarkDirty()"> Allow delete</div>'
      + '<div class="setting-field"><div class="setting-label">Max file size</div>'
      + '<input class="setting-input setting-input-short" type="text" id="agent-fs-maxFileSize" value="' + escAttr(fs.maxFileSize ? agentFormatBytes(fs.maxFileSize) : '') + '" onchange="agentsMarkDirty()"></div>'
      + '<div class="setting-field"><div class="setting-label">Blocked extensions</div>'
      + '<input class="setting-input" type="text" id="agent-fs-blocked" value="' + escAttr((fs.blockedExtensions || []).join(', ')) + '" onchange="agentsMarkDirty()"></div>';
  } else {
    html += '<div class="setting-check"><input type="checkbox" id="agent-fs-enabled" onchange="agentsMarkDirty()"> Enabled</div>';
  }
  html += '</div>';

  // Management
  html += '<div class="setting-card" id="agent-management-card"><div class="setting-card-title">Management</div>'
    + '<div class="setting-card-desc">Remote agent lifecycle controls.</div>'
    + '<table class="kv-table">';
  if (canManage) {
    // Software button starts disabled with a neutral label. loadAgentChannel
    // overwrites it with the channel-aware truth from the agent's update-status
    // response, so the button can never disagree with the channel row.
    html += '<tr><td>Configuration</td><td><button type="button" class="tb-btn" onclick="agentsViewConfig(\'' + eid + '\',\'' + ehub + '\')">View Config</button></td></tr>'
      + '<tr><td>Log output</td><td><button type="button" class="tb-btn" onclick="agentsViewLogs(\'' + eid + '\',\'' + ehub + '\')">View Logs</button></td></tr>'
      + '<tr><td>Release channel</td><td>' + renderChannelSelect('agent-channel-select', '') + ' <span id="agent-channel-status" class="tools-service-label">loading...</span></td></tr>'
      + '<tr><td>Software</td><td><button type="button" class="tb-btn" id="agent-update-btn" disabled onclick="agentsUpdate(\'' + eid + '\',\'' + ehub + '\')">Update</button>'
      + ' <span id="agent-update-status" class="tools-service-label">loading...</span></td></tr>'
      + '<tr><td>Restart</td><td><button type="button" class="tb-btn" onclick="agentsRestart(\'' + eid + '\',\'' + ehub + '\')">Restart</button></td></tr>';
  } else {
    html += '<tr><td colspan="2">Agent ' + (isOnline ? 'does not support remote management. Update telad to enable.' : 'is offline.') + '</td></tr>';
  }
  html += '</table></div>';

  // Danger Zone
  html += '<div class="setting-card setting-card-danger"><div class="setting-card-title">Danger Zone</div>'
    + '<div class="danger-row"><div class="danger-row-text">'
    + '<div class="danger-row-label">Force disconnect</div>'
    + '<div class="danger-row-desc">Close all active sessions for this agent.</div>'
    + '</div><button type="button" class="btn-danger btn-sm" disabled>Disconnect</button></div>'
    + '<div class="danger-row"><div class="danger-row-text">'
    + '<div class="danger-row-label">Remove machine</div>'
    + '<div class="danger-row-desc">Remove this machine from the hub. The agent will re-register.</div>'
    + '</div><button type="button" class="btn-danger btn-sm" disabled>Remove</button></div>'
    + '</div>';

  document.getElementById('agents-detail').innerHTML = html;

  if (canManage && document.getElementById('agent-channel-select')) {
    loadAgentChannel(toWSURL(a.hub), a.id);
  }
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

function agentsRestartSelected() {
  if (agentsSelectedId && agentsSelectedHub) agentsRestart(agentsSelectedId, agentsSelectedHub);
}

function agentsViewLogsSelected() {
  if (agentsSelectedId && agentsSelectedHub) agentsViewLogs(agentsSelectedId, agentsSelectedHub);
}

function agentsUndo() {
  // Re-render the current agent detail from cached data (discards unsaved edits)
  var agent = agentsData.find(function (a) { return a.id === agentsSelectedId; });
  if (agent) agentsShowDetail(agent);
  document.getElementById('agents-undo-btn').disabled = true;
  document.getElementById('agents-save-btn').disabled = true;
}

function agentsSaveConfig() {
  if (!agentsSelectedId || !agentsSelectedHub) return;

  // Gather editable fields
  var fields = {};
  var el;

  el = document.getElementById('agent-displayName');
  if (el) fields.displayName = el.value.trim();

  el = document.getElementById('agent-tags');
  if (el) {
    var raw = el.value.trim();
    fields.tags = raw ? raw.split(',').map(function (t) { return t.trim(); }).filter(Boolean) : [];
  }

  el = document.getElementById('agent-location');
  if (el) fields.location = el.value.trim();

  // File share fields
  el = document.getElementById('agent-fs-enabled');
  if (el) {
    fields.fileShare = { enabled: el.checked };
    var w = document.getElementById('agent-fs-writable');
    var d = document.getElementById('agent-fs-allowDelete');
    var m = document.getElementById('agent-fs-maxFileSize');
    var b = document.getElementById('agent-fs-blocked');
    if (w) fields.fileShare.writable = w.checked;
    if (d) fields.fileShare.allowDelete = d.checked;
    if (m && m.value.trim()) fields.fileShare.maxFileSize = m.value.trim();
    if (b) {
      var exts = b.value.trim();
      fields.fileShare.blockedExtensions = exts ? exts.split(',').map(function (e) { return e.trim(); }).filter(Boolean) : [];
    }
  }

  var wsHub = toWSURL(agentsSelectedHub);
  var saveBtn = document.getElementById('agents-save-btn');
  saveBtn.textContent = 'Saving...';
  saveBtn.disabled = true;

  goApp.SetAgentConfig(wsHub, agentsSelectedId, JSON.stringify(fields)).then(function (resp) {
    try { var data = JSON.parse(resp); } catch (e) {}
    if (data && (data.error || data.ok === false)) {
      tvLog('Save failed: ' + (data.error || data.message || 'unknown error'));
      saveBtn.textContent = 'Save';
      saveBtn.disabled = false;
      return;
    }
    tvLog('Agent config saved for ' + agentsSelectedId);
    agentsConfigDirty = false;
    saveBtn.textContent = 'Save';
    document.getElementById('agents-undo-btn').disabled = true;
    // Update the cached agent data with the saved values so the
    // display stays consistent without waiting for re-registration.
    var agent = agentsData.find(function (a) { return a.id === agentsSelectedId; });
    if (agent) {
      if (fields.displayName !== undefined) agent.displayName = fields.displayName;
      if (fields.tags !== undefined) agent.tags = fields.tags;
      if (fields.location !== undefined) agent.location = fields.location;
    }
  }).catch(function (err) {
    tvLog('Save failed: ' + err);
    saveBtn.textContent = 'Save';
    saveBtn.disabled = false;
  });
}

function agentsViewConfig(machineId, hub) {
  var wsHub = toWSURL(hub);
  goApp.GetAgentConfig(wsHub, machineId).then(function (resp) {
    try { var data = JSON.parse(resp); } catch (e) { showError('Invalid response'); return; }
    if (data.error) { showError('Config error: ' + data.error); return; }
    var pretty = JSON.stringify(data, null, 2);
    showTextViewDialog('Agent Config: ' + machineId, pretty);
  }).catch(function (err) { showError('Failed: ' + err); });
}

// Show a read-only text viewer dialog (monospace, scrollable).
function showTextViewDialog(title, text) {
  var overlay = document.getElementById('generic-dialog-overlay');
  document.getElementById('generic-dialog-title').textContent = title;
  document.getElementById('generic-dialog-message').style.display = 'none';
  var input = document.getElementById('generic-dialog-input');
  input.classList.add('hidden');

  var pre = document.createElement('pre');
  pre.className = 'text-view-content';
  pre.textContent = text;
  input.parentNode.insertBefore(pre, input);

  var okBtn = document.getElementById('generic-dialog-ok');
  okBtn.textContent = 'Close';
  okBtn.className = 'btn-primary';
  overlay.classList.remove('hidden');

  _dialogResolve = function () {
    overlay.classList.add('hidden');
    pre.remove();
    _dialogResolve = null;
  };
  _dialogMode = 'confirm';
}

function agentsViewLogs(machineId, hub) {
  var paneId = 'log-agent-' + machineId;
  var existing = document.getElementById(paneId);

  // Create the tab and pane if they don't exist yet
  if (!existing) {
    // Add tab button before the "+" button
    var tabBar = document.querySelector('.log-panel-tabs');
    var addBtn = tabBar.querySelector('.log-panel-tab-add');
    var tab = document.createElement('button');
    tab.className = 'log-panel-tab';
    tab.onclick = function () { switchLogTab(tab, paneId); };
    tab.innerHTML = '<span class="log-dot log-dot-idle" id="' + paneId + '-dot"></span>'
      + escHtml(machineId)
      + '<span class="log-tab-close" onclick="event.stopPropagation();removeAgentLogTab(\'' + escAttr(paneId) + '\',this.parentNode)" title="Close">&times;</span>';
    tabBar.insertBefore(tab, addBtn);

    // Add pane
    var pre = document.createElement('pre');
    pre.className = 'log-panel-output hidden';
    pre.id = paneId;
    pre.setAttribute('data-hub', hub);
    pre.textContent = 'Loading logs for ' + machineId + '...\n';
    var logPanel = document.getElementById('log-panel');
    logPanel.appendChild(pre);

    // Init scroll tracking
    initLogScrollTracking(paneId);
    saveLogTabs();
  }

  // Switch to the tab
  var tabBtn = document.querySelector('.log-panel-tabs').querySelector('[onclick*="' + paneId + '"]')
    || document.querySelector('.log-panel-tab:last-of-type');
  if (tabBtn) switchLogTab(tabBtn, paneId);

  // Expand the log panel if collapsed
  var panel = document.getElementById('log-panel');
  if (panel.classList.contains('collapsed')) toggleLogPanel();

  // Fetch logs
  var wsHub = toWSURL(hub);
  var dot = document.getElementById(paneId + '-dot');
  if (dot) dot.className = 'log-dot log-dot-warn';

  goApp.GetAgentLogs(wsHub, machineId, 500).then(function (resp) {
    try { var data = JSON.parse(resp); } catch (e) {
      document.getElementById(paneId).textContent = 'Error: invalid response\n';
      if (dot) dot.className = 'log-dot log-dot-idle';
      return;
    }
    if (data.error || data.ok === false) {
      document.getElementById(paneId).textContent = 'Error: ' + (data.error || data.message || 'unknown error') + '\n';
      if (dot) dot.className = 'log-dot log-dot-idle';
      return;
    }
    var payload = data.payload || data;
    var lines = payload.lines || [];
    var el = document.getElementById(paneId);
    el.textContent = lines.join('\n') + '\n';
    logAutoScroll(el);
    if (dot) dot.className = 'log-dot log-dot-live';
  }).catch(function (err) {
    document.getElementById(paneId).textContent = 'Failed: ' + err + '\n';
    if (dot) dot.className = 'log-dot log-dot-idle';
  });
}

function removeAgentLogTab(paneId, tabBtn) {
  var pane = document.getElementById(paneId);
  if (pane) pane.remove();
  if (tabBtn) tabBtn.remove();
  // Switch back to TelaVisor tab
  var tvTab = document.querySelector('.log-panel-tab');
  if (tvTab) switchLogTab(tvTab, 'log-tv');
  saveLogTabs();
}

// Collect open dynamic log tabs and persist to settings.
function saveLogTabs() {
  var tabs = [];
  var panes = document.querySelectorAll('.log-panel-output');
  panes.forEach(function (p) {
    var id = p.id;
    if (id.indexOf('log-agent-') === 0) {
      var machineId = id.substring('log-agent-'.length);
      // Find the hub from the tab button's data attribute
      var hub = p.getAttribute('data-hub') || '';
      tabs.push({ type: 'agent', id: machineId, hub: hub });
    } else if (id.indexOf('log-hub-') === 0) {
      var hubName = p.getAttribute('data-hub') || '';
      tabs.push({ type: 'hub', id: hubName, hub: hubName });
    }
  });
  goApp.GetSettings().then(function (s) {
    s.openLogTabs = tabs;
    goApp.SaveSettings(JSON.stringify(s));
  });
}

// Restore log tabs from saved settings on startup.
function restoreLogTabs(tabs) {
  if (!tabs || tabs.length === 0) return;
  tabs.forEach(function (t) {
    if (t.type === 'agent' && t.id && t.hub) {
      agentsViewLogs(t.id, t.hub);
    } else if (t.type === 'hub' && t.id) {
      hubViewLogs(t.id);
    }
  });
  // Switch back to TelaVisor tab after restoring
  var tvTab = document.querySelector('.log-panel-tab');
  if (tvTab) switchLogTab(tvTab, 'log-tv');
}

function agentsRestart(machineId, hub) {
  showConfirmDialog('Restart Agent', 'Restart telad on ' + machineId + '? Active sessions will be interrupted.', 'Restart').then(function (yes) {
    if (!yes) return;
    var wsHub = toWSURL(hub);
    goApp.RestartAgent(wsHub, machineId).then(function (resp) {
      try { var data = JSON.parse(resp); } catch (e) {}
      if (data && data.error) { showError('Restart failed: ' + data.error); return; }
      tvLog('Restart requested for ' + machineId);
    }).catch(function (err) { showError('Restart failed: ' + err); });
  });
}

function agentUpdateStatus(msg) {
  var el = document.getElementById('agent-update-status');
  if (el) el.textContent = msg;
}

function agentsUpdate(machineId, hub) {
  showConfirmDialog('Update Agent', 'Download and install the latest telad on ' + machineId + '? The agent will restart after updating.', 'Update').then(function (yes) {
    if (!yes) return;
    var wsHub = toWSURL(hub);
    var btn = document.getElementById('agent-update-btn');
    if (btn) btn.disabled = true;
    agentUpdateStatus('Sending update request...');
    tvLog('Updating telad on ' + machineId + '...');

    goApp.UpdateAgent(wsHub, machineId, '').then(function (resp) {
      try { var data = JSON.parse(resp); } catch (e) {}
      if (data && data.error) {
        agentUpdateStatus('');
        if (btn) btn.disabled = false;
        showError('Update failed: ' + data.error);
        return;
      }
      var msg = (data && data.message) || '';
      tvLog('Update: ' + (msg || 'requested for ' + machineId));

      if (msg.indexOf('already running') === 0) {
        agentUpdateStatus(msg);
        if (btn) btn.disabled = false;
        return;
      }

      // Extract target version from "updating to v0.4.0".
      var targetVer = '';
      var verMatch = msg.match(/updating to (\S+)/);
      if (verMatch) targetVer = verMatch[1];

      agentUpdateStatus('Agent is downloading update and restarting...');
      pollAgentOnline(wsHub, machineId, hub, targetVer, 0);
    }).catch(function (err) {
      agentUpdateStatus('');
      if (btn) btn.disabled = false;
      showError('Update failed: ' + err);
    });
  });
}

function pollAgentOnline(wsHub, machineId, hub, targetVer, attempt) {
  if (attempt > 30) {
    agentUpdateStatus('Agent did not come back online.');
    tvLog(machineId + ': agent did not come back after update');
    var btn = document.getElementById('agent-update-btn');
    if (btn) btn.disabled = false;
    return;
  }
  setTimeout(function () {
    goApp.GetAgentList().then(function (agents) {
      var agent = (agents || []).find(function (a) { return a.id === machineId; });
      var ver = (agent && agent.version) || '';
      if (agent && agent.online && ver && (!targetVer || ver === targetVer)) {
        agentUpdateStatus('Updated to ' + ver);
        tvLog(machineId + ': updated to ' + ver);
        var btn = document.getElementById('agent-update-btn');
        if (btn) btn.disabled = false;
        // Refresh sidebar and detail with fresh data.
        agentsData = agents;
        agentsRenderSidebar();
        var fresh = agents.find(function (a) { return a.id === machineId; });
        if (fresh) agentsShowDetail(fresh);
      } else {
        agentUpdateStatus('Waiting for agent to restart... (' + (attempt + 1) + ')');
        pollAgentOnline(wsHub, machineId, hub, targetVer, attempt + 1);
      }
    }).catch(function () {
      agentUpdateStatus('Waiting for agent to restart... (' + (attempt + 1) + ')');
      pollAgentOnline(wsHub, machineId, hub, targetVer, attempt + 1);
    });
  }, 2000);
}

// Legacy helpers removed -- agentsShowDetail now uses setting-card classes directly.

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
var filesCurrentAllowDelete = false;
var filesNavHistory = [];
var filesSelectedIndices = new Set();
var filesLastClickedIndex = -1;
var filesCurrentEntries = [];
var filesHideDotfiles = true;
var filesSortCol = 'name';        // current sort column: name, modified, type, size
var filesSortAsc = true;          // sort direction
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

function toggleHideDotfiles(hide) {
  filesHideDotfiles = hide;
  goApp.GetSettings().then(function (s) {
    s.hideDotfiles = hide;
    goApp.SaveSettings(JSON.stringify(s));
  });
  if (filesView === 'files') {
    filesRenderEntries();
  }
}

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
      document.getElementById('files-status').innerHTML = '<span id="files-status-counts"></span>';
      return;
    }

    var capabilitiesPromise;
    if (state.connected) {
      capabilitiesPromise = goApp.GetMachineCapabilities().catch(function (err) {
        tvLog('Capabilities fetch failed: ' + err);
        return {};
      });
    } else {
      filesMachineCapabilities = {};
      capabilitiesPromise = Promise.resolve({});
    }
    capabilitiesPromise.then(function (caps) {
      try {
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
            badge = '<span class="cap-tags">';
            if (fs.writable) {
              badge += '<span class="cap-tag cap-tag-yes"><span class="cap-dot cap-dot-yes"></span> Writable</span>';
              if (fs.allowDelete) {
                badge += '<span class="cap-tag cap-tag-yes"><span class="cap-dot cap-dot-yes"></span> Delete</span>';
              } else {
                badge += '<span class="cap-tag cap-tag-no"><span class="cap-dot cap-dot-no"></span> No delete</span>';
              }
            } else {
              badge += '<span class="cap-tag cap-tag-no"><span class="cap-dot cap-dot-no"></span> Read only</span>';
            }
            if (fs.maxFileSize) {
              badge += '<span class="cap-tag cap-tag-info">Max: ' + formatFileSize(fs.maxFileSize) + '</span>';
            }
            if (fs.blockedExtensions && fs.blockedExtensions.length > 0) {
              badge += '<span class="cap-tag cap-tag-info">Blocked: ' + escHtml(fs.blockedExtensions.join(', ')) + '</span>';
            }
            badge += '</span>';
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
      var countText = machineList.length + ' machine' + (machineList.length !== 1 ? 's' : '');
      document.getElementById('files-status').innerHTML = '<span id="files-status-counts">' + countText + '</span>';
      } catch (renderErr) {
        tvLog('Machine list render error: ' + renderErr);
      }
    }).catch(function (err) {
      tvLog('Machine list error: ' + err);
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
      var countText = machineList.length + ' machine' + (machineList.length !== 1 ? 's' : '');
      document.getElementById('files-status').innerHTML = '<span id="files-status-counts">' + countText + '</span>';
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

  // Determine writable and delete permissions from cached capabilities
  var mc = filesMachineCapabilities[name];
  var fs = mc && mc.fileShare;
  filesCurrentWritable = !!(fs && fs.enabled && fs.writable);
  filesCurrentAllowDelete = !!(fs && fs.enabled && fs.writable && fs.allowDelete);

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

  var pageSize = 50;
  function fetchPage(offset, accumulated) {
    var req = JSON.stringify({op: 'list', path: path, offset: offset, limit: pageSize});
    goApp.FileShareRequest(machine, req).then(function (respJSON) {
      if (gen !== filesListGeneration) return;
      try {
        var resp = JSON.parse(respJSON);
      } catch (e) {
        tvLog('Invalid file list response: ' + String(respJSON).substring(0, 200));
        listEl.innerHTML = '<div class="files-empty">Invalid response from server. The tunnel may not be ready yet. Try clicking Refresh.</div>';
        return;
      }
      if (!resp.ok) {
        listEl.innerHTML = '<div class="files-empty">' + escHtml(resp.error) + '</div>';
        return;
      }

      var entries = accumulated.concat(resp.entries || []);
      var total = resp.total || 0;
      if (total > 0 && entries.length < total) {
        // More pages to fetch
        fetchPage(offset + (resp.entries || []).length, entries);
        return;
      }

      filesCurrentPath = path;
      filesCurrentEntries = entries;
      filesSortEntries();
      filesRenderEntries();
      filesUpdateStatusBar();
      filesUpdateActionButtons();
    }).catch(function (err) {
      if (gen !== filesListGeneration) return;
      listEl.innerHTML = '<div class="files-empty">' + escHtml(String(err)) + '</div>';
    });
  }
  fetchPage(0, []);
}

function filesSortEntries() {
  filesCurrentEntries.sort(function (a, b) {
    // Directories always group together: dirs first when ascending, last when descending
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    var cmp = 0;
    switch (filesSortCol) {
      case 'name':
        cmp = a.name.localeCompare(b.name);
        break;
      case 'modified':
        cmp = (a.modTime || '').localeCompare(b.modTime || '');
        break;
      case 'type':
        var extA = a.isDir ? '' : (a.name.indexOf('.') !== -1 ? a.name.split('.').pop().toLowerCase() : '');
        var extB = b.isDir ? '' : (b.name.indexOf('.') !== -1 ? b.name.split('.').pop().toLowerCase() : '');
        cmp = extA.localeCompare(extB);
        if (cmp === 0) cmp = a.name.localeCompare(b.name);
        break;
      case 'size':
        cmp = (a.size || 0) - (b.size || 0);
        if (cmp === 0) cmp = a.name.localeCompare(b.name);
        break;
    }
    return filesSortAsc ? cmp : -cmp;
  });
}

function filesSortBy(col) {
  if (filesSortCol === col) {
    filesSortAsc = !filesSortAsc;
  } else {
    filesSortCol = col;
    // Default descending for date and size (newest/largest first)
    filesSortAsc = (col !== 'modified' && col !== 'size');
  }
  // Update arrow indicators
  ['name', 'modified', 'type', 'size'].forEach(function (c) {
    var arrow = document.getElementById('fh-arrow-' + c);
    if (!arrow) return;
    if (c === col) {
      arrow.classList.remove('hidden');
      arrow.innerHTML = filesSortAsc ? '&#x25B2;' : '&#x25BC;';
    } else {
      arrow.classList.add('hidden');
    }
  });
  filesClearSelection();
  filesSortEntries();
  filesRenderEntries();
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
    if (filesHideDotfiles && entry.name.charAt(0) === '.') return;
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
  document.getElementById('files-btn-delete').disabled = !hasSelection || !filesCurrentAllowDelete;
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
    case 'access': renderHubAccess(pane); break;
    case 'tokens': renderHubTokens(pane); break;
    case 'console': renderHubConsole(pane); break;
    case 'history': renderHubHistory(pane); break;
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
      if (hi.version) html += '<div class="settings-row"><div class="settings-label">Version</div><div class="settings-value">' + versionBadge(hi.version) + '</div></div>';
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

    // Management
    if (tokenRole === 'owner' || tokenRole === 'admin') {
      // Software button starts disabled with a neutral label. loadHubChannel
      // overwrites it with the channel-aware truth from the hub's
      // GET /api/admin/update response.
      html += '<div class="settings-group" id="hub-management-card"><div class="settings-group-header">Management</div>';
      html += '<div class="settings-row"><div class="settings-label">Log output</div>'
        + '<div class="settings-value"><button class="tb-btn" onclick="hubViewLogs(\'' + escAttr(hubName) + '\')">View Logs</button></div></div>';
      html += '<div class="settings-row"><div class="settings-label">Release channel</div>'
        + '<div class="settings-value">' + renderChannelSelect('hub-channel-select', '') + ' <span id="hub-channel-status" class="tools-service-label">loading...</span></div></div>';
      html += '<div class="settings-row"><div class="settings-label">Software</div>'
        + '<div class="settings-value"><button class="tb-btn" id="hub-update-btn" disabled onclick="hubUpdate(\'' + escAttr(hub) + '\',\'' + escAttr(hubName) + '\')">Update</button>'
        + ' <span id="hub-update-status" class="tools-service-label">loading...</span></div></div>';
      html += '<div class="settings-row"><div class="settings-label">Restart</div>'
        + '<div class="settings-value"><button class="tb-btn" onclick="hubRestart(\'' + escAttr(hub) + '\',\'' + escAttr(hubName) + '\')">Restart</button></div></div>';
      html += '</div>';
    }

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

    if ((tokenRole === 'owner' || tokenRole === 'admin') && document.getElementById('hub-channel-select')) {
      loadHubChannel(hub);
    }
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
  showConfirmDialog('Delete Token', 'Delete identity "' + id + '"? This removes the token and all its ACL entries.', 'Delete').then(function (yes) {
    if (!yes) return;
    goApp.AdminDeleteToken(currentAdminHub, id).then(function () {
      renderHubTokens(document.getElementById('hubs-admin-detail'));
    });
  });
}

function rotateToken(id) {
  showConfirmDialog('Rotate Token', 'Rotate token for "' + id + '"? The old token will stop working immediately.', 'Rotate').then(function (yes) {
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

// --- Access View (unified tokens + permissions) ---

function renderHubAccess(pane) {
  var hub = currentAdminHub;
  pane.innerHTML = '<h2>Access</h2><p class="empty-hint">Loading...</p>';

  goApp.AdminListAccess(hub).then(function (raw) {
    var data;
    try { data = JSON.parse(raw); } catch (e) { pane.innerHTML = '<p class="empty-hint">Invalid response.</p>'; return; }
    if (data.error) { pane.innerHTML = '<p class="empty-hint">' + escHtml(data.error) + '</p>'; return; }

    var entries = data.access || [];

    var html = '<h2>Access</h2>'
      + '<p class="hint" style="margin-bottom:16px;">Each identity and its effective per-machine permissions. Owner and admin tokens have implicit access to all machines.</p>';

    if (entries.length === 0) {
      html += '<p class="empty-hint">No access entries.</p>';
    } else {
      entries.forEach(function (entry) {
        var roleClass = entry.role === 'owner' ? 'owner' : entry.role === 'admin' ? 'admin' : '';
        html += '<div class="setting-card"><div class="setting-card-title-row">'
          + '<div><strong>' + escHtml(entry.id) + '</strong>'
          + ' <span class="pill ' + roleClass + '">' + escHtml(entry.role) + '</span>'
          + ' <span class="access-token-preview">' + escHtml(entry.tokenPreview || '') + '</span></div>';

        if (entry.role !== 'owner') {
          html += '<button type="button" class="tb-btn" onclick="accessRename(\'' + escAttr(entry.id) + '\')">Rename</button>';
        }

        html += '</div>';

        if (entry.role === 'owner' || entry.role === 'admin') {
          html += '<div class="setting-card-desc">All machines (implicit)</div>';
        } else if (entry.role === 'viewer') {
          html += '<div class="setting-card-desc">View-only (no machine permissions)</div>';
        } else if (!entry.machines || entry.machines.length === 0) {
          html += '<div class="setting-card-desc">No machine permissions granted.</div>';
        } else {
          entry.machines.forEach(function (m) {
            html += '<div class="access-machine-row">'
              + '<strong>' + escHtml(m.machineId) + '</strong> ';
            (m.permissions || []).forEach(function (p) {
              html += '<span class="pill">' + escHtml(p) + '</span> ';
            });
            html += '<button type="button" class="btn-danger btn-sm" onclick="accessRevokeMachine(\'' + escAttr(entry.id) + '\',\'' + escAttr(m.machineId) + '\')">Revoke</button>';
            html += '</div>';
          });
        }

        html += '</div>';
      });
    }

    html += '<div style="margin-top:16px;">'
      + '<button type="button" class="btn-primary btn-sm" onclick="accessGrantModal()">Grant Access</button>'
      + '</div>';

    pane.innerHTML = html;
  });
}

function accessRename(id) {
  showPromptDialog('Rename Identity', 'Enter a new name for "' + id + '":', id, 'Rename').then(function (newId) {
    if (!newId || newId === id) return;
    goApp.AdminRenameAccess(currentAdminHub, id, newId).then(function (raw) {
      var data; try { data = JSON.parse(raw); } catch (e) {}
      if (data && data.error) { showError('Rename failed: ' + data.error); return; }
      renderHubAccess(document.getElementById('hubs-admin-detail'));
    });
  });
}

function accessRevokeMachine(id, machineId) {
  showConfirmDialog('Revoke Access', 'Revoke all permissions for "' + id + '" on "' + machineId + '"?', 'Revoke').then(function (yes) {
    if (!yes) return;
    goApp.AdminRevokeMachineAccess(currentAdminHub, id, machineId).then(function () {
      renderHubAccess(document.getElementById('hubs-admin-detail'));
    });
  });
}

function accessGrantModal() {
  // Load tokens and machines for dropdowns
  Promise.all([
    goApp.AdminListTokens(currentAdminHub).then(function (r) { try { return JSON.parse(r); } catch (e) { return {}; } }),
    goApp.GetHubStatus(toWSURL(currentAdminHub)).then(function (s) { return s; })
  ]).then(function (results) {
    var tokenData = results[0];
    var statusData = results[1];
    var tokens = (tokenData.tokens || []).filter(function (t) { return t.role !== 'owner' && t.role !== 'admin'; });
    var machines = (statusData.machines || []).map(function (m) { return m.id; });

    var identityOpts = '<option value="">Select identity...</option>';
    tokens.forEach(function (t) {
      identityOpts += '<option value="' + escAttr(t.id) + '">' + escHtml(t.id) + ' (' + escHtml(t.role) + ')</option>';
    });

    var machineOpts = '<option value="*">* (all machines)</option>';
    machines.forEach(function (m) {
      machineOpts += '<option value="' + escAttr(m) + '">' + escHtml(m) + '</option>';
    });

    document.getElementById('grant-access-identity').innerHTML = identityOpts;
    document.getElementById('grant-access-machine').innerHTML = machineOpts;
    document.getElementById('grant-access-connect').checked = true;
    document.getElementById('grant-access-register').checked = false;
    document.getElementById('grant-access-manage').checked = false;
    document.getElementById('grant-access-modal').classList.remove('hidden');
  });
}

function submitAccessGrant(event) {
  event.preventDefault();
  var id = document.getElementById('grant-access-identity').value;
  var machine = document.getElementById('grant-access-machine').value;
  if (!id) return;

  var perms = [];
  if (document.getElementById('grant-access-connect').checked) perms.push('connect');
  if (document.getElementById('grant-access-register').checked) perms.push('register');
  if (document.getElementById('grant-access-manage').checked) perms.push('manage');
  if (perms.length === 0) return;

  goApp.AdminSetMachineAccess(currentAdminHub, id, machine, perms.join(',')).then(function (raw) {
    var data; try { data = JSON.parse(raw); } catch (e) {}
    if (data && data.error) { showError('Grant failed: ' + data.error); return; }
    closeModal('grant-access-modal');
    renderHubAccess(document.getElementById('hubs-admin-detail'));
  });
}

// --- Hub Console View ---

function renderHubConsole(pane) {
  var hub = currentAdminHub;
  var consoleUrl = hub.replace('wss://', 'https://').replace('ws://', 'http://');
  consoleUrl = consoleUrl.replace(/\/$/, '') + '/';
  pane.innerHTML = '<h2>Hub Console</h2>'
    + '<p class="section-desc">Embedded console for <strong>' + escHtml(hubNameFromUrl(hub)) + '</strong></p>'
    + '<div style="margin-bottom:8px;"><a href="' + escAttr(consoleUrl) + '" target="_blank" rel="noopener" style="font-size:0.82rem;color:var(--accent);">Open in browser</a></div>'
    + '<iframe src="' + escAttr(consoleUrl) + '" style="width:100%;height:500px;border:1px solid var(--border);border-radius:var(--radius);background:var(--bg);"></iframe>';
}

// --- Hub History View ---

function renderHubHistory(pane) {
  var hub = currentAdminHub;
  pane.innerHTML = '<h2>Connection History</h2>'
    + '<p class="section-desc">Recent sessions on <strong>' + escHtml(hubNameFromUrl(hub)) + '</strong></p>'
    + '<p class="loading">Loading...</p>';


  goApp.AdminAPICall(hub, 'GET', '/api/history', '').then(function (raw) {
    var data;
    try { data = JSON.parse(raw); } catch (e) { data = {}; }
    if (data.error) {
      pane.innerHTML = '<h2>Connection History</h2><p class="section-desc">' + escHtml(data.error) + '</p>';
      return;
    }
    var events = data.events || data || [];
    if (!Array.isArray(events)) events = [];

    var html = '<h2>Connection History</h2>'
      + '<p class="section-desc">Recent sessions on <strong>' + escHtml(hubNameFromUrl(hub)) + '</strong></p>';

    if (events.length === 0) {
      html += '<p class="empty-hint">No history available.</p>';
    } else {
      events.forEach(function (ev) {
        var time = ev.timestamp || ev.time || '';
        if (time) {
          try { time = new Date(time).toISOString().replace('T', ' ').replace(/\.\d+Z$/, 'Z'); } catch (e) {}
        }
        html += '<div class="history-item">'
          + '<span class="history-time">' + escHtml(time) + '</span>'
          + '<span class="history-event">' + escHtml(ev.event || ev.type || '') + '</span>'
          + '<span class="history-detail"> ' + escHtml(ev.machine || '') + (ev.identity ? ' (' + escHtml(ev.identity) + ')' : '') + '</span>'
          + '</div>';
      });
    }
    pane.innerHTML = html;
  });
}

// --- Hub Management ---

function removeHub(url) {
  showConfirmDialog('Remove Hub', 'Remove this hub and all its saved selections?', 'Remove').then(function (yes) {
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
  document.querySelectorAll('.hub-item, .machine-item, .profile-root, .profile-hub-header').forEach(function (e) {
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
    if (state.connected && state.attached) {
      var el = document.getElementById('log-tela');
      if (el && (el.textContent === 'Not connected.' || !el.textContent.trim())) {
        el.textContent = 'Attached to external tela. Log output is in the terminal where tela is running.';
      }
    } else if (!state.connected) {
      var el = document.getElementById('log-tela');
      if (el && el.textContent !== 'Not connected.' && !el.textContent.trim()) {
        el.textContent = 'Not connected.';
      }
    }
  });
}

// Auto-copy selected text on mouseup in log panes
(function () {
  setTimeout(function () {
    var outputs = document.querySelectorAll('.log-panel-output, .cmd-list');
    outputs.forEach(function (el) {
      el.addEventListener('mouseup', function () {
        var sel = window.getSelection();
        if (sel && sel.toString().length > 0) {
          navigator.clipboard.writeText(sel.toString()).catch(function () {});
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

    var logInput = document.getElementById('setting-logMaxLines');
    if (logInput) logInput.value = s.logMaxLines || 5000;

    // Theme radio
    var themeVal = s.theme || 'system';
    var themeRadios = document.querySelectorAll('input[name="setting-theme"]');
    themeRadios.forEach(function (r) { r.checked = r.value === themeVal; });
    applyTelaTheme(themeVal);
  });

  loadClientChannel();
}

function gatherSettings() {
  var themeRadio = document.querySelector('input[name="setting-theme"]:checked');
  // Preserve defaultProfile and binPath from current settings (they're managed in Client Settings tab now)
  var existing = {};
  try {
    var csProfile = document.getElementById('cs-default-profile');
    var csBinPath = document.getElementById('cs-binPath');
    if (csProfile) existing.defaultProfile = csProfile.value;
    if (csBinPath) {
      var val = csBinPath.value.trim();
      var def = csBinPath.getAttribute('data-default') || '';
      existing.binPath = val === def ? '' : val;
    }
  } catch (e) {}
  var logInput = document.getElementById('setting-logMaxLines');
  var logVal = logInput ? parseInt(logInput.value, 10) : 5000;
  if (isNaN(logVal) || logVal < 100) logVal = 100;

  return {
    autoConnect: document.getElementById('setting-autoConnect').checked,
    reconnectOnDrop: document.getElementById('setting-reconnectOnDrop').checked,
    confirmDisconnect: document.getElementById('setting-confirmDisconnect').checked,
    minimizeTo: 'tray',
    startMinimized: false,
    minimizeOnClose: document.getElementById('setting-minimizeOnClose').checked,
    autoCheckUpdates: document.getElementById('setting-autoCheckUpdates').checked,
    verboseDefault: document.getElementById('setting-verboseDefault').checked,
    defaultProfile: existing.defaultProfile || '',
    binPath: existing.binPath || '',
    theme: themeRadio ? themeRadio.value : 'system',
    logMaxLines: logVal
  };
}

// Apply: save settings, disable Apply buttons, stay in dialog.
function applySettings() {
  var s = gatherSettings();
  var errEl = document.getElementById('settings-error');
  errEl.classList.add('hidden');
  errEl.textContent = '';

  goApp.SaveSettings(JSON.stringify(s)).then(function () {
      settingsDirty = false;
      setSettingsButtonsDisabled(true);
      if (s.logMaxLines) logMaxLines = s.logMaxLines;
      document.getElementById('update-btn').disabled = true; document.getElementById('update-btn').title = 'No updates';
      updateDismissedForSession = false;
      updateSkippedVersion = '';
      checkForUpdate();
      refreshVersionDisplay();
    });
}

function setSettingsButtonsDisabled(disabled) {
  var apply = document.getElementById('settings-apply-btn');
  var applyClose = document.getElementById('settings-applyclose-btn');
  if (apply) apply.disabled = disabled;
  if (applyClose) applyClose.disabled = disabled;
}

// Backward-compat alias
function saveSettingsWithValidation() { applySettings(); }

var settingsDirty = false;

function markSettingsDirty() {
  settingsDirty = true;
  setSettingsButtonsDisabled(false);

  // Apply theme immediately on change (live preview)
  var themeRadio = document.querySelector('input[name="setting-theme"]:checked');
  if (themeRadio) applyTelaTheme(themeRadio.value);
}

// ── Theme ──────────────────────────────────────────────────────────

function applyTelaTheme(pref) {
  if (pref === 'system') {
    var prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    document.documentElement.setAttribute('data-theme', prefersDark ? 'dark' : 'light');
  } else {
    document.documentElement.setAttribute('data-theme', pref);
  }
}

// Listen for OS theme changes
try {
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function () {
    var themeRadio = document.querySelector('input[name="setting-theme"]:checked');
    var pref = themeRadio ? themeRadio.value : 'system';
    if (pref === 'system') applyTelaTheme('system');
  });
} catch (e) {}

// Apply & Close: save changes and close the dialog.
// Button is only enabled when dirty, so we always save here.
function closeSettings() {
  var s = gatherSettings();
  var errEl = document.getElementById('settings-error');
  errEl.classList.add('hidden');
  errEl.textContent = '';
  goApp.SaveSettings(JSON.stringify(s)).then(function () {
    settingsDirty = false;
    setSettingsButtonsDisabled(true);
    document.getElementById('update-btn').disabled = true; document.getElementById('update-btn').title = 'No updates';
    updateDismissedForSession = false;
    updateSkippedVersion = '';
    checkForUpdate();
    refreshVersionDisplay();
    toggleSettingsOverlay();
  }).catch(function (err) {
    errEl.textContent = 'Save failed: ' + err;
    errEl.classList.remove('hidden');
  });
}

// Cancel: prompt if changes were made, discard and close on confirm.
function cancelSettings() {
  if (settingsDirty) {
    showConfirmDialog('Unsaved Changes', 'You have unsaved settings changes. Discard them?', 'Discard').then(function (yes) {
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
  setSettingsButtonsDisabled(true);
  var errEl = document.getElementById('settings-error');
  if (errEl) { errEl.classList.add('hidden'); errEl.textContent = ''; }
  var pathInput = document.getElementById('setting-binPath');
  if (pathInput) pathInput.classList.remove('invalid');
  toggleSettingsOverlay();
}

// Keep saveSetting for backward compat (browse/restore call it)
function saveSetting() {
  applySettings();
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

// ── Remotes view (Hubs mode) ───────────────────────────────────────

function renderRemotesView(pane) {
  pane.innerHTML = '<h2>Remotes</h2>'
    + '<p class="section-desc">Hub directory endpoints for short name resolution. Equivalent to <code>tela remote</code>.</p>'
    + '<div id="remotes-list-pane"></div>'
    + '<div class="settings-inline-form" style="margin-top:12px;">'
    + '<input type="text" id="remote-name-input" placeholder="Name" title="Remote name" class="settings-sm-input">'
    + '<input type="text" id="remote-url-input" placeholder="Portal URL" title="Portal URL" class="settings-md-input">'
    + '<button type="button" class="tb-btn" onclick="addRemote()">Add</button>'
    + '</div>';
  refreshRemotesList();
}

function refreshRemotesList() {
  goApp.ListRemotes().then(function (remotes) {
    var el = document.getElementById('remotes-list-pane');
    if (!el) return;
    if (!remotes || remotes.length === 0) {
      el.innerHTML = '<p class="empty-hint">No remotes configured.</p>';
      return;
    }
    var html = '<table class="admin-table"><thead><tr><th>Name</th><th>URL</th><th></th></tr></thead><tbody>';
    remotes.forEach(function (r) {
      html += '<tr><td><strong>' + escHtml(r.name) + '</strong></td>'
        + '<td>' + escHtml(r.url) + '</td>'
        + '<td><button class="icon-btn danger" onclick="removeRemote(\'' + escAttr(r.name) + '\')">Remove</button></td></tr>';
    });
    html += '</tbody></table>';
    el.innerHTML = html;
  });
}

function addRemote() {
  var nameInput = document.getElementById('remote-name-input');
  var urlInput = document.getElementById('remote-url-input');
  var name = nameInput.value.trim();
  var url = urlInput.value.trim();
  if (!name || !url) return;
  goApp.AddRemote(name, url).then(function () {
    nameInput.value = '';
    urlInput.value = '';
    refreshRemotesList();
  }).catch(function (err) {
    tvLog('Add remote failed: ' + err);
  });
}

function removeRemote(name) {
  goApp.RemoveRemote(name).then(function () {
    refreshRemotesList();
  }).catch(function (err) {
    tvLog('Remove remote failed: ' + err);
  });
}

// ── Credentials view (Hubs mode) ──────────────────────────────────

function renderCredentialsView(pane) {
  pane.innerHTML = '<h2>Stored Credentials</h2>'
    + '<p class="section-desc">Hub tokens stored in the local credential file. Equivalent to <code>tela login</code> / <code>tela logout</code>.</p>'
    + '<div id="credentials-list-pane"></div>'
    + '<div style="margin-top:12px;"><button type="button" class="tb-btn tb-delete-btn" onclick="clearAllCredentials()">Clear All</button></div>';
  refreshCredentialsList();
}

function refreshCredentialsList() {
  goApp.ListCredentials().then(function (creds) {
    var el = document.getElementById('credentials-list-pane');
    if (!el) return;
    if (!creds || creds.length === 0) {
      el.innerHTML = '<p class="empty-hint">No stored credentials.</p>';
      return;
    }
    var html = '<table class="admin-table"><thead><tr><th>Hub</th><th>Identity</th><th></th></tr></thead><tbody>';
    creds.forEach(function (c) {
      var identity = c.identity || '';
      html += '<tr><td>' + escHtml(c.hubUrl) + '</td>'
        + '<td>' + escHtml(identity) + '</td>'
        + '<td><button class="icon-btn danger" onclick="removeCredential(\'' + escAttr(c.hubUrl) + '\')">Remove</button></td></tr>';
    });
    html += '</tbody></table>';
    el.innerHTML = html;
  });
}

function removeCredential(hubUrl) {
  showConfirmDialog('Remove Credential', 'Remove stored token for ' + hubUrl + '?', 'Remove').then(function (yes) {
    if (!yes) return;
    goApp.RemoveCredential(hubUrl).then(function () {
      refreshCredentialsList();
      refreshAll();
    }).catch(function (err) {
      tvLog('Remove credential failed: ' + err);
    });
  });
}

function clearAllCredentials() {
  showConfirmDialog('Clear All Credentials', 'This will delete all stored hub tokens. You will need to re-authenticate with each hub.', 'Delete All').then(function (yes) {
    if (!yes) return;
    goApp.ClearCredentialStore().then(function () {
      refreshCredentialsList();
      refreshAll();
    }).catch(function (err) {
      tvLog('Clear credentials failed: ' + err);
    });
  });
}

// ── Service management ────────────────────────────────────────────

function refreshServiceStatus() {
  goApp.GetServiceStatus().then(function (raw) {
    var el = document.getElementById('cs-service-status');
    if (!el) return;
    var s = (raw || '').toLowerCase();
    var installed = s.indexOf('installed: true') !== -1;
    var running = s.indexOf('running: true') !== -1;
    var notInstalled = s.indexOf('not installed') !== -1 || s === '';

    if (notInstalled) {
      el.innerHTML = '<span style="color:var(--text-muted);">Not installed</span>';
    } else if (running) {
      el.innerHTML = '<span class="bin-dot bin-dot-ok"></span> Running';
    } else if (installed) {
      el.innerHTML = '<span class="bin-dot bin-dot-missing"></span> Installed (stopped)';
    } else {
      el.innerHTML = '<span style="color:var(--text-muted);">' + escHtml(raw) + '</span>';
    }

    // Enable/disable buttons
    var installBtn = document.getElementById('svc-install-btn');
    var startBtn = document.getElementById('svc-start-btn');
    var stopBtn = document.getElementById('svc-stop-btn');
    var uninstallBtn = document.getElementById('svc-uninstall-btn');
    if (installBtn) installBtn.disabled = installed || running;
    if (startBtn) startBtn.disabled = !installed || running;
    if (stopBtn) stopBtn.disabled = !running;
    if (uninstallBtn) uninstallBtn.disabled = !installed && !running;
  });
}

function installService() {
  goApp.InstallAsService().then(function (msg) {
    tvLog('Service installed: ' + msg);
    refreshServiceStatus();
  }).catch(function (err) {
    tvLog('Install service failed: ' + err);
  });
}

function uninstallService() {
  showConfirmDialog('Uninstall Service', 'Remove the tela client system service?', 'Uninstall').then(function (yes) {
    if (!yes) return;
    goApp.UninstallService().then(function (msg) {
      tvLog('Service uninstalled: ' + msg);
      refreshServiceStatus();
    }).catch(function (err) {
      tvLog('Uninstall service failed: ' + err);
    });
  });
}

function startService() {
  goApp.ServiceStart().then(function (msg) {
    tvLog('Service started: ' + msg);
    refreshServiceStatus();
  }).catch(function (err) {
    tvLog('Start service failed: ' + err);
  });
}

function stopService() {
  goApp.ServiceStop().then(function (msg) {
    tvLog('Service stopped: ' + msg);
    refreshServiceStatus();
  }).catch(function (err) {
    tvLog('Stop service failed: ' + err);
  });
}

// ── Mount (WebDAV) process management ─────────────────────────────

function refreshMountStatus() {
  goApp.IsMountRunning().then(function (running) {
    // Client Settings card
    var el = document.getElementById('mount-status');
    if (el) {
      if (running) {
        el.innerHTML = '<span class="bin-dot bin-dot-ok"></span> Running';
      } else {
        el.innerHTML = '<span style="color:var(--text-muted);">Not running</span>';
      }
    }
    var startBtn = document.getElementById('mount-start-btn');
    var stopBtn = document.getElementById('mount-stop-btn');
    if (startBtn) startBtn.disabled = running;
    if (stopBtn) stopBtn.disabled = !running;

    // Topbar button
    updateMountButtonState();
  });
}

function toggleMount() {
  goApp.IsMountRunning().then(function (running) {
    if (running) {
      stopMountProcess();
    } else {
      startMountProcess();
    }
  });
}

function startMountProcess() {
  goApp.StartMount().then(function (msg) {
    tvLog('Mount started: ' + msg);
    refreshMountStatus();
  }).catch(function (err) {
    tvLog('Start mount failed: ' + err);
  });
}

function stopMountProcess() {
  goApp.StopMount().then(function () {
    tvLog('Mount stopped');
    refreshMountStatus();
  }).catch(function (err) {
    tvLog('Stop mount failed: ' + err);
  });
}

// ── Client Settings tab ────────────────────────────────────────────

var csDirty = false;

function markCSDirty() {
  csDirty = true;
  var undoBtn = document.getElementById('cs-undo-btn');
  var saveBtn = document.getElementById('cs-save-btn');
  if (undoBtn) undoBtn.disabled = false;
  if (saveBtn) saveBtn.disabled = false;
  // Also mark the settings dialog dirty so Close/Cancel know
  markSettingsDirty();
}

function clearCSDirty() {
  csDirty = false;
  var undoBtn = document.getElementById('cs-undo-btn');
  var saveBtn = document.getElementById('cs-save-btn');
  if (undoBtn) undoBtn.disabled = true;
  if (saveBtn) saveBtn.disabled = true;
}

function undoClientSettings() {
  refreshClientSettings();
  clearCSDirty();
}

function saveClientSettings() {
  applySettings();
  clearCSDirty();
}

function refreshClientSettings() {
  // Populate default profile dropdown
  goApp.GetSettings().then(function (s) {
    var sel = document.getElementById('cs-default-profile');
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
    // Binary path
    var pathInput = document.getElementById('cs-binPath');
    if (pathInput) {
      if (s.binPath) {
        pathInput.value = s.binPath;
      } else {
        goApp.GetDefaultBinPath().then(function (p) {
          pathInput.value = p;
          pathInput.setAttribute('data-default', p);
        });
      }
    }
  });
  // Installed tools
  refreshClientToolVersions();
  refreshServiceStatus();
  refreshMountStatus();
}

function refreshClientToolVersions() {
  var el = document.getElementById('cs-bin-status');
  if (el) renderToolsTable(el, true);
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
  return hubNameFromUrl(url);
}

function copyToClipboard(text) {
  if (navigator.clipboard) {
    navigator.clipboard.writeText(text);
  }
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

// ── Field validation ──────────────────────────────────────────────

// Mark a field as invalid with a dismissable error message.
function setFieldError(inputId, message) {
  var input = document.getElementById(inputId);
  if (!input) return;
  input.classList.add('invalid');
  // Remove any existing error for this field
  clearFieldError(inputId);
  // Insert error element after the input
  var err = document.createElement('div');
  err.className = 'field-error';
  err.id = inputId + '-error';
  err.innerHTML = '<span>' + escHtml(message) + '</span>'
    + '<span class="field-error-dismiss" onclick="clearFieldError(\'' + escAttr(inputId) + '\')">&times;</span>';
  input.parentNode.insertBefore(err, input.nextSibling);
}

// Clear the error state from a field.
function clearFieldError(inputId) {
  var input = document.getElementById(inputId);
  if (input) input.classList.remove('invalid');
  var err = document.getElementById(inputId + '-error');
  if (err) err.remove();
}

// Validate all fields with data-required when their parent form is saved.
// Returns true if all valid, false if any errors.
function validateRequiredFields(containerEl) {
  var valid = true;
  var inputs = containerEl.querySelectorAll('[data-required]');
  for (var i = 0; i < inputs.length; i++) {
    var input = inputs[i];
    if (input.disabled) continue;
    var val = (input.value || '').trim();
    if (!val) {
      setFieldError(input.id, input.getAttribute('data-required'));
      valid = false;
    } else {
      clearFieldError(input.id);
    }
  }
  return valid;
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

// DEL key in Files tab triggers delete on selected items
document.addEventListener('keydown', function (e) {
  if (e.key !== 'Delete') return;
  // Only when Files tab is active, files are shown, and no modal is open
  var filesPane = document.getElementById('tab-files');
  if (!filesPane || filesPane.classList.contains('hidden')) return;
  if (!document.getElementById('generic-dialog-overlay').classList.contains('hidden')) return;
  if (filesSelectedIndices.size === 0) return;
  if (!filesCurrentAllowDelete) return;
  e.preventDefault();
  filesDeleteSelected();
});

function toWSURL(url) {
  if (url.indexOf('https://') === 0) return 'wss://' + url.substring(8);
  if (url.indexOf('http://') === 0) return 'ws://' + url.substring(7);
  if (url.indexOf('wss://') !== 0 && url.indexOf('ws://') !== 0) return 'wss://' + url;
  return url;
}
