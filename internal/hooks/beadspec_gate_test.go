package hooks

import (
	"strings"
	"testing"
)

// TestPrePushIncludesBeadspecGate asserts the rendered pre-push hook
// invokes `bk guard beadspec` and blocks on its exit code 2 (bkg-4zi.6).
func TestPrePushIncludesBeadspecGate(t *testing.T) {
	body := RenderPrePush(false)
	if !strings.Contains(body, "$BK guard beadspec") {
		t.Errorf("pre-push hook must call `bk guard beadspec`:\n%s", body)
	}
	if !strings.Contains(body, `-eq 2`) {
		t.Errorf("pre-push hook must block on guard beadspec exit code 2")
	}
	// The gate must precede the (warn-only) doctor/pr-beads block so a
	// schema violation hard-blocks regardless of BEADKEEPER_BLOCK_ON_RED.
	gateIdx := strings.Index(body, "$BK guard beadspec")
	warnIdx := strings.Index(body, `if [ "$rc" -ne 0 ]`)
	if gateIdx == -1 || warnIdx == -1 || gateIdx > warnIdx {
		t.Errorf("beadspec gate should appear before the warn-only block (gate=%d warn=%d)", gateIdx, warnIdx)
	}
}
