//go:build !linux

package typedworkspace

import "context"

func ReadInstallationControl(context.Context, string, string, string, int64) ([]byte, error) {
	return nil, ErrCustody
}
