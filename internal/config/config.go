// Package config defines the persisted application configuration and the
// operations used to read and mutate it. The on-disk format is YAML so it can
// be hand-edited by users who live in kubeconfig land, and the same struct is
// written back when forwards are added/edited through the UI.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// TypeKubernetes is the only connection type implemented today. The field
// exists so the schema can grow other types (SSH tunnels, raw TCP, …) later
// without a breaking config migration.
const TypeKubernetes = "kubernetes"

// Target kinds understood by the kubernetes forwarder.
const (
	KindDeployment = "deployment"
	KindService    = "service"
	KindPod        = "pod"
)

// Forward describes a single toggleable port-forward.
type Forward struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	Context    string `yaml:"context"`
	Namespace  string `yaml:"namespace"`
	TargetKind string `yaml:"targetKind"`
	TargetName string `yaml:"targetName"`
	RemotePort int    `yaml:"remotePort"`
	LocalPort  int    `yaml:"localPort"`
	Address    string `yaml:"address,omitempty"`
	AutoStart  bool   `yaml:"autoStart"`
}

// BindAddress returns the local interface kubectl should bind to.
func (f Forward) BindAddress() string {
	if strings.TrimSpace(f.Address) == "" {
		return "127.0.0.1"
	}
	return f.Address
}

// Validate returns a human-readable error if the forward is not runnable.
func (f Forward) Validate() error {
	switch {
	case strings.TrimSpace(f.Name) == "":
		return fmt.Errorf("name is required")
	case f.Type != TypeKubernetes:
		return fmt.Errorf("unsupported type %q", f.Type)
	case strings.TrimSpace(f.Context) == "":
		return fmt.Errorf("context is required")
	case strings.TrimSpace(f.Namespace) == "":
		return fmt.Errorf("namespace is required")
	case f.TargetKind != KindDeployment && f.TargetKind != KindService && f.TargetKind != KindPod:
		return fmt.Errorf("targetKind must be deployment, service or pod")
	case strings.TrimSpace(f.TargetName) == "":
		return fmt.Errorf("target is required")
	case f.RemotePort <= 0 || f.RemotePort > 65535:
		return fmt.Errorf("remotePort must be between 1 and 65535")
	case f.LocalPort < 0 || f.LocalPort > 65535:
		return fmt.Errorf("localPort must be between 0 and 65535")
	}
	return nil
}

// Config is the root document. Operations are guarded by a mutex because the
// UI and the forward manager can touch it from different goroutines.
type Config struct {
	// LaunchAtLogin, when true, installs a macOS LaunchAgent so the app starts
	// automatically when the user logs in.
	LaunchAtLogin bool      `yaml:"launchAtLogin"`
	Forwards      []Forward `yaml:"forwards"`

	path string
	mu   sync.Mutex
}

// DefaultPath resolves the config location, honouring an override env var and
// otherwise using the OS-conventional config dir (on macOS that is
// ~/Library/Application Support/k8s-tray-forwarder/config.yaml).
func DefaultPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("K8S_TRAY_FORWARDER_CONFIG")); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "k8s-tray-forwarder", "config.yaml"), nil
}

// Load reads the config at path, creating an empty one if the file is absent.
func Load(path string) (*Config, error) {
	c := &Config{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.normalize()
	return c, nil
}

// normalize guarantees every forward has a unique, non-empty ID. Missing or
// duplicate IDs are repaired rather than rejected so a typo in a hand-edited
// file can't prevent the app from starting. Uniqueness matters because the
// forward manager keys running sessions by ID — two entries sharing one would
// alias the same session.
func (c *Config) normalize() {
	seen := make(map[string]bool, len(c.Forwards))
	for i := range c.Forwards {
		f := &c.Forwards[i]
		id := strings.TrimSpace(f.ID)
		if id == "" || seen[id] {
			id = newID(f.Name)
			for seen[id] {
				id = newID(f.Name)
			}
		}
		f.ID = id
		seen[id] = true
	}
}

// Path returns the file backing this config.
func (c *Config) Path() string { return c.path }

// LaunchAtLoginEnabled reports the persisted launch-at-login preference.
func (c *Config) LaunchAtLoginEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.LaunchAtLogin
}

// SetLaunchAtLogin updates the launch-at-login preference and persists it.
func (c *Config) SetLaunchAtLogin(v bool) error {
	c.mu.Lock()
	c.LaunchAtLogin = v
	c.mu.Unlock()
	return c.save()
}

// List returns a copy of the forwards so callers can range safely.
func (c *Config) List() []Forward {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Forward, len(c.Forwards))
	copy(out, c.Forwards)
	return out
}

// Get returns the forward with the given ID.
func (c *Config) Get(id string) (Forward, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.Forwards {
		if f.ID == id {
			return f, true
		}
	}
	return Forward{}, false
}

// Upsert inserts a new forward or replaces an existing one (matched by ID),
// then persists. A blank ID is assigned a fresh one.
func (c *Config) Upsert(f Forward) (Forward, error) {
	c.mu.Lock()
	if f.ID == "" {
		f.ID = newID(f.Name)
	}
	replaced := false
	for i := range c.Forwards {
		if c.Forwards[i].ID == f.ID {
			c.Forwards[i] = f
			replaced = true
			break
		}
	}
	if !replaced {
		c.Forwards = append(c.Forwards, f)
	}
	c.mu.Unlock()
	return f, c.save()
}

// Delete removes a forward by ID and persists.
func (c *Config) Delete(id string) error {
	c.mu.Lock()
	out := c.Forwards[:0]
	for _, f := range c.Forwards {
		if f.ID != id {
			out = append(out, f)
		}
	}
	c.Forwards = out
	c.mu.Unlock()
	return c.save()
}

// save writes the document atomically (temp file + rename) to avoid leaving a
// truncated config behind if the process dies mid-write.
func (c *Config) save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func newID(name string) string {
	slug := slugRe.ReplaceAllString(strings.ToLower(name), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "forward"
	}
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return slug + "-" + hex.EncodeToString(buf)
}
