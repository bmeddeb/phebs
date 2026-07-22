# GATE2-V2 Stage-2 final enumeration authorization worksheet — fbd8474-01

**Status: DRAFT — NOT AUTHORIZATION. ENUMERATION REMAINS BLOCKED.**

This worksheet records the byte-exact candidate for the final enumeration
authorization. It is deliberately not stored at the executable
`stage2-enumeration-authorization.json` path, and no strict PLAN approval
anchor exists. The embedded payload's `AUTHORIZED` value is required by the
runtime schema; inside this non-executable worksheet it grants no authority.

## Candidate identity

- Authorization ID: `t111-gate2-v2-enum-fbd8474-01`
- Canonical candidate size: 5,622 bytes
- Canonical candidate SHA-256:
  `sha256:ab0c53454382a7e75026d047f357767ce60c7f56d088473e8bd9163ad9627f2d`
- Schema: `t111-gate2-v2-stage2-enumeration-authorization-v1`
- Serializer:
  `json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n"`

The object has exactly the 33 fields in the accepted enumerator's
`AUTHORIZATION_FIELDS` predicate, with no missing or extra keys.

## Evidence and chronology binding

The candidate directly binds:

- P0-03 authorization
  `spike/t111/labeling/gate2-v2/stage2-prebuild-authorization.json`,
  `sha256:01814b900086620f0d3091c36c61d49b87a2198f693a848a96a302f26106a2b4`,
  at promotion commit
  `cba24190c646d1c09345971ae5518eb0d5abc2bb`.
- Sanitized evidence declaration
  `spike/t111/labeling/gate2-v2/stage2-prebuild-evidence.json`,
  `sha256:022ca2c7540655b909994a71ee77ac7e84cc6a4ca9f0853a216cb58896ddd0a7`.
- Machine-verdict evidence review
  `spike/t111/labeling/gate2-v2/stage2-prebuild-evidence-review-r1.md`,
  `sha256:1696187097558ddb7f31278a3dc711e1426f265e26b563d3704db141ef55f58b`,
  at accepted evidence commit
  `fbd84744edb6791ac2e6af1c47e1ef6e009767cf`.

The required strict ancestry is
`d1420272acaf01063cdfce486bbe2b047d41e214` →
`cba24190c646d1c09345971ae5518eb0d5abc2bb` →
`25eeae34c2abb2612466fecac08e16f36e2d01a7` →
`fbd84744edb6791ac2e6af1c47e1ef6e009767cf`.

Through the sealed declaration, the candidate transitively binds terminal
receipt
`sha256:360610e3c5ab738905690d4cf695035e211f112ecea978377c36e25c9d55e8a7`,
evidence receipt
`sha256:52c3470a8fcb83b26bb1c8a711517ce1bc58c5523a0cdcba25767d43137dcaba`,
the derived root, both run IDs, run receipts
`sha256:ffe162bab9b3209bc82716b74aadec8e369dacab267b7cee3fe2b4004bea9f97`
and
`sha256:c789b82da4e6eecc35ee320f4bf25bce90c6c53a8402ccda9239cdc461c43f6a`,
and the four byte-identical fact digests reproduced in the candidate payload.

## Accepted implementation trust closure

- Enumerator:
  `spike/t111/labeling/gate2-v2/stage2_enumerate.py`,
  `sha256:926190eee4e0b6d30d97b468fa625ea405ea98b11a7d4f8f20ea2f5cb19c1a91`.
- r7 machine review:
  `spike/t111/labeling/gate2-v2/stage2-enumerate-review-r7.md`,
  `sha256:82eacc1c633a53096a5dbeccf7515b8b74b7029e4b82e4c2a989157110bbeb61`.
- Accepted implementation commit and executable binding:
  `d1420272acaf01063cdfce486bbe2b047d41e214`.

The existing r4/r7 PLAN acceptance anchors remain the implementation trust
closure. This draft adds no authorization anchor.

## Fresh enumeration-owned state

For candidate ID `t111-gate2-v2-enum-fbd8474-01`:

- Ceremony directory:
  `/Users/ben/.local/share/t111-gate2-v2-enum-fbd8474-01-ceremony`
- Sole authorized `--out`:
  `/Users/ben/.local/share/t111-gate2-v2-enum-fbd8474-01-ceremony/output`
- Consumption marker:
  `/Users/ben/.local/share/t111-gate2-v2-enum-fbd8474-01-ceremony/consumed.json`
- Terminal receipt:
  `/Users/ben/.local/share/t111-gate2-v2-enum-fbd8474-01-ceremony/terminal.json`

