package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/paulmooreparks/tela/internal/channel"
)

// runServer starts the HTTP server and blocks until stop is closed.
func runServer(cfg Config, stop <-chan struct{}) {
	filesDir := filepath.Join(cfg.Data, "files")
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		log.Fatalf("create files dir %s: %v", filesDir, err)
	}

	mux := http.NewServeMux()

	// Serve channel manifests: GET /{channel}.json
	for _, ch := range []string{channel.Dev, channel.Beta, channel.Stable} {
		ch := ch
		mux.HandleFunc("/"+ch+".json", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			path := filepath.Join(cfg.Data, ch+".json")
			if _, err := os.Stat(path); os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			http.ServeFile(w, r, path)
		})
	}

	// Serve binary files: GET /files/{name}
	// http.FileServer sets Content-Type and handles Range requests.
	mux.Handle("/files/", http.StripPrefix("/files/", http.FileServer(http.Dir(filesDir))))

	// Health/status endpoint.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"service": "telachand",
			"version": version,
			"data":    cfg.Data,
		})
	})

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("listening on %s, serving from %s", cfg.Listen, cfg.Data)

	go func() {
		<-stop
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
