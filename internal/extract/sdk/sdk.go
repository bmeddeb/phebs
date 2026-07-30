// Package sdk is the capability-neutral API exposed to evidence extractors.
//
// It intentionally imports only context. In particular it does not expose the
// extraction worker, Git harness, filesystem paths, network clients, process
// execution, or the persistence layer. The architecture test in the parent
// package keeps both this package and extractor implementations on explicit
// import allowlists.
package sdk

import "context"

// Corpus is the only input capability given to an extractor. Implementations
// enumerate regular files at one immutable repository commit and return
// individually bounded blobs. Extraction is deliberately streaming: after a
// successful Read, the extractor must emit every fact citing that blob before
// it reads another file. This lets the trusted worker validate exact spans
// while retaining at most one source blob at a time.
type Corpus interface {
	RepoName() string
	Commit() string
	WalkFiles(ctx context.Context, visit func(path string) error) error
	Read(ctx context.Context, path string) (Blob, error)
}

// SCIPCorpus is the optional, narrowly bounded capability for reading the
// manifest-selected SCIP blob. SCIP indexes have an independent, larger byte
// limit than source files; keeping this separate prevents an extractor from
// using that allowance for arbitrary repository content. Implementations must
// apply the same immutable-commit and digest guarantees as Corpus.Read.
// The index is parser input only: facts must continue to cite a subsequent
// source Corpus.Read under the one-blob streaming contract.
type SCIPCorpus interface {
	ReadSCIPIndex(ctx context.Context) (SCIPInput, error)
}

// SCIPDocumentScope is the trusted path-admission capability paired with a
// scoped SCIP input. Built-in readers call it for every parsed document before
// retaining occurrences. Whole-repository legacy corpora may omit it.
type SCIPDocumentScope interface {
	SCIPDocumentInScope(path string) bool
}

// SCIPDocumentFilter is the optional focused-evidence lane filter paired with
// SCIPDocumentScope. Built-in SCIP readers first validate that every canonical
// document is in scope and globally safety-account its occurrences, then call
// this method before retaining any document semantics. Whole-repository
// corpora may omit it; their shipped behavior includes every in-scope
// document.
type SCIPDocumentFilter interface {
	SCIPDocumentIncluded(path string) bool
}

// SCIPInput preserves the real committed provenance of typed parser input.
// Present=false is a supported absence; Path may be empty when a configured
// unit deliberately has no typed-index designation.
type SCIPInput struct {
	Path string
	Blob
	Present bool
}

// Blob is one bounded, immutable file. Digest is SHA-256 over Content and is
// supplied by the trusted corpus harness so extractor output can cite it.
type Blob struct {
	Content string
	Digest  string
}

// AtomInput is the content-derived part of an evidence atom. Repository and
// commit placement are deliberately absent; the worker supplies those trusted
// provenance fields when it stages the fact.
type AtomInput struct {
	SchemaVersion       string
	BlobDigest          string
	StartByte           int
	EndByte             int
	RuleID              string
	AdapterConfigDigest string
	FactFingerprint     string
}

// AssertionInput is a semantic claim supported by the atom in the same Fact.
// Run ids, repository placement, and supporting ids are worker-owned.
type AssertionInput struct {
	Predicate string
	Subject   string
	Object    string
	Lineage   string
	Tier      string
	CodeRole  string
	Detail    string
}

// Fact is the streaming publication unit. One source span supports one claim;
// Emit returns only after the worker has accepted it into a bounded staged
// batch. This shape prevents an extractor from spoofing repository, commit,
// visibility, run, or supporting-atom provenance.
type Fact struct {
	Atom      AtomInput
	Assertion AssertionInput
	Path      string
	StartLine int
	EndLine   int
}

// FactChunk is the trusted worker transport for a bounded, ordered slice of
// validated facts. Extractors never construct chunks: they continue to stream
// one Fact at a time through Emit so the current source blob can be verified.
// The worker assigns the sequence and content-derived ID after validation and
// before staging the chunk under one invisible run.
type FactChunk struct {
	Schema   string
	Sequence uint64
	ID       string
	Facts    []Fact
}

// Coverage records successful scope and honest abstentions. Failures are
// retained for future extractor APIs, but the worker refuses to publish any
// result that reports one: an incomplete replacement set is never visible.
type Coverage struct {
	Protocols               []string
	Failures                []string
	UnresolvedCount         int
	ExcludedSCIPDocuments   int
	ExcludedSCIPDefinitions int
	ExcludedSCIPOccurrences int
}

// Emit streams one fact to the trusted worker harness. The fact must cite the
// most recent successful Corpus.Read; emit all of that blob's facts before the
// next read. Accepted facts enter a bounded, deterministic worker-owned chunk;
// Emit does not transfer chunk construction or publication authority to the
// extractor.
type Emit func(Fact) error

// Extractor is one evidence domain. Extract must be a pure function of the
// immutable Corpus and must return every Emit error to its caller.
type Extractor interface {
	Domain() string
	Version() string
	// Candidate declares whether a regular corpus path is in this extractor's
	// mandatory input population. The trusted worker inventories this predicate
	// before extraction and refuses publication unless every candidate was read.
	// The trusted Git walker may also evaluate it on symlink paths solely to
	// decide whether that alias requires same-commit validation; extractors still
	// enumerate and read only final regular paths.
	Candidate(path string) bool
	Extract(ctx context.Context, corpus Corpus, emit Emit) (Coverage, error)
}
