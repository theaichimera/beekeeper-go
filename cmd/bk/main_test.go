package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandPrintsVersion(t *testing.T) {
	t.Parallel()

	// Pin a known version for the duration of this test.
	orig := Version
	Version = "v0.0.0-test"
	t.Cleanup(func() { Version = orig })

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	got := strings.TrimSpace(out.String())
	want := "bk v0.0.0-test"
	if got != want {
		t.Fatalf("unexpected output\n got: %q\nwant: %q", got, want)
	}
}

func TestRootCommandHasVersionSubcommand(t *testing.T) {
	t.Parallel()
	root := newRootCmd()
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "version" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected root to register 'version' subcommand")
	}
}
