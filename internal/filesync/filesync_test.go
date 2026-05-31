package filesync

import (
	"os"
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestMatchInsideSyntheticRoot(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "FakeDropbox")
	if err := os.MkdirAll(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(fake, "projects", "my-app", ".beads", "x.db")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	m := MatchPath(inside, []string{fake}, Options{})
	if m == nil {
		t.Fatal("expected match, got nil")
	}
	fakeResolved, _ := filepath.EvalSymlinks(fake)
	if m.Root != fakeResolved {
		t.Fatalf("Root=%q want %q", m.Root, fakeResolved)
	}
}

func TestMatchOutsideReturnsNil(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "FakeDropbox")
	_ = os.MkdirAll(fake, 0o755)
	outside := filepath.Join(tmp, "elsewhere", "x.db")
	_ = os.MkdirAll(filepath.Dir(outside), 0o755)
	_ = os.WriteFile(outside, nil, 0o600)

	if m := MatchPath(outside, []string{fake}, Options{}); m != nil {
		t.Fatalf("expected nil, got %#v", m)
	}
}

func TestLongestRootWins(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	outer := filepath.Join(tmp, "outer")
	inner := filepath.Join(outer, "inner")
	_ = os.MkdirAll(inner, 0o755)
	target := filepath.Join(inner, "deep", "x.db")
	_ = os.MkdirAll(filepath.Dir(target), 0o755)
	_ = os.WriteFile(target, nil, 0o600)

	m := MatchPath(target, []string{outer, inner}, Options{})
	if m == nil {
		t.Fatal("expected match")
	}
	innerResolved, _ := filepath.EvalSymlinks(inner)
	if m.Root != innerResolved {
		t.Fatalf("Root=%q want %q (longest)", m.Root, innerResolved)
	}
}

func TestKnownRootsReadsEnv(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "FakeRoot")
	_ = os.MkdirAll(fake, 0o755)
	roots := KnownRoots(Options{
		Home: filepath.Join(tmp, "fake-home"),
		Env:  env(map[string]string{EnvVar: fake}),
	})
	resolved, _ := filepath.EvalSymlinks(fake)
	found := false
	for _, r := range roots {
		if r == resolved {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("KnownRoots did not include env-supplied root: %v", roots)
	}
}

func TestKnownRootsSkipsNonexistent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	nope := filepath.Join(tmp, "NeverExisted")
	roots := KnownRoots(Options{
		Home: filepath.Join(tmp, "fake-home"),
		Env:  env(map[string]string{EnvVar: nope}),
	})
	for _, r := range roots {
		if r == nope {
			t.Fatalf("KnownRoots included nonexistent root: %s", nope)
		}
	}
}

func TestLabelForWellKnownNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want string
	}{
		{"/Users/x/Dropbox", "Dropbox"},
		{"/Users/x/Dropbox (Personal)", "Dropbox"},
		{"/Users/x/Library/Mobile Documents", "iCloud Drive"},
		{"/Users/x/Library/CloudStorage", "macOS CloudStorage (OneDrive / Google Drive / Box / iCloud)"},
		{"/Users/x/OneDrive", "OneDrive"},
		{"/Users/x/Google Drive", "Google Drive"},
		{"/Users/x/GoogleDrive", "Google Drive"},
		{"/Users/x/google-drive", "Google Drive"},
		{"/Users/x/Insync", "Insync"},
		{"/Users/x/SomethingElse", "SomethingElse"},
	}
	for _, c := range cases {
		if got := LabelForRoot(c.path); got != c.want {
			t.Errorf("LabelForRoot(%q)=%q want %q", c.path, got, c.want)
		}
	}
}

func TestMatchUsesLabelForWellKnownDropbox(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	drop := filepath.Join(tmp, "Dropbox")
	_ = os.MkdirAll(drop, 0o755)
	target := filepath.Join(drop, "foo.db")
	_ = os.WriteFile(target, nil, 0o600)
	m := MatchPath(target, []string{drop}, Options{})
	if m == nil || m.Label != "Dropbox" {
		t.Fatalf("expected Dropbox label, got %#v", m)
	}
}

func TestKnownRootsDedups(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "FakeRoot")
	_ = os.MkdirAll(fake, 0o755)
	roots := KnownRoots(Options{
		Home:  filepath.Join(tmp, "fake-home"),
		Env:   env(map[string]string{EnvVar: fake + string(os.PathListSeparator) + fake}),
		Extra: []string{fake},
	})
	resolved, _ := filepath.EvalSymlinks(fake)
	n := 0
	for _, r := range roots {
		if r == resolved {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected 1 occurrence of %s, got %d", resolved, n)
	}
}
