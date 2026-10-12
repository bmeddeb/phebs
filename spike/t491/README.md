# T49.1b independent protobuf declaration universe

`enumerate_declarations.py` is a standalone lexer and recursive-descent
reader. It was written independently of phebs's protocompile-based
`protodecl` reader and consults no phebs code or output. It enumerates the
frozen surface of
[`PROTOBUF_DECLARATION_IDENTITY.md`](../../docs/PROTOBUF_DECLARATION_IDENTITY.md)
§5 over the T47.2 frozen corpus at its admitted derived commits:

- services;
- rpc operations;
- messages, including proto2 group messages;
- fields, including oneof, map and group fields;
- extensions.

Each declaration carries its §5 object identity and its whole-statement byte
span. Enums, enum values, reserved ranges, extension ranges and options are
counted as out of scope, never as universe members. The strata are
repository × role × predicate. A `vendor` path segment makes a declaration
vendor; otherwise a `tests` or `testdata` segment makes it test; everything
else is production. Any file the reader cannot parse is recorded, never
skipped.

```bash
python3 spike/t491/enumerate_declarations.py --corpus <frozen corpus clones> \
  --lock spike/t472/corpus.lock.json --out-dir spike/t491/universe
```

The committed [`universe/universe.json`](universe/universe.json) and its
members `universe/declarations.jsonl.gz` record 6,081 declarations from all
131 `.proto` blobs, with zero parse failures and zero duplicate object keys.
52 enums, 4 reserved ranges and 1 extension range are out of scope.
No phebs prediction was read or compared.

## Slice 2: oracle references, candidate export, comparison and seal tooling

`enumerate_declarations.py` also writes `universe/references.jsonl.gz`: 5,197
type references resolved by the oracle rule in
[DECLARATION_QUALITY_PROTOCOL.md](../../docs/DECLARATION_QUALITY_PROTOCOL.md)
§3. The declaration members are byte-identical to slice 1.
`export_candidate.py` exports run g's `proto-contract` partitioned-root facts
into private custody, reading the store twice and requiring equality.
`oracle_compare.py` implements §5. `seal_round.py` provides the
`seal`, `seed`, `draw` and `sheet` commands.

The ceremony, which Ben must perform, has three steps:

1. Seal:

   ```bash
   python3 spike/t491/seal_round.py seal --candidate-export /home/ben/phebs-rehearsals/t491-candidate-20261012/candidate-export.json --activation-at <UTC at least 15 minutes ahead> --out spike/t491/seal.json
   ```

2. Publish the gist before the activation time:

   ```bash
   gh gist create spike/t491/seal.json
   ```

3. After the first pulse past the activation minute, derive the seed, draw
   the sample and build the review sheet:

   ```bash
   python3 spike/t491/seal_round.py seed --document spike/t491/seal.json --gist-api-url https://api.github.com/gists/<id> --out spike/t491/seed.json
   ```

   ```bash
   python3 spike/t491/seal_round.py draw --document spike/t491/seal.json --seed-record spike/t491/seed.json --out spike/t491/sample.json
   ```

   ```bash
   python3 spike/t491/seal_round.py sheet --sample spike/t491/sample.json --corpus /home/ben/phebs-rehearsals/t472-candidate-run-20261011g/corpus --out-dir /home/ben/phebs-rehearsals/t491-oracle-sheet
   ```

No comparison has been run.
