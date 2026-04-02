package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"sort"
	"strings"

	"golang.zx2c4.com/wireguard/tun/netstack"

	"github.com/paulmooreparks/tela/internal/telelog"
)

// gatewayConfig is the YAML schema for a path-based reverse proxy
// that telad exposes on a single tunnel port.
type gatewayConfig struct {
	Port   uint16         `yaml:"port"`
	Routes []gatewayRoute `yaml:"routes"`
}

// gatewayRoute maps a URL path prefix to a local target port.
type gatewayRoute struct {
	Path   string `yaml:"path"`
	Target uint16 `yaml:"target"`
}

// compiledRoute holds a parsed route with its reverse proxy.
type compiledRoute struct {
	prefix string
	proxy  *httputil.ReverseProxy
}

// startGatewayListener creates an HTTP reverse proxy on the netstack
// that routes requests by path prefix to local services.
func startGatewayListener(lg *log.Logger, tnet *netstack.Net,
	agentIP string, cfg *gatewayConfig, targetHost string) func() {

	if cfg == nil || cfg.Port == 0 || len(cfg.Routes) == 0 {
		return func() {}
	}

	// Sort routes by path length, longest first (most specific match wins).
	routes := make([]gatewayRoute, len(cfg.Routes))
	copy(routes, cfg.Routes)
	sort.Slice(routes, func(i, j int) bool {
		return len(routes[i].Path) > len(routes[j].Path)
	})

	// Build a reverse proxy for each route target.
	var compiled []compiledRoute
	for _, r := range routes {
		targetURL, err := url.Parse(fmt.Sprintf("http://%s:%d", targetHost, r.Target))
		if err != nil {
			lg.Printf("[gateway] invalid target for %s: %v", r.Path, err)
			continue
		}
		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		// Suppress proxy error log spam; the target may be temporarily down.
		proxy.ErrorLog = telelog.NewLogger("gateway", nil)
		compiled = append(compiled, compiledRoute{
			prefix: r.Path,
			proxy:  proxy,
		})
	}

	if len(compiled) == 0 {
		lg.Printf("[gateway] no valid routes configured")
		return func() {}
	}

	// HTTP handler: match longest prefix, proxy to target.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, cr := range compiled {
			if strings.HasPrefix(r.URL.Path, cr.prefix) {
				cr.proxy.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, "no gateway route matched", http.StatusBadGateway)
	})

	// Listen on the netstack.
	listenAddr := netip.AddrPortFrom(netip.MustParseAddr(agentIP), cfg.Port)
	listener, err := tnet.ListenTCPAddrPort(listenAddr)
	if err != nil {
		lg.Printf("[gateway] listen failed on %s: %v", listenAddr, err)
		return func() {}
	}

	server := &http.Server{Handler: handler}
	go server.Serve(listener)

	lg.Printf("[gateway] listening on %s (%d routes)", listenAddr, len(compiled))
	for _, r := range routes {
		lg.Printf("[gateway]   %s -> %s:%d", r.Path, targetHost, r.Target)
	}

	return func() {
		server.Close()
		listener.Close()
	}
}

// gatewayServiceDescriptors returns the service descriptors and ports
// that should be added to the registration for a gateway.
func gatewayServiceDescriptors(cfg *gatewayConfig) ([]serviceDescriptor, []uint16) {
	if cfg == nil || cfg.Port == 0 {
		return nil, nil
	}
	svc := serviceDescriptor{
		Port:        cfg.Port,
		Name:        "gateway",
		Proto:       "http",
		Description: "Path-based reverse proxy",
	}
	return []serviceDescriptor{svc}, []uint16{cfg.Port}
}

// mergeGatewayIntoRegistration adds the gateway port and service to a registration.
func mergeGatewayIntoRegistration(reg *registration, cfg *gatewayConfig) {
	svcs, ports := gatewayServiceDescriptors(cfg)
	reg.Services = append(reg.Services, svcs...)
	reg.Ports = append(reg.Ports, ports...)
}
