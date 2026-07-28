package checkout

type fakeClient struct{}

func (fakeClient) Search(_ any, request any) (any, error) {
	return request, nil
}

func (fakeClient) SearchV2(_ any, request any) (any, error) {
	return request, nil
}

func (fakeClient) LegacySearch(_ any, request any) (any, error) {
	return request, nil
}

func ExampleSearch() {
	_, _ = Search(nil, fakeClient{}, "query")
}