The output path is owned exclusively by the enumeration authorization:
fire-time `--out` must equal it byte-for-byte. The namespace is textually
disjoint from estimator and P0 state. This drafting step does not access or
create it. Before fire, the ceremony directory must be established as a real
owner-only directory while the marker, terminal, output, and output-staging
targets remain absent; the accepted verifier enforces those conditions
fail-closed.

## Canonical candidate payload

```json
{"authorization_id":"t111-gate2-v2-enum-fbd8474-01","base_lock_sha256":"sha256:a0fe717d168dc1a857720dd9bfb5957e50dd9e6944470f7d17ec4671b550027b","binding":{"commit":"d1420272acaf01063cdfce486bbe2b047d41e214","status":"executable"},"cache_tree_sha256":"sha256:147b46b766f26214f1d03e9a2f7087c7eb7060132324f6b1e3fa2885bfb4e84f","derived_harness_manifest_sha256":"sha256:1de0135a57c7ba5353b808a80a3393e821dfc67e3b4f2e8e85eac2aeb8222fbe","derived_lock_sha256":"sha256:d02cd5ef2baff3101fd72ac02eb57c14fee91593d1ca80c772584153eed9540b","enumerator_sha256":"sha256:926190eee4e0b6d30d97b468fa625ea405ea98b11a7d4f8f20ea2f5cb19c1a91","environment":{"network":"disabled","variables":{"GIT_ASKPASS":"/usr/bin/false","GIT_CONFIG_COUNT":"3","GIT_CONFIG_GLOBAL":"/dev/null","GIT_CONFIG_KEY_0":"core.hooksPath","GIT_CONFIG_KEY_1":"core.fsmonitor","GIT_CONFIG_KEY_2":"core.useBuiltinFSMonitor","GIT_CONFIG_NOSYSTEM":"1","GIT_CONFIG_VALUE_0":"/dev/null","GIT_CONFIG_VALUE_1":"false","GIT_CONFIG_VALUE_2":"false","GIT_NO_LAZY_FETCH":"1","GIT_NO_REPLACE_OBJECTS":"1","GIT_OPTIONAL_LOCKS":"0","GIT_TERMINAL_PROMPT":"0","GOENV":"off","GONOSUMDB":"*","GOPRIVATE":"*","GOPROXY":"off","GOSUMDB":"off","GOTELEMETRY":"off","GOTOOLCHAIN":"local","GOWORK":"off","LANG":"C","LC_ALL":"C","PATH":"/usr/bin:/opt/homebrew/Cellar/go/1.26.5/libexec/bin:/bin","PYTHONDONTWRITEBYTECODE":"1","PYTHONHASHSEED":"0","TZ":"UTC"}},"fact_runs":{"run1":{"facts_sha256":{"dapr":"sha256:651cb712b4d3b00ef0fb694e20985aead6cb0c53cdee9555f5f8fe3e46bb0089","loki":"sha256:d6a3a30f5748a20eebe937840573f1edc34276245e2fc4fa82d66a701b01c82d","online-boutique":"sha256:aeb5f9538b639793831c0282b977a247427a327eae70971423d9d2eba7915034","temporal":"sha256:85a3816a189fdb6e6b175a4f011e7933d08f029f179266454028edd12f174d4f"},"receipt_sha256":"sha256:ffe162bab9b3209bc82716b74aadec8e369dacab267b7cee3fe2b4004bea9f97","run_id":"t111-gate2-v2-p0-d142027-03-run1"},"run2":{"facts_sha256":{"dapr":"sha256:651cb712b4d3b00ef0fb694e20985aead6cb0c53cdee9555f5f8fe3e46bb0089","loki":"sha256:d6a3a30f5748a20eebe937840573f1edc34276245e2fc4fa82d66a701b01c82d","online-boutique":"sha256:aeb5f9538b639793831c0282b977a247427a327eae70971423d9d2eba7915034","temporal":"sha256:85a3816a189fdb6e6b175a4f011e7933d08f029f179266454028edd12f174d4f"},"receipt_sha256":"sha256:c789b82da4e6eecc35ee320f4bf25bce90c6c53a8402ccda9239cdc461c43f6a","run_id":"t111-gate2-v2-p0-d142027-03-run2"}},"git_executable":"/usr/bin/git","git_sha256":"sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818","go_executable":"/opt/homebrew/Cellar/go/1.26.5/libexec/bin/go","go_sha256":"sha256:3f947495f00cb7f8088a5cfd694da8dc43869b33f5e7377b048fb18922ffb7e0","heads":{"dapr":"f4d431123309a2bd11fcc32523661b6b14e8462b","loki":"562a762ab1d07985edc561920d74e792f4a6aab9","online-boutique":"9a4616e77f0f9cbcbecaf27d711c38890dda1404","temporal":"f95c865cc08c1ac075a709d525977e17103e6417"},"p0_authorization":{"authorization_commit":"cba24190c646d1c09345971ae5518eb0d5abc2bb","path":"spike/t111/labeling/gate2-v2/stage2-prebuild-authorization.json","sha256":"sha256:01814b900086620f0d3091c36c61d49b87a2198f693a848a96a302f26106a2b4"},"prebuild_evidence":{"accepted_commit":"fbd84744edb6791ac2e6af1c47e1ef6e009767cf","path":"spike/t111/labeling/gate2-v2/stage2-prebuild-evidence.json","review":{"path":"spike/t111/labeling/gate2-v2/stage2-prebuild-evidence-review-r1.md","sha256":"sha256:1696187097558ddb7f31278a3dc711e1426f265e26b563d3704db141ef55f58b"},"sha256":"sha256:022ca2c7540655b909994a71ee77ac7e84cc6a4ca9f0853a216cb58896ddd0a7"},"prior_authorizations":{"estimator":{"path":"spike/t111/labeling/estimator-authorization.json","sha256":"sha256:f39c617471d4e8e0b92dafa41e3281f67e5b27dffd2188cc9b4c4420089d233d"}},"producer_toolchain_identity":"go_version=\"go version go1.26.5 darwin/arm64\";go_digest=sha256:3f947495f00cb7f8088a5cfd694da8dc43869b33f5e7377b048fb18922ffb7e0;git_version=\"git version 2.50.1 (Apple Git-155)\";git_digest=sha256:179301dcb41ea78accc3fa0048a7e6f6710d891945a751a34addd622020c1818","python_executable":"/Library/Developer/CommandLineTools/Library/Frameworks/Python3.framework/Versions/3.9/bin/python3.9","python_mode":"isolated-no-site","python_sha256":"sha256:bdea59019a38eb6600cc9e71e984a97fedadc406448431281e7657030f54987e","python_version":"3.9.6","receipt_sha256":"sha256:bbea9b7cae0189ed0a94ea58657c1ac229be245be653196711c2e2f73d8040ef","response_sha256":"sha256:85cb9c6f0589afc6c00468e13eb20e82d45a3430135e0a9ea0fcb334453aa20e","review":{"accepted_commit":"d1420272acaf01063cdfce486bbe2b047d41e214","record_sha256":"sha256:82eacc1c633a53096a5dbeccf7515b8b74b7029e4b82e4c2a989157110bbeb61","status":"accepted"},"schema":"t111-gate2-v2-stage2-enumeration-authorization-v1","stage0_harness_manifest_sha256":"sha256:3e30ea0069b5773af8154ebca8e5576dcc0e55094b808d1c72c97316a169c1b3","stage0_inventory_sha256":"sha256:a5d8e5635f57585b60ad9692dd41334d19661a8ca068f20a31ecad022327441e","stage1_snapshot_sha256":"sha256:487dcc78f33ba4e08626b35d9500e78eb66276d48b984393f36bccd6636779a1","state":{"ceremony_directory":"/Users/ben/.local/share/t111-gate2-v2-enum-fbd8474-01-ceremony","consumption_marker":"/Users/ben/.local/share/t111-gate2-v2-enum-fbd8474-01-ceremony/consumed.json","output_dir":"/Users/ben/.local/share/t111-gate2-v2-enum-fbd8474-01-ceremony/output","terminal_receipt":"/Users/ben/.local/share/t111-gate2-v2-enum-fbd8474-01-ceremony/terminal.json"},"status":"AUTHORIZED","t111_binary_sha256":"sha256:7d6db7fca68981e05758ca41b0b0ae109d935ace71ef228d52b5184928e93f65","typedcalloracle_binary_sha256":"sha256:43f4a0328ac5f5cd8e62236163552871c514d7a60eb1f161db9016575b46c9cc"}
```

## Promotion sequence

1. Independently review this worksheet and its embedded canonical bytes.
2. If accepted, publish those exact bytes at
   `spike/t111/labeling/gate2-v2/stage2-enumeration-authorization.json`
   and add the sole strict PLAN row
   `GATE2-V2 Stage-2 enumeration authorization | AUTHORIZATION: APPROVED`
   binding the ID and canonical digest.
3. Obtain separate operator fire-time approval before a single launch.

Until those steps are complete, enumeration, preparation, selection, and
coordinate disclosure remain blocked. This worksheet is not a launch request,
does not access ceremony or derived state, and authorizes no execution.
