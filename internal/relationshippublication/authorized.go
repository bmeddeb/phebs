package relationshippublication

import "context"

type AuthorizeRepository func(context.Context, string) (bool, error)

// OpenAuthorizedRoot resolves repository authorization before validating the
// repository spelling or touching pointer, root, count, index, or member bytes.
func OpenAuthorizedRoot(
	ctx context.Context,
	root, repository string,
	authorize AuthorizeRepository,
) (*Publication, Root, error) {
	if authorize == nil {
		return nil, Root{}, ErrDenied
	}
	allowed, err := authorize(ctx, repository)
	if err != nil {
		return nil, Root{}, err
	}
	if !allowed {
		return nil, Root{}, ErrDenied
	}
	publication, err := OpenCurrent(ctx, root, repository)
	if err != nil {
		return nil, Root{}, err
	}
	return publication, publication.Root(), nil
}

// OpenAuthorizedService preserves the same authorization-first boundary and
// returns only the selected service partition after a final pointer fence.
func OpenAuthorizedService(
	ctx context.Context,
	root, repository, serviceKey string,
	authorize AuthorizeRepository,
) (Root, ServiceReceipt, *ServiceMember, error) {
	publication, rootValue, err := OpenAuthorizedRoot(ctx, root, repository, authorize)
	if err != nil {
		return Root{}, ServiceReceipt{}, nil, err
	}
	receipt, member, err := publication.OpenService(ctx, serviceKey)
	if err != nil {
		return Root{}, receipt, nil, err
	}
	return rootValue, receipt, member, nil
}
