package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ReadSyncBranchFromJSON reads `.beads/config.json` for the
// `sync.branch` key. Returns "" + nil when the file is missing or the
// key is absent — both are normal.
//
// Mirrors the JSON fallback path in project.read_git_state.
func ReadSyncBranchFromJSON(repo string) (string, error) {
	path := filepath.Join(repo, BeadsConfigJSONRelPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var raw rawBeadsConfigJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		// Malformed config.json is non-fatal; Python's
		// `try... except OSError, json.JSONDecodeError: pass`.
		return "", nil
	}
	return raw.Sync.Branch, nil
}
