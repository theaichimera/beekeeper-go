package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeIdentity(t *testing.T, repo string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".beadkeeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, IdentityRelPath), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadIdentityConfigPresent(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentity(t, repo, `
[identity]
canonical = ["alice", "bob"]

[identity.aliases]
"alice@example.com" = "alice"
"robert"            = "bob"
`)
	cfg, err := LoadIdentityConfig(repo)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil")
	}
	if len(cfg.Canonical) != 2 {
		t.Fatalf("canonical=%v", cfg.Canonical)
	}
	if _, ok := cfg.Canonical["alice"]; !ok {
		t.Fatal("alice missing from canonical")
	}
	if cfg.Aliases["alice@example.com"] != "alice" {
		t.Fatalf("alias map wrong: %v", cfg.Aliases)
	}
	if got := cfg.Map("alice@example.com"); got != "alice" {
		t.Fatalf("Map alias -> %q want alice", got)
	}
	if got := cfg.Map("alice"); got != "alice" {
		t.Fatalf("Map canonical -> %q want alice", got)
	}
	if got := cfg.Map("carol"); got != "" {
		t.Fatalf("Map unknown -> %q want empty", got)
	}
}

func TestLoadIdentityConfigMissing(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	cfg, err := LoadIdentityConfig(repo)
	if err != nil || cfg != nil {
		t.Fatalf("missing file should yield (nil, nil); got cfg=%v err=%v", cfg, err)
	}
}

func TestLoadIdentityConfigCanonicalAsString(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeIdentity(t, repo, `
[identity]
canonical = "alice"

[identity.aliases]
"a@x" = "alice"
`)
	cfg, err := LoadIdentityConfig(repo)
	if err != nil || cfg == nil {
		t.Fatalf("cfg=%v err=%v", cfg, err)
	}
	if _, ok := cfg.Canonical["alice"]; !ok {
		t.Fatal("string-form canonical not honored")
	}
}

func TestParseBdConfigValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw, want string
	}{
		{"beads-sync\n", "beads-sync"},
		{"  beads-sync  ", "beads-sync"},
		{"sync.branch=beads-sync\n", "beads-sync"},
		{"sync.branch = beads-sync\n", "beads-sync"},
		{"sync.branch (not set in config.yaml)\n", ""},
		{"(NOT SET)", ""},
		{"", ""},
		{"branch with space", ""}, // multi-token => not a valid branch
		{"\t\n  ", ""},
	}
	for _, c := range cases {
		if got := ParseBdConfigValue(c.raw); got != c.want {
			t.Errorf("ParseBdConfigValue(%q)=%q want %q", c.raw, got, c.want)
		}
	}
}
