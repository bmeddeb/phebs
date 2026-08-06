# T39.R1 — Mirror-lock contention diagnosis

T39.R1 reproduces the T39.2 stop mechanism at the production concurrency
boundary without reopening the private target or authorizing a rerun.
Aggregate extraction and caller-leaf execution correctly serialize on one
repository immutable-mirror lock. The caller worker, however, previously
started its five-minute execution deadline before waiting for that lock. An
admitted aggregate extraction could therefore spend the caller deadline while
the caller performed no work; three ordinary retries could exhaust solely on
the same wait.

The correction changes only the budget boundary. Pointer/control preflight
retains a bounded five-minute context. Mirror acquisition then uses the
runner's lease-heartbeated parent context. After acquisition, caller work gets
a fresh five-minute execution context. The repository lock, three-attempt
limit, admission rules, retry accounting, publication fences, and all policy
digests are unchanged. Parent cancellation and lease loss still interrupt the
wait, and a canceled waiter opens no caller plan and mutates no caller state.

The deterministic receipt binds the retained stopped T39.2 run and T39.5
no-release decision. It classifies bounded serialization as the required
change, records the executable gates, and explicitly denies target-rerun or
release authority. T39.2 remains stopped and nonsuperseded.

## Reproduction

```sh
t39r1_tmp=$(mktemp -d)
go run ./spike/t39r1/cmd/author -root . -out "$t39r1_tmp/results.json"
cmp "$t39r1_tmp/results.json" spike/t39r1/results.json
go test -race ./internal/callerexecute ./internal/store ./spike/t39r1
make docs-check
make verify-glossary
```

Any target rerun still requires a new explicit approval, nonce, frozen plan,
teardown deadline, and source-free receipt. No timeout or attempt limit was
raised, and this closure establishes no SLO, accuracy, release, migration, or
decommission-safety claim. `GATE2-V2` remains `NOT_ESTABLISHED`.
