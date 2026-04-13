# Run Tela as an OS service

All three Tela binaries can run as native operating system services: Windows Service Control Manager (SCM), Linux systemd, and macOS launchd. This chapter covers installing, starting, stopping, and reconfiguring each binary as a service so tunnels survive reboots and logouts without manual intervention.

{{#include ../../../howto/services.md:3:}}
