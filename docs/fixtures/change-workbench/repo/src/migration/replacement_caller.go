package migration

import searchv1 "example.invalid/workbench-closure/gen/proto/searchv1"

func SearchReplacement(
	ctx any,
	client searchv1.CodeSearchClient,
	request any,
) (any, error) {
	return client.SearchV2(ctx, request)
}
