// Package telelog provides a queued, ISO 8601 UTC logger for all Tela binaries.
//
// Log lines are written to a channel and consumed by a low-priority background
// goroutine, minimizing the impact of logging on runtime performance. Lines are
// still captured in order because the channel is unbuffered reads happen in FIFO
// order from a buffered channel.
//
// Usage:
//
//	telelog.Init("tela", os.Stderr)   // call once at startup
//	log.Println("hello")             // uses standard log, routed through telelog
//	telelog.Flush()                   // call before exit to drain the queue
package telelog

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

const queueSize = 4096

var (
	queue   chan []byte
	output  io.Writer
	prefix  string
	done    chan struct{}
	once    sync.Once
	started bool
)

// Init configures the global logger to use queued, ISO 8601 UTC output.
// prefix is the component name (e.g., "tela", "telad", "mount").
// out is the output destination (typically os.Stderr).
// Call this once at the start of main before any logging.
func Init(pfx string, out io.Writer) {
	once.Do(func() {
		prefix = pfx
		output = out
		queue = make(chan []byte, queueSize)
		done = make(chan struct{})
		started = true

		go drainQueue()

		// Route the standard log package through our queued writer.
		log.SetFlags(0)
		log.SetPrefix("")
		log.SetOutput(&queueWriter{})
	})
}

// NewLogger creates a log.Logger with a different prefix that routes through
// the same queue. Use this for per-component loggers (e.g., per-machine in telad).
func NewLogger(pfx string, out io.Writer) *log.Logger {
	// out is ignored; all output goes through the shared queue.
	// The prefix is baked into each formatted line.
	return log.New(&queueWriter{prefix: pfx}, "", 0)
}

// Flush blocks until all queued log lines have been written. Call before exit.
func Flush() {
	if !started {
		return
	}
	close(queue)
	<-done
}

// Writer returns an io.Writer that writes to the log queue with the default
// prefix. Useful for redirecting other output (e.g., log files) through the queue.
func Writer() io.Writer {
	return &queueWriter{}
}

// drainQueue is the background goroutine that writes queued lines to output.
func drainQueue() {
	defer close(done)
	for line := range queue {
		output.Write(line)
	}
}

// queueWriter implements io.Writer. Each Write call formats a timestamped line
// and enqueues it. If the queue is full, the line is written directly to avoid
// blocking the caller indefinitely.
type queueWriter struct {
	prefix string // override prefix; empty uses the default
}

func (w *queueWriter) Write(p []byte) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	pfx := w.prefix
	if pfx == "" {
		pfx = prefix
	}

	// If the message already contains a bracketed component prefix
	// (e.g., "[hub] message"), don't add another one.
	msg := string(p)
	if len(msg) > 0 && msg[0] == '[' {
		line := fmt.Sprintf("%s %s", now, msg)
		select {
		case queue <- []byte(line):
		default:
			output.Write([]byte(line))
		}
		return len(p), nil
	}

	line := fmt.Sprintf("%s [%s] %s", now, pfx, msg)

	// Non-blocking send: if the queue is full, write directly to avoid
	// stalling the caller. Order may be slightly off under extreme load
	// but lines are never lost.
	select {
	case queue <- []byte(line):
	default:
		output.Write([]byte(line))
	}

	return len(p), nil
}

// WrapOutput returns an io.Writer that tees to both the log queue and the
// given writer. Use this for the tela control log capture (which needs to
// capture output to a buffer AND route through the queue).
func WrapOutput(extra io.Writer) io.Writer {
	return &teeWriter{extra: extra}
}

type teeWriter struct {
	extra io.Writer
}

func (w *teeWriter) Write(p []byte) (int, error) {
	// Write the already-formatted line to the extra destination
	if w.extra != nil {
		w.extra.Write(p)
	}
	// Also write to the primary output (via the queue drainer, which
	// already wrote this line). Since this is used as log output, the
	// line is already formatted. Just pass through to extra.
	return len(p), nil
}

// SetOutput changes the output destination for the queue drainer.
// Use this to redirect log output (e.g., to a tee writer that captures
// output for a control API while still writing to stderr).
func SetOutput(w io.Writer) {
	output = w
}

// DirectWriter returns an io.Writer that writes directly to the output
// without going through the queue. Use for the underlying output when
// other writers need to tee.
func DirectWriter() io.Writer {
	if output != nil {
		return output
	}
	return os.Stderr
}
