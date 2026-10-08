//go:build !darwin && !linux

package t4013

import "testing"

func TestPrivateProcessSessionMembershipUnsupported(t *testing.T) {
	for _, sessionID := range []int{-1, 0, 1, 2147483647} {
		if members, err := PrivateProcessSessionMembership(sessionID); err == nil || members != nil {
			t.Fatalf("unsupported native membership succeeded for %d: %v / %v", sessionID, members, err)
		}
	}
}
