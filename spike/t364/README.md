# T36.4 source-observation plane closure receipt

This retained model closes Epic 36 with one neutral Go content identity parsed
once and projected independently through the gRPC caller, Thrift caller, Kafka
producer, and Kafka consumer adapters. `results.json` contains counts and
digests only: no source text, path, object ID, tool error, repository identity,
or evidence sample.

The named gates in the receipt own the real publication, recovery, cache,
authorization, HTTP/MCP, backup, lifecycle, and adapter assertions. The model
does not substitute synthetic counts for those production-path tests.

Regenerate the canonical receipt for review with:

```sh
go run ./spike/t364/cmd/author
```

The receipt establishes bounded mechanics only. It makes no accuracy,
completeness, supported-scale, SLO, migration, decommission, extractor
promotion, or release claim; `GATE2-V2` remains `NOT_ESTABLISHED`.
