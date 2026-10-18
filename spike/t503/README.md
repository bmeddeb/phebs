# T50.3 public-source comparison qualification checkpoint

This is a preregistration and source-label checkpoint. **No SCIP index,
Phebs prediction, comparison output or score has been produced.** Another
agent must review the exact frozen inputs and source-only labels before those
steps. This establishes no production release.

The population is four unchanged real clients from
[`grpc/grpc-go@5f1ccf56`](https://github.com/grpc/grpc-go/tree/5f1ccf56ea5ec964bbf447a10b561214f86387a6/examples),
their two committed declaration/generated-client packages, and the required
public data helper. All are Apache-2.0; the exact license is an input.
`population.lock.json` records every upstream path, URL, size, Git blob and
SHA-256. The four clients contain nine generated-method calls: five selected
endpoint occurrences and four outside-pair negatives.

The selected old endpoint is `/helloworld.Greeter/SayHello`; replacement is
`/grpc.examples.echo.Echo/UnaryEcho`. These are evaluator-selected roles. They
do not describe an upstream migration, API equivalence or replacement promise.
Three evaluator-authored package units and one explicit unavailable mapping
exercise supplied attribution. They are not claims about deployed services,
owners or runtime topology. Source-only labels and the prospective thresholds,
denominators, refusal rules and local operating bounds are in `labels.json`
and `preregistration.json`.

## Input provenance

The evaluator root uniformly removes `examples/`; original source bodies,
protobuf declarations, generated-client files and notices stay unchanged.
The upstream module's workspace replacements and newer dependencies cannot
describe this deliberately selected standalone subset. `inputs/go.mod` and
`inputs/go.sum` therefore name the evaluator's exact independent dependencies.
The original upstream module files remain separately bound as context.

Preparation resolved the public dependency graph with `go list -deps ./...`;
it did not invoke the indexer, a generator, an extractor or application code.
The initial offline dependency check refused unavailable cache entries and the
required helper before the module was frozen. No quality population was dropped
or selected from product output. Subsequent indexing runs offline against the
frozen module graph with the pinned Go/scip-go tools and environment.

`prepare.py` validates frozen bytes, source labels and the uniform path mapping,
then creates one new evaluation directory. It does not mutate Git or run any
prediction. It retains an explicit `origin.json`; no evaluator commit is
represented as the upstream commit.

```sh
python3 spike/t503/prepare.py --out /tmp/phebs-t503-evaluation
# Optional offline source cache retains original upstream paths:
python3 spike/t503/prepare.py --source-cache /path/to/cache --out /tmp/phebs-t503-evaluation
python3 -m unittest discover -s spike/t503 -p 'test_*.py'
```

## Independent checkpoint review

Read only the frozen population, public source bodies, input snapshots,
source-only labels and the preregistered product contract. Do not inspect
classifier/grouping implementation or any later candidate outputs when checking
these labels. Confirm all nine actual generated-method calls, their exact
selector byte/line ranges and receiver/declaration identity; five selected side
memberships; four outside-pair exclusions; three supplied package IDs; the
unavailable metadata-client unit; and both complete expected comparison sets.

The primary expected occurrence result has five rows: two old-only and three
new-only. At unit level there are four rows: one old-only, one new-only, one
both and one unresolved. Missing unit attribution does not erase a resolved
source call. Zero real denominators remain unavailable; absent real classes
receive only separately named mechanics regression coverage.

`checkpoint.json` binds the exact files for that review. Acceptance must identify
that checkpoint digest and the independent reviewer, or record discrepancies
before any prediction. A discrepancy changes the preregistration and requires
a fresh checkpoint; it is not adjudicated against product output.

Only after acceptance may the lead authorize a serialized indexer/native slot,
record the distinct actual evaluation commits/tree/bundle and repository-role
bindings, and run the frozen matrix. The exact Go caller/declaration recipe and
existing comparison service are reused; no classifier is added here.

## Qualification limits

The proposed operating measurement is a finite Darwin-local small-source read
result on the actual 24-GiB host, with the explicit bounds in the preregistration.
It is neither a Linux capacity result nor a rehearsal/historical workload rerun.
Any Linux-targeted claim remains pending its own host evidence. A second copy
of the corpus may exercise independent publication fences without increasing
the quality denominator. Every hard stop stays visible; no silent retry,
resampling, label repair, broad cohort or scale inference is permitted.

The common Epic 48 bar applies: Caller Map/Atlas's bounded-test amendment does
not waive this product's measured result. Ordinary signed startup later uses
disposable integration keys only. Production signing remains pending because
the existing key is unavailable. Source composition qualification, operating
observations, UI/HTTP/MCP authority, retained lifecycle gates and production
signature are distinct requirements.

Steady-state product work is unchanged. The preparation tool performs bounded
one-shot source reads/downloads and file writes. It introduces no production
request, worker, sync tick, lock, cache, schema, publication or child process.
