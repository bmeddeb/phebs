package resolvermaterialize

import (
	"context"
	"errors"

	"github.com/bmeddeb/phebs/internal/gitobj"
)

type resolverBlobSession interface {
	ReadBlob(context.Context, string, int64) ([]byte, error)
	Close() error
}

type resolverBlobSessionOpener func(context.Context, string) (resolverBlobSession, error)

func openResolverBlobSession(ctx context.Context, directory string) (resolverBlobSession, error) {
	return gitobj.NewBatchBlobReader(ctx, directory)
}

// One Build owns this serial, exact-OID reader under the worker's existing
// repository lock and deadline. Empty builds and exact-current no-ops start no
// child. Every actual read still charges the source and successful-return sinks;
// this lease holds no object cache and never crosses a publication boundary.
type resolverBlobLease struct {
	ctx       context.Context
	directory string
	open      resolverBlobSessionOpener
	reader    resolverBlobSession
}

func (lease *resolverBlobLease) read(ctx context.Context, directory, oid string, expectedBytes int64) ([]byte, error) {
	if lease == nil || lease.ctx == nil || ctx == nil || lease.open == nil || directory != lease.directory {
		return nil, errors.New("resolver blob lease is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lease.reader == nil {
		reader, err := lease.open(lease.ctx, lease.directory)
		if err != nil {
			return nil, err
		}
		if reader == nil {
			return nil, errors.New("resolver blob session is unavailable")
		}
		lease.reader = reader
	}
	return lease.reader.ReadBlob(ctx, oid, expectedBytes)
}

func (lease *resolverBlobLease) close() error {
	if lease.reader == nil {
		return nil
	}
	return lease.reader.Close() // Includes the sole actual native Wait, even on cancellation.
}
