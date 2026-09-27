//go:build !linux

package typedworkspace

import "context"

func drainOwner(context.Context, string, DrainAuthority, func(string) error) (DrainReport, error) {
	return DrainReport{Held: true}, ErrCustody
}

func inspectDrainOwner(context.Context, string, Node, DrainAuthority) (DrainInspection, error) {
	return DrainInspection{}, ErrCustody
}

func inspectDrainNamespace(context.Context, string, string, Node) ([]OwnerCensusEntry, bool, error) {
	return nil, false, ErrCustody
}
