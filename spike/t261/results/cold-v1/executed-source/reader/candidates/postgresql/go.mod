module phebs-spike-t261-postgresql

go 1.26.0

require (
	github.com/pganalyze/pg_query_go/v6 v6.2.5
	phebs-spike-t261 v0.0.0
)

require google.golang.org/protobuf v1.33.0 // indirect

replace phebs-spike-t261 => ../..
