//go:build !darwin

package main

import (
	"context"
	"os"

	"github.com/bmeddeb/phebs/internal/recovery"
)

type t422ArchiveCustody struct{}

func openT422ArchiveCustody(context.Context, *os.File, string, os.FileInfo, [2]int32, t422ArchiveInput) (*t422ArchiveCustody, error) {
	return nil, errT422ArchiveControl
}

func (*t422ArchiveCustody) check(context.Context) error { return errT422ArchiveControl }
func (*t422ArchiveCustody) read(context.Context, t422ArchiveInput) (recovery.ArchiveTransitionManifest, error) {
	return recovery.ArchiveTransitionManifest{}, errT422ArchiveControl
}
func (*t422ArchiveCustody) close() error { return nil }
