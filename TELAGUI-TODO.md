# TelaGUI Code Review TODO

Issues identified during comprehensive code review (2026-03-19).
Ordered by fix priority.

## Priority 1: Race conditions (crash/corruption risk)

- [ ] **ST1** Fix race condition on controlWS (app.go:1254-1298)
  - Read goroutine deferred close races with DisconnectControlWS
  - SendControlCommand releases lock before WriteMessage
  - Fix: hold lock for write, use dedicated close channel

- [ ] **ST2** Synchronize currentProfileName access (app.go:1496+)
  - Global variable accessed from multiple functions without mutex
  - Fix: protect with a.mu or move into App struct

## Priority 2: Deadlock risk

- [ ] **ST3** Flatten nested Go calls in checkForUpdate (app.js:243-258)
  - Four levels of nested .then() with Go method calls
  - Fix: chain sequentially or use a single Go method that returns all needed data

## Priority 3: Quick security wins

- [ ] **S2** Add rel="noopener noreferrer" to external links (index.html:202-228)
  - 11 external links with target="_blank" missing rel attribute

## Priority 4: Polling stability

- [ ] **ST4** Add inFlight guard to connection poll (app.js:890-910)
  - setInterval fires every second with no overlap prevention
  - Fix: add boolean flag, skip if previous call hasn't returned

## Priority 5: Code deduplication

- [ ] **C1** Extract buildConnections() from duplicated logic (app.js:720-738, 803-820)
  - saveSelections() and doConnect() build identical groups/connections structure

- [ ] **C2** Extract makeServiceKey() utility (app.js, ~15 occurrences)
  - Pattern hubURL + '||' + machineId + '||' + serviceName repeated everywhere

## Priority 6: Error handling

- [ ] **ST7** Replace empty .catch() in loadSavedSelections (app.js:314)
  - Silently swallows all load errors; user gets no feedback

- [ ] **C3** Standardize error handling across frontend (app.js, throughout)
  - Mix of empty catch, alert(), if(err), raw error display
  - Define a consistent pattern (e.g., showError() helper)

## Priority 7: Accessibility basics

- [ ] **U1** Add tooltip/message when Connect is disabled (app.js:785)
  - No explanation for why the button won't click

- [ ] **U4** Add :focus-visible styles (style.css)
  - No visible focus indicator for keyboard navigation

- [ ] **U6** Add ARIA roles to tab bar (index.html:26-32)
  - Tab buttons need role="tab", aria-selected, aria-controls
  - Tab panes need role="tabpanel"

## Priority 8: Dead code cleanup

- [ ] **CL2** Remove dead CSS (style.css)
  - .settings-radio-group, .settings-radio, .settings-select (lines 981-1021)
  - Verify .profile-yaml-preview usage

## Lower priority

- [ ] **S1** Move control WebSocket auth from URL query param to first-message (app.go:1247)
  - Low practical risk (localhost, same user) but bad pattern

- [ ] **S3** Stop duplicating tokens into profile YAML (app.go:918-922)
  - Reference credential store dynamically instead

- [ ] **S4** Validate imported profile content (app.go:1735-1739)
  - Check URLs, port ranges, service names

- [ ] **S5** Sanitize Go error messages shown to user (app.js:1078)
  - Display generic messages, log details to console

- [ ] **ST5** Replace force-exit in shutdown with context timeout (app.go:475-478)
  - os.Exit(0) after 2 seconds kills in-progress cleanup

- [ ] **ST6** Prevent event listener accumulation (app.js:174-198, 1029-1049)
  - Document-level listeners for sidebar resize never removed
  - Hub-credential input listener added on every modal open

- [ ] **ST8** Reuse a.httpClient instead of creating new clients (app.go:1064,1139,1318,1347,1378)
  - Use context-based timeouts on shared client

- [ ] **ST9** Exit with code 1 on fatal error (main.go:50-52)
  - Currently prints error and exits 0

- [ ] **U2** Notify user when port conflict causes remapping (app.js:673)

- [ ] **U3** Increase small click targets (style.css:259, 322, 649)
  - Profile new button 28->44px, resize handle 4->8px, modal close 20->44px

- [ ] **U5** Improve hover states for topbar and tab buttons (style.css:79, 141)

- [ ] **C4** Standardize CSS naming conventions (style.css)

- [ ] **C5** Consolidate HTTP client usage (app.go)

- [ ] **CL1** Break up refreshAll() into smaller functions (app.js:457-544)

- [ ] **CL3** Rename _refreshIsConnected (app.js:462)

- [ ] **CL4** Use log.Printf instead of println (main.go:51)

- [ ] **CL5** Define named constant for Windows process flags (hide_windows.go:13,31)

- [ ] **CL6** Handle DLL/proc errors instead of ignoring (hide_windows.go:19-21)
