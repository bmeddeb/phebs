# T26.1 source-only label format

These labels are authored from licensed, pinned source bytes before any imported
parser or bounded grammar runs. Literal byte/line-span helpers may serialize and
check the author's choices. They must not discover SQL syntax or obtain labels
from a candidate parser. Source slices are half-open UTF-8 byte ranges.

Each `labels/*.json` has `format: "t261-source-labels-v1"`, `dialect`
(`postgresql` or `mysql`), `repository`, `commit`, `source_path`,
`source_sha256`, `plane`, and a `statements` array. Each statement has:

- `id`: source-local, occurrence-distinct stable label identifier.
- `start`, `end`, `sha256`: exact complete statement/envelope source slice.
- `syntax_class`: the author's explicit syntax classification.
- `expected_disposition`: `declaration`, `query`, `migration-event`, `wrapper`,
  `procedural-sql`, or `unsupported-statement`.
- `objects`: zero or more independently labelled declaration objects.
- `sites`: zero or more independently labelled authored-query table sites.
- `events`: zero or more independently labelled migration events, always with
  `objects: []`; each event has `predicate`, `name`, `quoted`, exact identifier
  token `start`/`end`/`sha256`, optional `relation`/ordered `columns`, and the
  statement records explicit Goose `direction` (`up` or `down`). Both branches
  are history occurrences, never applied or folded into current schema.
- `note`: optional explanation, including a wrapper or gap's disposition.

An object has `kind`, exact `name`, `quoted`, `start`, `end`, `sha256`, and may
have `relation`, ordered `columns`, `target_relation`, ordered `target_columns`,
opaque `type_spelling`, `source_role`, and `note`. Object kinds are `table`,
`column`, `primary_key`, `unique`, `check`, `foreign_key`, `index`, `view`,
`view_column`, `enum`, `sequence`, `exclusion`, and `range_type`. An explicit
schema qualifier uses separate optional `schema` and `schema_quoted` fields,
never a delimiter-split qualified-name string. Each named object's span is exactly its
declaration-name identifier token, including source quotes/backticks. An
anonymous constraint's span is exactly its defining `PRIMARY KEY`, `UNIQUE`,
`CHECK`, or `FOREIGN KEY` keyword phrase, and its identity is the source-local
`@<keyword-byte-offset>`. Statement envelopes separately retain complete SQL.
A table/view identity retains its explicit qualifier and exact name tokens;
no default search path is guessed. Named constraints and indexes retain their
names. A primary key is one object;
it does not also become an implicit index label. Likewise a unique constraint
is one `unique` label, and an independently written index is one `index` label.
All members and FK ends retain order; repeated FK pairs remain distinct.

A site has exact `name`, `quoted`, `start`, `end`, `sha256`, `access` (`read`,
`write`, or `read-write`), `expected_binding` (`unique`, `name-only`, `missing`,
or `ambiguous`), optional `declaration_relation`, and optional `note`. Each
authored relation-literal occurrence is one site. Aliases, CTE-local names,
derived-table aliases, column qualifiers, functions, and generated Go duplicates
are not additional physical table sites. They are retained in statement notes
where relevant to semantics or an explicit gap. `INSERT ... ON DUPLICATE KEY
UPDATE` has one read-write target site, not a second fabricated table occurrence.

`inventory_notes` may retain source-wide facts that are not declaration or
usage objects. Every non-comment top-level statement must have a disposition.
Dump SET/DROP wrappers do not become migration history. MySQL's temporary
stand-in view is a labelled wrapper; the final versioned-comment view and its
ten output columns are the single current declaration population. PostgreSQL
procedural bodies are one top-level procedural gap and are not searched for
current declarations. Tooling-only sqlc models remain explicitly marked.

The frozen declaration denominator is the sum of all labelled object kinds,
reported both by kind and source; a candidate's unsupported objects remain in
that denominator. Migration, table-site, and binding denominators are separate.
Generated-source negative controls and authored supplemental syntax controls
have separate populations and never inflate the public-corpus denominators.
The generated local public-fixture dump has `source_origin: generated-fixture`
and `commit: null` while authoring labels: its exact content hash/path is bound
externally by the evaluator to the lead's immutable preregistration Git
checkpoint, avoiding a self-referential future commit or false upstream blob
claim. Upstream corpus labels retain their actual pinned repository commits.
