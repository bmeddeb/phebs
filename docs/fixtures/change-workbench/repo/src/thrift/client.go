package thrift

import searchthrift "example.invalid/workbench-closure/gen/thrift/search"

func Search(
	ctx any,
	client *searchthrift.CodeSearchClient,
	request any,
) (any, error) {
	return client.Search(ctx, request)
}

func Register(handler searchthrift.CodeSearch) *searchthrift.CodeSearchProcessor {
	return searchthrift.NewCodeSearchProcessor(handler)
}
