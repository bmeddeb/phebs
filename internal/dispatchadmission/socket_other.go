//go:build !darwin && !linux

package dispatchadmission

import "os"

func NewPipe() (parent, child *os.File, err error) { return nil, nil, ErrConfig }

func protectInheritance(_ *os.File) error { return ErrConfig }
