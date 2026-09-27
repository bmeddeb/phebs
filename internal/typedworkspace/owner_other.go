//go:build !linux

package typedworkspace

import (
	"context"
	"os"
)

func ownerReplace(*os.File, string, string) error { return ErrCustody }

func ownerBaseLease(context.Context, *os.File) (func(), error) { return nil, ErrCustody }
