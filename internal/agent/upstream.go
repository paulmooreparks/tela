package agent

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// upstreamConfig describes a dependency that the local service needs to reach.
// telad listens on localhost:Port and forwards connections to Target.
type upstreamConfig struct {
	Port   uint16 `yaml:"port"`           // localhost port to listen on
	Target string `yaml:"target"`         // host:port to forward to
	Name   string `yaml:"name,omitempty"` // human-readable label
}

// startUpstreamListeners creates TCP listeners on localhost for each upstream
// and forwards connections to the configured target address. Returns a cleanup
// function that closes all listeners.
//
// Upstreams run for the lifetime of the telad process, independent of tunnel
// sessions. They provide the indirection layer that lets services call
// localhost:PORT while telad routes to the actual target.
func startUpstreamListeners(lg *log.Logger, upstreams []upstreamConfig) func() {
	if len(upstreams) == 0 {
		return func() {}
	}

	var listeners []net.Listener
	for _, u := range upstreams {
		addr := fmt.Sprintf("0.0.0.0:%d", u.Port)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			lg.Printf("[upstream] listen failed on %s: %v", addr, err)
			continue
		}
		listeners = append(listeners, l)

		name := u.Name
		if name == "" {
			name = fmt.Sprintf("port %d", u.Port)
		}
		lg.Printf("[upstream] %s: :%d -> %s", name, u.Port, u.Target)

		go acceptUpstream(lg, l, u.Target, name)
	}

	lg.Printf("[upstream] %d listener(s) started", len(listeners))

	return func() {
		for _, l := range listeners {
			l.Close()
		}
	}
}

// acceptUpstream accepts connections on the listener and forwards each to the target.
func acceptUpstream(lg *log.Logger, l net.Listener, target, name string) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return // listener closed
		}
		go forwardUpstream(lg, conn, target, name)
	}
}

// forwardUpstream dials the target and copies data bidirectionally.
func forwardUpstream(lg *log.Logger, src net.Conn, target, name string) {
	defer src.Close()

	dst, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		lg.Printf("[upstream] %s: dial %s failed: %v", name, target, err)
		return
	}
	defer dst.Close()

	if tc, ok := dst.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	if tc, ok := src.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}

	logVerbose(lg, "[upstream] %s: connection %s -> %s", name, src.RemoteAddr(), target)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(src, dst)
	}()

	wg.Wait()
}
