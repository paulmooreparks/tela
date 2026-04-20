// Package credstore manages hub credentials (tokens) in user and system-level
// configuration files. It provides a simple lookup mechanism so users don't have
// to pass tokens on every command.
package credstore

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/paulmooreparks/tela/internal/service"
)

// Credential holds a hub-associated auth token and optional identity label.
type Credential struct {
	Token    string `yaml:"token"`
	Identity string `yaml:"identity,omitempty"`
}

// Store is the on-disk representation of credentials.yaml.
type Store struct {
	Hubs   map[string]Credential `yaml:"hubs"`
	Update UpdateConfig          `yaml:"update,omitempty"`
}

// UpdateConfig stores the client's release channel preference. The tela
// client itself uses this to decide which channel manifest to consult.
// Hub and agent channels are stored separately in their own YAML configs.
// See DESIGN-channel-sources.md for the sources data model.
type UpdateConfig struct {
	// Channel is the currently selected channel name. Resolution happens
	// via channel.ResolveBase(Channel, Sources).
	Channel string `yaml:"channel,omitempty"`

	// Sources maps channel names to base URLs. Same shape as hubConfig
	// and agent configFile; shared across the three on this host via
	// TelaVisor's sources-editing UI.
	Sources map[string]string `yaml:"sources,omitempty"`
}

// UserDir returns the user-level tela config directory.
// Windows: %APPDATA%\tela
// Unix: ~/.tela
func UserDir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "tela")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tela")
}

// UserPath returns the path to the user-level credential store.
func UserPath() string {
	return filepath.Join(UserDir(), "credentials.yaml")
}

// SystemPath returns the path to the system-level credential store.
// Windows: %ProgramData%\Tela\credentials.yaml
// Unix: /etc/tela/credentials.yaml
func SystemPath() string {
	return filepath.Join(service.ConfigDir(), "credentials.yaml")
}

// Load reads a credential store from the given path.
// If the file does not exist, returns an empty store (not nil).
func Load(path string) (*Store, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{Hubs: make(map[string]Credential)}, nil
		}
		return nil, fmt.Errorf("read credentials: %w", err)
	}

	var store Store
	if err := yaml.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	if store.Hubs == nil {
		store.Hubs = make(map[string]Credential)
	}

	return &store, nil
}

// Save writes the credential store to the given path with restrictive permissions.
func (s *Store) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}

	// Always use 0600 for user credentials, regardless of platform.
	// System credentials use the standard service file permission.
	perm := os.FileMode(0600)
	if strings.HasPrefix(path, service.ConfigDir()) {
		perm = service.ConfigFilePerm()
	}

	if err := os.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	return nil
}

// Get retrieves a credential by hub URL.
// The URL is normalized before lookup.
func (s *Store) Get(hubURL string) (Credential, bool) {
	if s.Hubs == nil {
		return Credential{}, false
	}
	cred, ok := s.Hubs[NormalizeHubURL(hubURL)]
	return cred, ok
}

// Set stores a credential for a hub URL.
// The URL is normalized before storage.
func (s *Store) Set(hubURL string, cred Credential) {
	if s.Hubs == nil {
		s.Hubs = make(map[string]Credential)
	}
	s.Hubs[NormalizeHubURL(hubURL)] = cred
}

// Remove deletes a credential for a hub URL.
// Returns true if the credential was present.
func (s *Store) Remove(hubURL string) bool {
	if s.Hubs == nil {
		return false
	}
	normalized := NormalizeHubURL(hubURL)
	_, ok := s.Hubs[normalized]
	if ok {
		delete(s.Hubs, normalized)
	}
	return ok
}

// LookupToken returns the token for a hub URL by checking:
// 1. User credential store (~/.tela/credentials.yaml)
// 2. System credential store (/etc/tela/credentials.yaml or %ProgramData%\Tela\credentials.yaml)
// Returns empty string if not found.
func LookupToken(hubURL string) string {
	// Try user store first
	userStore, err := Load(UserPath())
	if err == nil && userStore != nil {
		if cred, ok := userStore.Get(hubURL); ok {
			return cred.Token
		}
	}

	// Fall back to system store
	systemStore, err := Load(SystemPath())
	if err == nil && systemStore != nil {
		if cred, ok := systemStore.Get(hubURL); ok {
			return cred.Token
		}
	}

	return ""
}

// NormalizeHubURL normalizes a hub URL for consistent lookup.
// Strips trailing slashes and lowercases the scheme.
func NormalizeHubURL(rawURL string) string {
	// Strip trailing slash
	url := strings.TrimRight(rawURL, "/")

	// Find the scheme separator
	if idx := strings.Index(url, "://"); idx > 0 {
		scheme := strings.ToLower(url[:idx])
		rest := url[idx:]
		return scheme + rest
	}

	return url
}
