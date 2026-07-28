package proto

const (
	searchOperation       = "/demo.search.v1.CodeSearch/Search"
	searchV2Operation     = "/demo.search.v1.CodeSearch/SearchV2"
	legacySearchOperation = "/demo.search.v1.CodeSearch/LegacySearch"
)

type Server struct{}

func (Server) Register() []string {
	return []string{searchOperation, searchV2Operation, legacySearchOperation}
}
