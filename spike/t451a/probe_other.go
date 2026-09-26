//go:build !linux

package t451a

import "errors"

func Probe(string) error { return errors.New("neutral probes require the isolated Linux worker") }
