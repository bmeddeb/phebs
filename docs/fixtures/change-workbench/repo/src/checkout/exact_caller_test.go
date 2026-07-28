package checkout

type fakeClient struct{}

func (fakeClient) Invoke(_ string, request any) (any, error) {
	return request, nil
}

func ExampleSearch() {
	_, _ = Search(fakeClient{}, "query")
}
