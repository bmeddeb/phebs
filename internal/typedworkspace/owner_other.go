//go:build !linux

package typedworkspace

import "os"

func ownerReplace(*os.File, string, string) error { return ErrCustody }
