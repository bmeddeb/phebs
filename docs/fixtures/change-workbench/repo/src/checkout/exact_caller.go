package checkout

import searchv1 "example.invalid/workbench-closure/gen/proto/searchv1"

func Search(ctx any, client searchv1.CodeSearchClient, request any) (any, error) {
	return client.Search(ctx, request)
}
