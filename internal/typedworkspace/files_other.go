//go:build !linux

package typedworkspace

import (
	"github.com/bmeddeb/phebs/internal/typedindex"
	"os"
)

type metadata struct {
	device, inode uint64
}

func openDirectory(string, bool) (*os.File, error)                     { return nil, ErrCustody }
func openRelative(*os.File, string, bool) (*os.File, error)            { return nil, ErrCustody }
func mkdir(*os.File, string) error                                     { return ErrCustody }
func createFile(*os.File, string) (*os.File, error)                    { return nil, ErrCustody }
func fileInfo(*os.File, typedindex.BundleFile, bool) (metadata, error) { return metadata{}, ErrCustody }
func directoryInfo(*os.File, string, bool) (Node, error)               { return Node{}, ErrCustody }
func capacity(*os.File) (space, error)                                 { return space{}, ErrCustody }
func renameExclusive(*os.File, string, string) error                   { return ErrCustody }
