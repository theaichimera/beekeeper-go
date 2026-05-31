// Package config reads beadkeeper-recognised config files:
//
//   - `.beadkeeper/identity.toml` — canonical-handle map used by lease
//     and identity. Schema mirrors Python identity.py.
//   - `.beads/config.json` — fallback for `sync.branch` when bd CLI
//     isn't available or returns the "(not set)" sentinel.
//   - bd config-get output ("sync.branch (not set in config.yaml)" etc.)
//     normalisation.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// IdentityRelPath is the project-relative path of the identity
	// config (Python: `.beadkeeper/identity.toml`).
	IdentityRelPath = ".beadkeeper/identity.toml"

	// BeadsConfigJSONRelPath is the project-relative path of bd's
	// JSON config (when present), used as a sync.branch fallback.
	BeadsConfigJSONRelPath = ".beads/config.json"
)

// IdentityConfig is the parsed `.beadkeeper/identity.toml`.
//
// Schema:
//
//	[identity]
//	canonical = ["alice", "bob"]
//
//	[identity.aliases]
//	"alice@example.com" = "alice"
//	"a.smith"           = "alice"
//
// `Canonical` is the set of canonical handles; `Aliases` maps an alias
// to a canonical handle. Both are case-sensitive (matches Python).
type IdentityConfig struct {
	Canonical map[string]struct{}
	Aliases   map[string]string
}

// Map returns the canonical form of `handle`, or "" if unknown.
// `handle` is returned unchanged if it IS already canonical.
func (c *IdentityConfig) Map(handle string) string {
	if c == nil {
		return ""
	}
	if _, ok := c.Canonical[handle]; ok {
		return handle
	}
	if target, ok := c.Aliases[handle]; ok {
		if _, canon := c.Canonical[target]; canon {
			return target
		}
	}
	return ""
}

// rawIdentityTOML matches the file on disk. The TOML library treats
// dotted keys like `[identity.aliases]` as a nested table, which we
// reconstruct here.
type rawIdentityTOML struct {
	Identity struct {
		Canonical interface{}            `toml:"canonical"`
		Aliases   map[string]interface{} `toml:"aliases"`
	} `toml:"identity"`
}

// LoadIdentityConfig reads `<repo>/.beadkeeper/identity.toml`. Returns
// (nil, nil) when the file is missing — identity is opt-in by design,
// so an absent config is not an error.
//
// On parse / IO errors the function returns (nil, err) so callers can
// distinguish "missing" from "broken".
func LoadIdentityConfig(repo string) (*IdentityConfig, error) {
	path := filepath.Join(repo, IdentityRelPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var raw rawIdentityTOML
	if _, err := toml.Decode(string(data), &raw); err != nil {
		// Malformed identity.toml is treated as "missing" by Python —
		// mirror that. The error is returned so callers may log.
		return &IdentityConfig{
			Canonical: map[string]struct{}{},
			Aliases:   map[string]string{},
		}, err
	}
	cfg := &IdentityConfig{
		Canonical: map[string]struct{}{},
		Aliases:   map[string]string{},
	}
	switch v := raw.Identity.Canonical.(type) {
	case string:
		cfg.Canonical[v] = struct{}{}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				cfg.Canonical[s] = struct{}{}
			}
		}
	}
	for k, v := range raw.Identity.Aliases {
		s, ok := v.(string)
		if !ok {
			continue
		}
		cfg.Aliases[k] = s
	}
	return cfg, nil
}

// --- bd config-get normalisation ----------------------------------------

// ParseBdConfigValue mirrors `_parse_bd_config_value` in project.py:
// turn raw `bd config get sync.branch` output into a value or "" for
// unset. Recognises:
//
//   - empty / whitespace-only -> ""
//   - "(not set" substring -> "" (bd's sentinel)
//   - "key=value" -> value (after the first '=')
//   - any value containing whitespace -> "" (not a valid branch name)
func ParseBdConfigValue(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(strings.ToLower(s), "(not set") {
		return ""
	}
	if i := strings.IndexByte(s, '='); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	if s == "" {
		return ""
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return ""
		}
	}
	return s
}

// --- bd config.json fallback --------------------------------------------

// rawBeadsConfigJSON mirrors the shape of .beads/config.json that bd
// emits in some versions (`{"sync": {"branch": "..."}}`).
type rawBeadsConfigJSON struct {
	Sync struct {
		Branch string `json:"branch"`
	} `json:"sync"`
}
