package migration

const searchV2Operation = "/demo.search.v1.CodeSearch/SearchV2"

type ExactClient interface {
	Invoke(operation string, request any) (any, error)
}

func SearchReplacement(client ExactClient, request any) (any, error) {
	return client.Invoke(searchV2Operation, request)
}
