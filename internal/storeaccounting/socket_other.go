//go:build !darwin && !linux

package storeaccounting

import "os"

func protectInheritance(*os.File) error { return ErrConfig }
