# TelaGUI Code Review TODO

Issues identified during comprehensive code review (2026-03-19).
Ordered by fix priority.

## Priority 1: Race conditions (crash/corruption risk)

- [x] **ST1** Fix race condition on controlWS (app.go)
- [x] **ST2** Synchronize currentProfileName access (app.go)

## Priority 2: Deadlock risk

- [x] **ST3** Flatten nested Go calls in checkForUpdate -- replaced with single GetUpdateInfo()

## Priority 3: Quick security wins

- [x] **S2** Add rel="noopener noreferrer" to external links

## Priority 4: Polling stability

- [x] **ST4** Add inFlight guard to connection poll

## Priority 5: Code deduplication

- [x] **C1** Extract buildConnections() from duplicated logic
- [x] **C2** Extract makeServiceKey() utility

## Priority 6: Error handling

- [x] **ST7** Replace empty .catch() in loadSavedSelections with console.warn
- [x] **C3** Standardize error handling -- showError() toast replaces alert() calls

## Priority 7: Accessibility basics

- [ ] **U1** Add tooltip/message when Connect is disabled
- [ ] **U4** Add :focus-visible styles
- [ ] **U6** Add ARIA roles to tab bar

## Priority 8: Dead code cleanup

- [x] **CL2** Remove dead CSS (settings-radio-group, settings-radio, settings-select)

## Completed (lower priority)

- [x] **S1** Move control WebSocket auth from URL query param to first-message
- [x] **ST5** Improved shutdown: close WebSocket before kill, added comment explaining force-exit
- [x] **ST8** Consolidate HTTP clients -- doRequest() helper with context timeouts
- [x] **ST9** Exit with code 1 on fatal error
- [x] **CL4** Use log.Printf instead of println
- [x] **CL5** Define named constant for Windows process flags (createNoWindow)
- [x] **CL6** Handle DLL/proc errors with logging

## Not a bug (reviewed, no change needed)

- **ST6** Event listener accumulation -- all listeners are in IIFEs that run once at startup. No leak.

## Remaining

- [ ] **S3** Stop duplicating tokens into profile YAML
- [ ] **S4** Validate imported profile content (URLs, port ranges)
- [ ] **S5** Sanitize Go error messages shown to user
- [ ] **U2** Notify user when port conflict causes remapping
- [ ] **U3** Increase small click targets (new profile button, resize handle, modal close)
- [ ] **U5** Improve hover states for topbar and tab buttons
- [ ] **C4** Standardize CSS naming conventions
- [ ] **CL1** Break up refreshAll() into smaller functions
- [ ] **CL3** Rename _refreshIsConnected variable
