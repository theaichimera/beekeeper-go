package main

import (
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// defaultPaths returns args if non-empty, else ["."].
func defaultPaths(args []string) []string {
	if len(args) == 0 {
		return []string{"."}
	}
	return args
}

// isTTY reports whether w is the stdout of an interactive terminal.
// Mirrors Python's `_is_tty(sys.stdout)`. Returns false on test
// writers (bytes.Buffer) so color is suppressed in golden tests.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// silentExit is the project's exit-without-cobra-noise. cobra's
// default error path prints "Error: ..." which we don't want for
// non-zero "this is the doctor verdict" exits.
var silentExit = os.Exit

func upper(s string) string {
	return strings.ToUpper(s)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func intsCSV(xs []int) string {
	var b strings.Builder
	for i, x := range xs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(x))
	}
	return b.String()
}

// sortedKeys lifts a set into a stable []string.
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysString(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeysSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
