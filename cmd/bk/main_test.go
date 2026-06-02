package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandPrintsVersion(t *testing.T) {
	// NOT t.Parallel: mutates package-level Version, which is now
	// read by newRootCmd's cobra struct literal (bkg-qp4). Racing
	// against any other parallel test that builds the root would
	// trigger -race; serialize this one.

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

// TestRootVersionFlag — bkg-qp4. Cobra wires `--version` / `-v` when
// the root command's Version field is set. This pins both spellings
// against future regressions (rejecting `--version` was the originally
// reported behavior).
//
// NOT t.Parallel: the test mutates the package-level `Version` var
// via Cleanup. Running the outer test in parallel with its subtests
// would let Cleanup restore Version before the subtests build their
// root command, causing them to read "dev" instead of the pinned tag.
func TestRootVersionFlag(t *testing.T) {
	orig := Version
	Version = "v0.0.0-test"
	t.Cleanup(func() { Version = orig })

	for _, flag := range []string{"--version", "-v"} {
		flag := flag
		t.Run(flag, func(t *testing.T) {
			root := newRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{flag})
			if err := root.Execute(); err != nil {
				t.Fatalf("%s failed: %v", flag, err)
			}
			s := out.String()
			if !strings.Contains(s, "v0.0.0-test") {
				t.Fatalf("%s output missing version: %q", flag, s)
			}
		})
	}
}

func TestRootCommandHasVersionSubcommand(t *testing.T) {
	// NOT t.Parallel: newRootCmd reads package-level Version (bkg-qp4),
	// so this races with TestVersionCommandPrintsVersion / TestRootVersionFlag
	// under -race. Subcommand-registration check is fast; serialize.
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
