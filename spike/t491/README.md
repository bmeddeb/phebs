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
