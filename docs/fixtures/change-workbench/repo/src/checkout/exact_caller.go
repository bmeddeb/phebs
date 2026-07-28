package checkout

const searchOperation = "/demo.search.v1.CodeSearch/Search"

type ExactClient interface {
	Invoke(operation string, request any) (any, error)
}

func Search(client ExactClient, request any) (any, error) {
	return client.Invoke(searchOperation, request)
}
