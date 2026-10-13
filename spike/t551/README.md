# T55.1 Kafka producer site-coverage round

Protocol: [`docs/KAFKA_PRODUCER_QUALITY_PROTOCOL.md`](../../docs/KAFKA_PRODUCER_QUALITY_PROTOCOL.md).

```bash
H=/home/ben/phebs-rehearsals/t551-history-20261013   # bare full-history clones, one per lock dir
python3 spike/t551/enumerate_population.py --history $H --out-dir spike/t551/population
go build -tags t551oracle -o /tmp/t551oracle ./spike/t551/oracle && /tmp/t551oracle -units spike/t551/population/units.jsonl.gz -history $H > sites.jsonl
go build -tags t551export -o /tmp/t551export ./spike/t551/export && /tmp/t551export -units spike/t551/population/units.jsonl.gz -history $H > candidate.jsonl
python3 spike/t551/score.py --sites sites.jsonl --candidate candidate.jsonl [--adjudications adjudications.json]
```

Tests: `go test -tags t551oracle ./spike/t551/oracle` and `cd spike/t551 && python3 -m unittest test_score`.
