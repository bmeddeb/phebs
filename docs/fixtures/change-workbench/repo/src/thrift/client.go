package thrift

type CodeSearchClient struct{}

func (CodeSearchClient) Search(request any) (any, error) {
	return request, nil
}
