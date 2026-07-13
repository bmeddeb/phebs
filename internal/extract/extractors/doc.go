// Package extractors holds evidence-extractor implementations (T12.3+).
//
// The pure-reader invariant (PLAN ADR 2026-07-12) is structural here: code in
// this subtree receives only the capability-neutral extractor SDK. It never
// executes corpus-provided code, generators, plugins, build scripts, or
// binaries; never dynamically loads corpus artifacts; never writes into the
// corpus; and has no network access while parsing. TestPureReaderImports in
// the parent package enforces an explicit production-import and artifact
// allowlist recursively below this directory, including helper packages.
package extractors
