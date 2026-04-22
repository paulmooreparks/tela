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

// Brand-link click target per TDL: navigate to the current mode's
// default ("home") tab. For Clients mode that's Status; for
// Infrastructure mode it's Hubs.
function goHome() {
  var mode = currentMode || 'clients';
  var bar = document.getElementById(tabBars[mode]);
  if (!bar) return;
  var firstTab = bar.querySelector('.main-tab');
  if (firstTab) firstTab.click();
}

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
  if (name === 'updates') refreshUpdatesTab();
  if (name === 'agents') agentsRefresh();
  if (name === 'hubs') refreshHubsTab();
  if (name === 'access') refreshAccessTab();
  if (name === 'remotes') refreshRemotesList();
  if (name === 'credentials') refreshCredentialsList();
  if (name === 'client-settings') refreshClientSettings();
}

// switchTabByName drives a tab change from a programmatic source (a
// link in another pane, a notification, etc.) instead of a click on
// the actual tab button. It mirrors switchTab's effect by locating
// the right .main-tab in whichever tab bar is currently visible and
// dispatching a click. The visible tab bar test handles the
// Clients/Infrastructure mode split: the same button id can appear
// in both bars, but only one is in the DOM tree of a visible bar at
// a time.
function switchTabByName(name) {
  var bars = document.querySelectorAll('.main-tab-bar');
  for (var i = 0; i < bars.length; i++) {
    if (bars[i].classList.contains('hidden')) continue;
    var btns = bars[i].querySelectorAll('.main-tab');
    for (var j = 0; j < btns.length; j++) {
      var btn = btns[j];
      var oc = btn.getAttribute('onclick') || '';
      if (oc.indexOf("switchTab('" + name + "'") !== -1) {
        btn.click();
        return;
      }
    }
  }
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
    if (collapsed) {
      panel.style.height = '';
    } else if (s.logPanelHeight > 0) {
      panel.style.height = s.logPanelHeight + 'px';
    }
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
          + '<span class="dot dot-online"></span>' + escHtml(h) + '</button>';
      });
    }
    if (agents.length > 0) {
      html += '<div class="attach-log-section">Agents</div>';
      agents.forEach(function (a) {
        var dotClass = a.online ? 'dot-online' : 'dot-offline';
        var label = a.displayName || a.id;
        html += '<button class="attach-log-item" onclick="agentsViewLogs(\'' + escAttr(a.id) + '\',\'' + escAttr(a.hub) + '\');this.closest(\'.attach-log-popover\').remove()">'
          + '<span class="log-dot ' + dotClass + '"></span>' + escHtml(label) + '</button>';
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
    tab.innerHTML = '<span class="dot dot-offline" id="' + paneId + '-dot"></span>'
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

  var dot = document.getElementById(paneId + '-dot');
  if (dot) dot.className = 'dot dot-degraded';

  goApp.GetHubLogs(hubName, 500).then(function (resp) {
    try { var data = JSON.parse(resp); } catch (e) {
      document.getElementById(paneId).textContent = 'Error: invalid response\n';
      if (dot) dot.className = 'dot dot-offline';
      return;
    }
    if (data.error) {
      document.getElementById(paneId).textContent = 'Error: ' + data.error + '\n';
      if (dot) dot.className = 'dot dot-offline';
      return;
    }
    var lines = data.lines || [];
    var el = document.getElementById(paneId);
    el.textContent = lines.join('\n') + '\n';
    logAutoScroll(el);
    if (dot) dot.className = 'dot dot-online';
  }).catch(function (err) {
    document.getElementById(paneId).textContent = 'Failed: ' + err + '\n';
    if (dot) dot.className = 'dot dot-offline';
  });
}

function hubUpdateStatus(msg) {
  var el = document.getElementById('hub-update-status');
  if (el) el.textContent = msg;
}

// ── Release channels ──────────────────────────────────────────────

// channelSources is the authoritative list of release channels available in
// every channel dropdown. It is populated at startup by loadChannelSources()
// and contains built-in channels (dev, beta, stable) followed by any custom
// channels the user has added in Application Settings.
var channelSources = [
  { name: 'dev', manifestBase: '' },
  { name: 'beta', manifestBase: '' },
  { name: 'stable', manifestBase: '' }
];

// findChannelManifestBase returns the manifestBase for the given channel name,
// or '' if it is a built-in channel or not found.
function findChannelManifestBase(name) {
  for (var i = 0; i < channelSources.length; i++) {
    if (channelSources[i].name === name) return channelSources[i].manifestBase || '';
  }
  return '';
}

// loadChannelSources fetches the channel list from the Go backend and updates
// the channelSources global. Optionally calls onDone when complete.
function loadChannelSources(onDone) {
  goApp.GetChannelSources().then(function (raw) {
    try {
      var sources = JSON.parse(raw);
      if (Array.isArray(sources) && sources.length > 0) channelSources = sources;
    } catch (e) {}
    if (onDone) onDone();
  }).catch(function () { if (onDone) onDone(); });
}

// renderChannelSelect emits the HTML for a channel dropdown with the
// given id. selected is the channel name to pre-select ('' leaves it on
// dev so the UI is never blank before load completes).
// Uses the channelSources global so custom channels appear automatically.
function renderChannelSelect(id, selected) {
  var sel = selected || 'dev';
  var html = '<select id="' + escAttr(id) + '" class="tb-select">';
  for (var i = 0; i < channelSources.length; i++) {
    var c = channelSources[i].name;
    html += '<option value="' + escAttr(c) + '"' + (c === sel ? ' selected' : '') + '>' + escHtml(c) + '</option>';
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
  // updateAvailable=false has two cases:
  //   1. cur === latest: genuinely up to date.
  //   2. cur != latest: this binary is from a different release line
  //      (e.g. local.51 on a dev channel where dev's HEAD is dev.11),
  //      so the semver comparison says no update is needed but the
  //      versions are not actually equal. Showing only "cur (latest)"
  //      hides the channel HEAD and confuses the operator. Make it
  //      explicit when current != latest.
  if (cur === latest) return cur + ' (latest)';
  return 'currently ' + cur + ' (channel HEAD ' + latest + ')';
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
  } else if (cur === latest) {
    btn.textContent = 'Up to date';
    btn.disabled = true;
    statusEl.textContent = cur + ' (latest)';
  } else {
    // Different release line: cur is "newer" than channel HEAD by semver
    // (e.g. local.51 vs dev.11), so no upgrade is offered, but the two
    // are not equal. Surface the channel HEAD so the operator can see
    // what's actually on the channel they switched to.
    btn.textContent = 'Up to date';
    btn.disabled = true;
    statusEl.textContent = 'currently ' + cur + ' (channel HEAD ' + latest + ')';
  }
}

// refreshHubManagement re-fetches the hub's channel status (which drives
// the Release channel dropdown, Software button label, and version hints)
// and its Channel Sources list. Wired to the Refresh button in the hub's
// Management header.
function refreshHubManagement(hubURL) {
  loadHubChannel(hubURL);
  loadHubChannelSources(hubURL);
}

// refreshAgentManagement does the same for a remote agent, re-running the
// update-status and channel-sources-list mgmt actions so a just-updated
// agent reflects its new version and sources without a manual reload.
function refreshAgentManagement(hubURL, machineID) {
  loadAgentChannel(hubURL, machineID);
  loadAgentChannelSources(hubURL, machineID);
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
    // Repopulate from the hub's own sources (built-ins + hub's update.sources)
    // before applying the current channel selection. The hub is the authority
    // on what channels it can resolve; we never substitute the client's list.
    // GetHubSources may return {"error":"..."} on pre-channel-sources hubs;
    // treat that as an empty list and let the fallback below synthesize an
    // entry for whatever channel the hub reports as active.
    goApp.GetHubSources(hubURL).then(function (raw2) {
      var parsed = null;
      try { parsed = JSON.parse(raw2); } catch (e) {}
      var customs = Array.isArray(parsed) ? parsed : [];
      sel.innerHTML = '';
      ['dev', 'beta', 'stable'].forEach(function (n) {
        var opt = document.createElement('option');
        opt.value = n; opt.textContent = n;
        sel.appendChild(opt);
      });
      customs.forEach(function (src) {
        var opt = document.createElement('option');
        opt.value = src.name; opt.textContent = src.name;
        sel.appendChild(opt);
      });
      if (info.channel) {
        // Honor whatever the hub reports, even if it's not in the dropdown
        // (e.g. a custom channel removed via the CLI but still active, or a
        // pre-channel-sources hub where the sources endpoint 404s).
        var found = Array.prototype.some.call(sel.options, function (o) { return o.value === info.channel; });
        if (!found) {
          var opt = document.createElement('option');
          opt.value = info.channel; opt.textContent = info.channel + ' (active, not in sources)';
          sel.appendChild(opt);
        }
        sel.value = info.channel;
      }
    });
    status.textContent = formatChannelStatus(info);
    applySoftwareButton('hub-update-btn', 'hub-update-status', info);
    sel.onchange = function () {
      var newCh = sel.value;
      showConfirmDialog('Switch Channel', 'Switch this hub to the ' + newCh + ' channel? New updates will follow the ' + newCh + ' release line.', 'Switch').then(function (yes) {
        if (!yes) { sel.value = info.channel || 'dev'; return; }
        status.textContent = 'switching to ' + newCh + '...';
        // No manifestBase override -- the hub resolves from its own
        // update.sources map for custom channels and from baked-in
        // DefaultBases for dev/beta/stable.
        goApp.SetHubChannel(hubURL, newCh, '').then(function (r) {
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
    // Repopulate the dropdown from the union of: built-ins, the agent's own
    // sources, and the hub's sources marked as suggestions. The agent is the
    // authority on what channels it can resolve; hub-only suggestions are
    // shown but require an explicit push before the agent will accept them.
    Promise.all([
      goApp.GetAgentSources(hubURL, machineID).then(function (r) { try { return JSON.parse(r); } catch (e) { return null; } }),
      goApp.GetHubSources(hubURL).then(function (r) { try { return JSON.parse(r); } catch (e) { return null; } })
    ]).then(function (results) {
      var agentSrcs = Array.isArray(results[0]) ? results[0] : [];
      var hubSrcs = Array.isArray(results[1]) ? results[1] : [];
      var agentNames = {};
      agentSrcs.forEach(function (s) { agentNames[s.name] = true; });
      sel.innerHTML = '';
      ['dev', 'beta', 'stable'].forEach(function (n) {
        var opt = document.createElement('option');
        opt.value = n; opt.textContent = n;
        sel.appendChild(opt);
      });
      agentSrcs.forEach(function (src) {
        var opt = document.createElement('option');
        opt.value = src.name; opt.textContent = src.name;
        opt.setAttribute('data-base', src.manifestBase || '');
        sel.appendChild(opt);
      });
      hubSrcs.forEach(function (src) {
        if (agentNames[src.name]) return;
        var opt = document.createElement('option');
        opt.value = src.name;
        opt.textContent = src.name + ' (suggest from hub)';
        opt.setAttribute('data-base', src.manifestBase || '');
        opt.setAttribute('data-from-hub', '1');
        sel.appendChild(opt);
      });
      if (info.channel) {
        var found = Array.prototype.some.call(sel.options, function (o) { return o.value === info.channel; });
        if (!found) {
          var opt = document.createElement('option');
          opt.value = info.channel;
          opt.textContent = info.channel + ' (active, not in sources)';
          sel.appendChild(opt);
        }
        sel.value = info.channel;
      }
    });
    status.textContent = formatChannelStatus(info);
    applySoftwareButton('agent-update-btn', 'agent-update-status', info);
    // Cache per-agent update status so the sidebar can show TDL version glyphs.
    // Only cache when we have a latestVersion to compare against.
    if (info.latestVersion && info.currentVersion) {
      agentStatusCache[machineID] = {
        version: info.currentVersion,
        status: info.updateAvailable ? 'outdated' : 'current'
      };
      agentsRenderSidebar();
    }
    sel.onchange = function () {
      var newCh = sel.value;
      var opt = sel.options[sel.selectedIndex];
      var fromHub = opt && opt.getAttribute('data-from-hub') === '1';
      var hubBase = opt ? (opt.getAttribute('data-base') || '') : '';
      var doSwitch = function () {
        status.textContent = 'switching to ' + newCh + '...';
        goApp.SetAgentChannel(hubURL, machineID, newCh, '').then(function (r) {
          var res = {};
          try { res = JSON.parse(r); } catch (e) { }
          if (res.error) { status.textContent = 'error: ' + res.error; return; }
          loadAgentChannel(hubURL, machineID);
        });
      };
      if (fromHub) {
        // Suggestion picked: push the source to the agent first, then switch.
        showConfirmDialog('Push Source to Agent',
          'The "' + newCh + '" channel exists on this hub but not on agent ' + machineID + '. Push the source to the agent and switch?',
          'Push and switch'
        ).then(function (yes) {
          if (!yes) { sel.value = info.channel || 'dev'; return; }
          status.textContent = 'pushing source...';
          goApp.SetAgentSource(hubURL, machineID, newCh, hubBase).then(function (r) {
            var res = {};
            try { res = JSON.parse(r); } catch (e) { }
            if (res.error) { status.textContent = 'error: ' + res.error; sel.value = info.channel || 'dev'; return; }
            doSwitch();
          });
        });
        return;
      }
      showConfirmDialog('Switch Agent Channel', 'Switch ' + machineID + ' to the ' + newCh + ' channel?', 'Switch').then(function (yes) {
        if (!yes) { sel.value = info.channel || 'dev'; return; }
        doSwitch();
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
      goApp.SetClientChannel(newCh, findChannelManifestBase(newCh)).then(function (r) {
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
    badge.className = 'status status-offline';
    badge.innerHTML = '<span class="status-dot"></span>Disconnected';
    container.innerHTML = '<p class="empty-hint">No services selected. Go to <strong>Profiles</strong> to select hubs, machines, and services.</p>';
    return;
  }

  // Single Go call -- not nested inside anything
  goApp.GetConnectionState().then(function (state) {
    if (state.connected && state.attached) {
      badge.className = 'status status-online';
      badge.innerHTML = '<span class="status-dot"></span>Attached (external tela)';
    } else if (state.connected) {
      badge.className = 'status status-online';
      badge.innerHTML = '<span class="status-dot"></span>Connected &middot; PID ' + escHtml(String(state.pid));
    } else {
      badge.className = 'status status-offline';
      badge.innerHTML = '<span class="status-dot"></span>Disconnected';
    }

    var html = '';

    Object.keys(groups).forEach(function (gk) {
      var g = groups[gk];
      html += '<div class="settings-group">'
        + '<div class="settings-group-header">'
        + escHtml(g.machine)
        + '<span class="status-hub-label">on ' + escHtml(g.hubName) + '</span>'
        + '</div>';

      g.services.forEach(function (svc) {
        var indicatorClass = 'dot-offline';
        var statusText = 'Not connected';
        var localClass = 'inactive';

        if (state.connected) {
          var tunnelKey = g.machine + ':' + svc.localPort;
          var tunnelCount = activeTunnels[tunnelKey] || 0;
          var portFound = false;
          var machineConnected = false;

          // Determine if this service has a bound listener via the control API.
          if (boundServicesCache && boundServicesCache.length > 0) {
            for (var si = 0; si < boundServicesCache.length; si++) {
              var bs = boundServicesCache[si];
              if (bs.machine === g.machine) {
                machineConnected = true;
                if (parseInt(bs.local) === parseInt(svc.localPort)) {
                  portFound = true;
                  break;
                }
              }
            }
          }

          if (portFound) {
            if (tunnelCount > 0) {
              indicatorClass = 'dot-online';
              statusText = 'Active (' + tunnelCount + ')';
              localClass = 'active';
            } else {
              indicatorClass = 'dot-online';
              statusText = 'Listening';
              localClass = 'active';
            }
          } else if (machineConnected) {
            indicatorClass = 'dot-offline';
            statusText = 'Unavailable';
          } else {
            indicatorClass = 'dot-degraded';
            statusText = 'Connecting...';
          }
        }

        // Clickable link for HTTP services (gateway, http) when listening
        var svcNameLower = (svc.service || '').toLowerCase();
        var isHttpService = svcNameLower === 'gateway' || svcNameLower === 'http' || svcNameLower === 'web';
        var localAddr = svc.bindAddr ? svc.bindAddr + ':' + svc.localPort : 'localhost:' + svc.localPort;
        var localDisplay = localAddr;
        if (isHttpService && portFound) {
          var httpAddr = svc.bindAddr || 'localhost';
          localDisplay = '<a href="http://' + httpAddr + ':' + svc.localPort + '" target="_blank" rel="noopener" class="status-svc-link">' + localAddr + '</a>';
        }

        var statusCopyIcon = '<svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3a1.5 1.5 0 0 0-1.5-1.5h-6A1.5 1.5 0 0 0 2 3v6A1.5 1.5 0 0 0 3.5 10.5H5"/></svg>';
        var localCell = portFound
          ? '<button type="button" class="copy-btn" onclick="copyToClipboard(\'' + escAttr(localAddr) + '\')" title="Copy address">' + statusCopyIcon + '</button>' + localDisplay
          : localDisplay;
        html += '<div class="settings-row status-svc-row">'
          + '<span class="dot ' + indicatorClass + '"></span>'
          + '<div class="status-svc-name">' + escHtml(svc.service) + '</div>'
          + '<div class="status-svc-remote">' + (svc.servicePort ? ':' + svc.servicePort : '') + '</div>'
          + '<div class="status-svc-local ' + localClass + '">' + localCell + '</div>'
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
          // Record in the bound services cache so refreshStatus can detect it.
          if (!boundServicesCache) boundServicesCache = [];
          boundServicesCache.push({
            name: evt.name,
            local: evt.local,
            remote: evt.remote,
            bindAddr: evt.bindAddr || '',
            machine: evt.machine,
            hub: evt.hub || ''
          });
          // Update the selected service's local port with the actual bound port
          // (may differ from profile port if remapped due to conflict).
          // Match by machine + remote port since port-based services have no name.
          Object.keys(selectedServices).forEach(function (key) {
            var sel = selectedServices[key];
            if (sel.machine === evt.machine && sel.servicePort === evt.remote) {
              if (evt.local) sel.localPort = evt.local;
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
    if (quitBtn) { quitBtn.classList.add('pulse'); quitBtn.disabled = true; }
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
    if (dot) dot.className = 'dot dot-online';
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
    if (logDot) logDot.className = 'dot dot-online';

    // Fetch bound services, then refresh UI
    function fetchAndRefresh() {
      goApp.GetControlServices().then(function (svcs) {
        applyBoundServices(svcs);
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
    if (s.logPanelCollapsed) {
      panel.classList.add('collapsed');
      panel.style.height = '';
      var toggle = panel.querySelector('.log-panel-toggle');
      if (toggle) toggle.innerHTML = '&#x25B2;';
    } else if (s.logPanelHeight > 0) {
      panel.style.height = s.logPanelHeight + 'px';
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
// Load channel sources early so renderChannelSelect has custom channels
// available before any channel dropdown is first rendered.
loadChannelSources();

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

  // Version stamp in the topbar. The .status-current / .status-outdated
  // classes are toggled by refreshUpdateInfo() once update status is known.
  goApp.GetVersion().then(function (v) {
    var el = document.getElementById('app-version');
    if (el && v) el.textContent = v;
  }).catch(function () {});
})();

function dismissConnectTooltip() {
  var tip = document.getElementById('connect-tooltip');
  if (tip) tip.classList.add('hidden');
}

// Position the connect tooltip arrow centered under #connect-btn.
function positionConnectTooltip() {
  var tip = document.getElementById('connect-tooltip');
  var btn = document.getElementById('connect-btn');
  var arrow = tip ? tip.querySelector('.connect-tooltip-arrow') : null;
  if (!tip || !btn || !arrow || tip.classList.contains('hidden')) return;
  var btnRect = btn.getBoundingClientRect();
  var tipW = tip.offsetWidth;
  var btnCenterX = btnRect.left + btnRect.width / 2;
  var tipLeft = btnCenterX - tipW / 2;
  if (tipLeft < 8) tipLeft = 8;
  if (tipLeft + tipW > window.innerWidth - 8) tipLeft = window.innerWidth - 8 - tipW;
  tip.style.top = (btnRect.bottom + 6) + 'px';
  tip.style.left = tipLeft + 'px';
  tip.style.right = 'auto';
  arrow.style.left = (btnCenterX - tipLeft - 6) + 'px';
  arrow.style.right = 'auto';
}
window.addEventListener('resize', positionConnectTooltip);
setTimeout(positionConnectTooltip, 100);

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
// compareVersions returns -1, 0, or 1 for a < b, a === b, a > b.
// Handles Tela version tags of the form vX.Y.Z[-pre.N] by splitting on
// non-numeric separators and comparing each numeric component in order.
function compareVersions(a, b) {
  var norm = function (s) {
    return String(s || '').replace(/^v/i, '').split(/[.\-]/).map(function (p) {
      var n = parseInt(p, 10);
      return isNaN(n) ? p : n;
    });
  };
  var pa = norm(a), pb = norm(b);
  var len = Math.max(pa.length, pb.length);
  for (var i = 0; i < len; i++) {
    var x = pa[i] === undefined ? 0 : pa[i];
    var y = pb[i] === undefined ? 0 : pb[i];
    // Numeric parts compare numerically; string parts compare lexicographically.
    if (typeof x === 'number' && typeof y === 'number') {
      if (x < y) return -1;
      if (x > y) return 1;
    } else {
      var xs = String(x), ys = String(y);
      if (xs < ys) return -1;
      if (xs > ys) return 1;
    }
  }
  return 0;
}

function versionBadge(ver) {
  if (!ver) return '<span class="version-badge" data-ver="">unknown</span>';
  return '<span class="version-badge" data-ver="' + escAttr(ver) + '">' + formatVersionBadge(ver) + '</span>';
}

function formatVersionBadge(ver) {
  if (!latestVersion) {
    return escHtml(ver) + ' <span class="tools-service-label">(checking...)</span>';
  }
  var cmp = compareVersions(ver, latestVersion);
  if (cmp === 0) {
    return '<span class="status status-current">' + escHtml(ver) + '</span>'
      + ' <span class="tools-service-label">(latest: ' + escHtml(latestVersion) + ')</span>';
  }
  if (cmp > 0) {
    // Installed version is ahead of the channel (e.g. local dev build).
    return '<span class="status status-current">' + escHtml(ver) + '</span>'
      + ' <span class="tools-service-label">(channel: ' + escHtml(latestVersion) + ')</span>';
  }
  return '<span class="status status-outdated">' + escHtml(ver) + '</span>'
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
    var ubtn = document.getElementById('update-btn');
    var vbadge = document.getElementById('app-version');
    if (!info.pending || (!info.guiBehind && !info.cliBehind)) {
      ubtn.disabled = true;
      ubtn.classList.remove('chrome-warn');
      ubtn.title = 'No updates';
      if (vbadge) {
        vbadge.classList.remove('status-outdated');
        vbadge.classList.add('status-current');
      }
      return;
    }
    if (updateDismissedForSession) return;
    if (updateSkippedVersion && updateSkippedVersion === info.version) return;
    ubtn.disabled = false;
    ubtn.classList.add('chrome-warn');
    ubtn.title = 'Update available';
    if (vbadge && info.guiBehind) {
      vbadge.classList.remove('status-current');
      vbadge.classList.add('status-outdated');
    }
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

// updateLocalService self-updates a locally installed Tela service
// (telad or telahubd) directly: stop service, download from the channel
// manifest with SHA-256 verification, atomic swap, start service. The
// Go side returns {ok:true, version, path} on success or {error} on
// failure -- there is no asynchronous "is it really restarted yet?"
// step to guess about, so we update the table immediately on return.
function updateLocalService(name, btn) {
  btn.disabled = true;
  btn.textContent = 'Updating...';
  tvLog('Updating local ' + name + ' service...');
  goApp.UpdateLocalService(name).then(function (resp) {
    var data = {};
    try { data = JSON.parse(resp); } catch (e) {}
    if (data.error) {
      btn.disabled = false;
      btn.textContent = 'Retry';
      tvLog(name + ' update failed: ' + data.error);
      showError(name + ' update failed: ' + data.error);
      return;
    }
    btn.textContent = 'Done';
    tvLog(name + ' updated to ' + (data.version || 'unknown') + ' at ' + (data.path || ''));
    refreshBinStatus();
    refreshUpdateTable();
    refreshClientToolVersions();
    checkForUpdate();
  }).catch(function (err) {
    btn.disabled = false;
    btn.textContent = 'Retry';
    tvLog(name + ' update failed: ' + err);
    showError(name + ' update failed: ' + err);
  });
}

// Backwards-compatible alias kept for any HTML still calling the old
// telad-only entry point.
function updateServiceAgent(btn) {
  updateLocalService('telad', btn);
}

// Shared: render a tools table with optional per-binary action buttons.
// container: DOM element to fill. showActions: if true, show Update/Install buttons.
// onDone: optional callback after rendering.
function renderToolsTable(container, showActions, onDone, forceRefresh) {
  var binsFn = forceRefresh ? goApp.RefreshBinStatus() : goApp.GetBinStatus();
  binsFn.then(function (bins) {
    goApp.GetVersion().then(function (guiVer) {
      var latest = (bins && bins.length > 0 && bins[0].latest) ? bins[0].latest : '';
      if (!forceRefresh && updateInfo && updateInfo.version) latest = updateInfo.version;
      if (forceRefresh && latest) {
        latestVersion = latest;
        if (updateInfo) updateInfo.version = latest;
        refreshVersionBadges();
      }
      var tvUpToDate = !latest || guiVer === latest;

      var html = '<table class="tools-table"><thead><tr><th>Tool</th><th>Installed</th><th>Available</th>';
      if (showActions) html += '<th></th>';
      html += '</tr></thead><tbody>';

      // TelaVisor
      var tvStatusClass = tvUpToDate ? 'status status-current' : 'status status-outdated';
      html += '<tr><td>TelaVisor</td>'
        + '<td class="tools-version"><span class="' + tvStatusClass + '">' + escHtml(guiVer || 'dev') + '</span></td>'
        + '<td class="tools-version">' + escHtml(latest || guiVer || 'dev') + '</td>';
      if (showActions) {
        var tvAction = tvUpToDate ? '' : '<button class="btn btn-sm tools-action-btn" onclick="restartToUpdate(this)">Update &amp; Restart</button>';
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
          var installedCell;
          if (b.found || svcOnly) {
            var installedStatus = mainUpToDate ? 'status status-current' : 'status status-outdated';
            installedCell = '<span class="' + installedStatus + '">' + escHtml(mainVer) + '</span>';
          } else {
            installedCell = escHtml(mainVer);
          }

          // Label: show "service (running/stopped)" when the service is the only installation.
          var nameLabel = escHtml(b.name);
          if (svcOnly) {
            var svcStatus = b.serviceRunning ? 'running' : 'stopped';
            nameLabel += ' <span class="tools-service-label">service (' + svcStatus + ')</span>';
          }

          html += '<tr><td>' + nameLabel + '</td>'
            + '<td class="tools-version">' + installedCell + '</td>'
            + '<td class="tools-version">' + escHtml(b.latest || '?') + '</td>';
          if (showActions) {
            var action = '';
            if (b.found) {
              action = b.upToDate ? '' : '<button class="btn btn-sm tools-action-btn" onclick="refreshSingleBinary(\'' + escAttr(b.name) + '\', this)">Update</button>';
            } else if (svcOnly && !mainUpToDate) {
              action = '<button class="btn btn-sm tools-action-btn" onclick="updateLocalService(\'' + escAttr(b.name) + '\', this)">Update</button>';
            } else if (!svcOnly) {
              action = '<button class="btn btn-sm tools-action-btn" onclick="refreshSingleBinary(\'' + escAttr(b.name) + '\', this)">Install</button>';
            }
            html += '<td>' + action + '</td>';
          }
          html += '</tr>';

          // Show a service sub-row only when BOTH a local copy and a service exist.
          if (b.found && b.serviceInstalled) {
            var svcVer = b.serviceVersion || 'unknown';
            var svcState = b.serviceRunning ? 'running' : 'stopped';
            var svcUpToDate = b.serviceVersion === b.latest;
            var svcInstalledClass = svcUpToDate ? 'status status-current' : 'status status-outdated';
            html += '<tr class="tools-service-row"><td>'
              + '<span class="tools-service-label">service (' + svcState + ')</span></td>'
              + '<td class="tools-version"><span class="' + svcInstalledClass + '">' + escHtml(svcVer) + '</span></td>'
              + '<td class="tools-version">' + escHtml(b.latest || '?') + '</td>';
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
  (function () { var ub = document.getElementById('update-btn'); if (ub) { ub.disabled = true; ub.classList.remove('chrome-warn'); ub.title = 'No updates'; } })();
  toggleUpdateOverlay();
}

function ignoreUpdateForever() {
  if (updateInfo) updateSkippedVersion = updateInfo.version;
  (function () { var ub = document.getElementById('update-btn'); if (ub) { ub.disabled = true; ub.classList.remove('chrome-warn'); ub.title = 'No updates'; } })();
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
          localPort: svc.remote || 0,
          bindAddr: ''
        };
      });
    });
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
        mountBtn.classList.add('chrome-accent');
        mountBtn.title = 'Unmount file shares';
      } else if (mc && mc.mount) {
        mountBtn.disabled = false;
        mountBtn.classList.remove('chrome-accent');
        mountBtn.title = 'Mount file shares (' + mc.mount + ')';
      } else {
        mountBtn.disabled = true;
        mountBtn.classList.remove('chrome-accent');
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
  refreshMountPreview();
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
  // The Clients-mode dashboard, profile YAML, and selectedServices
  // map all key hubs by their wss:// URL (the form the tela CLI
  // client writes into the profile). The portal directory now reports
  // hubs as https:// because the admin proxy uses HTTP. Convert once
  // here and pass the wss form everywhere downstream so reconciliation
  // with the existing profile data still works.
  var hubKey = portalToWSURL(hub.url);
  var hubForCallbacks = { url: hubKey, name: hub.name, hasToken: hub.hasToken, source: hub.source };

  var hubContainer = document.createElement('div');
  var included = isHubIncluded(hubKey);
  hubContainer.className = 'profile-hub-group' + (included ? '' : ' profile-hub-excluded');

  var hubHeader = document.createElement('div');
  hubHeader.className = 'profile-hub-header';
  if (selectedHubURL === hubKey && !selectedMachineId) hubHeader.classList.add('selected');
  var hubDisabled = isConnected ? ' disabled' : '';
  hubHeader.innerHTML = '<input type="checkbox"' + (included ? ' checked' : '') + hubDisabled
    + ' onclick="event.stopPropagation(); toggleHubInclusion(\'' + escAttr(hubKey) + '\', this.checked)">'
    + '<span class="dot hub-dot"></span>'
    + '<span class="hub-name">' + escHtml(hub.name) + '</span>'
    + (!hub.hasToken ? '<span class="status status-error">No token</span>' : '');
  hubHeader.onclick = function (e) {
    if (e.target.tagName === 'INPUT') return;
    selectHub(hubForCallbacks, hubHeader);
  };
  hubContainer.appendChild(hubHeader);
  content.appendChild(hubContainer);

  if (hub.hasToken) {
    goApp.GetHubStatus(hub.name).then(function (status) {
      hubStatusCache[hubKey] = status;
      hubHeader.querySelector('.hub-dot').className = 'dot hub-dot ' + (status.online ? 'dot-online' : 'dot-error');

      if (status.machines) {
        reconcileServicePorts(hubKey, status.machines);
      }

      if (included) {
        var renderedMachines = {};
        if (status.machines) {
          status.machines.forEach(function (m) {
            var mId = m.id || m.hostname;
            renderedMachines[mId] = true;
            var mEl = document.createElement('div');
            mEl.className = 'machine-item';
            if (selectedHubURL === hubKey && selectedMachineId === mId) mEl.classList.add('selected');
            var dotClass = m.agentConnected ? 'dot-online' : 'dot-offline';
            mEl.innerHTML = '<span class="dot ' + dotClass + '"></span>'
              + escHtml(mId);
            mEl.onclick = function (e) {
              e.stopPropagation();
              selectMachine(hubForCallbacks, m, mEl);
            };
            hubContainer.appendChild(mEl);
          });
        }
        // Show machines in the profile that aren't in the status response
        Object.keys(selectedServices).forEach(function (key) {
          var sel = selectedServices[key];
          if (sel.hub !== hubKey) return;
          if (renderedMachines[sel.machine]) return;
          renderedMachines[sel.machine] = true;
          var mEl = document.createElement('div');
          mEl.className = 'machine-item machine-unreachable';
          if (selectedHubURL === hubKey && selectedMachineId === sel.machine) mEl.classList.add('selected');
          mEl.innerHTML = '<span class="dot dot-offline"></span>'
            + escHtml(sel.machine)
            + '<span class="unreachable-badge">unreachable</span>';
          mEl.onclick = function (e) {
            e.stopPropagation();
            selectMachine(hubForCallbacks, { id: sel.machine, services: [], agentConnected: false, unreachable: true }, mEl);
          };
          hubContainer.appendChild(mEl);
        });
      }
    }).catch(function () {
      hubHeader.querySelector('.hub-dot').className = 'dot hub-dot dot-error';
    });
  }
}

// portalToWSURL converts an https://host or http://host URL returned
// by the portal directory back into the wss://host or ws://host form
// the Clients-mode profile and selectedServices map use as the hub
// identifier. Idempotent: a wss:// URL passes through unchanged.
function portalToWSURL(u) {
  if (!u) return u;
  if (u.indexOf('https://') === 0) return 'wss://' + u.slice(8);
  if (u.indexOf('http://') === 0) return 'ws://' + u.slice(7);
  return u;
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
          + '<div class="settings-group-header">File Share Mount</div>'
          + '<div class="settings-group-desc">Mounts a local drive that contains all file shares from connected machines. Each machine appears as a folder under the mount point.</div>'
          + '<div class="settings-group-body"><div class="mount-config-form">'
          + '<div class="mount-config-row"><label class="mount-config-check"><input type="checkbox" id="ps-mount-enable"' + (mountEnabled ? ' checked' : '') + allDis + ' onchange="onPsMountEnableToggle(this.checked)"> Enable</label></div>'
          + '<div class="mount-config-row"><label class="mount-config-label" for="ps-mount-point">Mount point</label><input type="text" id="ps-mount-point" placeholder="' + (navigator.platform.indexOf('Win') >= 0 ? 'T:' : '/mnt/tela') + '" class="form-input mono" value="' + escAttr(mc.mount || '') + '" data-required="Mount point is required when mount is enabled"' + mountFieldDis + ' onchange="onPsMountChange()" onblur="validatePsMount()"></div>'
          + '<div class="mount-config-row"><label class="mount-config-label" for="ps-mount-port">WebDAV port</label><input type="number" id="ps-mount-port" class="form-input mono mount-config-port" min="1024" max="65535" value="' + (mc.port || 18080) + '"' + mountFieldDis + ' onchange="onPsMountChange()"></div>'
          + '<div class="mount-config-row"><label class="mount-config-check"><input type="checkbox" id="ps-mount-auto"' + (mc.auto ? ' checked' : '') + mountFieldDis + ' onchange="onPsMountChange()"> Auto-mount on connect</label></div>'
          + '<div id="mount-preview-container"></div>'
          + '</div></div></div>';

        html += '<div class="settings-group">'
          + '<div class="settings-group-header">MTU</div>'
          + '<div class="settings-group-body"><div class="mount-config-form">'
          + '<div class="mount-config-row"><input type="number" id="ps-mtu" class="form-input mono mount-config-port" value="' + mtuVal + '" title="Tunnel MTU"' + ((isDefault || locked) ? ' disabled' : '') + ' onchange="onPsMtuChange()"></div>'
          + '<div class="mount-config-row"><label class="mount-config-check"><input type="checkbox" id="ps-mtu-default"' + (isDefault ? ' checked' : '') + allDis + ' onchange="onPsMtuDefaultToggle(this.checked)"> Use default (1100)</label></div>'
          + '</div></div></div>';

        pane.innerHTML = html;
        refreshMountPreview();
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

      var copyIcon = '<svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="5" width="9" height="9" rx="1.5"/><path d="M11 5V3a1.5 1.5 0 0 0-1.5-1.5h-6A1.5 1.5 0 0 0 2 3v6A1.5 1.5 0 0 0 3.5 10.5H5"/></svg>';
      var html = '<div class="settings-group">'
        + '<div class="settings-group-header">Profile</div>'
        + '<div class="preview-info-row">'
        + '<span class="preview-info-label">File:</span>'
        + '<button type="button" class="copy-btn" onclick="copyToClipboard(document.getElementById(\'preview-path\').textContent)" title="Copy path">' + copyIcon + '</button>'
        + '<code class="preview-info-value" id="preview-path">' + escHtml(path) + '</code>'
        + '</div>'
        + '<div class="preview-info-row">'
        + '<span class="preview-info-label">CLI:</span>'
        + '<button type="button" class="copy-btn" onclick="copyToClipboard(document.getElementById(\'preview-cli\').textContent)" title="Copy command">' + copyIcon + '</button>'
        + '<code class="preview-info-value" id="preview-cli">' + escHtml(cli) + '</code>'
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
  refreshMountPreview();
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
  refreshMountPreview();
  markProfileDirty();
}

var mountDriveIcon = '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2"><rect x="2" y="3" width="12" height="10" rx="1.5"/><line x1="2" y1="9" x2="14" y2="9"/><circle cx="12" cy="11.5" r="0.8" fill="currentColor" stroke="none"/></svg>';
var mountFolderIcon = '<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2"><path d="M1.5 3V13H14.5V5H6.5L5 3H1.5Z"/></svg>';

function refreshMountPreview() {
  var container = document.getElementById('mount-preview-container');
  if (!container) return;

  var enableEl = document.getElementById('ps-mount-enable');
  if (!enableEl || !enableEl.checked) {
    container.innerHTML = '<div class="mount-preview"><div class="mount-preview-empty">File share mount is disabled.</div></div>';
    return;
  }

  var mountPoint = (document.getElementById('ps-mount-point').value || '').trim();
  var isWin = navigator.platform.indexOf('Win') >= 0;
  var sep = isWin ? '\\' : '/';
  var driveDisplay;
  if (mountPoint) {
    driveDisplay = mountPoint.replace(/[\/\\]+$/, '') + sep;
  } else {
    driveDisplay = isWin ? 'T:\\' : '/mnt/tela/';
  }

  // Collect machines with file sharing from included hubs. Any machine
  // on an included hub that offers file sharing appears in the preview,
  // regardless of whether the user selected services on that machine.
  // buildConnections() adds connection entries for these machines so
  // tela opens a tunnel to them even without TCP service mappings.
  var fsNames = [];
  var seen = {};
  Object.keys(hubStatusCache).forEach(function (hubURL) {
    if (!isHubIncluded(hubURL)) return;
    var status = hubStatusCache[hubURL];
    if (!status || !status.machines) return;
    status.machines.forEach(function (m) {
      var mId = m.id || m.hostname;
      var fs = m.capabilities && m.capabilities.fileShare;
      if (fs && fs.enabled && !seen[mId]) {
        seen[mId] = true;
        fsNames.push(mId);
      }
    });
  });

  var html = '<div class="mount-preview">';
  if (fsNames.length === 0) {
    html += '<div class="mount-preview-empty">No selected machines offer file sharing.</div>';
  } else {
    html += '<div class="mp-row"><span class="mp-icon">' + mountDriveIcon + '</span><span class="mp-drive">' + escHtml(driveDisplay) + '</span></div>';
    fsNames.forEach(function (name) {
      html += '<div class="mp-row mp-indent"><span class="mp-icon">' + mountFolderIcon + '</span><span class="mp-name">' + escHtml(name) + '</span></div>';
    });
  }
  html += '</div>';
  container.innerHTML = html;
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
          var selSvc = selectedServices[key];
          var svcBindAddr = selSvc && selSvc.bindAddr ? selSvc.bindAddr : '';
          var localDisplay2 = svcBindAddr ? svcBindAddr + ':' + localPort : 'localhost:' + localPort;
          html += '<div class="profile-svc-local">' + localDisplay2 + '</div>';
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
  Object.keys(selectedServices).forEach(function (key) {
    var sel = selectedServices[key];
    sel.localPort = sel.servicePort;
  });
  updateConnectButton();
  checkDirty();
  refreshCurrentPane();
  refreshMountPreview();
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

// applyBoundServices stores the bound service list in the cache and updates
// selectedServices local ports so the display shows the actual bound port
// even when port+10000 remapping occurred.
function applyBoundServices(services) {
  boundServicesCache = services || [];
  boundServicesCache.forEach(function (bs) {
    Object.keys(selectedServices).forEach(function (key) {
      var sel = selectedServices[key];
      if (sel.machine === bs.machine && sel.servicePort === bs.remote) {
        sel.localPort = bs.local;
      }
    });
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
    btn.classList.remove('chrome-accent', 'chrome-warn', 'pulse');
    btn.disabled = (connPhase === 'disconnecting');
    if (connPhase === 'connected') {
      btn.classList.add('chrome-accent');
      btn.title = connAttached ? 'Detach' : 'Disconnect';
    } else if (connPhase === 'connecting' || connPhase === 'disconnecting') {
      btn.classList.add('chrome-warn', 'pulse');
      btn.title = connPhase === 'connecting' ? 'Connecting...' : 'Disconnecting...';
    } else {
      btn.title = 'Connect';
    }
  }

  // Mount button: check connection + config
  updateMountButtonState();

  // Tela log dot
  var dot = document.getElementById('log-tela-dot');
  if (dot) dot.className = (connPhase === 'connected' || connPhase === 'connecting') ? 'dot dot-online' : 'dot dot-offline';

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
        // WebSocket connected. Fetch bound services -- events may have fired
        // before the WebSocket was listening, so always populate the cache here.
        goApp.GetControlServices().then(function (services) {
          if (services && services.length > 0) {
            applyBoundServices(services);
          }
          if (connPhase === 'connecting') {
            if (boundServicesCache && boundServicesCache.length > 0) {
              tvLog('Connected');
              setConnPhase('connected');
              onConnectionChanged();
            }
          } else {
            // Already transitioned via service_bound event; just refresh display.
            refreshStatus();
          }
        });
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
            var vchk = document.getElementById('verbose-check');
            if (vchk) vchk.checked = true;
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
// Per-agent update status cache. Populated when the agent detail is loaded and
// loadAgentChannel() resolves. Keyed by machine ID; each entry records the
// version that was current when the status was checked so it auto-invalidates
// if the agent is updated between views.
var agentStatusCache = {}; // { [id]: { version: string, status: 'current'|'outdated' } }

function agentsRefresh() {
  goApp.GetConnectionState().then(function (state) {
    document.getElementById('agents-pair-btn').disabled = !state.connected;
  }).catch(function () {
    document.getElementById('agents-pair-btn').disabled = true;
  });
  goApp.GetAgentList().then(function (agents) {
    agentsData = agents || [];
    agentsData.sort(function (a, b) {
      var an = (a.displayName || a.id || '').toLowerCase();
      var bn = (b.displayName || b.id || '').toLowerCase();
      if (an < bn) return -1;
      if (an > bn) return 1;
      return 0;
    });
    agentsRenderSidebar();
    if (agentsSelectedId) {
      var found = agentsData.find(function (a) { return a.id === agentsSelectedId; });
      if (found) agentsShowDetail(found);
      else document.getElementById('agents-detail').innerHTML = '<div class="agents-detail-empty">Select an agent to view details.</div>';
    }
    prefetchAgentStatuses(agentsData);
  }).catch(function () {
    document.getElementById('agents-sidebar-list').innerHTML = '<p class="empty-hint" style="padding:16px;">Failed to load agents.</p>';
  });
}

// prefetchAgentStatuses fetches update status for every online agent in the
// background after the agent list loads, so sidebar version badges populate
// without requiring the user to click each agent. Agents are queried one at a
// time to avoid flooding the hub; already-cached entries are skipped.
function prefetchAgentStatuses(agents) {
  var online = agents.filter(function (a) { return a.online && a.hub && a.id; });
  var i = 0;
  function next() {
    if (i >= online.length) return;
    var a = online[i++];
    var ver = a.version || '';
    var cached = agentStatusCache[a.id];
    if (cached && cached.version === ver) { next(); return; }
    goApp.GetAgentChannelInfo(a.hub, a.id).then(function (raw) {
      var info = {};
      try { info = JSON.parse(raw); } catch (e) {}
      if (info && info.payload && typeof info.payload === 'object') info = info.payload;
      if (info && info.ok === false && /unknown action/i.test(info.message || '')) info = {};
      if (info.latestVersion && info.currentVersion) {
        agentStatusCache[a.id] = {
          version: info.currentVersion,
          status: info.updateAvailable ? 'outdated' : 'current'
        };
        agentsRenderSidebar();
      }
    }).catch(function () {}).then(next);
  }
  // Brief delay so the initial render completes before the first fetch.
  setTimeout(next, 300);
}

function agentsRenderSidebar() {
  var html = '';
  agentsData.forEach(function (a) {
    var active = a.id === agentsSelectedId ? ' active' : '';
    var dotClass = a.online ? 'dot-online' : 'dot-offline';
    var ver = a.version || 'unknown';
    var linkedBadge = (a.linkedAgentIds && a.linkedAgentIds.length)
      ? ' <span class="chip" title="Registered on ' + (a.linkedAgentIds.length + 1) + ' hubs">' + (a.linkedAgentIds.length + 1) + ' hubs</span>'
      : '';
    var label = a.displayName || a.id;
    var cached = agentStatusCache[a.id];
    var verStatusClass = (cached && cached.version === ver)
      ? (cached.status === 'current' ? ' status-current' : ' status-outdated')
      : '';
    html += '<div class="agents-sidebar-item' + active + '" onclick="agentsSelect(\'' + escAttr(a.id) + '\')">'
      + '<span class="dot ' + dotClass + '"></span>'
      + '<div>'
      + '<div class="agents-sidebar-name">' + escHtml(label) + linkedBadge + '</div>'
      + '<div class="agents-sidebar-version' + verStatusClass + '">' + escHtml(ver) + '</div>'
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

  var label = a.displayName || a.id;
  var html = '<div class="detail-header">'
    + '<div class="detail-title">' + escHtml(label)
    + ' <span class="status ' + (isOnline ? 'status-online' : 'status-offline') + '">'
    + '<span class="status-dot"></span>' + (isOnline ? 'Online' : 'Offline') + '</span></div>'
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
    + '<tr><td>Active sessions</td><td>' + String(a.sessionCount || 0) + '</td></tr>';

  // Linked registrations: same agent on other hubs.
  if (a.linkedAgentIds && a.linkedAgentIds.length) {
    var linkedNames = a.linkedAgentIds.map(function (key) {
      // key is "hubId|agentId" from portalaggregate. Find the matching
      // agent in agentsData by hubId to surface its hub name.
      var sibling = agentsData.find(function (other) {
        return other !== a && other.agentId && a.agentId && other.agentId === a.agentId;
      });
      return sibling ? escHtml(sibling.hub) : escHtml(key);
    });
    html += '<tr><td>Also on</td><td>' + linkedNames.join(', ') + '</td></tr>';
  }

  html += '</table></div>';

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

  // File Shares
  var capShares = (a.capabilities && a.capabilities.shares) || [];
  html += '<div class="setting-card"><div class="setting-card-title">File Shares</div>'
    + '<div class="setting-card-desc">Sandboxed directory access through the encrypted tunnel.</div>';
  if (canManage) {
    // Editable table -- paths are loaded from config-get after render.
    html += '<table class="shares-table"><thead><tr>'
      + '<th>Name</th><th>Path</th><th>Writable</th><th>Allow delete</th><th></th>'
      + '</tr></thead><tbody id="agent-shares-body">';
    if (capShares.length === 0) {
      html += '<tr id="agent-shares-empty"><td colspan="5" class="shares-empty">No shares configured.</td></tr>';
    } else {
      capShares.forEach(function (s) {
        html += agentShareRowHTML(s.name, '', s.writable, s.allowDelete);
      });
    }
    html += '</tbody></table>'
      + '<button class="btn btn-sm" style="margin-top:6px" onclick="agentSharesAdd()">Add share</button>';
  } else if (capShares.length > 0) {
    // Read-only list from capabilities.
    html += '<table class="kv-table">';
    capShares.forEach(function (s) {
      var flags = [];
      if (s.writable) flags.push('writable');
      if (s.allowDelete) flags.push('allow delete');
      html += '<tr><td>' + escHtml(s.name) + '</td>'
        + '<td class="tools-service-label">' + escHtml(flags.join(', ') || 'read-only') + '</td></tr>';
    });
    html += '</table>';
  } else {
    html += '<div class="setting-card-desc" style="margin-top:4px">No shares configured.</div>';
  }
  html += '</div>';

  // Management
  html += '<div class="setting-card" id="agent-management-card">'
    + '<div class="setting-card-title-row"><div class="setting-card-title">Management</div>'
    + '<button type="button" class="btn btn-sm tools-action-btn" onclick="refreshAgentManagement(\'' + ehub + '\',\'' + eid + '\')" title="Refresh channel status">&#x21BB; Refresh</button>'
    + '</div>'
    + '<div class="setting-card-desc">Remote agent lifecycle controls.</div>'
    + '<table class="kv-table">';
  if (canManage) {
    // Software button starts disabled with a neutral label. loadAgentChannel
    // overwrites it with the channel-aware truth from the agent's update-status
    // response, so the button can never disagree with the channel row.
    html += '<tr><td>Configuration</td><td><button type="button" class="btn btn-sm" onclick="agentsViewConfig(\'' + eid + '\',\'' + ehub + '\')">View Config</button></td></tr>'
      + '<tr><td>Log output</td><td><button type="button" class="btn btn-sm" onclick="agentsViewLogs(\'' + eid + '\',\'' + ehub + '\')">View Logs</button></td></tr>'
      + '<tr><td>Release channel</td><td>' + renderChannelSelect('agent-channel-select', '') + ' <span id="agent-channel-status" class="tools-service-label">loading...</span></td></tr>'
      + '<tr><td>Software</td><td><button type="button" class="btn btn-sm" id="agent-update-btn" disabled onclick="agentsUpdate(\'' + eid + '\',\'' + ehub + '\')">Update</button>'
      + ' <span id="agent-update-status" class="tools-service-label">loading...</span></td></tr>'
      + '<tr><td>Restart</td><td><button type="button" class="btn btn-sm" onclick="agentsRestart(\'' + eid + '\',\'' + ehub + '\')">Restart</button></td></tr>';
  } else {
    html += '<tr><td colspan="2">Agent ' + (isOnline ? 'does not support remote management. Update telad to enable.' : 'is offline.') + '</td></tr>';
  }
  html += '</table></div>';

  // Channel Sources card (only when manageable)
  if (canManage) {
    html += '<div class="setting-card" id="agent-channel-sources-card">'
      + '<div class="setting-card-title">Channel Sources</div>'
      + '<div class="setting-card-desc">Custom release channels available in this agent\'s Release channel dropdown. Built-in channels (dev, beta, stable) are always present.</div>'
      + '<div id="agent-channel-sources-list" class="channel-sources-list"></div>'
      + '<div class="channel-source-add-row" style="margin-top:8px;">'
      + '<input type="text" id="agent-new-channel-name" class="tb-input channel-src-input-name" placeholder="name (e.g. local)">'
      + '<input type="text" id="agent-new-channel-base" class="tb-input channel-src-input-base" placeholder="http://localhost:9900/">'
      + '<button type="button" class="btn btn-sm" onclick="agentAddChannelSource(\'' + ehub + '\',\'' + eid + '\')">Add</button>'
      + '</div>'
      + '</div>';
  }

  // Danger Zone
  html += '<div class="setting-card setting-card-danger"><div class="setting-card-title">Danger Zone</div>'
    + '<div class="danger-row"><div class="danger-row-text">'
    + '<div class="danger-row-label">Force disconnect</div>'
    + '<div class="danger-row-desc">Close all active sessions for this agent.</div>'
    + '</div><button type="button" class="btn btn-sm btn-danger" disabled>Disconnect</button></div>'
    + '<div class="danger-row"><div class="danger-row-text">'
    + '<div class="danger-row-label">Remove machine</div>'
    + '<div class="danger-row-desc">Remove this machine from the hub. The agent will re-register.</div>'
    + '</div><button type="button" class="btn btn-sm btn-danger" disabled>Remove</button></div>'
    + '</div>';

  document.getElementById('agents-detail').innerHTML = html;

  if (canManage && document.getElementById('agent-channel-select')) {
    loadAgentChannel(a.hub, a.id);
  }
  if (canManage && document.getElementById('agent-channel-sources-list')) {
    loadAgentChannelSources(a.hub, a.id);
  }
  if (canManage && document.getElementById('agent-shares-body')) {
    loadAgentSharePaths(a.hub, a.id);
  }
}

// Returns the inner HTML for one editable share row.
function agentShareRowHTML(name, path, writable, allowDelete) {
  return '<tr class="agent-share-row">'
    + '<td><input type="text" class="setting-input agent-share-name" value="' + escAttr(name) + '" placeholder="share-name" onchange="agentsMarkDirty()"></td>'
    + '<td><input type="text" class="setting-input agent-share-path" value="' + escAttr(path) + '" placeholder="/path/on/agent" onchange="agentsMarkDirty()"></td>'
    + '<td class="shares-check-cell"><input type="checkbox" class="agent-share-writable"' + (writable ? ' checked' : '') + ' onchange="agentsMarkDirty()"></td>'
    + '<td class="shares-check-cell"><input type="checkbox" class="agent-share-allowDelete"' + (allowDelete ? ' checked' : '') + ' onchange="agentsMarkDirty()"></td>'
    + '<td><button class="btn btn-sm btn-danger" onclick="agentShareRemove(this)">Remove</button></td>'
    + '</tr>';
}

// Fetches the full agent config to populate the path column of the shares table.
// Capabilities don't include path (it's an agent-local detail), so we need config-get.
function loadAgentSharePaths(hub, machineId) {
  goApp.GetAgentConfig(hub, machineId).then(function (resp) {
    var data;
    try { data = JSON.parse(resp); } catch (e) { return; }
    if (!data.ok || !data.payload) return;
    var machines = data.payload.machines || [];
    var machine = machines.find(function (m) { return m.name === machineId; });
    if (!machine) return;
    var configShares = machine.shares || [];
    if (configShares.length === 0) return;

    var tbody = document.getElementById('agent-shares-body');
    if (!tbody) return;

    // Build a name->path map from the config.
    var pathMap = {};
    configShares.forEach(function (s) { if (s.name) pathMap[s.name] = s.path || ''; });

    // Populate path inputs that were left empty during initial render.
    tbody.querySelectorAll('.agent-share-row').forEach(function (row) {
      var nameEl = row.querySelector('.agent-share-name');
      var pathEl = row.querySelector('.agent-share-path');
      if (nameEl && pathEl && pathEl.value === '' && pathMap[nameEl.value] !== undefined) {
        pathEl.value = pathMap[nameEl.value];
      }
    });

    // If config has shares not in capabilities (e.g. agent reconnecting),
    // add any that are missing from the table.
    var renderedNames = {};
    tbody.querySelectorAll('.agent-share-name').forEach(function (el) { renderedNames[el.value] = true; });
    var hadEmpty = !!document.getElementById('agent-shares-empty');
    configShares.forEach(function (s) {
      if (!s.name || renderedNames[s.name]) return;
      if (hadEmpty) {
        var empty = document.getElementById('agent-shares-empty');
        if (empty) { empty.remove(); hadEmpty = false; }
      }
      var row = document.createElement('tr');
      row.className = 'agent-share-row';
      row.innerHTML = agentShareRowHTML(s.name, s.path || '', s.writable, s.allowDelete);
      tbody.appendChild(row);
    });
  }).catch(function () {});
}

function agentSharesAdd() {
  var tbody = document.getElementById('agent-shares-body');
  if (!tbody) return;
  var empty = document.getElementById('agent-shares-empty');
  if (empty) empty.remove();
  var row = document.createElement('tr');
  row.className = 'agent-share-row';
  row.innerHTML = agentShareRowHTML('', '', false, false);
  tbody.appendChild(row);
  agentsMarkDirty();
}

function agentShareRemove(btn) {
  var row = btn.closest('tr');
  if (row) row.remove();
  agentsMarkDirty();
  var tbody = document.getElementById('agent-shares-body');
  if (tbody && !tbody.querySelector('.agent-share-row')) {
    var empty = document.createElement('tr');
    empty.id = 'agent-shares-empty';
    empty.innerHTML = '<td colspan="5" class="shares-empty">No shares configured.</td>';
    tbody.appendChild(empty);
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
    goApp.AdminAPICall(hub, 'POST', 'pair-code',
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

  // File shares
  var shareRows = document.querySelectorAll('.agent-share-row');
  if (shareRows.length > 0) {
    fields.shares = Array.prototype.map.call(shareRows, function (row) {
      return {
        name: row.querySelector('.agent-share-name').value.trim(),
        path: row.querySelector('.agent-share-path').value.trim(),
        writable: row.querySelector('.agent-share-writable').checked,
        allowDelete: row.querySelector('.agent-share-allowDelete').checked
      };
    }).filter(function (s) { return s.name && s.path; });
  }

  var saveBtn = document.getElementById('agents-save-btn');
  saveBtn.textContent = 'Saving...';
  saveBtn.disabled = true;

  goApp.SetAgentConfig(agentsSelectedHub, agentsSelectedId, JSON.stringify(fields)).then(function (resp) {
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
  goApp.GetAgentConfig(hub, machineId).then(function (resp) {
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
  okBtn.className = 'btn btn-primary';
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
    tab.innerHTML = '<span class="dot dot-offline" id="' + paneId + '-dot"></span>'
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
  var dot = document.getElementById(paneId + '-dot');
  if (dot) dot.className = 'dot dot-degraded';

  goApp.GetAgentLogs(hub, machineId, 500).then(function (resp) {
    try { var data = JSON.parse(resp); } catch (e) {
      document.getElementById(paneId).textContent = 'Error: invalid response\n';
      if (dot) dot.className = 'dot dot-offline';
      return;
    }
    if (data.error || data.ok === false) {
      document.getElementById(paneId).textContent = 'Error: ' + (data.error || data.message || 'unknown error') + '\n';
      if (dot) dot.className = 'dot dot-offline';
      return;
    }
    var payload = data.payload || data;
    var lines = payload.lines || [];
    var el = document.getElementById(paneId);
    el.textContent = lines.join('\n') + '\n';
    logAutoScroll(el);
    if (dot) dot.className = 'dot dot-online';
  }).catch(function (err) {
    document.getElementById(paneId).textContent = 'Failed: ' + err + '\n';
    if (dot) dot.className = 'dot dot-offline';
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
    goApp.RestartAgent(hub, machineId).then(function (resp) {
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
    var btn = document.getElementById('agent-update-btn');
    if (btn) btn.disabled = true;
    agentUpdateStatus('Sending update request...');
    tvLog('Updating telad on ' + machineId + '...');

    goApp.UpdateAgent(hub, machineId, '').then(function (resp) {
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
      pollAgentOnline(machineId, hub, targetVer, 0);
    }).catch(function (err) {
      agentUpdateStatus('');
      if (btn) btn.disabled = false;
      showError('Update failed: ' + err);
    });
  });
}

function pollAgentOnline(machineId, hub, targetVer, attempt) {
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
        pollAgentOnline(machineId, hub, targetVer, attempt + 1);
      }
    }).catch(function () {
      agentUpdateStatus('Waiting for agent to restart... (' + (attempt + 1) + ')');
      pollAgentOnline(machineId, hub, targetVer, attempt + 1);
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
var filesCurrentShare = '';
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
  filesCurrentShare = '';
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
        var statusClass = state.connected ? 'dot-online' : 'dot-error';
        var badge, disabled;

        if (!state.connected) {
          badge = '<span class="chip chip-cap-no files-machine-badge">disconnected</span>';
          disabled = true;
        } else {
          var mc = caps[m.name];
          var shares = (mc && mc.shares) || [];
          if (shares.length > 0) {
            badge = '<span class="cap-tags">';
            shares.forEach(function (s) {
              badge += '<span class="chip chip-cap-info">' + escHtml(s.name) + '</span>';
              if (s.writable) {
                badge += '<span class="chip chip-cap-yes">Writable</span>';
              } else {
                badge += '<span class="chip chip-cap-no">Read only</span>';
              }
            });
            badge += '</span>';
            disabled = false;
          } else {
            badge = '<span class="chip files-machine-badge">no file share</span>';
            disabled = true;
          }
        }

        var onclick = disabled ? '' : ' onclick="filesOpenMachine(\'' + escAttr(m.name) + '\')"';
        var cls = disabled ? ' disabled' : '';

        html += '<div class="files-machine-card' + cls + '"' + onclick + '>'
          + '<span class="dot ' + statusClass + '"></span>'
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
          + '<span class="dot dot-online"></span>'
          + '<span class="fe-icon-machine"></span>'
          + '<div><div class="files-machine-name">' + escHtml(m.name) + '</div>'
          + '<div class="files-machine-meta">' + escHtml(m.hub) + '</div></div>'
          + '<span class="chip files-machine-badge">unknown</span>'
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
  filesCurrentShare = '';
  filesCurrentPath = '';
  filesNavHistory = ['machines'];
  filesClearSelection();

  // If the machine has exactly one share (from capabilities), skip the share
  // list and enter it directly. The user can navigate up to see the share as a
  // folder, and up again to return to the machine list.
  var mc = filesMachineCapabilities[name];
  var shares = (mc && mc.shares) || [];
  if (shares.length === 1) {
    var s = shares[0];
    filesCurrentShare = s.name;
    filesCurrentWritable = !!s.writable;
    filesCurrentAllowDelete = !!(s.writable && s.allowDelete);
  }
  filesListDir(name, '');
}

// ── File list view ──
// When filesCurrentShare === '', filesListDir fetches list-shares and renders
// the shares as folder entries. Double-clicking one calls filesEnterShare().
// When filesCurrentShare !== '', it fetches the actual directory listing.

function filesListDir(machine, path) {
  filesView = 'files';
  var gen = ++filesListGeneration; // guard against stale responses

  document.getElementById('files-header').style.display = 'flex';
  // Hide the action bar at the share-picker level (no upload/mkdir on shares).
  document.getElementById('files-actionbar').style.display = filesCurrentShare ? 'flex' : 'none';
  document.getElementById('files-btn-back').disabled = (filesNavHistory.length === 0);
  document.getElementById('files-btn-up').disabled = false;
  filesRenderPath();
  if (filesCurrentShare) filesUpdateActionButtons();

  var listEl = document.getElementById('files-content');
  listEl.innerHTML = '<div class="files-empty">Loading...</div>';

  // At the machine root (no share selected), fetch the share list and render
  // each share as a folder entry. Entering one calls filesEnterShare().
  if (!filesCurrentShare) {
    var req = JSON.stringify({op: 'list-shares'});
    goApp.FileShareRequest(machine, req).then(function (respJSON) {
      if (gen !== filesListGeneration) return;
      try {
        var resp = JSON.parse(respJSON);
        if (!resp.ok) {
          listEl.innerHTML = '<div class="files-empty">' + escHtml(resp.error || 'No shares') + '</div>';
          return;
        }
        filesCurrentPath = '';
        filesCurrentEntries = (resp.shares || []).map(function (s) {
          return { name: s.name, isDir: true, size: 0, modTime: '', _shareInfo: s };
        });
        filesRenderEntries();
        filesUpdateStatusBar();
      } catch (e) {
        listEl.innerHTML = '<div class="files-empty">Could not read share list.</div>';
      }
    }).catch(function () {
      if (gen !== filesListGeneration) return;
      listEl.innerHTML = '<div class="files-empty">Could not connect to file share service.</div>';
    });
    return;
  }

  var pageSize = 50;
  function fetchPage(offset, accumulated) {
    var req = JSON.stringify({op: 'list', share: filesCurrentShare, path: path, offset: offset, limit: pageSize});
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
    if (entry._shareInfo) {
      filesEnterShare(entry._shareInfo);
    } else {
      var path = filesCurrentPath ? filesCurrentPath + '/' + entry.name : entry.name;
      filesNavigateTo(path);
    }
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
    var req = JSON.stringify({op: 'move', share: filesCurrentShare, path: srcPath, newPath: dstPath});
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
    var req = JSON.stringify({op: 'move', share: filesCurrentShare, path: srcPath, newPath: dstPath});
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
    var req = JSON.stringify({op: 'move', share: filesCurrentShare, path: srcPath, newPath: dstPath});
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

function filesEnterShare(shareInfo) {
  filesNavHistory.push(filesCurrentMachine + ':share:');
  filesCurrentShare = shareInfo.name;
  filesCurrentWritable = !!shareInfo.writable;
  filesCurrentAllowDelete = !!(shareInfo.writable && shareInfo.allowDelete);
  filesCurrentPath = '';
  filesClearSelection();
  filesListDir(filesCurrentMachine, '');
}

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
  // Return to machine root (share list view).
  if (prev.indexOf(':share:') >= 0) {
    filesCurrentMachine = prev.substring(0, prev.indexOf(':share:'));
    filesCurrentShare = '';
    filesCurrentPath = '';
    filesClearSelection();
    filesListDir(filesCurrentMachine, '');
    return;
  }
  var colonIdx = prev.indexOf(':');
  filesCurrentMachine = colonIdx >= 0 ? prev.substring(0, colonIdx) : prev;
  filesCurrentPath = colonIdx >= 0 ? prev.substring(colonIdx + 1) : '';
  filesClearSelection();
  filesListDir(filesCurrentMachine, filesCurrentPath);
}

function filesGoUp() {
  if (!filesCurrentMachine) return;
  if (!filesCurrentPath) {
    if (filesCurrentShare) {
      // At share root. If this machine has multiple shares show the share list;
      // if only one share skip the one-item list and go to the machine list.
      var mc = filesMachineCapabilities[filesCurrentMachine];
      var shareCount = (mc && mc.shares) ? mc.shares.length : 0;
      if (shareCount > 1) {
        filesNavHistory.push(filesCurrentMachine + ':share:');
        filesCurrentShare = '';
        filesClearSelection();
        filesListDir(filesCurrentMachine, '');
      } else {
        filesShowMachineList();
      }
    } else {
      filesShowMachineList();
    }
    return;
  }
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
  // filesView === 'files': machine root (share='' path=''), share root (share!='' path=''), or subdir.
  var html = '<span class="files-path-seg root" onclick="filesShowMachineList()">Machines</span>';
  html += '<span class="files-path-sep">&#x203A;</span>';

  if (filesCurrentShare) {
    // Inside a share: Machines > machine > share > path...
    html += '<span class="files-path-seg" onclick="filesNavHistory.push(filesCurrentMachine+\':share:\'); filesCurrentShare=\'\'; filesClearSelection(); filesListDir(filesCurrentMachine,\'\');">' + escHtml(filesCurrentMachine) + '</span>';
    html += '<span class="files-path-sep">&#x203A;</span>';
    html += '<span class="files-path-seg"' + dropAttrs('') + ' onclick="filesNavHistory.push(filesCurrentMachine+\':\'+filesCurrentPath); filesCurrentPath=\'\'; filesClearSelection(); filesListDir(filesCurrentMachine,\'\');">' + escHtml(filesCurrentShare) + '</span>';
  } else {
    // Machine root (share list): Machines > machine
    html += '<span class="files-path-seg">' + escHtml(filesCurrentMachine) + '</span>';
  }
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
    goApp.FileShareDownload(machine, filesCurrentShare, remotePath, localPath).then(function (respJSON) {
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
    goApp.FileShareUpload(machine, filesCurrentShare, localPath, remoteName).then(function (respJSON) {
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
      var req = JSON.stringify({op: 'delete', share: filesCurrentShare, path: remotePath});
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
    var req = JSON.stringify({op: 'mkdir', share: filesCurrentShare, path: fullPath});
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
    var req = JSON.stringify({op: 'rename', share: filesCurrentShare, path: oldPath, newName: newName});
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
  var modeClass = filesCurrentWritable ? 'dot-online' : 'dot-degraded';
  var modeText = filesCurrentWritable ? 'read-write' : 'read-only';
  document.getElementById('files-status').innerHTML = '<span>' + text + '</span>'
    + (totalSize > 0 ? '<span class="files-status-sep"></span><span>' + formatFileSize(totalSize) + '</span>' : '')
    + '<span class="files-status-mode"><span class="dot ' + modeClass + '"></span>' + modeText + '</span>';
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
var knownHubsData = []; // populated by refreshHubsTab, keyed by .name

// hubURLFor returns the actual hub URL for a given hub name, falling
// back to the name itself if no hub record is found.
function hubURLFor(hubName) {
  var h = knownHubsData.find(function (x) { return x.name === hubName; });
  return (h && h.url) ? h.url : hubName;
}

function refreshHubsTab() {
  var select = document.getElementById('hub-admin-select');
  if (!select) return;

  // Load hubs and settings in parallel; restore last-selected hub from
  // settings on first paint of this session (after that, prefer the
  // in-session selection so a manual switch sticks until next refresh).
  Promise.all([goApp.GetKnownHubs(), goApp.GetSettings()]).then(function (results) {
    var hubs = results[0] || [];
    hubs.sort(function (a, b) {
      var an = (a.name || '').toLowerCase();
      var bn = (b.name || '').toLowerCase();
      if (an < bn) return -1;
      if (an > bn) return 1;
      return 0;
    });
    knownHubsData = hubs;
    var settings = results[1] || {};
    var prev = select.value || settings.lastSelectedHub || '';
    select.innerHTML = '';
    if (hubs.length === 0) {
      select.innerHTML = '<option value="">No hubs configured</option>';
      currentAdminHub = '';
      renderHubAdminDetail();
      return;
    }
    hubs.forEach(function (hub) {
      var opt = document.createElement('option');
      opt.value = hub.name;
      opt.textContent = hub.name;
      select.appendChild(opt);
    });
    if (prev && hubs.some(function (h) { return h.name === prev; })) {
      select.value = prev;
    }
    currentAdminHub = select.value;
    renderHubAdminDetail();
  });
}

function onHubAdminSelect() {
  currentAdminHub = document.getElementById('hub-admin-select').value;
  goApp.SaveLastSelectedHub(currentAdminHub).catch(function () { /* best effort */ });
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
    case 'console': renderHubConsole(pane); break;
    case 'history': renderHubHistory(pane); break;
    default: pane.innerHTML = '';
  }
}

// --- Hub Settings View ---

function renderHubSettings(pane) {
  var hub = currentAdminHub;
  var hubName = hub;
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
    var hubUrl = hubURLFor(hub);
    var consoleUrl = toConsoleURL(hubUrl);

    var html = '<h2>Hub Settings</h2>'
      + '<p class="section-desc">Connection and configuration for <strong>' + escHtml(hubName) + '</strong></p>';

    // Connection group
    html += '<div class="settings-group"><div class="settings-group-header">Connection</div>';
    html += '<div class="settings-row"><div class="settings-label">URL</div><div class="settings-value">' + escHtml(hubUrl) + '</div></div>';

    var statusBadge = hubInfoData
      ? '<span class="status status-online"><span class="status-dot"></span>Online</span>'
      : '<span class="status status-offline"><span class="status-dot"></span>Offline</span>';
    html += '<div class="settings-row"><div class="settings-label">Status</div><div class="settings-value" style="font-family:var(--font)">' + statusBadge + '</div></div>';

    html += '<div class="settings-row"><div class="settings-label">Your role</div><div class="settings-value" style="font-family:var(--font)"><span class="chip chip-role-' + tokenRole + '">' + tokenRole + '</span></div></div>';
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
      html += '<div class="settings-group" id="hub-management-card">'
        + '<div class="settings-group-header" style="display:flex;align-items:center;justify-content:space-between;gap:8px;">'
        + '<span>Management</span>'
        + '<button type="button" class="btn btn-sm tools-action-btn" onclick="refreshHubManagement(\'' + escAttr(hub) + '\')" title="Refresh channel status">&#x21BB; Refresh</button>'
        + '</div>';
      html += '<div class="settings-row"><div class="settings-label">Log output</div>'
        + '<div class="settings-value"><button class="btn btn-sm" onclick="hubViewLogs(\'' + escAttr(hubName) + '\')">View Logs</button></div></div>';
      html += '<div class="settings-row"><div class="settings-label">Release channel</div>'
        + '<div class="settings-value">' + renderChannelSelect('hub-channel-select', '') + ' <span id="hub-channel-status" class="tools-service-label">loading...</span></div></div>';
      html += '<div class="settings-row"><div class="settings-label">Software</div>'
        + '<div class="settings-value"><button class="btn btn-sm" id="hub-update-btn" disabled onclick="hubUpdate(\'' + escAttr(hub) + '\',\'' + escAttr(hubName) + '\')">Update</button>'
        + ' <span id="hub-update-status" class="tools-service-label">loading...</span></div></div>';
      html += '<div class="settings-row"><div class="settings-label">Restart</div>'
        + '<div class="settings-value"><button class="btn btn-sm" onclick="hubRestart(\'' + escAttr(hub) + '\',\'' + escAttr(hubName) + '\')">Restart</button></div></div>';
      html += '</div>';

      // Channel Sources card: custom release channels stored in the hub's
      // own update.sources map. These are visible in the hub's Release
      // channel dropdown above and persist in the hub's YAML config.
      // Uses setting-card markup to match the Channel Sources card on the
      // Agents page so spacing and typography are consistent.
      html += '<div class="setting-card" id="hub-channel-sources-card">'
        + '<div class="setting-card-title">Channel Sources</div>'
        + '<div class="setting-card-desc">Custom release channels available in this hub\'s Release channel dropdown. Built-in channels (dev, beta, stable) are always present.</div>'
        + '<div id="hub-channel-sources-list" class="channel-sources-list"></div>'
        + '<div class="channel-source-add-row" style="margin-top:8px;">'
        + '<input type="text" id="hub-new-channel-name" class="tb-input channel-src-input-name" placeholder="name (e.g. local)">'
        + '<input type="text" id="hub-new-channel-base" class="tb-input channel-src-input-base" placeholder="http://localhost:9900/">'
        + '<button type="button" class="btn btn-sm" onclick="hubAddChannelSource(\'' + escAttr(hub) + '\')">Add</button>'
        + '</div>'
        + '</div>';
    }

    // Danger zone
    html += '<div class="settings-group danger-zone"><div class="settings-group-header">Danger Zone</div>'
      + '<div class="settings-row"><div class="settings-label">Remove hub</div>'
      + '<div class="settings-value danger-value">'
      + '<span class="danger-desc">Remove this hub from TelaVisor. Does not affect the hub itself.</span>'
      + '<button class="btn btn-sm btn-danger" onclick="removeHub(\'' + escAttr(hub) + '\')">Remove Hub</button>'
      + '</div></div>'
      + '<div class="settings-row"><div class="settings-label">Clear credentials</div>'
      + '<div class="settings-value danger-value">'
      + '<span class="danger-desc">Delete all stored hub tokens. You will need to re-add hubs.</span>'
      + '<button class="btn btn-sm btn-danger" onclick="clearCredentialStore()">Clear All</button>'
      + '</div></div></div>';

    pane.innerHTML = html;

    if ((tokenRole === 'owner' || tokenRole === 'admin') && document.getElementById('hub-channel-select')) {
      loadHubChannel(hub);
      loadHubChannelSources(hub);
    }
  }

  goApp.GetHubInfo(hub).then(function (raw) {
    try {
      var parsed = JSON.parse(raw);
      hubInfoData = (parsed && parsed.error) ? null : parsed;
    } catch (e) { hubInfoData = null; }
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
    + '<p class="section-desc">Registered machines on <strong>' + escHtml(hub) + '</strong></p>'
    + '<p class="loading">Loading...</p>';


  goApp.GetHubStatus(hub).then(function (status) {
    if (!status.online) {
      pane.innerHTML = '<h2>Machines</h2><p class="section-desc">Hub is offline or unreachable.</p>';
      return;
    }
    var html = '<h2>Machines</h2>'
      + '<p class="section-desc">Registered machines on <strong>' + escHtml(hub) + '</strong></p>';

    if (!status.machines || status.machines.length === 0) {
      html += '<p class="empty-hint">No machines registered.</p>';
    } else {
      status.machines.forEach(function (m) {
        var dotClass = m.agentConnected ? 'dot-online' : 'dot-offline';
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
          + '<span class="dot dot-lg ' + dotClass + '"></span>'
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

// formatRole converts a wire-level role value (lowercase, possibly
// empty) into the capitalized label the UI shows everywhere:
//   ""       -> "User"   (empty is the default user role)
//   "user"   -> "User"
//   "admin"  -> "Admin"
//   "owner"  -> "Owner"
//   "viewer" -> "Viewer"
// Unknown values are passed through with the first letter capitalized
// so future roles display sanely without a code change.
function formatRole(r) {
  if (!r) return 'User';
  return r.charAt(0).toUpperCase() + r.slice(1);
}

function submitCreateToken(event) {
  event.preventDefault();
  var id = document.getElementById('new-token-id').value.trim();
  var selectedRole = document.getElementById('new-token-role').value;
  if (!id) return;
  // Backend treats an empty role as "user" (the default). The dropdown
  // shows "User" for clarity; we normalize to "" on the wire because
  // the backend's role validation rejects the literal "user".
  var wireRole = (selectedRole === 'user') ? '' : selectedRole;

  goApp.AdminCreateToken(currentAdminHub, id, wireRole).then(function (raw) {
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
      showResultModal('Token Created', 'Copy this token now. It will not be shown again.', data.token, 'Identity: ' + id + ' | Role: ' + formatRole(selectedRole));
    }
    accessReload();
  });
}

function deleteToken(id) {
  showConfirmDialog('Delete Token', 'Delete identity "' + id + '"? This removes the token and all its ACL entries.', 'Delete').then(function (yes) {
    if (!yes) return;
    goApp.AdminDeleteToken(currentAdminHub, id).then(function (raw) {
      var data;
      try { data = JSON.parse(raw || '{}'); } catch (e) { data = {}; }
      if (data.error) {
        showError('Delete failed: ' + data.error);
        return;
      }
      accessReload();
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
      accessReload();
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

// --- Hub Console View ---

// toConsoleURL turns a hub identifier (which may be a bare host name
// like "owlsnest.parkscomputing.com" or a wss:// URL) into an absolute
// https:// URL with a trailing slash, suitable for an <a href> the
// system browser can open.
function toConsoleURL(hub) {
  var s = (hub || '').trim();
  if (s.indexOf('wss://') === 0) s = 'https://' + s.substring(6);
  else if (s.indexOf('ws://') === 0) s = 'http://' + s.substring(5);
  else if (s.indexOf('http://') !== 0 && s.indexOf('https://') !== 0) {
    s = 'https://' + s;
  }
  return s.replace(/\/$/, '') + '/';
}

function renderHubConsole(pane) {
  var hub = currentAdminHub;
  var consoleUrl = toConsoleURL(hubURLFor(hub));
  pane.innerHTML = '<h2>Hub Console</h2>'
    + '<p class="section-desc">Embedded console for <strong>' + escHtml(hub) + '</strong></p>'
    + '<div style="margin-bottom:8px;"><a href="' + escAttr(consoleUrl) + '" target="_blank" rel="noopener" style="font-size:0.82rem;color:var(--accent);">Open in browser</a></div>'
    + '<iframe src="' + escAttr(consoleUrl) + '" style="width:100%;height:500px;border:1px solid var(--border);border-radius:var(--radius);background:var(--bg);"></iframe>';
}

// --- Hub History View ---

function renderHubHistory(pane) {
  var hub = currentAdminHub;
  pane.innerHTML = '<h2>Connection History</h2>'
    + '<p class="section-desc">Recent sessions on <strong>' + escHtml(hub) + '</strong></p>'
    + '<p class="loading">Loading...</p>';


  goApp.GetHubHistory(hub).then(function (raw) {
    var data;
    try { data = JSON.parse(raw); } catch (e) { data = {}; }
    if (data.error) {
      pane.innerHTML = '<h2>Connection History</h2><p class="section-desc">' + escHtml(data.error) + '</p>';
      return;
    }
    var events = data.events || data || [];
    if (!Array.isArray(events)) events = [];

    var html = '<h2>Connection History</h2>'
      + '<p class="section-desc">Recent sessions on <strong>' + escHtml(hub) + '</strong></p>';

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
  var chk = document.getElementById('verbose-check');
  verboseMode = chk ? chk.checked : !verboseMode;
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
      dot.className = state.connected ? 'dot dot-online' : 'dot dot-offline';
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

// ── Portal sources (Remotes tab) ──────────────────────────────────
//
// The Remotes tab in Infrastructure mode is the portal-sources UI.
// "Local" is the embedded in-process portal that ships inside TV;
// remote entries are portals the user has signed into via the
// OAuth 2.0 device authorization grant. portalSourceToggleEnabled,
// portalSourceRemove, and the openAddPortalSourceDialog flow below
// are the documented entry points the Remotes tab calls.

// State for the in-progress device code flow.
var portalSourceFlow = null;
// The host-derived name that PortalDeviceAuthComplete persisted.
var portalSourceOriginalName = '';

function portalSourceToggleEnabled(name, enabled) {
  goApp.PortalSetSourceEnabled(name, enabled).then(function () {
    refreshRemotesList();
    if (typeof refreshHubsTab === 'function') refreshHubsTab();
    if (typeof refreshAll === 'function') refreshAll();
  }).catch(function (err) {
    showError('Failed to update remote: ' + err);
  });
}

function portalSourceRemove(name) {
  showConfirmDialog('Remove Remote', 'Remove the remote "' + name + '"? You can sign in again later.', 'Remove').then(function (yes) {
    if (!yes) return;
    goApp.PortalRemoveSource(name).then(function () {
      refreshRemotesList();
      if (typeof refreshHubsTab === 'function') refreshHubsTab();
      if (typeof refreshAll === 'function') refreshAll();
    }).catch(function (err) {
      showError('Failed to remove remote: ' + err);
    });
  });
}

function openAddPortalSourceDialog() {
  portalSourceFlow = null;
  document.getElementById('portal-source-step-url').classList.remove('hidden');
  document.getElementById('portal-source-step-code').classList.add('hidden');
  document.getElementById('portal-source-step-name').classList.add('hidden');
  document.getElementById('portal-source-url-input').value = '';
  document.getElementById('portal-source-error').classList.add('hidden');
  document.getElementById('add-portal-source-modal').classList.remove('hidden');
}

function closeAddPortalSourceDialog() {
  document.getElementById('add-portal-source-modal').classList.add('hidden');
  portalSourceFlow = null;
}

function portalSourceBeginSignIn() {
  var url = document.getElementById('portal-source-url-input').value.trim();
  var errEl = document.getElementById('portal-source-error');
  errEl.classList.add('hidden');
  if (!url) {
    errEl.textContent = 'Please enter a portal URL.';
    errEl.classList.remove('hidden');
    return;
  }
  errEl.textContent = 'Discovering portal...';
  errEl.classList.remove('hidden');

  goApp.PortalDeviceAuthStart(url).then(function (result) {
    portalSourceFlow = result;
    document.getElementById('portal-source-step-url').classList.add('hidden');
    document.getElementById('portal-source-step-code').classList.remove('hidden');
    document.getElementById('portal-source-user-code').textContent = result.userCode;
    document.getElementById('portal-source-verification-uri').textContent = result.verificationURI;
    document.getElementById('portal-source-poll-status').textContent = 'Waiting for authorization in your browser...';

    // Pre-fill source name with the host portion of the URL.
    var hostGuess = result.baseURL.replace(/^https?:\/\//, '').replace(/\/$/, '').split(':')[0];
    document.getElementById('portal-source-name-input').value = hostGuess;

    // Kick off polling. PortalDeviceAuthComplete blocks until token,
    // denied, or expired -- one promise, no client-side loop.
    portalSourceOriginalName = hostGuess;
    goApp.PortalDeviceAuthComplete(hostGuess, result.baseURL, result.deviceCode, result.interval).then(function () {
      // Backend persisted and marked the source active. Switch to the
      // name step so the user can confirm or rename.
      document.getElementById('portal-source-step-code').classList.add('hidden');
      document.getElementById('portal-source-step-name').classList.remove('hidden');
      document.getElementById('portal-source-poll-status').textContent = 'Authorized.';
    }).catch(function (err) {
      document.getElementById('portal-source-poll-status').textContent = 'Sign-in failed: ' + err;
    });
  }).catch(function (err) {
    errEl.textContent = 'Failed: ' + err;
    errEl.classList.remove('hidden');
  });
}

function portalSourceFinish() {
  // The backend already persisted the source under the host-derived
  // name during PortalDeviceAuthComplete. If the user typed a
  // different name, rename it via PortalRenameSource.
  var name = document.getElementById('portal-source-name-input').value.trim();
  var errEl = document.getElementById('portal-source-name-error');
  errEl.classList.add('hidden');
  if (!name) {
    errEl.textContent = 'Please enter a name.';
    errEl.classList.remove('hidden');
    return;
  }

  function finish() {
    portalSourceOriginalName = '';
    closeAddPortalSourceDialog();
    refreshRemotesList();
    if (typeof refreshHubsTab === 'function') refreshHubsTab();
  }

  if (portalSourceOriginalName && name !== portalSourceOriginalName) {
    goApp.PortalRenameSource(portalSourceOriginalName, name).then(function () {
      finish();
    }).catch(function (err) {
      errEl.textContent = 'Rename failed: ' + err;
      errEl.classList.remove('hidden');
    });
  } else {
    finish();
  }
}

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

  // Load channel sources first, then populate the client channel select with
  // the full list (including any custom entries).
  loadChannelSources(function () {
    populateChannelSelect('client-channel-select');
    loadClientChannel();
  });
}

// populateChannelSelect rebuilds a channel <select> element's options from
// the current channelSources global, preserving any currently-selected value.
function populateChannelSelect(id) {
  var sel = document.getElementById(id);
  if (!sel) return;
  var current = sel.value;
  sel.innerHTML = '';
  channelSources.forEach(function (src) {
    var opt = document.createElement('option');
    opt.value = src.name;
    opt.textContent = src.name;
    sel.appendChild(opt);
  });
  if (current) sel.value = current;
}

// refreshChannelSourcesUI renders the custom channel sources list in the
// Client Settings card. Source of truth is the credential store (read via
// goApp.GetClientSources). All Add/Edit/Remove actions persist immediately;
// after the backend call returns we reload channelSources and re-render so
// the UI always reflects what is actually on disk.
function refreshChannelSourcesUI() {
  var container = document.getElementById('custom-channel-sources');
  if (!container) return;
  goApp.GetClientSources().then(function (raw) {
    var customs = [];
    try { customs = JSON.parse(raw) || []; } catch (e) {}
    if (customs.length === 0) {
      container.innerHTML = '<div class="channel-sources-empty">No custom channel sources.</div>';
      return;
    }
    var html = '<table class="channel-sources-table">';
    customs.forEach(function (src) {
      html += '<tr data-src-name="' + escAttr(src.name) + '">'
        + '<td class="channel-src-name">' + escHtml(src.name) + '</td>'
        + '<td class="channel-src-base">' + escHtml(src.manifestBase) + '</td>'
        + '<td class="channel-src-actions">'
        + '<button type="button" class="btn btn-sm channel-src-edit-btn">Edit</button>'
        + ' <button type="button" class="btn btn-sm btn-danger channel-src-remove-btn">Remove</button>'
        + '</td>'
        + '</tr>';
    });
    html += '</table>';
    container.innerHTML = html;
    container.querySelectorAll('tr[data-src-name]').forEach(function (row) {
      var name = row.getAttribute('data-src-name');
      row.querySelector('.channel-src-edit-btn').onclick = function () { editChannelSource(name); };
      row.querySelector('.channel-src-remove-btn').onclick = function () { removeChannelSource(name); };
    });
  });
}

// reloadChannelSourcesAndDropdowns refreshes the in-memory channelSources
// global (used to populate every channel dropdown) and re-renders the
// Client Settings card. Call after any successful CRUD mutation.
function reloadChannelSourcesAndDropdowns() {
  loadChannelSources(function () {
    populateChannelSelect('client-channel-select');
    refreshChannelSourcesUI();
  });
}

function editChannelSource(oldName) {
  var row = document.querySelector('#custom-channel-sources tr[data-src-name="' + oldName + '"]');
  if (!row) return;
  var src = channelSources.find(function (s) { return s.name === oldName; });
  if (!src) return;
  row.innerHTML = '<td><input type="text" class="tb-input channel-src-input-name" value="' + escAttr(src.name) + '"></td>'
    + '<td><input type="text" class="tb-input channel-src-input-base" value="' + escAttr(src.manifestBase) + '"></td>'
    + '<td class="channel-src-actions">'
    + '<button type="button" class="btn btn-sm channel-src-save-btn">Save</button>'
    + ' <button type="button" class="btn btn-sm channel-src-cancel-btn">Cancel</button>'
    + '</td>';
  row.querySelector('.channel-src-save-btn').onclick = function () { saveChannelSourceEdit(oldName, row); };
  row.querySelector('.channel-src-cancel-btn').onclick = function () { refreshChannelSourcesUI(); };
}

function saveChannelSourceEdit(oldName, row) {
  var newName = row.querySelector('.channel-src-input-name').value.trim().toLowerCase();
  var newBase = row.querySelector('.channel-src-input-base').value.trim();
  if (!newName) { showError('Channel name is required.'); return; }
  if (!/^[a-z0-9-]+$/.test(newName)) { showError('Channel name must contain only lowercase letters, digits, and hyphens.'); return; }
  if (['dev', 'beta', 'stable'].indexOf(newName) !== -1) { showError('Cannot use a built-in channel name.'); return; }
  if (!newBase) { showError('Channel URL is required.'); return; }
  var apply = function () {
    goApp.SetClientSource(newName, newBase).then(function (raw) {
      var resp = {};
      try { resp = JSON.parse(raw) || {}; } catch (e) {}
      if (resp.error) { showError(resp.error); return; }
      reloadChannelSourcesAndDropdowns();
    });
  };
  if (newName !== oldName) {
    goApp.RemoveClientSource(oldName).then(function () { apply(); });
  } else {
    apply();
  }
}

function addChannelSource() {
  var nameEl = document.getElementById('new-channel-name');
  var baseEl = document.getElementById('new-channel-base');
  if (!nameEl || !baseEl) return;
  var name = nameEl.value.trim().toLowerCase();
  var base = baseEl.value.trim();
  if (!name || !base) return;
  if (!/^[a-z0-9-]+$/.test(name)) { showError('Channel name must contain only lowercase letters, digits, and hyphens.'); return; }
  if (name === 'dev' || name === 'beta' || name === 'stable') { showError('Cannot add a built-in channel name.'); return; }
  for (var i = 0; i < channelSources.length; i++) {
    if (channelSources[i].name === name) { showError('Channel already exists.'); return; }
  }
  goApp.SetClientSource(name, base).then(function (raw) {
    var resp = {};
    try { resp = JSON.parse(raw) || {}; } catch (e) {}
    if (resp.error) { showError(resp.error); return; }
    nameEl.value = '';
    baseEl.value = '';
    reloadChannelSourcesAndDropdowns();
  });
}

function removeChannelSource(name) {
  goApp.RemoveClientSource(name).then(function (raw) {
    var resp = {};
    try { resp = JSON.parse(raw) || {}; } catch (e) {}
    if (resp.error) { showError(resp.error); return; }
    reloadChannelSourcesAndDropdowns();
  });
}

// ── Hub channel sources ─────────────────────────────────────────────
//
// Mirrors the Client Settings card but targets the hub's update.sources
// map via the admin API (GET/PUT/DELETE /api/admin/update/sources). All
// CRUD operations persist immediately and re-render from the server's
// authoritative response, matching the per-source CLI commands.

function loadHubChannelSources(hub) {
  var container = document.getElementById('hub-channel-sources-list');
  if (!container) return;
  goApp.GetHubSources(hub).then(function (raw) {
    var parsed = null;
    try { parsed = JSON.parse(raw); } catch (e) {}
    // Pre-channel-sources hub returns {"error":"..."} (the route 404s).
    // Hide the whole card rather than show a broken form.
    if (parsed && !Array.isArray(parsed) && parsed.error) {
      var card = document.getElementById('hub-channel-sources-card');
      if (card) card.style.display = 'none';
      return;
    }
    var customs = Array.isArray(parsed) ? parsed : [];
    if (customs.length === 0) {
      container.innerHTML = '<div class="channel-sources-empty">No custom channel sources.</div>';
      return;
    }
    var html = '<table class="channel-sources-table">';
    customs.forEach(function (src) {
      html += '<tr data-src-name="' + escAttr(src.name) + '">'
        + '<td class="channel-src-name">' + escHtml(src.name) + '</td>'
        + '<td class="channel-src-base">' + escHtml(src.manifestBase) + '</td>'
        + '<td class="channel-src-actions">'
        + '<button type="button" class="btn btn-sm channel-src-edit-btn">Edit</button>'
        + ' <button type="button" class="btn btn-sm btn-danger channel-src-remove-btn">Remove</button>'
        + '</td>'
        + '</tr>';
    });
    html += '</table>';
    container.innerHTML = html;
    container.querySelectorAll('tr[data-src-name]').forEach(function (row) {
      var name = row.getAttribute('data-src-name');
      row.querySelector('.channel-src-edit-btn').onclick = function () { hubEditChannelSource(hub, name); };
      row.querySelector('.channel-src-remove-btn').onclick = function () { hubRemoveChannelSource(hub, name); };
    });
  });
}

function hubEditChannelSource(hub, oldName) {
  var row = document.querySelector('#hub-channel-sources-list tr[data-src-name="' + oldName + '"]');
  if (!row) return;
  var oldBase = row.querySelector('.channel-src-base') ? row.querySelector('.channel-src-base').textContent : '';
  row.innerHTML = '<td><input type="text" class="tb-input channel-src-input-name" value="' + escAttr(oldName) + '"></td>'
    + '<td><input type="text" class="tb-input channel-src-input-base" value="' + escAttr(oldBase) + '"></td>'
    + '<td class="channel-src-actions">'
    + '<button type="button" class="btn btn-sm channel-src-save-btn">Save</button>'
    + ' <button type="button" class="btn btn-sm channel-src-cancel-btn">Cancel</button>'
    + '</td>';
  row.querySelector('.channel-src-save-btn').onclick = function () { hubSaveChannelSourceEdit(hub, oldName, row); };
  row.querySelector('.channel-src-cancel-btn').onclick = function () { loadHubChannelSources(hub); };
}

function hubSaveChannelSourceEdit(hub, oldName, row) {
  var newName = row.querySelector('.channel-src-input-name').value.trim().toLowerCase();
  var newBase = row.querySelector('.channel-src-input-base').value.trim();
  if (!newName) { showError('Channel name is required.'); return; }
  if (!/^[a-z0-9-]+$/.test(newName)) { showError('Channel name must contain only lowercase letters, digits, and hyphens.'); return; }
  if (['dev', 'beta', 'stable'].indexOf(newName) !== -1) { showError('Cannot use a built-in channel name.'); return; }
  if (!newBase) { showError('Channel URL is required.'); return; }
  var apply = function () {
    goApp.SetHubSource(hub, newName, newBase).then(function (raw) {
      var resp = {};
      try { resp = JSON.parse(raw) || {}; } catch (e) {}
      if (resp.error) { showError(resp.error); return; }
      loadHubChannelSources(hub);
      // Re-populate the Release channel dropdown so the new/renamed entry appears.
      reloadHubChannelDropdown(hub);
    });
  };
  if (newName !== oldName) {
    goApp.RemoveHubSource(hub, oldName).then(function () { apply(); });
  } else {
    apply();
  }
}

function hubAddChannelSource(hub) {
  var nameEl = document.getElementById('hub-new-channel-name');
  var baseEl = document.getElementById('hub-new-channel-base');
  if (!nameEl || !baseEl) return;
  var name = nameEl.value.trim().toLowerCase();
  var base = baseEl.value.trim();
  if (!name || !base) return;
  if (!/^[a-z0-9-]+$/.test(name)) { showError('Channel name must contain only lowercase letters, digits, and hyphens.'); return; }
  if (name === 'dev' || name === 'beta' || name === 'stable') { showError('Cannot add a built-in channel name.'); return; }
  goApp.SetHubSource(hub, name, base).then(function (raw) {
    var resp = {};
    try { resp = JSON.parse(raw) || {}; } catch (e) {}
    if (resp.error) { showError(resp.error); return; }
    nameEl.value = '';
    baseEl.value = '';
    loadHubChannelSources(hub);
    reloadHubChannelDropdown(hub);
  });
}

function hubRemoveChannelSource(hub, name) {
  goApp.RemoveHubSource(hub, name).then(function (raw) {
    var resp = {};
    try { resp = JSON.parse(raw) || {}; } catch (e) {}
    if (resp.error) { showError(resp.error); return; }
    loadHubChannelSources(hub);
    reloadHubChannelDropdown(hub);
  });
}

// reloadHubChannelDropdown rebuilds the hub's Release channel dropdown
// from the union of built-in channels and the hub's current sources, then
// re-selects whatever the hub currently reports as its active channel.
function reloadHubChannelDropdown(hub) {
  var sel = document.getElementById('hub-channel-select');
  if (!sel) return;
  goApp.GetHubSources(hub).then(function (raw) {
    var customs = [];
    try { customs = JSON.parse(raw) || []; } catch (e) {}
    var current = sel.value;
    sel.innerHTML = '';
    ['dev', 'beta', 'stable'].forEach(function (n) {
      var opt = document.createElement('option');
      opt.value = n; opt.textContent = n;
      sel.appendChild(opt);
    });
    customs.forEach(function (src) {
      var opt = document.createElement('option');
      opt.value = src.name; opt.textContent = src.name;
      sel.appendChild(opt);
    });
    if (current) sel.value = current;
  });
}

// ── Agent channel sources ───────────────────────────────────────────
//
// Mirrors the Hub Settings card but routes through the hub-mediated agent
// management protocol (channel-sources-list/set/remove mgmt actions).

function loadAgentChannelSources(hub, machineID) {
  var container = document.getElementById('agent-channel-sources-list');
  if (!container) return;
  goApp.GetAgentSources(hub, machineID).then(function (raw) {
    var parsed = null;
    try { parsed = JSON.parse(raw); } catch (e) {}
    // Pre-channel-sources agent returns {"error":"unknown action ..."}.
    // Hide the whole card rather than show a broken form.
    if (parsed && !Array.isArray(parsed) && parsed.error) {
      var card = document.getElementById('agent-channel-sources-card');
      if (card) card.style.display = 'none';
      return;
    }
    var customs = Array.isArray(parsed) ? parsed : [];
    if (customs.length === 0) {
      container.innerHTML = '<div class="channel-sources-empty">No custom channel sources.</div>';
      return;
    }
    var html = '<table class="channel-sources-table">';
    customs.forEach(function (src) {
      html += '<tr data-src-name="' + escAttr(src.name) + '">'
        + '<td class="channel-src-name">' + escHtml(src.name) + '</td>'
        + '<td class="channel-src-base">' + escHtml(src.manifestBase) + '</td>'
        + '<td class="channel-src-actions">'
        + '<button type="button" class="btn btn-sm channel-src-edit-btn">Edit</button>'
        + ' <button type="button" class="btn btn-sm btn-danger channel-src-remove-btn">Remove</button>'
        + '</td>'
        + '</tr>';
    });
    html += '</table>';
    container.innerHTML = html;
    container.querySelectorAll('tr[data-src-name]').forEach(function (row) {
      var name = row.getAttribute('data-src-name');
      row.querySelector('.channel-src-edit-btn').onclick = function () { agentEditChannelSource(hub, machineID, name); };
      row.querySelector('.channel-src-remove-btn').onclick = function () { agentRemoveChannelSource(hub, machineID, name); };
    });
  });
}

function agentEditChannelSource(hub, machineID, oldName) {
  var row = document.querySelector('#agent-channel-sources-list tr[data-src-name="' + oldName + '"]');
  if (!row) return;
  var oldBase = row.querySelector('.channel-src-base') ? row.querySelector('.channel-src-base').textContent : '';
  row.innerHTML = '<td><input type="text" class="tb-input channel-src-input-name" value="' + escAttr(oldName) + '"></td>'
    + '<td><input type="text" class="tb-input channel-src-input-base" value="' + escAttr(oldBase) + '"></td>'
    + '<td class="channel-src-actions">'
    + '<button type="button" class="btn btn-sm channel-src-save-btn">Save</button>'
    + ' <button type="button" class="btn btn-sm channel-src-cancel-btn">Cancel</button>'
    + '</td>';
  row.querySelector('.channel-src-save-btn').onclick = function () { agentSaveChannelSourceEdit(hub, machineID, oldName, row); };
  row.querySelector('.channel-src-cancel-btn').onclick = function () { loadAgentChannelSources(hub, machineID); };
}

function agentSaveChannelSourceEdit(hub, machineID, oldName, row) {
  var newName = row.querySelector('.channel-src-input-name').value.trim().toLowerCase();
  var newBase = row.querySelector('.channel-src-input-base').value.trim();
  if (!newName) { showError('Channel name is required.'); return; }
  if (!/^[a-z0-9-]+$/.test(newName)) { showError('Channel name must contain only lowercase letters, digits, and hyphens.'); return; }
  if (['dev', 'beta', 'stable'].indexOf(newName) !== -1) { showError('Cannot use a built-in channel name.'); return; }
  if (!newBase) { showError('Channel URL is required.'); return; }
  var apply = function () {
    goApp.SetAgentSource(hub, machineID, newName, newBase).then(function (raw) {
      var resp = {};
      try { resp = JSON.parse(raw) || {}; } catch (e) {}
      if (resp.error) { showError(resp.error); return; }
      loadAgentChannelSources(hub, machineID);
      reloadAgentChannelDropdown(hub, machineID);
    });
  };
  if (newName !== oldName) {
    goApp.RemoveAgentSource(hub, machineID, oldName).then(function () { apply(); });
  } else {
    apply();
  }
}

function agentAddChannelSource(hub, machineID) {
  var nameEl = document.getElementById('agent-new-channel-name');
  var baseEl = document.getElementById('agent-new-channel-base');
  if (!nameEl || !baseEl) return;
  var name = nameEl.value.trim().toLowerCase();
  var base = baseEl.value.trim();
  if (!name || !base) return;
  if (!/^[a-z0-9-]+$/.test(name)) { showError('Channel name must contain only lowercase letters, digits, and hyphens.'); return; }
  if (name === 'dev' || name === 'beta' || name === 'stable') { showError('Cannot add a built-in channel name.'); return; }
  goApp.SetAgentSource(hub, machineID, name, base).then(function (raw) {
    var resp = {};
    try { resp = JSON.parse(raw) || {}; } catch (e) {}
    if (resp.error) { showError(resp.error); return; }
    nameEl.value = '';
    baseEl.value = '';
    loadAgentChannelSources(hub, machineID);
    reloadAgentChannelDropdown(hub, machineID);
  });
}

function agentRemoveChannelSource(hub, machineID, name) {
  goApp.RemoveAgentSource(hub, machineID, name).then(function (raw) {
    var resp = {};
    try { resp = JSON.parse(raw) || {}; } catch (e) {}
    if (resp.error) { showError(resp.error); return; }
    loadAgentChannelSources(hub, machineID);
    reloadAgentChannelDropdown(hub, machineID);
  });
}

function reloadAgentChannelDropdown(hub, machineID) {
  var sel = document.getElementById('agent-channel-select');
  if (!sel) return;
  goApp.GetAgentSources(hub, machineID).then(function (raw) {
    var customs = [];
    try { customs = JSON.parse(raw) || []; } catch (e) {}
    var current = sel.value;
    sel.innerHTML = '';
    ['dev', 'beta', 'stable'].forEach(function (n) {
      var opt = document.createElement('option');
      opt.value = n; opt.textContent = n;
      sel.appendChild(opt);
    });
    customs.forEach(function (src) {
      var opt = document.createElement('option');
      opt.value = src.name; opt.textContent = src.name;
      sel.appendChild(opt);
    });
    if (current) sel.value = current;
  });
}

function gatherSettings() {
  var themeRadio = document.querySelector('input[name="setting-theme"]:checked');
  var logInput = document.getElementById('setting-logMaxLines');
  var logVal = logInput ? parseInt(logInput.value, 10) : 5000;
  if (isNaN(logVal) || logVal < 100) logVal = 100;

  var s = {
    autoConnect: document.getElementById('setting-autoConnect').checked,
    reconnectOnDrop: document.getElementById('setting-reconnectOnDrop').checked,
    confirmDisconnect: document.getElementById('setting-confirmDisconnect').checked,
    minimizeTo: 'tray',
    startMinimized: false,
    minimizeOnClose: document.getElementById('setting-minimizeOnClose').checked,
    autoCheckUpdates: document.getElementById('setting-autoCheckUpdates').checked,
    verboseDefault: document.getElementById('setting-verboseDefault').checked,
    theme: themeRadio ? themeRadio.value : 'system',
    logMaxLines: logVal
  };

  // defaultProfile and binPath are managed in Client Settings tab.
  // Only include them when those DOM elements are present; when they are
  // absent (e.g. update triggered from the update overlay) omit them so
  // SaveSettings preserves whatever is already on disk.
  var csProfile = document.getElementById('cs-default-profile');
  var csBinPath = document.getElementById('cs-binPath');
  if (csProfile) s.defaultProfile = csProfile.value;
  if (csBinPath) {
    var val = csBinPath.value.trim();
    var def = csBinPath.getAttribute('data-default') || '';
    s.binPath = val === def ? '' : val;
  }

  return s;
}

// Apply: save settings, disable Apply buttons, stay in dialog.
function applySettings() {
  var s = gatherSettings();
  var errEl = document.getElementById('settings-error');
  errEl.classList.add('hidden');
  errEl.textContent = '';

  return goApp.SaveSettings(JSON.stringify(s)).then(function () {
      settingsDirty = false;
      setSettingsButtonsDisabled(true);
      if (s.logMaxLines) logMaxLines = s.logMaxLines;
      (function () { var ub = document.getElementById('update-btn'); if (ub) { ub.disabled = true; ub.classList.remove('chrome-warn'); ub.title = 'No updates'; } })();
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
  var resolved;
  if (pref === 'system') {
    resolved = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  } else {
    resolved = pref;
  }
  document.documentElement.setAttribute('data-theme', resolved);
  // Topbar tracks the active theme via .tb-light / .tb-dark classes so
  // chrome buttons and brand mark can resolve --tb-* custom properties.
  var tb = document.querySelector('.topbar');
  if (tb) {
    tb.classList.toggle('tb-light', resolved === 'light');
    tb.classList.toggle('tb-dark', resolved !== 'light');
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
    (function () { var ub = document.getElementById('update-btn'); if (ub) { ub.disabled = true; ub.classList.remove('chrome-warn'); ub.title = 'No updates'; } })();
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
        dotClass = 'dot-online';
        verText = b.version;
      } else if (b.found && !b.upToDate) {
        dotClass = 'dot-degraded';
        verText = b.version + ' (latest: ' + (b.latest || '?') + ')';
        action = '<button class="btn btn-sm" onclick="installBinary(\'' + escAttr(b.name) + '\')">Update</button>';
      } else {
        dotClass = 'dot-error';
        verText = 'not found';
        action = '<button class="btn btn-sm" onclick="installBinary(\'' + escAttr(b.name) + '\')">Install</button>';
      }

      html += '<div class="bin-status-item">'
        + '<span class="dot ' + dotClass + '"></span>'
        + '<span class="bin-status-name">' + escHtml(b.name) + '</span>'
        + '<span class="bin-status-ver">' + escHtml(verText) + '</span>'
        + action
        + '<button type="button" class="btn btn-sm btn-icon" onclick="refreshSingleBinary(\'' + escAttr(b.name) + '\', this)" title="Check for updates">&#x21BB;</button>'
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

// restartToUpdate triggers TelaVisor's own self-update path: stage the
// new binary next to the running one, install the new tela CLI, then
// relaunch. The Go side does the heavy lifting; we just disable the
// button while it runs and surface any error to the log panel.
function restartToUpdate(btn) {
  if (btn) { btn.disabled = true; btn.textContent = 'Updating...'; }
  tvLog('Updating TelaVisor...');
  goApp.RestartToUpdate().then(function () {
    // On success the Go side calls os.Exit(0) so we never get here for
    // a real self-update. The package-managed branch does return; in
    // that case the tela CLI was updated but TelaVisor itself was not.
    tvLog('Update completed (TelaVisor itself is package-managed; restart it via your package manager).');
    if (btn) { btn.disabled = false; btn.textContent = 'Update & Restart'; }
    refreshBinStatus();
  }).catch(function (err) {
    var msg = String(err);
    tvLog('Update failed: ' + msg);
    if (btn) { btn.disabled = false; btn.textContent = 'Update & Restart'; }
    // Size or hash mismatch means the channel server is still serving the
    // previous binary while the manifest already lists the new one. This
    // commonly happens when a channel provider uses a file-sync service
    // (e.g. OneDrive, Dropbox) that uploads the manifest before finishing
    // the larger binary upload. Wait a minute or two and retry.
    if (/size mismatch|hash mismatch|verify/i.test(msg)) {
      showConfirmDialog(
        'Update Verification Failed',
        'The downloaded binary does not match the channel manifest.\n\n' +
        'A CDN or proxy in front of the channel server may be serving a ' +
        'cached copy of the previous binary. Purge the cache for the ' +
        'channel files URL and try again.',
        'OK'
      );
    }
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
//
// The Remotes tab is the portal-sources UI. "Local" is the embedded
// in-process portal that ships inside TV; remote entries are portals
// the user has signed into via the OAuth 2.0 device authorization
// grant. The Add button opens a 3-step modal (URL -> user code in
// browser -> name confirm) that drives the device code flow.
//
// renderRemotesView is kept as a no-op shim for legacy callers in
// the Hubs-mode admin nav. The standalone Remotes tab uses the
// markup in index.html and refreshRemotesList directly; the shim is
// retained until that nav item is removed in a follow-up.

function renderRemotesView(pane) {
  pane.innerHTML = '<h2>Remotes</h2>'
    + '<p class="section-desc">Portals TelaVisor talks to for hubs and agents.</p>'
    + '<div id="remotes-list-pane"></div>'
    + '<div class="remotes-add-row">'
    + '<button type="button" class="btn btn-sm" onclick="openAddPortalSourceDialog()">Add Remote...</button>'
    + '</div>';
  refreshRemotesList();
}

function refreshRemotesList() {
  var el = document.getElementById('remotes-list-pane');
  if (!el) return;
  goApp.PortalListSources().then(function (sources) {
    sources = sources || [];
    if (sources.length === 0) {
      el.innerHTML = '<p class="empty-hint">No remotes configured.</p>';
      return;
    }
    var html = '';
    sources.forEach(function (s) {
      var isEmbedded = s.kind === 'embedded';
      var cbId = 'ps-en-' + s.name.replace(/[^a-zA-Z0-9]/g, '_');
      html += '<div class="portal-sources-row">'
        + '<div class="source-info">'
        + '<div class="source-name-line">'
        + '<span class="source-name">' + escHtml(s.name) + '</span>'
        + '</div>'
        + '<div class="source-url">' + escHtml(s.url) + '</div>'
        + '</div>'
        + '<div class="source-actions">'
        + '<label class="settings-checkbox-label">'
        + '<input type="checkbox" id="' + escAttr(cbId) + '"'
        + (s.enabled ? ' checked' : '')
        + ' onchange="portalSourceToggleEnabled(\'' + escAttr(s.name) + '\', this.checked)">'
        + ' Enabled</label>';
      if (!isEmbedded) {
        html += '<button type="button" class="btn btn-sm btn-danger" onclick="portalSourceRemove(\'' + escAttr(s.name) + '\')">Remove</button>';
      }
      html += '</div></div>';
    });
    el.innerHTML = html;
  }).catch(function (err) {
    el.innerHTML = '<p class="empty-hint">Failed to load remotes: ' + escHtml(String(err)) + '</p>';
  });
}

function addRemote() { openAddPortalSourceDialog(); }
function removeRemote(name) { portalSourceRemove(name); }

// ── Credentials view (Hubs mode) ──────────────────────────────────

function renderCredentialsView(pane) {
  pane.innerHTML = '<h2>Stored Credentials</h2>'
    + '<p class="section-desc">Hub tokens stored in the local credential file. Equivalent to <code>tela login</code> / <code>tela logout</code>.</p>'
    + '<div id="credentials-list-pane"></div>'
    + '<div style="margin-top:12px;"><button type="button" class="btn btn-sm btn-danger" onclick="clearAllCredentials()">Clear All</button></div>';
  refreshCredentialsList();
}

function refreshCredentialsList() {
  goApp.ListCredentials().then(function (creds) {
    var el = document.getElementById('credentials-list-pane');
    if (!el) return;
    if (!creds || creds.length === 0) {
      // Check whether any remote portal sources are enabled. If so,
      // those portals manage hub credentials on the user's behalf,
      // which is why the local credential store may be empty.
      goApp.PortalListSources().then(function (sources) {
        var hasRemote = false;
        if (sources) {
          for (var i = 0; i < sources.length; i++) {
            if (sources[i].kind === 'remote' && sources[i].enabled) {
              hasRemote = true;
              break;
            }
          }
        }
        if (hasRemote) {
          el.innerHTML = '<p class="empty-hint">No locally stored credentials. '
            + 'Hubs discovered through a remote portal use tokens managed by the portal. '
            + 'Use <code>tela login</code> to store a token locally for direct hub access.</p>';
        } else {
          el.innerHTML = '<p class="empty-hint">No stored credentials.</p>';
        }
      }).catch(function () {
        el.innerHTML = '<p class="empty-hint">No stored credentials.</p>';
      });
      return;
    }
    var html = '<table class="admin-table"><thead><tr><th>Hub</th><th>Identity</th><th></th></tr></thead><tbody>';
    creds.forEach(function (c) {
      var identity = c.identity || '';
      html += '<tr><td>' + escHtml(c.hubUrl) + '</td>'
        + '<td>' + escHtml(identity) + '</td>'
        + '<td><button class="btn btn-sm btn-danger" onclick="removeCredential(\'' + escAttr(c.hubUrl) + '\')">Remove</button></td></tr>';
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
    var s = (raw || '');
    var lower = s.toLowerCase();

    // Parse system service section (appears before "User autostart:")
    var sysSection = lower.split('user autostart:')[0] || '';
    var sysInstalled = sysSection.indexOf('installed: true') !== -1;
    var sysRunning = sysSection.indexOf('running: true') !== -1;

    // Parse user autostart section (appears after "User autostart:")
    var userSection = lower.split('user autostart:')[1] || '';
    var userInstalled = userSection.indexOf('installed: true') !== -1;
    var userRunning = userSection.indexOf('running: true') !== -1;

    // System service UI
    var el = document.getElementById('cs-service-status');
    if (el) {
      if (!sysInstalled) {
        el.innerHTML = '<span style="color:var(--text-muted);">Not installed</span>';
      } else if (sysRunning) {
        el.innerHTML = '<span class="bin-dot bin-dot-ok"></span> Running';
      } else {
        el.innerHTML = '<span class="bin-dot bin-dot-missing"></span> Installed (stopped)';
      }
    }
    var installBtn = document.getElementById('svc-install-btn');
    var startBtn = document.getElementById('svc-start-btn');
    var stopBtn = document.getElementById('svc-stop-btn');
    var uninstallBtn = document.getElementById('svc-uninstall-btn');
    if (installBtn) installBtn.disabled = sysInstalled || sysRunning;
    if (startBtn) startBtn.disabled = !sysInstalled || sysRunning;
    if (stopBtn) stopBtn.disabled = !sysRunning;
    if (uninstallBtn) uninstallBtn.disabled = !sysInstalled && !sysRunning;

    // User autostart UI
    var uel = document.getElementById('cs-user-task-status');
    if (uel) {
      if (!userInstalled) {
        uel.innerHTML = '<span style="color:var(--text-muted);">Not installed</span>';
      } else if (userRunning) {
        uel.innerHTML = '<span class="bin-dot bin-dot-ok"></span> Running';
      } else {
        uel.innerHTML = '<span class="bin-dot bin-dot-missing"></span> Installed (stopped)';
      }
    }
    var uInstallBtn = document.getElementById('utask-install-btn');
    var uStartBtn = document.getElementById('utask-start-btn');
    var uStopBtn = document.getElementById('utask-stop-btn');
    var uUninstallBtn = document.getElementById('utask-uninstall-btn');
    if (uInstallBtn) uInstallBtn.disabled = userInstalled || userRunning;
    if (uStartBtn) uStartBtn.disabled = !userInstalled || userRunning;
    if (uStopBtn) uStopBtn.disabled = !userRunning;
    if (uUninstallBtn) uUninstallBtn.disabled = !userInstalled && !userRunning;

    // Disable system service install when not elevated
    goApp.IsElevated().then(function (elevated) {
      if (!elevated && installBtn) {
        installBtn.disabled = true;
        installBtn.title = 'Requires administrator privileges';
      }
    });
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

// ── User autostart management ─────────────────────────────────────

function installUserTask() {
  goApp.InstallAsUserTask().then(function (msg) {
    tvLog('User autostart installed: ' + msg);
    refreshServiceStatus();
  }).catch(function (err) {
    tvLog('Install user autostart failed: ' + err);
    showError('Install failed: ' + err);
  });
}

function uninstallUserTask() {
  showConfirmDialog('Uninstall User Autostart', 'Remove the user autostart task?', 'Uninstall').then(function (yes) {
    if (!yes) return;
    goApp.UninstallUserTask().then(function (msg) {
      tvLog('User autostart uninstalled: ' + msg);
      refreshServiceStatus();
    }).catch(function (err) {
      tvLog('Uninstall user autostart failed: ' + err);
    });
  });
}

function startUserTask() {
  goApp.UserTaskStart().then(function (msg) {
    tvLog('User autostart started: ' + msg);
    refreshServiceStatus();
  }).catch(function (err) {
    tvLog('Start user autostart failed: ' + err);
  });
}

function stopUserTask() {
  goApp.UserTaskStop().then(function (msg) {
    tvLog('User autostart stopped: ' + msg);
    refreshServiceStatus();
  }).catch(function (err) {
    tvLog('Stop user autostart failed: ' + err);
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
  applySettings().then(function () {
    clearCSDirty();
    // Refresh the Installed Tools section so it reflects any binPath change.
    refreshClientToolVersions();
  });
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
  // Channel sources card (render from credstore via backend binding)
  refreshChannelSourcesUI();
}

// refreshClientToolVersions is the legacy entry point used by code
// paths that mutate local binaries (service install/uninstall, mount
// enable/disable, profile import/export). Since the Installed Tools
// card moved to the Updates tab, the function now renders into the
// Updates tab's target when it exists. Older callers see no behavior
// change when the Updates tab has been opened at least once; before
// that, the function is a no-op and the next Updates-tab open
// picks up fresh state via refreshUpdatesTab's own force-refresh.
function refreshClientToolVersions() {
  var upd = document.getElementById('updates-tools-status');
  if (upd) renderToolsTable(upd, true, null, true);
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

function migrateAllProfiles() {
  goApp.MigrateAllProfiles().then(function (msg) {
    tvLog(msg);
  }).catch(function (err) {
    showError('Migrate failed: ' + err);
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
    groups[groupKey].services.push({ name: sel.service, remote: sel.servicePort });
  });

  // When file share mount is enabled, add connection entries for
  // machines that offer file sharing but have no selected services.
  // tela opens a tunnel to these machines so the WebDAV mount can
  // access their file shares.
  var mountEl = document.getElementById('ps-mount-enable');
  if (mountEl && mountEl.checked) {
    Object.keys(hubStatusCache).forEach(function (hubURL) {
      if (!isHubIncluded(hubURL)) return;
      var status = hubStatusCache[hubURL];
      if (!status || !status.machines) return;
      status.machines.forEach(function (m) {
        var mId = m.id || m.hostname;
        var fs = m.capabilities && m.capabilities.fileShare;
        if (!fs || !fs.enabled) return;
        var gk = hubURL + '||' + mId;
        if (!groups[gk]) {
          groups[gk] = { hub: hubURL, machine: mId, services: [] };
        }
      });
    });
  }

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
  if (window.runtime && window.runtime.ClipboardSetText) {
    window.runtime.ClipboardSetText(text);
  } else if (navigator.clipboard) {
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

// Modal stack: when a modal opens a child modal, the child must render
// above its parent. We compute the highest z-index across all currently
// visible .modal-overlay elements and assign the new overlay one higher.
//
// Called by the generic dialog (which is the one that gets stacked on
// top of other modals). Also notifies the Go backend whenever the
// total modal-open count changes, so OS-level window close can be
// blocked while any modal is active (TDL: "modals capture window
// chrome").
function _countVisibleModals() {
  var count = 0;
  document.querySelectorAll('.modal-overlay').forEach(function (el) {
    if (el.classList.contains('hidden')) return;
    if (el.style.display === 'none') return;
    count++;
  });
  return count;
}
function _notifyModalState() {
  var open = _countVisibleModals() > 0;
  if (window.goApp && typeof goApp.SetModalOpen === 'function') {
    goApp.SetModalOpen(open);
  }
}
function modalStackPush(overlay) {
  var maxZ = 100;
  document.querySelectorAll('.modal-overlay').forEach(function (el) {
    if (el === overlay) return;
    if (el.classList.contains('hidden')) return;
    if (el.style.display === 'none') return;
    var z = parseInt(el.style.zIndex || '100', 10);
    if (isNaN(z)) z = 100;
    if (z > maxZ) maxZ = z;
  });
  overlay.style.zIndex = String(maxZ + 10);
  // Defer one tick so the `hidden` class removal has landed before the
  // count runs. Caller pushes first then removes hidden, so schedule
  // the notify for after that sequence completes.
  setTimeout(_notifyModalState, 0);
}
function modalStackPop(overlay) {
  overlay.style.zIndex = '';
  setTimeout(_notifyModalState, 0);
}

// Observe every .modal-overlay and notify the Go backend whenever its
// visibility changes. This catches all modal toggles regardless of how
// they are opened (classList, style.display, or via modalStackPush).
// Installed once at load time after the DOM is ready.
(function installModalObservers() {
  function install() {
    document.querySelectorAll('.modal-overlay').forEach(function (el) {
      var obs = new MutationObserver(function () {
        _notifyModalState();
      });
      obs.observe(el, { attributes: true, attributeFilter: ['class', 'style'] });
    });
    _notifyModalState();
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', install);
  } else {
    install();
  }
})();

function showConfirmDialog(title, message, okLabel) {
  return new Promise(function (resolve) {
    _dialogResolve = resolve;
    _dialogMode = 'confirm';
    document.getElementById('generic-dialog-title').textContent = title;
    document.getElementById('generic-dialog-message').textContent = message;
    document.getElementById('generic-dialog-input').classList.add('hidden');
    var okBtn = document.getElementById('generic-dialog-ok');
    okBtn.textContent = okLabel || 'OK';
    okBtn.className = (okLabel && okLabel.toLowerCase().indexOf('delete') >= 0) ? 'btn btn-destructive' : 'btn btn-primary';
    var overlay = document.getElementById('generic-dialog-overlay');
    modalStackPush(overlay);
    overlay.classList.remove('hidden');
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
    okBtn.className = 'btn btn-primary';
    var overlay = document.getElementById('generic-dialog-overlay');
    modalStackPush(overlay);
    overlay.classList.remove('hidden');
    setTimeout(function () { input.focus(); input.select(); }, 50);
  });
}

function genericDialogOk() {
  var overlay = document.getElementById('generic-dialog-overlay');
  overlay.classList.add('hidden');
  modalStackPop(overlay);
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
  var overlay = document.getElementById('generic-dialog-overlay');
  overlay.classList.add('hidden');
  modalStackPop(overlay);
  if (_dialogResolve) {
    if (_dialogMode === 'prompt') {
      _dialogResolve(null);
    } else {
      _dialogResolve(false);
    }
    _dialogResolve = null;
  }
}

// Allow Enter to submit and Escape to cancel on the generic dialog
document.addEventListener('keydown', function (e) {
  var overlay = document.getElementById('generic-dialog-overlay');
  if (!overlay || overlay.classList.contains('hidden')) return;
  if (e.key === 'Enter') { e.preventDefault(); genericDialogOk(); }
  if (e.key === 'Escape') { e.preventDefault(); genericDialogCancel(); }
});

// Enter/Escape for all other modal overlays that are NOT form-based.
// Form-based modals (create-token, pair-code, grant-access, add-hub)
// already submit on Enter via their <form onsubmit> handler.
document.addEventListener('keydown', function (e) {
  if (e.key !== 'Enter' && e.key !== 'Escape') return;
  // Do not interfere with the generic dialog (handled above).
  var genOverlay = document.getElementById('generic-dialog-overlay');
  if (genOverlay && !genOverlay.classList.contains('hidden')) return;

  // Map of non-form modal overlay IDs to their primary-action and
  // cancel functions.
  var modals = [
    { id: 'disconnect-overlay',       ok: confirmDisconnect,          cancel: cancelDisconnect },
    { id: 'mtu-dialog',               ok: saveMTUDialog,              cancel: closeMTUDialog },
    { id: 'result-modal',             ok: copyResultAndClose,         cancel: function () { closeModal('result-modal'); } },
    { id: 'add-portal-source-modal',  ok: portalSourceModalEnter,     cancel: closeAddPortalSourceDialog }
  ];

  for (var i = 0; i < modals.length; i++) {
    var m = modals[i];
    var el = document.getElementById(m.id);
    if (!el || el.classList.contains('hidden')) continue;
    // If the focused element is a button, let native click handle
    // Enter so we do not double-fire.
    if (e.key === 'Enter' && document.activeElement &&
        document.activeElement.tagName === 'BUTTON') return;
    e.preventDefault();
    if (e.key === 'Enter') { m.ok(); } else { m.cancel(); }
    return;
  }
});

// Dispatch Enter to the correct step inside the Add Remote dialog.
function portalSourceModalEnter() {
  var stepUrl  = document.getElementById('portal-source-step-url');
  var stepName = document.getElementById('portal-source-step-name');
  if (stepUrl && !stepUrl.classList.contains('hidden')) {
    portalSourceBeginSignIn();
  } else if (stepName && !stepName.classList.contains('hidden')) {
    portalSourceFinish();
  }
  // step-code has no primary action (it is waiting for the browser).
}

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

// ── Access tab ───────────────────────────────────────────────────────
//
// Consolidated identity + ACL management, a peer of Hubs and Agents.
// Two projections of the same data:
//   - By machine:  machine rail + identity matrix for the selected machine
//   - By identity: identity rail + machine matrix for the selected identity
//
// Pending changes live in accessState.pending, keyed by "id|machineId".
// Both views read and write the same pending set, so toggling between
// views never loses staged work. Save fires per-identity PUTs in
// parallel with If-Match; a 412 on any identity opens the conflict
// modal listing the affected rows.

// Cached hub capabilities keyed by hub name. Populated on Access page
// open; a stale entry is acceptable because capabilities only grow.
// Used by the services filter UI to decide whether to expose the
// per-service-access-control feature.
var hubCapabilitiesCache = {};

// hubSupportsPerServiceACL reads the capabilities cache and returns
// true when the named hub advertises per-service-access-control.
// Defaults to false when the cache has no entry, keeping the filter UI
// hidden on pre-0.15 hubs.
function hubSupportsPerServiceACL(hubName) {
  var caps = hubCapabilitiesCache[hubName] || [];
  return caps.indexOf('per-service-access-control') !== -1;
}

var accessState = {
  hub: '',             // currently selected hub name
  view: 'by-machine',  // 'by-machine' | 'by-identity'
  loaded: false,
  loadError: '',
  entries: [],         // accessEntry[] from AdminListAccess (role, version, machines)
  machines: [],        // MachineStatus[] from GetHubStatus (id, services, online)
  capabilities: [],    // hub capabilities array (e.g. per-service-access-control)
  selection: {
    machineId: '',     // selected machine in by-machine view; "*" = wildcard
    identityId: '',    // selected identity in by-identity view
  },
  pending: {},         // "id|machineId" -> pendingEntry
  expanded: {},        // "id|machineId" -> bool, services-cell disclosure state
  lastSaveConflict: null, // [{id, current: accessEntry}] populated on 412 save
};

// Pending entry shape:
//   {
//     id:         string     // identity ID
//     machineId:  string     // target machine (or "*")
//     baseline:  {permissions: [], services: []} // state loaded from server
//     desired:   {permissions: [], services: []} // state after staged edits
//     revoke:    bool                             // stage a DELETE on save
//     version:   number                           // identity version at load
//   }

// accessPendingKey builds the pending-map key for an (id, machineId)
// pair. Machine "*" is the wildcard ACL, stored the same way as any
// specific machine.
function accessPendingKey(id, machineId) { return id + '|' + machineId; }

// accessEntryFor returns the accessEntry with the given id, or null.
function accessEntryFor(id) {
  for (var i = 0; i < accessState.entries.length; i++) {
    if (accessState.entries[i].id === id) return accessState.entries[i];
  }
  return null;
}

// accessMachineFor returns the MachineStatus with the given id, or null.
function accessMachineFor(machineId) {
  for (var i = 0; i < accessState.machines.length; i++) {
    if (accessState.machines[i].id === machineId) return accessState.machines[i];
  }
  return null;
}

// accessBaselineFor returns the {permissions, services} grant an
// identity has on a machine according to the server's view (no pending
// changes applied). An absent grant yields empty arrays.
function accessBaselineFor(id, machineId) {
  var entry = accessEntryFor(id);
  if (!entry) return { permissions: [], services: [] };
  for (var i = 0; i < (entry.machines || []).length; i++) {
    var m = entry.machines[i];
    if (m.machineId === machineId) {
      return {
        permissions: (m.permissions || []).slice(),
        services: (m.services || []).slice(),
      };
    }
  }
  return { permissions: [], services: [] };
}

// accessWildcardInherited returns which permissions an identity
// receives from the wildcard "*" ACL as reported by the hub's
// /api/admin/access response. The hub is the source of truth for
// cascade rules; the UI reads the derived data on each accessEntry
// rather than re-implementing the cascade logic, so any future change
// to the protocol flows through without a client update.
//
// Identities with no wildcard inheritance (or identities not yet
// loaded) return a zeroed struct: every caller treats those as "not
// inherited" and falls back to the identity's explicit grants.
function accessWildcardInherited(id) {
  var entry = accessEntryFor(id);
  if (!entry) return { connect: false, manage: false, services: [] };
  var perms = entry.wildcardInherited || [];
  return {
    connect: perms.indexOf('connect') !== -1,
    manage: perms.indexOf('manage') !== -1,
    services: (entry.wildcardInheritedServices || []).slice(),
  };
}

// accessEffectiveGrant returns the grant that should be rendered for
// (id, machineId): the pending desired state if a pending entry exists,
// otherwise the baseline. Revoke-staged rows return empty permissions
// but retain the baseline's services so the operator can see what is
// about to be removed.
function accessEffectiveGrant(id, machineId) {
  var key = accessPendingKey(id, machineId);
  var p = accessState.pending[key];
  if (!p) return accessBaselineFor(id, machineId);
  if (p.revoke) return { permissions: [], services: p.baseline.services.slice() };
  return { permissions: p.desired.permissions.slice(), services: p.desired.services.slice() };
}

function accessIsPending(id, machineId) {
  return !!accessState.pending[accessPendingKey(id, machineId)];
}

function accessIsRevoked(id, machineId) {
  var p = accessState.pending[accessPendingKey(id, machineId)];
  return !!(p && p.revoke);
}

// accessArraysEqual compares two string arrays as sets (order-insensitive).
function accessArraysEqual(a, b) {
  if ((a || []).length !== (b || []).length) return false;
  var s = {};
  for (var i = 0; i < a.length; i++) s[a[i]] = true;
  for (var j = 0; j < b.length; j++) if (!s[b[j]]) return false;
  return true;
}

// accessPendingMatchesBaseline returns true if a pending entry's
// desired state equals its baseline (i.e. the row is no longer dirty
// and the entry can be removed).
function accessPendingMatchesBaseline(p) {
  if (!p) return true;
  if (p.revoke) return false; // revoke is always a change
  return accessArraysEqual(p.desired.permissions, p.baseline.permissions) &&
         accessArraysEqual(p.desired.services, p.baseline.services);
}

function accessIdentityDirty(id) {
  for (var k in accessState.pending) {
    if (Object.prototype.hasOwnProperty.call(accessState.pending, k)) {
      if (accessState.pending[k].id === id) return true;
    }
  }
  return false;
}

function accessMachineDirty(machineId) {
  for (var k in accessState.pending) {
    if (Object.prototype.hasOwnProperty.call(accessState.pending, k)) {
      if (accessState.pending[k].machineId === machineId) return true;
    }
  }
  return false;
}

function accessAnyDirty() {
  for (var k in accessState.pending) {
    if (Object.prototype.hasOwnProperty.call(accessState.pending, k)) return true;
  }
  return false;
}

// accessEnsurePending returns the pending entry for (id, machineId),
// creating one seeded from the baseline if none exists.
function accessEnsurePending(id, machineId) {
  var key = accessPendingKey(id, machineId);
  if (accessState.pending[key]) return accessState.pending[key];
  var baseline = accessBaselineFor(id, machineId);
  var entry = accessEntryFor(id);
  accessState.pending[key] = {
    id: id,
    machineId: machineId,
    baseline: baseline,
    desired: {
      permissions: baseline.permissions.slice(),
      services: baseline.services.slice(),
    },
    revoke: false,
    version: entry ? (entry.version || 0) : 0,
  };
  return accessState.pending[key];
}

// accessDropPending removes the entry for (id, machineId) when it has
// converged back to the baseline, so the row stops rendering dirty.
function accessDropIfClean(id, machineId) {
  var key = accessPendingKey(id, machineId);
  var p = accessState.pending[key];
  if (p && accessPendingMatchesBaseline(p)) {
    delete accessState.pending[key];
  }
}

// ── Data loading ───────────────────────────────────────────────────

function refreshAccessTab() {
  var select = document.getElementById('access-hub-select');
  if (!select) return;

  Promise.all([goApp.GetKnownHubs(), goApp.GetSettings()]).then(function (results) {
    var hubs = (results[0] || []).slice();
    hubs.sort(function (a, b) {
      var an = (a.name || '').toLowerCase();
      var bn = (b.name || '').toLowerCase();
      if (an < bn) return -1;
      if (an > bn) return 1;
      return 0;
    });
    // Keep the shared cache in sync so hubURLFor() works for callers
    // outside the Hubs tab.
    knownHubsData = hubs;

    var settings = results[1] || {};
    var prev = accessState.hub || settings.lastSelectedHub || '';

    select.innerHTML = '';
    if (hubs.length === 0) {
      select.innerHTML = '<option value="">No hubs configured</option>';
      accessState.hub = '';
      accessState.loaded = false;
      renderAccessEmpty('Add a hub to manage access.');
      return;
    }
    hubs.forEach(function (hub) {
      var opt = document.createElement('option');
      opt.value = hub.name;
      opt.textContent = hub.name;
      select.appendChild(opt);
    });
    if (prev && hubs.some(function (h) { return h.name === prev; })) {
      select.value = prev;
    }
    accessState.hub = select.value;

    if (settings.lastAccessView === 'by-identity' || settings.lastAccessView === 'by-machine') {
      accessState.view = settings.lastAccessView;
    }
    accessApplyViewToggle();
    accessLoadData();
  }).catch(function (err) {
    console.error('[access] refreshAccessTab failed:', err);
    renderAccessError('Could not load hubs: ' + (err && err.message ? err.message : err));
  });
}

function onAccessHubChange() {
  var select = document.getElementById('access-hub-select');
  var newHub = select ? select.value : '';
  if (accessAnyDirty()) {
    // Switching hubs abandons pending changes on the old hub; warn the
    // operator before dropping their work.
    showConfirmDialog(
      'Discard staged changes?',
      'You have pending access changes on ' + accessState.hub + '. Switching hubs discards them.',
      'Discard'
    ).then(function (yes) {
      if (!yes) {
        // Restore the dropdown to the previous hub.
        if (select) select.value = accessState.hub;
        return;
      }
      accessState.hub = newHub;
      accessState.pending = {};
      accessState.expanded = {};
      accessState.selection = { machineId: '', identityId: '' };
      if (newHub) goApp.SaveLastSelectedHub(newHub).catch(function () { /* best effort */ });
      accessLoadData();
    });
    return;
  }
  accessState.hub = newHub;
  accessState.pending = {};
  accessState.expanded = {};
  accessState.selection = { machineId: '', identityId: '' };
  if (accessState.hub) {
    goApp.SaveLastSelectedHub(accessState.hub).catch(function () { /* best effort */ });
  }
  accessLoadData();
}

// accessLoadData fetches identities + machines + capabilities for the
// selected hub and re-renders. Pending changes are cleared on a fresh
// load; callers that want to preserve pending state should not call
// this (use incremental updates instead).
//
// Each bridge call is wrapped in Promise.resolve so a rejection from
// one (e.g. a transient hub-unreachable error from GetHubStatus) does
// not leave the whole pane stuck on the Loading placeholder. Any
// non-access failure degrades gracefully with empty data; only a
// failing AdminListAccess call triggers the error state, since
// identities are the minimum viable payload for the page.
function accessLoadData() {
  if (!accessState.hub) {
    accessState.loaded = false;
    renderAccessEmpty('Select a hub to view access.');
    return;
  }
  var hub = accessState.hub;
  accessState.loaded = false;
  accessState.loadError = '';
  accessState.pending = {};
  accessState.lastSaveConflict = null;
  renderAccessLoading();

  function safely(promise, label) {
    return Promise.resolve(promise).catch(function (err) {
      console.error('[access] ' + label + ' failed:', err);
      return null;
    });
  }

  Promise.all([
    safely(goApp.AdminListAccess(hub), 'AdminListAccess'),
    safely(goApp.GetHubStatus(hub), 'GetHubStatus'),
    safely(goApp.HubCapabilities(hub), 'HubCapabilities'),
  ]).then(function (results) {
    if (accessState.hub !== hub) return; // raced with another hub switch

    var access = {};
    if (results[0]) {
      try { access = JSON.parse(results[0] || '{}'); } catch (e) { access = { error: 'invalid response' }; }
    } else {
      access = { error: 'hub unreachable' };
    }
    if (access.error) {
      accessState.loaded = false;
      accessState.loadError = access.error;
      renderAccessError('Could not load access for ' + hub + ': ' + access.error);
      return;
    }
    var status = results[1] || {};
    var caps = {};
    if (results[2]) {
      try { caps = JSON.parse(results[2] || '{}'); } catch (e) { caps = {}; }
    }

    accessState.entries = access.access || [];
    accessState.machines = status.machines || [];
    accessState.capabilities = caps.capabilities || [];
    accessState.loaded = true;
    hubCapabilitiesCache[hub] = accessState.capabilities;

    // Resolve selection against the freshly loaded data. If the
    // previously selected machine or identity no longer exists (it was
    // deleted out-of-band), fall back to the first available entry of
    // the relevant kind so the detail pane is never stranded.
    if (accessState.view === 'by-machine') {
      if (!accessMachineFor(accessState.selection.machineId) &&
          accessState.selection.machineId !== '*') {
        accessState.selection.machineId = accessState.machines.length ? accessState.machines[0].id : '*';
      }
    } else {
      if (!accessEntryFor(accessState.selection.identityId)) {
        accessState.selection.identityId = accessState.entries.length ? accessState.entries[0].id : '';
      }
    }
    accessUpdateToolbar();
    renderAccessPane();
  }).catch(function (err) {
    console.error('[access] accessLoadData post-resolve failed:', err);
    if (accessState.hub !== hub) return;
    accessState.loaded = false;
    accessState.loadError = (err && err.message) ? err.message : String(err);
    renderAccessError('Could not load access for ' + hub + ': ' + accessState.loadError);
  });
}

// ── Selection + toolbar state ─────────────────────────────────────

function accessSetView(view) {
  if (view !== 'by-machine' && view !== 'by-identity') return;
  if (accessState.view === view) return;
  accessState.view = view;
  accessApplyViewToggle();
  goApp.GetSettings().then(function (s) {
    s = s || {};
    s.lastAccessView = view;
    goApp.SaveSettings(JSON.stringify(s));
  }).catch(function () { /* best effort */ });
  // Populate a default selection for the new view so the detail pane
  // shows something useful straight away.
  if (accessState.loaded) {
    if (view === 'by-machine' && !accessState.selection.machineId) {
      accessState.selection.machineId = accessState.machines.length ? accessState.machines[0].id : '*';
    }
    if (view === 'by-identity' && !accessState.selection.identityId) {
      accessState.selection.identityId = accessState.entries.length ? accessState.entries[0].id : '';
    }
  }
  renderAccessPane();
}

function accessApplyViewToggle() {
  var bm = document.getElementById('access-view-by-machine');
  var bi = document.getElementById('access-view-by-identity');
  if (!bm || !bi) return;
  bm.classList.toggle('active', accessState.view === 'by-machine');
  bm.setAttribute('aria-selected', accessState.view === 'by-machine' ? 'true' : 'false');
  bi.classList.toggle('active', accessState.view === 'by-identity');
  bi.setAttribute('aria-selected', accessState.view === 'by-identity' ? 'true' : 'false');

  var hdr = document.getElementById('access-sidebar-header');
  if (hdr) {
    hdr.textContent = accessState.view === 'by-identity' ? 'Identities' : 'Machines';
  }
}

// accessUpdateToolbar enables/disables Undo and Save based on the
// pending set.
function accessUpdateToolbar() {
  var dirty = accessAnyDirty();
  var undo = document.getElementById('access-undo-btn');
  var save = document.getElementById('access-save-btn');
  if (undo) undo.disabled = !dirty;
  if (save) save.disabled = !dirty;
}

function accessSelectMachine(machineId) {
  accessState.selection.machineId = machineId;
  renderAccessPane();
}

function accessSelectIdentity(id) {
  accessState.selection.identityId = id;
  renderAccessPane();
}

// ── Rendering ─────────────────────────────────────────────────────

function renderAccessPane() {
  if (!accessState.hub) {
    renderAccessEmpty('Select a hub to view access.');
    return;
  }
  if (!accessState.loaded) {
    if (accessState.loadError) {
      renderAccessError(accessState.loadError);
    } else {
      renderAccessLoading();
    }
    return;
  }
  renderAccessSidebar();
  renderAccessDetail();
  accessUpdateToolbar();
}

function renderAccessLoading() {
  var list = document.getElementById('access-sidebar-list');
  var detail = document.getElementById('access-detail');
  if (list) list.innerHTML = '<p class="empty-hint">Loading access for <strong>' + escHtml(accessState.hub) + '</strong>&hellip;</p>';
  if (detail) detail.innerHTML = '<div class="access-detail-empty">Loading&hellip;</div>';
}

function renderAccessError(msg) {
  var list = document.getElementById('access-sidebar-list');
  var detail = document.getElementById('access-detail');
  if (list) list.innerHTML = '<p class="empty-hint">' + escHtml(msg) + '</p>';
  if (detail) detail.innerHTML = '<div class="access-detail-empty">' + escHtml(msg) + '</div>';
}

function renderAccessEmpty(msg) {
  var list = document.getElementById('access-sidebar-list');
  var detail = document.getElementById('access-detail');
  if (list) list.innerHTML = '<p class="empty-hint">' + escHtml(msg) + '</p>';
  if (detail) detail.innerHTML = '<div class="access-detail-empty">' + escHtml(msg) + '</div>';
}

function renderAccessSidebar() {
  var list = document.getElementById('access-sidebar-list');
  if (!list) return;
  if (accessState.view === 'by-machine') {
    renderAccessSidebarMachines(list);
  } else {
    renderAccessSidebarIdentities(list);
  }
}

function renderAccessSidebarMachines(list) {
  var machines = accessState.machines.slice();
  machines.sort(function (a, b) { return (a.id || '').localeCompare(b.id || ''); });
  var html = '';
  machines.forEach(function (m) {
    var isActive = (m.id === accessState.selection.machineId);
    var dirty = accessMachineDirty(m.id);
    var statusClass = m.agentConnected ? 'dot-online' : 'dot-offline';
    var statusLabel = m.agentConnected ? 'Online' : 'Offline';
    html += '<div class="access-sidebar-item' + (isActive ? ' active' : '') +
      '" onclick="accessSelectMachine(\'' + escAttr(m.id) + '\')">'
      + '<div class="access-sidebar-primary">'
      + '<span class="dot ' + statusClass + '" title="' + statusLabel + '"></span>'
      + escHtml(m.id)
      + (dirty ? '<span class="access-sidebar-dirty">&bull; pending</span>' : '')
      + '</div>'
      + '<div class="access-sidebar-secondary">'
      + ((m.services || []).length + ' service' + ((m.services || []).length === 1 ? '' : 's'))
      + '</div>'
      + '</div>';
  });
  // Wildcard entry: ACL that applies to any machine not listed.
  var wildDirty = accessMachineDirty('*');
  html += '<div class="access-sidebar-item' + (accessState.selection.machineId === '*' ? ' active' : '') +
    '" onclick="accessSelectMachine(\'*\')">'
    + '<div class="access-sidebar-primary">'
    + '<span class="chip">Wildcard</span> all machines'
    + (wildDirty ? '<span class="access-sidebar-dirty">&bull; pending</span>' : '')
    + '</div>'
    + '<div class="access-sidebar-secondary">Fallback rule</div>'
    + '</div>';
  list.innerHTML = html || '<p class="empty-hint">No machines registered.</p>';
}

function renderAccessSidebarIdentities(list) {
  var entries = accessState.entries.slice();
  entries.sort(function (a, b) {
    // Sort implicit roles (owner/admin/viewer) before user so operators
    // see the built-in identities first. Then by id within role group.
    var roleOrder = { owner: 0, admin: 1, viewer: 2, user: 3 };
    var ra = roleOrder[a.role] != null ? roleOrder[a.role] : 4;
    var rb = roleOrder[b.role] != null ? roleOrder[b.role] : 4;
    if (ra !== rb) return ra - rb;
    return (a.id || '').localeCompare(b.id || '');
  });
  var html = '';
  entries.forEach(function (e) {
    var isActive = (e.id === accessState.selection.identityId);
    var dirty = accessIdentityDirty(e.id);
    var roleLabel = formatRole(e.role);
    var summary = accessIdentitySummary(e);
    html += '<div class="access-sidebar-item' + (isActive ? ' active' : '') +
      '" onclick="accessSelectIdentity(\'' + escAttr(e.id) + '\')">'
      + '<div class="access-sidebar-primary">'
      + escHtml(e.id)
      + ' <span class="chip">' + escHtml(roleLabel) + '</span>'
      + (dirty ? '<span class="access-sidebar-dirty">&bull; pending</span>' : '')
      + '</div>'
      + '<div class="access-sidebar-secondary">' + escHtml(summary) + '</div>'
      + '</div>';
  });
  list.innerHTML = html || '<p class="empty-hint">No identities on this hub.</p>';
}

// accessIdentitySummary produces the secondary-line text in the
// by-identity rail: a short description of what the identity can do.
// A wildcard ("*") grant is not counted as a machine; it expands to
// every machine the hub has registered. We report it explicitly so
// "1 machine" never means "all machines."
function accessIdentitySummary(e) {
  if (e.role === 'owner' || e.role === 'admin') return 'All machines (implicit)';
  if (e.role === 'viewer') return 'Read-only console';
  var specific = 0;
  var hasWildcard = false;
  (e.machines || []).forEach(function (m) {
    if (m.machineId === '*') hasWildcard = true;
    else specific++;
  });
  if (specific === 0 && !hasWildcard) return 'No grants';
  if (specific === 0 && hasWildcard) return 'All machines (wildcard)';
  var label = specific + ' machine' + (specific === 1 ? '' : 's');
  if (hasWildcard) label += ' + wildcard';
  return label;
}

function renderAccessDetail() {
  var detail = document.getElementById('access-detail');
  if (!detail) return;
  if (accessState.view === 'by-machine') {
    renderAccessDetailByMachine(detail);
  } else {
    renderAccessDetailByIdentity(detail);
  }
}

// ── By-machine detail ─────────────────────────────────────────────

function renderAccessDetailByMachine(pane) {
  var machineId = accessState.selection.machineId;
  if (!machineId) {
    pane.innerHTML = '<div class="access-detail-empty">Select a machine to view access.</div>';
    return;
  }
  var m = accessMachineFor(machineId);
  var isWildcard = (machineId === '*');
  var title = isWildcard ? '* (all machines)' : machineId;

  var html = '<header class="access-detail-header">';
  html += '<div><h2>' + escHtml(title) + '</h2>';
  if (isWildcard) {
    html += '<div class="access-detail-meta">Wildcard fallback. Applies to any machine not listed above.</div>';
  } else if (m) {
    var statusBadge = m.agentConnected
      ? '<span class="status status-online"><span class="status-dot"></span>Online</span>'
      : '<span class="status status-offline"><span class="status-dot"></span>Offline</span>';
    html += '<div class="access-detail-meta">' + statusBadge;
    if (m.os) html += ' &middot; <span>' + escHtml(m.os) + '</span>';
    html += '</div>';
    if ((m.services || []).length > 0) {
      html += '<div class="access-service-list">';
      m.services.forEach(function (svc) {
        var name = svc.name || ('port-' + svc.port);
        html += '<span class="chip">' + escHtml(name + ' · ' + svc.port) + '</span>';
      });
      html += '</div>';
    }
  } else {
    html += '<div class="access-detail-meta">Unknown machine.</div>';
  }
  html += '</div></header>';

  html += '<p class="hint">Every identity on this hub. Toggle permissions to stage changes; press <strong>Save</strong> in the toolbar to commit. Owner and admin roles have implicit all-access; their checkboxes are locked.</p>';

  html += '<table class="access-matrix"><thead><tr>'
    + '<th>Identity</th>'
    + '<th class="access-col-check">Connect</th>'
    + '<th class="access-col-check">Register</th>'
    + '<th class="access-col-check">Manage</th>'
    + '<th>Connect services</th>'
    + '<th class="access-matrix-actions">Actions</th>'
    + '</tr></thead><tbody>';

  // Implicit-role rows first: owner, admin(s), viewer(s). Full weight,
  // disabled checkboxes; no revoke (role is the source of truth, not
  // the per-machine ACL).
  var entries = accessState.entries.slice();
  entries.sort(function (a, b) {
    var roleOrder = { owner: 0, admin: 1, viewer: 2, user: 3 };
    var ra = roleOrder[a.role] != null ? roleOrder[a.role] : 4;
    var rb = roleOrder[b.role] != null ? roleOrder[b.role] : 4;
    if (ra !== rb) return ra - rb;
    return (a.id || '').localeCompare(b.id || '');
  });
  entries.forEach(function (e) {
    if (e.role === 'owner' || e.role === 'admin' || e.role === 'viewer') {
      html += renderAccessMatrixRow_Implicit(e, machineId, isWildcard);
    }
  });
  // Explicit (user) rows: interactive.
  entries.forEach(function (e) {
    if (e.role === 'owner' || e.role === 'admin' || e.role === 'viewer') return;
    html += renderAccessMatrixRow_Explicit(e, machineId, isWildcard);
  });

  html += '</tbody></table>';
  pane.innerHTML = html;
}

function renderAccessMatrixRow_Implicit(e, machineId, isWildcard) {
  var roleLabel = formatRole(e.role);
  var allChecked = (e.role === 'owner' || e.role === 'admin');
  var checkConnect = allChecked ? 'checked' : '';
  var checkRegister = allChecked ? 'checked' : '';
  var checkManage = allChecked ? 'checked' : '';
  var note;
  if (e.role === 'owner') {
    note = 'Implicit all-access &middot; cannot be filtered or revoked';
  } else if (e.role === 'admin') {
    note = 'Implicit all-access';
  } else {
    note = 'Read-only console access (no connect)';
  }
  var services = allChecked ? '<span class="services-summary-muted">All services</span>'
                           : '<span class="services-summary-muted">&mdash;</span>';
  return '<tr class="access-matrix-row access-matrix-row-implicit">'
    + '<td><strong>' + escHtml(e.id) + '</strong> <span class="chip">' + escHtml(roleLabel) + '</span>'
    +   '<div class="implicit-note">' + note + '</div></td>'
    + '<td class="access-matrix-check"><input type="checkbox" ' + checkConnect + ' disabled></td>'
    + '<td class="access-matrix-check"><input type="checkbox" ' + checkRegister + ' disabled></td>'
    + '<td class="access-matrix-check"><input type="checkbox" ' + checkManage + ' disabled></td>'
    + '<td>' + services + '</td>'
    + '<td class="access-matrix-actions"></td>'
    + '</tr>';
}

function renderAccessMatrixRow_Explicit(e, machineId, isWildcard) {
  var grant = accessEffectiveGrant(e.id, machineId);
  var pending = accessIsPending(e.id, machineId);
  var revoked = accessIsRevoked(e.id, machineId);
  // Permissions inherited from the wildcard "*" ACL. On the wildcard
  // row itself there is no parent wildcard to inherit from, so the
  // struct is zeroed. Connect and manage cascade; register does not.
  var inherited = isWildcard
    ? { connect: false, manage: false, services: [] }
    : accessWildcardInherited(e.id);
  var rowClass = 'access-matrix-row';
  if (revoked) rowClass += ' access-matrix-row-revoked';
  else if (pending) rowClass += ' access-matrix-row-dirty';

  var perms = grant.permissions;
  var hasConnect = perms.indexOf('connect') !== -1;
  var hasRegister = perms.indexOf('register') !== -1;
  var hasManage = perms.indexOf('manage') !== -1;

  var connectCell = renderInheritableCheck(e.id, machineId, 'connect', hasConnect, inherited.connect);
  var manageCell = renderInheritableCheck(e.id, machineId, 'manage', hasManage, inherited.manage);
  // Register is not meaningful on the wildcard row (hubs never register
  // against "*") and does not cascade from the wildcard, so a plain
  // interactive checkbox without inheritance logic is correct for
  // specific-machine rows.
  var registerCell = isWildcard
    ? '<td class="access-matrix-na">&mdash;</td>'
    : '<td class="access-matrix-check"><input type="checkbox" ' + (hasRegister ? 'checked ' : '') +
      'onchange="accessToggleCheck(\'' + escAttr(e.id) + '\',\'' + escAttr(machineId) + '\',\'register\',this.checked)"></td>';

  var dirtyInd = '';
  if (revoked) dirtyInd = '<span class="dirty-indicator">&bull; will be revoked</span>';
  else if (pending) dirtyInd = '<span class="dirty-indicator">&bull; pending</span>';

  var actions;
  if (revoked) {
    actions = '<button type="button" class="btn btn-sm" onclick="accessUndoRevokeRow(\'' + escAttr(e.id) + '\',\'' + escAttr(machineId) + '\')">Undo</button>';
  } else {
    actions = '<button type="button" class="btn btn-sm" onclick="accessEditServices(\'' + escAttr(e.id) + '\',\'' + escAttr(machineId) + '\')">Edit&hellip;</button>'
      + '<button type="button" class="btn btn-sm btn-danger" onclick="accessStageRevoke(\'' + escAttr(e.id) + '\',\'' + escAttr(machineId) + '\')">Revoke</button>';
  }

  return '<tr class="' + rowClass + '">'
    + '<td><strong class="identity-name">' + escHtml(e.id) + '</strong> <span class="chip">' + escHtml(formatRole(e.role)) + '</span>' + dirtyInd + '</td>'
    + connectCell
    + registerCell
    + manageCell
    + '<td>' + renderAccessServicesCellWithInheritance(e.id, machineId, grant, inherited) + '</td>'
    + '<td class="access-matrix-actions">' + actions + '</td>'
    + '</tr>';
}

// renderInheritableCheck renders a permission checkbox that may be
// inherited from the wildcard ACL. When inherited is true the cell
// reads as checked + disabled with a title tooltip pointing at the
// wildcard row; clicking is a no-op because the wildcard alone grants
// the permission. When not inherited, the checkbox is a plain
// interactive toggle bound to accessToggleCheck.
function renderInheritableCheck(id, machineId, perm, ownChecked, inherited) {
  if (inherited) {
    return '<td class="access-matrix-check access-matrix-check-inherited" title="Granted via the wildcard * ACL. Edit the wildcard row to change.">'
      + '<input type="checkbox" checked disabled></td>';
  }
  return '<td class="access-matrix-check"><input type="checkbox" ' + (ownChecked ? 'checked ' : '') +
    'onchange="accessToggleCheck(\'' + escAttr(id) + '\',\'' + escAttr(machineId) + '\',\'' + perm + '\',this.checked)"></td>';
}

// renderAccessServicesCellWithInheritance wraps renderAccessServicesCell
// and annotates the cell when connect is inherited from the wildcard.
// When the identity has no specific-machine connect but the wildcard
// does, we render the wildcard's effective services (or "All services")
// with a "via *" marker so the operator sees the real state rather
// than an em-dash that suggests no access.
function renderAccessServicesCellWithInheritance(id, machineId, grant, inherited) {
  var ownConnect = (grant.permissions || []).indexOf('connect') !== -1;
  if (!ownConnect && inherited.connect) {
    var svc = inherited.services || [];
    if (svc.length === 0) {
      return '<span class="services-summary-muted" title="Inherited from wildcard">All services (via *)</span>';
    }
    var html = '<div class="services-cell" title="Inherited from wildcard">'
      + '<span class="services-summary-muted">via *</span>'
      + '<div class="services-chips">';
    svc.forEach(function (s) {
      html += '<span class="chip">' + escHtml(s) + '</span>';
    });
    html += '</div></div>';
    return html;
  }
  return renderAccessServicesCell(id, machineId, grant);
}

// renderAccessServicesCell renders the Connect services column for an
// explicit row as a disclosure widget: a compact "N of M services"
// summary button with a rotating chevron that toggles a chip cloud
// below. An unfiltered grant shows "All services" in muted italics
// with no disclosure (nothing to expand). An unexpanded filtered grant
// defaults to open when the filter is small (<= 3 chips) so the common
// case reads without interaction.
//
// Revoked rows show the baseline services (not the empty desired
// state) so the operator can see what is about to be removed. The
// row-level CSS on .access-matrix-row-revoked strikes the chips and
// the "All services" label through.
function renderAccessServicesCell(id, machineId, grant) {
  if (accessIsRevoked(id, machineId)) {
    var baseline = accessBaselineFor(id, machineId);
    return renderAccessServicesCellInner(id, machineId, baseline, /*isRevoked*/ true);
  }
  return renderAccessServicesCellInner(id, machineId, grant, /*isRevoked*/ false);
}

function renderAccessServicesCellInner(id, machineId, grant, isRevoked) {
  if ((grant.permissions || []).indexOf('connect') === -1) {
    return '<span class="services-summary-muted">&mdash;</span>';
  }
  var services = grant.services || [];
  if (services.length === 0) {
    return '<span class="services-summary-muted">All services</span>';
  }
  // Revoked rows collapse the disclosure so the chip cloud is always
  // visible: the operator needs to see what is being removed without
  // clicking a button they can't un-stage via (the Undo action is in
  // the actions column).
  if (isRevoked) {
    var html = '<div class="services-chips">';
    services.forEach(function (svc) {
      html += '<span class="chip">' + escHtml(svc) + '</span>';
    });
    html += '</div>';
    return html;
  }

  var total = accessMachineServiceCount(machineId, services.length);
  var key = accessPendingKey(id, machineId);
  var expanded = accessState.expanded[key];
  if (expanded === undefined) expanded = services.length <= 3;

  var html2 = '<div class="services-cell">';
  html2 += '<button type="button" class="services-summary" aria-expanded="' + (expanded ? 'true' : 'false') +
    '" onclick="accessToggleServicesExpand(\'' + escAttr(id) + '\',\'' + escAttr(machineId) + '\')">';
  html2 += '<span class="services-summary-chevron">&#x25B8;</span>';
  html2 += '<span>' + services.length + ' of ' + total + ' service' + (total === 1 ? '' : 's') + '</span>';
  html2 += '</button>';
  if (expanded) {
    html2 += '<div class="services-chips">';
    services.forEach(function (svc) {
      html2 += '<span class="chip">' + escHtml(svc) + '</span>';
    });
    html2 += '</div>';
  }
  html2 += '</div>';
  return html2;
}

// accessMachineServiceCount returns the number of services the hub
// advertises for machineId, falling back to the granted count when the
// machine is unknown (wildcard "*" or a machine whose agent is offline).
function accessMachineServiceCount(machineId, fallback) {
  if (machineId === '*') return fallback;
  var m = accessMachineFor(machineId);
  if (m && Array.isArray(m.services)) return m.services.length;
  return fallback;
}

function accessToggleServicesExpand(id, machineId) {
  var key = accessPendingKey(id, machineId);
  // Flip undefined->true, anything else to its inverse. Preserves the
  // auto-expand-small-lists default from renderAccessServicesCell.
  var cur = accessState.expanded[key];
  if (cur === undefined) cur = true; // was auto-expanded; collapse
  accessState.expanded[key] = !cur;
  renderAccessPane();
}

// ── By-identity detail ────────────────────────────────────────────

function renderAccessDetailByIdentity(pane) {
  var id = accessState.selection.identityId;
  if (!id) {
    pane.innerHTML = '<div class="access-detail-empty">Select an identity to view access.</div>';
    return;
  }
  var entry = accessEntryFor(id);
  if (!entry) {
    pane.innerHTML = '<div class="access-detail-empty">Identity ' + escHtml(id) + ' not found.</div>';
    return;
  }
  var html = '<header class="access-detail-header">';
  html += '<div><h2>' + escHtml(entry.id) + ' <span class="chip">' + escHtml(formatRole(entry.role)) + '</span></h2>';
  html += '<div class="access-token-preview">' + escHtml(entry.tokenPreview || '') + '</div>';
  html += '</div>';
  html += '<div class="access-detail-actions">'
    + '<button type="button" class="btn btn-sm" onclick="accessRenameIdentity(\'' + escAttr(entry.id) + '\')">Rename</button>'
    + '<button type="button" class="btn btn-sm" onclick="accessChangeRole(\'' + escAttr(entry.id) + '\')">Change role&hellip;</button>'
    + '<button type="button" class="btn btn-sm" onclick="accessRotateIdentityToken(\'' + escAttr(entry.id) + '\')">Rotate token</button>';
  if (entry.role !== 'owner') {
    html += '<button type="button" class="btn btn-sm btn-danger" onclick="accessDeleteIdentity(\'' + escAttr(entry.id) + '\')">Delete identity</button>';
  }
  html += '</div></header>';

  if (entry.role === 'owner' || entry.role === 'admin') {
    html += '<p class="hint">Implicit all-access. ' + escHtml(formatRole(entry.role)) +
      ' tokens can register, connect, and manage every machine on this hub without per-machine ACL entries. Demote to User in Change role to restrict.</p>';
    pane.innerHTML = html;
    return;
  }
  if (entry.role === 'viewer') {
    html += '<p class="hint">Read-only console access. Viewer tokens see the built-in hub console; they cannot open sessions or manage agents.</p>';
    pane.innerHTML = html;
    return;
  }

  html += '<p class="hint">Per-machine permissions for this identity. Toggle to stage changes; press <strong>Save</strong> in the toolbar to commit.</p>';

  html += '<table class="access-matrix"><thead><tr>'
    + '<th>Machine</th>'
    + '<th class="access-col-check">Connect</th>'
    + '<th class="access-col-check">Register</th>'
    + '<th class="access-col-check">Manage</th>'
    + '<th>Connect services</th>'
    + '<th class="access-matrix-actions">Actions</th>'
    + '</tr></thead><tbody>';

  // Render a row for every known machine plus a wildcard fallback. The
  // operator can grant from here even if the identity has no existing
  // grant on that machine.
  var machines = accessState.machines.slice();
  machines.sort(function (a, b) { return (a.id || '').localeCompare(b.id || ''); });
  machines.forEach(function (m) {
    html += renderAccessMatrixRow_ByIdentity(entry, m, false);
  });
  html += renderAccessMatrixRow_ByIdentity(entry, { id: '*' }, true);

  html += '</tbody></table>';
  pane.innerHTML = html;
}

function renderAccessMatrixRow_ByIdentity(entry, m, isWildcard) {
  var machineId = m.id;
  var grant = accessEffectiveGrant(entry.id, machineId);
  var pending = accessIsPending(entry.id, machineId);
  var revoked = accessIsRevoked(entry.id, machineId);
  var rowClass = 'access-matrix-row';
  if (revoked) rowClass += ' access-matrix-row-revoked';
  else if (pending) rowClass += ' access-matrix-row-dirty';
  if (!isWildcard && m.agentConnected === false) rowClass += ' access-matrix-row-offline';
  if (isWildcard) rowClass += ' access-matrix-row-wildcard';

  var perms = grant.permissions;
  var hasConnect = perms.indexOf('connect') !== -1;
  var hasRegister = perms.indexOf('register') !== -1;
  var hasManage = perms.indexOf('manage') !== -1;

  var registerCell = isWildcard
    ? '<td class="access-matrix-na">&mdash;</td>'
    : '<td class="access-matrix-check"><input type="checkbox" ' + (hasRegister ? 'checked ' : '') +
      'onchange="accessToggleCheck(\'' + escAttr(entry.id) + '\',\'' + escAttr(machineId) + '\',\'register\',this.checked)"></td>';

  var dirtyInd = '';
  if (revoked) dirtyInd = '<span class="dirty-indicator">&bull; will be revoked</span>';
  else if (pending) dirtyInd = '<span class="dirty-indicator">&bull; pending</span>';

  var meta;
  if (isWildcard) {
    meta = '<div class="access-matrix-meta">Applies to any machine not listed</div>';
  } else {
    var svcCount = (m.services || []).length;
    var statusBadge = m.agentConnected
      ? '<span class="status status-online"><span class="status-dot"></span>Online</span>'
      : '<span class="status status-offline"><span class="status-dot"></span>Offline</span>';
    meta = '<div class="access-matrix-meta">' + statusBadge + ' &middot; ' +
      svcCount + ' service' + (svcCount === 1 ? '' : 's') + '</div>';
  }

  var label = isWildcard ? '<strong>* (all machines)</strong>' : '<strong>' + escHtml(machineId) + '</strong>';

  var actions;
  if (revoked) {
    actions = '<button type="button" class="btn btn-sm" onclick="accessUndoRevokeRow(\'' + escAttr(entry.id) + '\',\'' + escAttr(machineId) + '\')">Undo</button>';
  } else if (perms.length === 0 && !pending) {
    // No grant yet; Revoke is meaningless. Edit... still makes sense
    // once the connect box is ticked, but before that it's not useful.
    actions = '';
  } else {
    actions = '<button type="button" class="btn btn-sm" onclick="accessEditServices(\'' + escAttr(entry.id) + '\',\'' + escAttr(machineId) + '\')">Edit&hellip;</button>'
      + '<button type="button" class="btn btn-sm btn-danger" onclick="accessStageRevoke(\'' + escAttr(entry.id) + '\',\'' + escAttr(machineId) + '\')">Revoke</button>';
  }

  return '<tr class="' + rowClass + '">'
    + '<td>' + label + meta + dirtyInd + '</td>'
    + '<td class="access-matrix-check"><input type="checkbox" ' + (hasConnect ? 'checked ' : '') +
      'onchange="accessToggleCheck(\'' + escAttr(entry.id) + '\',\'' + escAttr(machineId) + '\',\'connect\',this.checked)"></td>'
    + registerCell
    + '<td class="access-matrix-check"><input type="checkbox" ' + (hasManage ? 'checked ' : '') +
      'onchange="accessToggleCheck(\'' + escAttr(entry.id) + '\',\'' + escAttr(machineId) + '\',\'manage\',this.checked)"></td>'
    + '<td>' + renderAccessServicesCell(entry.id, machineId, grant) + '</td>'
    + '<td class="access-matrix-actions">' + actions + '</td>'
    + '</tr>';
}

// ── Interaction handlers ──────────────────────────────────────────

function accessToggleCheck(id, machineId, perm, checked) {
  var p = accessEnsurePending(id, machineId);
  if (p.revoke) {
    // Toggling a checkbox on a revoke-staged row clears the revoke and
    // treats the click as a normal edit on top of the baseline.
    p.revoke = false;
  }
  var perms = p.desired.permissions.slice();
  var idx = perms.indexOf(perm);
  if (checked && idx === -1) perms.push(perm);
  if (!checked && idx !== -1) perms.splice(idx, 1);
  p.desired.permissions = perms;
  // Services filter only makes sense when connect is granted. Clear
  // it when connect is removed so we don't send a stale filter on
  // save; the server ignores services without connect anyway but the
  // UI would still render chips, which is confusing.
  if (perms.indexOf('connect') === -1) {
    p.desired.services = [];
  }
  accessDropIfClean(id, machineId);
  renderAccessPane();
}

function accessStageRevoke(id, machineId) {
  var baseline = accessBaselineFor(id, machineId);
  if ((baseline.permissions || []).length === 0 && !accessIsPending(id, machineId)) {
    // Nothing to revoke.
    return;
  }
  var p = accessEnsurePending(id, machineId);
  p.revoke = true;
  p.desired.permissions = [];
  p.desired.services = [];
  renderAccessPane();
}

function accessUndoRevokeRow(id, machineId) {
  var key = accessPendingKey(id, machineId);
  var p = accessState.pending[key];
  if (!p) return;
  p.revoke = false;
  p.desired.permissions = p.baseline.permissions.slice();
  p.desired.services = p.baseline.services.slice();
  accessDropIfClean(id, machineId);
  renderAccessPane();
}

function accessUndo() {
  // Simple global undo: drop every pending change. A per-row stack
  // would be nicer but this matches Profiles / Agents' Undo semantics
  // (the whole pending batch is rolled back).
  accessState.pending = {};
  renderAccessPane();
}

// ── Save ──────────────────────────────────────────────────────────

function accessSave() {
  var pending = accessState.pending;
  var keys = [];
  for (var k in pending) {
    if (Object.prototype.hasOwnProperty.call(pending, k)) keys.push(k);
  }
  if (keys.length === 0) return;

  var hub = accessState.hub;
  var saveBtn = document.getElementById('access-save-btn');
  if (saveBtn) saveBtn.disabled = true;

  var promises = keys.map(function (k) {
    var p = pending[k];
    var ifMatch = p.version > 0 ? String(p.version) : '';
    if (p.revoke) {
      return goApp.AdminRevokeMachineAccess(hub, p.id, p.machineId, ifMatch).then(function (raw) {
        return { key: k, pending: p, result: parseJSON(raw) };
      });
    }
    var perms = p.desired.permissions.join(',');
    var services = p.desired.services.join(',');
    return goApp.AdminSetMachineAccess(hub, p.id, p.machineId, perms, services, ifMatch).then(function (raw) {
      return { key: k, pending: p, result: parseJSON(raw) };
    });
  });

  Promise.all(promises).then(function (results) {
    if (accessState.hub !== hub) return; // user switched hubs mid-save
    var conflicts = [];
    var errors = [];
    results.forEach(function (r) {
      if (r.result.conflict) {
        conflicts.push({ key: r.key, pending: r.pending, current: r.result.current, version: r.result.version });
      } else if (r.result.error) {
        errors.push({ key: r.key, pending: r.pending, message: r.result.error });
      }
    });

    if (errors.length > 0 && conflicts.length === 0) {
      // No conflicts, just other errors: show them and keep pending
      // state so the operator can retry.
      var msg = errors.map(function (e) { return e.pending.id + '/' + e.pending.machineId + ': ' + e.message; }).join('\n');
      showError('Save failed:\n' + msg);
      accessLoadData(); // reload so successful writes are reflected
      return;
    }

    if (conflicts.length > 0) {
      accessState.lastSaveConflict = conflicts;
      openAccessConflictModal(conflicts);
      return;
    }

    // All writes succeeded: clear pending and reload from server so
    // the new versions and any server-side derived changes (e.g.,
    // normalized service names) show up.
    accessState.pending = {};
    accessLoadData();
  }).catch(function (err) {
    showError('Save failed: ' + (err && err.message ? err.message : err));
    accessUpdateToolbar();
  });
}

function parseJSON(raw) {
  try { return JSON.parse(raw || '{}'); } catch (e) { return { error: 'invalid server response' }; }
}

// ── Conflict modal ────────────────────────────────────────────────

function openAccessConflictModal(conflicts) {
  var listHtml = '';
  conflicts.forEach(function (c) {
    listHtml += '<li><strong>' + escHtml(c.pending.id) + '</strong> on <strong>' + escHtml(c.pending.machineId) + '</strong></li>';
  });
  var html = '<div class="modal-overlay" id="access-conflict-modal" role="dialog" aria-modal="true" aria-labelledby="access-conflict-title">'
    + '<div class="modal-dialog"><div class="modal-header">'
    + '<h3 id="access-conflict-title" class="access-conflict-title">Server state changed</h3>'
    + '<button type="button" class="modal-close" onclick="accessDiscardAfterConflict()" aria-label="Close">&times;</button>'
    + '</div><div class="modal-body">'
    + '<p>Another operator modified access on <strong>' + escHtml(accessState.hub) + '</strong> since you loaded this view. The following rows were affected:</p>'
    + '<ul>' + listHtml + '</ul>'
    + '<p>Reload to see the current server state, then re-apply your staged changes.</p>'
    + '<p>If you prefer to commit your batch as-is and overwrite the other operator\'s edits on those rows, you can <button type="button" class="link" onclick="accessForceOverwriteConflicts()">force overwrite instead</button>. That path requires a second confirmation.</p>'
    + '</div><div class="modal-actions">'
    + '<button type="button" class="btn" onclick="accessDiscardAfterConflict()">Discard my changes</button>'
    + '<button type="button" class="btn btn-primary" onclick="accessReloadAfterConflict()">Reload</button>'
    + '</div></div></div>';
  var host = document.getElementById('access-conflict-modal-host') || (function () {
    var el = document.createElement('div');
    el.id = 'access-conflict-modal-host';
    document.body.appendChild(el);
    return el;
  })();
  host.innerHTML = html;
  accessUpdateToolbar();
}

// closeAccessConflictModal tears down the modal's DOM without touching
// pending state. Callers must pair it with one of the three recovery
// actions below; otherwise pending state would carry stale If-Match
// values that guarantee 412 on the next Save. This function is kept
// internal on purpose — the modal's buttons drive the recovery paths.
function closeAccessConflictModal() {
  var host = document.getElementById('access-conflict-modal-host');
  if (host) host.innerHTML = '';
}

// accessReloadAfterConflict abandons pending changes and reloads the
// server state. The operator can re-stage any changes they still want
// to make against the fresh baseline. This is the safe default.
function accessReloadAfterConflict() {
  closeAccessConflictModal();
  accessState.pending = {};
  accessState.lastSaveConflict = null;
  accessLoadData();
}

// accessDiscardAfterConflict throws away pending changes and closes
// the modal without reloading. Use when the operator realizes their
// batch was wrong and wants to walk away from it. Wired to the Cancel
// button and the X close button so there is no path where the operator
// can linger with stale If-Match values in pending state.
function accessDiscardAfterConflict() {
  closeAccessConflictModal();
  accessState.pending = {};
  accessState.lastSaveConflict = null;
  accessUpdateToolbar();
  renderAccessPane();
}

function accessForceOverwriteConflicts() {
  var conflicts = accessState.lastSaveConflict || [];
  if (conflicts.length === 0) { closeAccessConflictModal(); return; }
  showConfirmDialog(
    'Force overwrite?',
    'Commit your staged changes without the concurrency check. The other operator\'s edits on the conflicting rows will be lost.',
    'Force overwrite'
  ).then(function (yes) {
    if (!yes) return;
    closeAccessConflictModal();
    var hub = accessState.hub;
    // Retry the conflicting writes with no If-Match. Non-conflicting
    // rows have already been committed and can be ignored.
    var promises = conflicts.map(function (c) {
      var p = c.pending;
      if (p.revoke) {
        return goApp.AdminRevokeMachineAccess(hub, p.id, p.machineId, '').then(parseJSON);
      }
      return goApp.AdminSetMachineAccess(hub, p.id, p.machineId,
        p.desired.permissions.join(','), p.desired.services.join(','), '').then(parseJSON);
    });
    Promise.all(promises).then(function () {
      accessState.pending = {};
      accessState.lastSaveConflict = null;
      accessLoadData();
    }).catch(function (err) {
      showError('Force overwrite failed: ' + (err && err.message ? err.message : err));
    });
  });
}

// accessReload re-renders the Access page after a successful mutation
// (token create / rotate / delete, etc.). When the Access tab is not
// the active tab, it's a no-op; the next switchTab('access') does a
// full refresh anyway. Placeholder until the full data layer lands.
function accessReload() {
  var pane = document.getElementById('tab-access');
  if (!pane || pane.classList.contains('hidden')) return;
  refreshAccessTab();
}

// Toolbar action stubs for wiring still being built. Full behavior
// lands in the toolbar-actions commit; the stubs keep clicks from
// throwing while the rest of the surface is under construction.
function accessAddIdentity() {
  // Reuse the existing create-token modal; the modal's submit path
  // now calls accessReload() which refreshes the Access page in place.
  if (typeof promptCreateToken === 'function') {
    // The old flow expected currentAdminHub to be set; the new flow
    // uses accessState.hub. Bridge them for reuse.
    currentAdminHub = accessState.hub;
    promptCreateToken();
  }
}
function accessPairCode() {
  if (typeof promptPairCode === 'function') {
    currentAdminHub = accessState.hub;
    promptPairCode();
  }
}
function accessRescanServices() { accessLoadData(); }

// ── By-identity detail header actions ─────────────────────────────
//
// These wrappers glue the Access page to the existing confirm-modal
// helpers and the admin bridge. They currently share currentAdminHub
// with the (dead) legacy Hubs admin plumbing so the helper functions
// keep working without a bigger refactor.

function accessRenameIdentity(id) {
  currentAdminHub = accessState.hub;
  showPromptDialog('Rename Identity', 'Enter a new name for "' + id + '":', id, 'Rename').then(function (newId) {
    if (!newId || newId === id) return;
    var entry = accessEntryFor(id);
    var ifMatch = entry ? String(entry.version || '') : '';
    goApp.AdminRenameAccess(accessState.hub, id, newId, ifMatch).then(function (raw) {
      var data = parseJSON(raw);
      if (data.conflict) {
        showError('Identity "' + id + '" was modified by another operator. Reload and try again.');
        accessLoadData();
        return;
      }
      if (data.error) { showError('Rename failed: ' + data.error); return; }
      if (accessState.selection.identityId === id) accessState.selection.identityId = newId;
      accessLoadData();
    });
  });
}

function accessChangeRole(id) {
  var entry = accessEntryFor(id);
  if (!entry) return;
  // Owner demotion is allowed at this layer; the backend's last-owner
  // guard rejects the specific case of demoting the only owner and
  // surfaces that as an error in the modal. Operators with multiple
  // owner identities can freely rebalance.
  document.getElementById('crm-identity').textContent = id;
  document.getElementById('crm-role').value = (entry.role === '' || entry.role === 'user') ? '' : entry.role;
  var errEl = document.getElementById('crm-error');
  errEl.classList.add('hidden'); errEl.textContent = '';
  // Stash the identity ID on the modal so submitChangeRole can read it.
  document.getElementById('access-change-role-modal').setAttribute('data-identity', id);
  document.getElementById('access-change-role-modal').classList.remove('hidden');
}

function submitChangeRole() {
  var modal = document.getElementById('access-change-role-modal');
  var id = modal.getAttribute('data-identity') || '';
  if (!id) { closeModal('access-change-role-modal'); return; }
  var newRole = document.getElementById('crm-role').value;
  var entry = accessEntryFor(id);
  var current = entry ? entry.role : '';
  // Normalize current: the backend stores "" for user; the UI shows
  // "" in the dropdown for the same value. If they match, bail
  // silently so we don't emit a no-op PATCH.
  if (current === newRole || (current === 'user' && newRole === '') || (current === '' && newRole === '')) {
    closeModal('access-change-role-modal');
    return;
  }

  // Owner promotion is a privilege escalation that confers full
  // control of the hub including the power to demote or delete
  // other admins. A second confirm forces the operator to
  // acknowledge the severity. Owner demotion is already guarded on
  // the server by the last-owner rule, so no extra confirm is needed
  // when going owner -> admin (or any other role).
  if (newRole === 'owner' && current !== 'owner') {
    showConfirmDialog(
      'Promote to Owner?',
      'Promoting "' + id + '" to Owner grants full control of this hub, including the power to demote or remove any other admin. Continue?',
      'Promote to Owner'
    ).then(function (yes) {
      if (!yes) return;
      doChangeRole(id, newRole, entry);
    });
    return;
  }

  doChangeRole(id, newRole, entry);
}

// doChangeRole issues the PATCH. Factored out of submitChangeRole so
// the owner-promotion confirm and the regular path share one code
// path for the If-Match wiring, conflict handling, and error
// surfacing.
function doChangeRole(id, newRole, entry) {
  var ifMatch = entry ? String(entry.version || '') : '';
  goApp.AdminChangeRole(accessState.hub, id, newRole, ifMatch).then(function (raw) {
    var data = parseJSON(raw);
    if (data.conflict) {
      var errEl = document.getElementById('crm-error');
      errEl.textContent = 'This identity was modified by another operator. Close and reload, then try again.';
      errEl.classList.remove('hidden');
      return;
    }
    if (data.error) {
      var errEl = document.getElementById('crm-error');
      errEl.textContent = data.error;
      errEl.classList.remove('hidden');
      return;
    }
    closeModal('access-change-role-modal');
    accessLoadData();
  });
}

function accessRotateIdentityToken(id) {
  currentAdminHub = accessState.hub;
  if (typeof rotateToken === 'function') rotateToken(id);
}

function accessDeleteIdentity(id) {
  currentAdminHub = accessState.hub;
  if (typeof deleteToken === 'function') deleteToken(id);
}

// ── Edit services modal ───────────────────────────────────────────

// aesState pins the identity+machine currently being edited so the
// modal's submit handler can reach them without re-parsing the DOM.
var aesState = { id: '', machineId: '' };

function accessEditServices(id, machineId) {
  if (!hubSupportsPerServiceACL(accessState.hub)) {
    // Expose the capability hint even on unsupported hubs so the
    // operator understands why the filter is unavailable. Opening the
    // modal in a disabled state is more informative than a toast.
    aesOpenDisabled(id, machineId);
    return;
  }
  aesState.id = id;
  aesState.machineId = machineId;

  var grant = accessEffectiveGrant(id, machineId);
  var isRestricted = (grant.services || []).length > 0;

  document.getElementById('aes-identity').textContent = id;
  document.getElementById('aes-machine').textContent = machineId === '*' ? '* (all machines)' : machineId;
  document.getElementById('aes-scope-all').checked = !isRestricted;
  document.getElementById('aes-scope-restrict').checked = isRestricted;
  document.getElementById('aes-disabled-hint').style.display = 'none';

  aesPopulateServicesList(machineId, grant.services || []);
  aesUpdateVisibility();
  document.getElementById('access-edit-services-modal').classList.remove('hidden');
}

function aesOpenDisabled(id, machineId) {
  aesState.id = id;
  aesState.machineId = machineId;
  document.getElementById('aes-identity').textContent = id;
  document.getElementById('aes-machine').textContent = machineId === '*' ? '* (all machines)' : machineId;
  document.getElementById('aes-scope-all').checked = true;
  document.getElementById('aes-scope-restrict').checked = false;
  document.getElementById('aes-scope-restrict').disabled = true;
  document.getElementById('aes-services-group').style.display = 'none';
  document.getElementById('aes-disabled-hint').style.display = '';
  document.getElementById('access-edit-services-modal').classList.remove('hidden');
}

function aesPopulateServicesList(machineId, selected) {
  var list = document.getElementById('aes-services-list');
  var input = document.getElementById('aes-services-freetext');
  var hint = document.getElementById('aes-services-hint');
  document.getElementById('aes-scope-restrict').disabled = false;

  // Wildcard machine: no concrete service list to enumerate, so fall
  // back to free-text. Users may legitimately want a cross-machine
  // filter like "jellyfin on any machine that exposes one."
  if (machineId === '*') {
    list.innerHTML = '';
    list.style.display = 'none';
    input.style.display = '';
    input.value = (selected || []).join(', ');
    hint.textContent = 'Wildcard machine: enter comma-separated service names.';
    return;
  }

  var m = accessMachineFor(machineId);
  var services = (m && m.services) ? m.services : [];
  input.style.display = 'none';
  if (services.length === 0) {
    list.style.display = '';
    list.innerHTML = '<p class="empty-hint">No services advertised by this machine.</p>';
    hint.textContent = '';
    return;
  }

  var selectedSet = {};
  (selected || []).forEach(function (s) { selectedSet[s] = true; });

  var html = '';
  services.forEach(function (svc) {
    var name = svc.name || ('port-' + svc.port);
    html += '<label class="aes-service-row">';
    html += '<input type="checkbox" value="' + escAttr(name) + '"' + (selectedSet[name] ? ' checked' : '') + '>';
    html += '<span>' + escHtml(name) + '</span>';
    html += '<span class="aes-service-port">port ' + escHtml(String(svc.port || '?')) + '</span>';
    html += '</label>';
  });
  list.innerHTML = html;
  list.style.display = '';
  hint.textContent = 'Tick the services to include.';
}

function aesUpdateVisibility() {
  var group = document.getElementById('aes-services-group');
  var restrict = document.getElementById('aes-scope-restrict').checked;
  group.style.display = restrict ? '' : 'none';
}

function aesApply() {
  var scope = document.getElementById('aes-scope-all').checked ? 'all' : 'restrict';
  var services = [];

  if (scope === 'restrict') {
    if (aesState.machineId === '*') {
      var raw = document.getElementById('aes-services-freetext').value || '';
      services = raw.split(',').map(function (s) { return s.trim(); }).filter(function (s) { return s.length > 0; });
    } else {
      var boxes = document.querySelectorAll('#aes-services-list input[type="checkbox"]:checked');
      for (var i = 0; i < boxes.length; i++) services.push(boxes[i].value);
    }
    // Deduplicate. The free-text path can produce duplicates ("git, git");
    // the checkbox path cannot, but dedup is free and keeps the server
    // view tidy.
    var seen = {};
    services = services.filter(function (s) {
      if (seen[s]) return false;
      seen[s] = true;
      return true;
    });
    if (services.length === 0) {
      showError('Pick at least one service, or switch back to "Allow all services".');
      return;
    }
  }

  var p = accessEnsurePending(aesState.id, aesState.machineId);
  // Editing the services filter is a positive action, so it clears any
  // pending revoke on the same row.
  if (p.revoke) {
    p.revoke = false;
    p.desired.permissions = p.baseline.permissions.slice();
  }
  if (scope === 'restrict') {
    // A services filter without connect is meaningless; the backend
    // would ignore it. Force connect on when the operator is
    // explicitly restricting.
    if (p.desired.permissions.indexOf('connect') === -1) {
      p.desired.permissions.push('connect');
    }
    p.desired.services = services;
  } else {
    // "Allow all services" just clears the filter. It does not touch
    // the connect permission: if the operator unchecked connect a
    // moment ago, the modal should not silently re-grant it.
    p.desired.services = [];
  }
  accessDropIfClean(aesState.id, aesState.machineId);
  closeModal('access-edit-services-modal');
  renderAccessPane();
}

// ── Export audit ──────────────────────────────────────────────────

function accessExportAudit() {
  if (!accessState.loaded) {
    showError('Load a hub before exporting the audit.');
    return;
  }
  // Trim the machines snapshot to just the identifying fields; the
  // full MachineStatus carries transient runtime state (sessionCount,
  // lastSeen) that would make diffs noisy. Services are retained in
  // their full form since they drive the ACL semantics.
  var audit = {
    exportedAt: new Date().toISOString(),
    hub: accessState.hub,
    entries: accessState.entries,
    machines: accessState.machines.map(function (m) {
      return {
        id: m.id,
        agentConnected: m.agentConnected === true,
        services: (m.services || []).map(function (s) {
          return { name: s.name || '', port: s.port || 0, proto: s.proto || '' };
        }),
      };
    }),
  };
  var json = JSON.stringify(audit, null, 2);
  // Route through the native OS save dialog, not a browser download,
  // so the operator picks a real filesystem location the same way
  // they do from the log pane's Save button. See SaveAccessAudit in
  // app.go for the Wails-bound dialog + writer.
  goApp.SaveAccessAudit(accessState.hub, json).then(function (path) {
    if (path) tvLog('Exported access audit to ' + path);
  }).catch(function (err) {
    showError('Export failed: ' + (err && err.message ? err.message : err));
  });
}

// ── Updates tab ─────────────────────────────────────────────────────
//
// Lifecycle UI for the binaries installed on this machine: TelaVisor
// itself, the tela CLI, and any local telad / telahubd copies. Hub
// and agent update surfaces live on their respective Infrastructure
// tabs and are unaffected by this tab.
//
// The tab mirrors the channel selector from Application Settings so
// the most-touched workflow (check, switch channel, install) stays
// in one place. The underlying state (GetClientChannel /
// SetClientChannel) is the same, so edits from either surface stay
// in sync.

function refreshUpdatesTab() {
  var tools = document.getElementById('updates-tools-status');
  if (tools) tools.innerHTML = '<p class="loading">Checking for updates&hellip;</p>';
  populateChannelSelect('updates-channel-select');
  var sel = document.getElementById('updates-channel-select');
  var status = document.getElementById('updates-channel-status');
  if (sel && !sel.dataset.wired) {
    sel.dataset.wired = '1';
    sel.addEventListener('change', function () {
      var newCh = sel.value;
      if (status) status.textContent = 'Switching to ' + newCh + '\u2026';
      goApp.SetClientChannel(newCh, findChannelManifestBase(newCh)).then(function (r) {
        var data;
        try { data = typeof r === 'string' ? JSON.parse(r) : r; } catch (e) { data = {}; }
        if (data && data.error) {
          if (status) status.textContent = 'Error: ' + data.error;
          return;
        }
        if (status) status.textContent = 'Now on ' + newCh + '.';
        // The channel switch moves the goalpost for "latest"; re-run
        // the check so the tools table reflects the new baseline.
        goApp.CheckForUpdatesNow().then(function () {
          renderUpdatesToolsTable();
        });
      }).catch(function (err) {
        if (status) status.textContent = 'Error: ' + err;
      });
    });
  }
  goApp.GetClientChannel().then(function (info) {
    if (!sel) return;
    var ch = (info && info.channel) ? info.channel : 'dev';
    sel.value = ch;
    if (status) status.textContent = 'Currently on ' + ch + '.';
  }).catch(function () { /* best effort */ });
  // Force-refresh: a tab switch onto Updates is the operator asking
  // "tell me the current state," which is exactly what forceRefresh
  // does inside renderToolsTable.
  renderUpdatesToolsTable(true);
}

function renderUpdatesToolsTable(forceRefresh) {
  var el = document.getElementById('updates-tools-status');
  if (!el) return;
  renderToolsTable(el, /*showActions*/ true, null, !!forceRefresh);
}

