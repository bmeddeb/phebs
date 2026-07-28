package ambiguous

type SearchClient interface {
	Search(request any) (any, error)
}

// CallBySharedName deliberately lacks declaration lineage. Search exists in
// both protobuf and Thrift declarations, so this is reviewable name-only
// evidence and never a resolved caller.
func CallBySharedName(client SearchClient, request any) (any, error) {
	return client.Search(request)
}
