# MED-505 amd64 material producers (2026-11-24)

The 2026-10-27 offline material package (`med505-offline-materials-v3.tar`) existed only on
private hosts and is gone. These producers rebuild it reproducibly. They ran inside a
dedicated Ubuntu 24.04 KVM guest on `dave` with Docker 29.1.3 (LXD VM `med505`). The
2026-11-08 rule applies: the tagged native test is the acceptance.

## Reproduced byte-identical (existing pins unchanged)

| Material | Pin | Recipe |
|---|---|---|
| Go 1.25.7 SDK archive | `12e6d6a1…` (`go/bin/go` `76ac600b…`) | go.dev download |
| Go 1.25.0 SDK archive | `2852af0c…` | go.dev download (tool builds) |
| Bazel 9.0.0 | `c44a93f2…` | GitHub release asset |
| rules_go v0.59.0 zip | `68af54cb…` | GitHub release asset |
| BCR rules_go 0.59.0 `MODULE.bazel`, `source.json`, version patch | `b7e43e74…`, `1df17bb7…`, `f0ea14c6…` | bcr.bazel.build |
| scip-go 0.2.7 | `31bf2f3b…` | `spike/t457/native_tool_pins_amd64.json`, offline container |
| gopackagesdriver (rules_go v0.59.0) | `2b58a9c9…` | same |
| `phebs-pack` / `phebs-rules-go-builder` | `b9a2b8c5…` / `b59577e0…` | `med505_amd64_admission_v2.json` `amd64_bootstrap_tools.recipe`, Go 1.25.7 |
| 344 precompiled std archives | every per-file digest in `sdk_standard_libraries.archives` | that recipe, inside the image below |

## Rebuilt with a new identity

The sysroot file selection and serializer were never recorded, so `tools/cc-sysroot.zip`
is rebuilt here and re-pinned in `internal/typedbazel/provider/files.go`.

1. `Dockerfile.compiler`: `ubuntu:24.04` pinned by digest, plus GCC 13.3.0-6ubuntu2~24.04.1
   and `libc6-dev` from the Ubuntu snapshot of 2026-11-24 (glibc 2.39-0ubuntu8.12, binutils
   2.42-4ubuntu2.10). The image also serves as the runtime image. Its environment is `PATH`
   only, and it has no volumes.
2. `select-sysroot.sh`, run inside that image: selects the files of the 41 installed
   packages in the no-recommends closure of `gcc-13` and `libc6-dev` (documentation
   excluded), adds `lib/` and `lib64/` copies of the merged-usr shared objects that glibc's
   linker scripts and the compiler wrapper name, and dereferences every link. 2,311 files.
3. `sysrootzip`, run with Go 1.25.7: a sorted, fixed-timestamp, deflate ZIP of regular files
   within the provider's unpack bounds. Two runs give the same bytes:
   `sha256:429c220debedc299b88279c94fd54b7c70fcf0c848900ddb36876af04a4e8d3a`,
   170,197,504 bytes.

Checks:
- In a bare `ubuntu:24.04` container with no host compiler and no network, the provider's
  amd64 `cc` wrapper over the unpacked ZIP compiles, links and runs a C program and a cgo
  Go program.
- The 344 std archives built through that wrapper are byte-identical to the admitted set.
