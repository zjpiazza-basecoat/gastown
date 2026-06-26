// Package gtcontext manages local CLI contexts for local and remote Gas Towns.
package gtcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultConfigFile = "contexts.json"

// Type identifies how the local gt CLI talks to a Town.
type Type string

const (
	TypeLocal      Type = "local"
	TypeRemote     Type = "remote"
	TypeKubernetes Type = "kubernetes"
)

// Context describes one local CLI target.
type Context struct {
	Type        Type   `json:"type"`
	URL         string `json:"url,omitempty"`
	Token       string `json:"token,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	KubeContext string `json:"kubeContext,omitempty"`
	TownRoot    string `json:"townRoot,omitempty"`
}

// Config is the on-disk context registry.
type Config struct {
	Current  string             `json:"current,omitempty"`
	Contexts map[string]Context `json:"contexts,omitempty"`
}

// Path returns the default context config path.
func Path() (string, error) {
	if p := os.Getenv("GT_CONTEXT_FILE"); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "gastown", defaultConfigFile), nil
}

// Load reads the context config. Missing config is treated as empty/local.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{Contexts: map[string]Context{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	return &cfg, nil
}

// Save writes the context config.
func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0600)
}

// Names returns sorted configured context names.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Contexts))
	for name := range c.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CurrentContext returns the selected context. No selection means local mode.
func CurrentContext() (string, Context, bool, error) {
	cfg, err := Load()
	if err != nil {
		return "", Context{}, false, err
	}
	if cfg.Current == "" || cfg.Current == "local" {
		return "local", Context{Type: TypeLocal}, true, nil
	}
	ctx, ok := cfg.Contexts[cfg.Current]
	if !ok {
		return cfg.Current, Context{}, false, fmt.Errorf("current context %q is not configured", cfg.Current)
	}
	return cfg.Current, ctx, true, nil
}

// IsRemoteSelected reports whether the current CLI context should bypass local workspace/tmux.
func IsRemoteSelected() (bool, Context, error) {
	_, ctx, _, err := CurrentContext()
	if err != nil {
		return false, Context{}, err
	}
	return ctx.Type == TypeRemote, ctx, nil
}

// NormalizeType validates and normalizes a context type string.
func NormalizeType(s string) (Type, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch Type(s) {
	case TypeLocal, TypeRemote, TypeKubernetes:
		return Type(s), nil
	default:
		return "", fmt.Errorf("unsupported context type %q (want local, remote, or kubernetes)", s)
	}
}
