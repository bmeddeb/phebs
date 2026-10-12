# T53.3 Buf WIRE compatibility round

Protocol and claim card: [`docs/WIRE_COMPATIBILITY_PROTOCOL.md`](../../docs/WIRE_COMPATIBILITY_PROTOCOL.md).

- `enumerate_changes.py`: the real-history change population (frozen in `population/`).
- `wire_oracle.py`: the independent truth (no Buf, no phebs code or output).
- `run_round.py`: `run` drives the real binary; `score` classifies against truth and adjudications.

The containerd and grpc-go corpus clones lack historical blobs, so the
population reads full-history bare clones. Rebuild them with:

```bash
mkdir -p /home/ben/phebs-rehearsals/t533-history-20261012 && cd /home/ben/phebs-rehearsals/t533-history-20261012
git clone --bare https://github.com/containerd/containerd.git containerd
git clone --bare https://github.com/grpc/grpc-go.git grpc-go
git clone --bare /home/ben/phebs-rehearsals/t472-candidate-run-20261011g/corpus/etcd etcd
for r in containerd grpc-go; do git -C $r fetch /home/ben/phebs-rehearsals/t472-candidate-run-20261011g/corpus/$r HEAD:refs/heads/t472-derived; done
```

Seal values (publish in a gist before `run`):

```bash
git rev-parse HEAD; python3 -c "import json;print(json.load(open('spike/t533/population/population.json'))['units_sha256'])"; sha256sum spike/t533/{enumerate_changes,wire_oracle,run_round}.py
```

After the seal (build `bin/phebs` and the helpers first, e.g. `make bin/buf bin/zoekt-git-index bin/phebs-focused-index && go build -o bin/phebs ./cmd/phebs`):

```bash
python3 spike/t533/run_round.py run --binary bin/phebs --helpers bin --history /home/ben/phebs-rehearsals/t533-history-20261012 --run-root /home/ben/phebs-rehearsals/t533-round-<stamp>
python3 spike/t533/run_round.py score --history /home/ben/phebs-rehearsals/t533-history-20261012 --results /home/ben/phebs-rehearsals/t533-round-<stamp>/results.jsonl.gz [--adjudications adjudications.json]
```

Tests: `cd spike/t533 && python3 -m unittest test_enumerate_changes test_wire_oracle test_run_round`.

## Round 2 (narrower claim, protocol §9)

```bash
B=/home/ben/phebs-rehearsals/t533r2-history-20261012; mkdir -p $B
git clone --bare https://github.com/vitessio/vitess.git $B/vitess
git clone --bare https://github.com/istio/istio.git $B/istio
python3 spike/t533/enumerate_changes.py --corpus $B --lock spike/t472/corpus.lock.json --pinned vitessio/vitess --pinned istio/istio --out-dir spike/t533/population-r2
python3 spike/t533/run_round.py run --binary bin/phebs --helpers bin --history $B --population spike/t533/population-r2 --run-root /home/ben/phebs-rehearsals/t533r2-round-<stamp>
python3 spike/t533/run_round.py score --history $B --population spike/t533/population-r2 --results <run-root>/results.jsonl.gz --adjudications <adjudications.json>
```
