package beadspec

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestLeafPackage enforces the one-way dependency contract: beadspec
// must not import anything from beekeeper-go/internal/* or
// beekeeper-go/cmd/*. This keeps the package extractable into its own
// repo by a folder move (see bkg-4zi).
func TestLeafPackage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no .go files found")
	}
	forbidden := []string{
		"github.com/theaichimera/beekeeper-go/internal",
		"github.com/theaichimera/beekeeper-go/cmd",
	}
	for _, f := range files {
		fset := token.NewFileSet()
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range af.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if strings.HasPrefix(p, bad) {
					t.Errorf("%s imports %q — beadspec must remain a leaf package", f, p)
				}
			}
		}
	}
}
