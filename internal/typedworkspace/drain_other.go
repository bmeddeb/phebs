//go:build !linux

package typedworkspace

import "context"

func drainOwner(context.Context, string, DrainAuthority, func(string) error) (DrainReport, error) {
	return DrainReport{Held: true}, ErrCustody
}
