//go:build linux && phebs_t42_native && phebs_t42_checkpoint_native

package t421

import "testing"

// Separate source-selected full composition. This tag/test never substitutes
// for MED-495 or permits a changed driver image to reuse an old admission.
func TestLinuxEpochRequiredNativeCheckpointLiveBorrow(t *testing.T) {
	runLinuxEpochRequiredNativeLiveBorrow(t, true)
}
