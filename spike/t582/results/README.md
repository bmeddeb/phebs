# T58.2 finite Thrift observation result — NO-GO

The unchanged `thrift-consumer` 1.2.0 reader fails the independently reviewed
finite source contract. All 125 labelled sites and every one of 22 raw assertions
were scored: 11 supported candidates matched, six unsupported/unrelated sites
were promoted, and the registration ambiguity supplied a count without the
required source-distinct candidate identities. The method ambiguity emitted
both candidate edges; the two required file gaps were correct. No generated
body, unmatched or duplicate emission was hidden or excluded.

The public mismatch is `testutils/mock_agent.go:175`, the unproved local
`samplingManager.GetSamplingStrategy` name. The five authored positive
promotions cover an ordinary receiver, local constructor, foreign constructor,
shadowed package alias and handler receiver. They remain failures; neither the
oracle nor reader was changed to fit output. Processor construction is not
runtime service registration. Hints do not establish exact receiver, IDL,
wire, cross-module or runtime identity.

`first-reader/` retains the exact two first reader streams, score, build/VCS
metadata and custody receipt. Source commit is
`3feaf78e91635b9a811b9e0e201ed979092c1d42`, reviewed contract
`778d4cb3bca185a9327782a8d241f878da56c36e0439e4988674b0248f981e54`.
The two streams are byte-identical: 126,527 bytes each, SHA-256
`e14e23779da5c4e21d0a1997fe9c77f0e4245c5d531f8e7e89033eff2e0e88ae`.

`admission-stop-worktree-vcs/` preserves the original first source-admission
stop: offline build succeeded, but the pinned Go tool did not recognize the
worktree's `.git` file, so actual binary metadata lacked VCS fields. No reader
or scorer ran in that attempt. Root's existing clean-clone solution used the
**same** reviewed commit and exact input/implementation bytes, with a real
`.git` directory; actual metadata then recorded that revision and
`vcs.modified=false`. This was a source-admission correction before any reader
outcome. The already-passed Go controls and six Python controls were not
repeated. No source, label, recognizer or bound was changed, and no quality
retry occurred.

The admitted offline build took 0.570s; reader processes took 0.391s and 0.157s.
Sampled direct-reader RSS peaked at 80,379,904 bytes; the direct Python runner's
lifetime high-water value was 101,285,888 bytes. These are observed process
values, not hard heap or compiler-family bounds. CPU is unmeasured. All
supervised controls/build/metadata/readers joined, their owned groups were
absent, and scratch was removed after that proof. Six read-only Git metadata
direct-child waits have the separate qualification retained in the receipts.
No disposable binary, scratch or source clone is retained here.

This is one historical purposive public sample plus authored controls, with
source-derived independently reviewed labels and disclosed prior reader
exposure. It establishes no blind/general accuracy, scale, SLO, absence,
exact identity or runtime claim. No recipe registration, release, signature,
activation or adoption follows; T58.3 is not satisfied by this NO-GO.
`outcome-cost.json` records scopes, costs and limitations. No further validation
round is performed under this contract.
