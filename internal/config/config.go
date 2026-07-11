// Package config defines and loads the phebs YAML config schema (T1.1).
//
// The schema is phebs' own (PLAN §1): upstream's JSON schemas are never
// copied. See docs/config.example.yaml for the annotated reference.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      Server       `yaml:"server"`
	Auth        Auth         `yaml:"auth"`
	Sync        Sync         `yaml:"sync"`
	Connections []Connection `yaml:"connections"`
}

type Sync struct {
	// CleanupOrphans deletes repo rows and mirrors no connection claims
	// (mirrors upstream's isAutoCleanupDisabled semantics, default off).
	CleanupOrphans bool `yaml:"cleanup_orphans"`
	// PollInterval is the job-runner poll cadence (Go duration, default
	// "15s"). Lower it when watch mode should feel instant.
	PollInterval string `yaml:"poll_interval"`
}

// Interval returns the parsed poll cadence; validation guarantees the
// string parses, so the default only covers the empty case.
func (s Sync) Interval() time.Duration {
	if d, err := time.ParseDuration(s.PollInterval); err == nil && d > 0 {
		return d
	}
	return 15 * time.Second
}

type Server struct {
	// Addr is the listen address. Default ":3070".
	Addr string `yaml:"addr"`
	// DataDir holds all state: shards at <DataDir>/index, bare repos at
	// <DataDir>/repos/<host>/<path>.git, the embedded DB at <DataDir>/db.
	// Default "~/.phebs".
	DataDir string `yaml:"data_dir"`
}

type Auth struct {
	// APIKey is the single bearer token for the API (T1.4). Empty means the
	// API is open; serve logs a loud warning in that case.
	APIKey string `yaml:"api_key"`
}

// Connection declares one source of repos to sync (Epic 2 consumes these).
type Connection struct {
	Name string `yaml:"name"` // required, unique, [a-z0-9-]+
	Type string `yaml:"type"` // "github" or "git"

	// github only. Token is a PAT; ${ENV} references are expanded so secrets
	// stay out of the file.
	Token   string   `yaml:"token"`
	Orgs    []string `yaml:"orgs"`
	Users   []string `yaml:"users"`
	Repos   []string `yaml:"repos"` // "owner/name"
	Exclude Exclude  `yaml:"exclude"`

	// git only: clone URL of a single repository. Plain absolute paths and
	// file:// URLs point at local repos.
	URL string `yaml:"url"`
	// Watch (local git only): poll the repo's HEAD and re-sync/re-index
	// when it moves — live search over a working repo.
	Watch bool `yaml:"watch"`
}

// IsLocalURL reports whether a git connection URL points at a repo on this
// machine (plain absolute path or file://).
func IsLocalURL(url string) bool {
	return strings.HasPrefix(url, "/") || strings.HasPrefix(url, "file://")
}

// Exclude filters repos out of a github connection's listing.
type Exclude struct {
	Archived bool     `yaml:"archived"`
	Forks    bool     `yaml:"forks"`
	Repos    []string `yaml:"repos"` // glob patterns on "owner/name"
}

func (e Exclude) isZero() bool {
	return !e.Archived && !e.Forks && len(e.Repos) == 0
}

var nameRE = regexp.MustCompile(`^[a-z0-9-]+$`)

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes and validates config bytes. Errors carry YAML line numbers:
// syntax/type/unknown-field errors from the strict decoder, semantic errors
// from the node tree.
func Parse(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("empty config")
		}
		return nil, err
	}
	var doc yaml.Node
	// cannot fail: the strict decode above already parsed the same bytes
	_ = yaml.Unmarshal(data, &doc)
	if err := cfg.validate(connectionLines(&doc)); err != nil {
		return nil, err
	}
	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// connectionLines returns the YAML line of each entry under "connections".
func connectionLines(doc *yaml.Node) []int {
	if len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "connections" {
			seq := root.Content[i+1]
			lines := make([]int, len(seq.Content))
			for j, item := range seq.Content {
				lines[j] = item.Line
			}
			return lines
		}
	}
	return nil
}

func (c *Config) validate(lines []int) error {
	var errs []error
	fail := func(i int, format string, args ...any) {
		line := 0
		if i < len(lines) {
			line = lines[i]
		}
		errs = append(errs, fmt.Errorf("line %d: connections[%d]: %s", line, i, fmt.Sprintf(format, args...)))
	}

	if c.Sync.PollInterval != "" {
		if d, err := time.ParseDuration(c.Sync.PollInterval); err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("sync.poll_interval %q: not a positive Go duration", c.Sync.PollInterval))
		}
	}

	seen := map[string]bool{}
	for i, conn := range c.Connections {
		switch {
		case conn.Name == "":
			fail(i, "name is required")
		case !nameRE.MatchString(conn.Name):
			fail(i, "name %q must match %s", conn.Name, nameRE)
		case seen[conn.Name]:
			fail(i, "duplicate name %q", conn.Name)
		}
		seen[conn.Name] = true

		switch conn.Type {
		case "github":
			if len(conn.Orgs)+len(conn.Users)+len(conn.Repos) == 0 {
				fail(i, "github connection needs at least one of orgs, users, repos")
			}
			if conn.URL != "" {
				fail(i, "url is only valid for type git")
			}
			if conn.Watch {
				fail(i, "watch is only valid for local git connections")
			}
			for _, pat := range conn.Exclude.Repos {
				if _, err := path.Match(pat, "x/y"); err != nil {
					fail(i, "bad exclude pattern %q: %v", pat, err)
				}
			}
		case "git":
			if conn.URL == "" {
				fail(i, "git connection requires url")
			}
			if len(conn.Orgs)+len(conn.Users)+len(conn.Repos) > 0 || conn.Token != "" || !conn.Exclude.isZero() {
				fail(i, "orgs/users/repos/token/exclude are only valid for type github")
			}
			if conn.Watch && !IsLocalURL(conn.URL) {
				fail(i, "watch requires a local url (absolute path or file://)")
			}
		case "":
			fail(i, "type is required (github or git)")
		default:
			fail(i, "unknown type %q (want github or git)", conn.Type)
		}
	}
	return errors.Join(errs...)
}

func (c *Config) applyDefaults() error {
	// ${ENV} expansion for secrets kept out of the file (see docs/MANUAL.md).
	// A non-empty api_key that expands to empty means an unset/misspelled env
	// var — fail closed rather than silently disabling API auth.
	if raw := c.Auth.APIKey; raw != "" {
		if c.Auth.APIKey = os.ExpandEnv(raw); c.Auth.APIKey == "" {
			return fmt.Errorf("auth.api_key %q expands to empty (unset environment variable?); refusing to start with auth disabled", raw)
		}
	}
	for i := range c.Connections {
		c.Connections[i].Token = os.ExpandEnv(c.Connections[i].Token)
	}
	if c.Server.Addr == "" {
		c.Server.Addr = ":3070"
	}
	if c.Server.DataDir == "" {
		c.Server.DataDir = "~/.phebs"
	}
	if c.Server.DataDir == "~" || len(c.Server.DataDir) > 1 && c.Server.DataDir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve data_dir: %w", err)
		}
		c.Server.DataDir = filepath.Join(home, c.Server.DataDir[1:])
	}
	abs, err := filepath.Abs(c.Server.DataDir)
	if err != nil {
		return fmt.Errorf("resolve data_dir: %w", err)
	}
	c.Server.DataDir = abs
	return nil
}
