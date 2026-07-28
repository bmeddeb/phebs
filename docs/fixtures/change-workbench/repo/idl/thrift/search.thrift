namespace go demo.search

struct SearchRequest {
  1: required string query
  2: optional list<string> repositories
}

struct SearchResponse {
  1: required list<string> paths
  2: optional bool truncated
}

service CodeSearch {
  SearchResponse search(1: SearchRequest request)
}
