"""Owned T45.1a projection over the pinned rules_go 0.63.0 provider API."""

load("@rules_go//go:def.bzl", "GoArchive", "GoInfo")
load("@rules_go//go/private:providers.bzl", "GoStdLib")
load("@rules_go//go/private:common.bzl", "GO_TOOLCHAIN_LABEL", "RULES_GO_STDLIB_PREFIX")

PhebsPlanInfo = provider(fields = ["files"])

# rules_go 0.63 deliberately excludes these Go 1.25 SDK helper subtrees from
# GoSDK.srcs. Its local GoStdlibList nevertheless lists them from the imported
# SDK. Declare their exact File labels; never infer membership by walking the
# physical SDK. Mode selection still comes from the provider-owned list.
_SDK_HELPER_SOURCES = [
    "crypto/internal/sysrand/internal/seccomp/seccomp_linux.go",
    "crypto/internal/sysrand/internal/seccomp/seccomp_unsupported.go",
    "encoding/json/internal/jsontest/testcase.go",
    "encoding/json/internal/jsontest/testdata.go",
    "internal/obscuretestdata/obscuretestdata.go",
    "internal/testpty/pty.go",
    "internal/testpty/pty_cgo.go",
    "internal/testpty/pty_darwin.go",
    "internal/testpty/pty_none.go",
    "log/slog/internal/benchmarks/benchmarks.go",
    "log/slog/internal/benchmarks/handlers.go",
    "net/internal/cgotest/resstate.go",
    "net/internal/socktest/switch.go",
    "net/internal/socktest/switch_posix.go",
    "net/internal/socktest/switch_stub.go",
    "net/internal/socktest/switch_unix.go",
    "net/internal/socktest/switch_windows.go",
    "net/internal/socktest/sys_cloexec.go",
    "net/internal/socktest/sys_unix.go",
    "net/internal/socktest/sys_windows.go",
    "reflect/internal/example1/example.go",
    "reflect/internal/example2/example.go",
]

def _helper_impl(ctx):
    out = ctx.actions.declare_file(ctx.label.name)
    ctx.actions.symlink(output = out, target_file = ctx.file.src, is_executable = True)
    return [DefaultInfo(executable = out)]

pinned_helper = rule(
    implementation = _helper_impl,
    attrs = {"src": attr.label(allow_single_file = True, mandatory = True)},
    executable = True,
)

def _file(f):
    return {"path": f.path, "short_path": f.short_path, "owner": str(f.owner), "source": f.is_source, "tree": f.is_directory}

def _stdlib_ref(ctx):
    value = getattr(ctx.rule.attr, "_stdlib", None) or getattr(ctx.rule.attr, "_go_context_data", None)
    if value and GoStdLib in value:
        f = value[GoStdLib]._list_json
        return {"owner": str(f.owner), "list": f.path}
    fail("T45.1a GoArchive lacks exact GoStdLib provider")

def _impl(target, ctx):
    transitive = []
    for name in dir(ctx.rule.attr):
        value = getattr(ctx.rule.attr, name)
        values = value if type(value) == "list" else [value]
        for dep in values:
            if type(dep) == "Target" and PhebsPlanInfo in dep:
                transitive.append(dep[PhebsPlanInfo].files)

    outputs = []
    payload = None
    inputs = []
    if GoArchive in target and str(target[GoArchive].data.export_file.owner) != str(target.label):
        # Alias/reset wrappers may expose an archive produced by a configured
        # dependency. Preserve that exact provider reference instead of
        # relabelling its owner or silently dropping this target's package edge.
        export = target[GoArchive].data.export_file
        payload = {"version": "phebs-t451a-projection-input-v1", "owner": str(target.label),
                   "roots": [], "embeds": [], "archives": [],
                   "forward": {"owner": str(export.owner), "export": export.path}}
    elif GoArchive in target:
        root = target[GoArchive]
        archives = []
        pending = [root]
        seen = {}
        # Only archives produced by this configured rule belong here. Imported
        # archives are projected at their own configured rule, then joined by
        # the exact declared export artifact and configured owner.
        for _ in range(256):
            if not pending:
                break
            arc = pending.pop()
            export = arc.data.export_file
            if str(export.owner) != str(target.label):
                continue
            if export.path in seen:
                continue
            seen[export.path] = True
            if len(seen) > 64:
                fail("T45.1a archive bound")
            src = arc.source
            mode = src.mode
            sources = [f for f in arc.data.srcs if f.extension == "go"]
            inputs.extend(sources)
            cgo_dir = getattr(arc.data, "cgo_out_dir", None)
            if cgo_dir:
                inputs.append(cgo_dir)
            archives.append({
                "name": arc.data.name,
                "label": str(arc.data.label),
                "export": export.path,
                "import_path": arc.data.importpath,
                "import_map": arc.data.importmap,
                "goos": mode.goos,
                "goarch": mode.goarch,
                "tags": mode.tags,
                "cgo": not mode.pure,
                "test_filter": getattr(src, "testfilter", None) or "",
                "sources": [_file(f) for f in sources],
                "cgo_directory": _file(cgo_dir) if cgo_dir else None,
                "imports": [{"path": dep.data.importpath, "owner": str(dep.data.export_file.owner), "export": dep.data.export_file.path} for dep in arc.direct],
                "stdlib": _stdlib_ref(ctx),
            })
            pending.extend(arc.direct)
        if pending:
            fail("T45.1a archive traversal bound")
        if not archives:
            fail("T45.1a archive owner mismatch")
        embeds = [str(dep.label) for dep in getattr(ctx.rule.attr, "embed", [])]
        payload = {"version": "phebs-t451a-projection-input-v1", "owner": str(target.label), "roots": [root.data.export_file.path], "embeds": embeds, "archives": archives}
    elif GoStdLib in target and str(target[GoStdLib]._list_json.owner) == str(target.label):
        stdlib = target[GoStdLib]
        sdk = ctx.toolchains[GO_TOOLCHAIN_LABEL].sdk
        mode = target[GoInfo].mode
        declared_sources = depset(ctx.files._sdk_helper_srcs, transitive = [sdk.srcs])
        sources = [f for f in declared_sources.to_list() if f.extension == "go"]
        caches = stdlib.cache_dir.to_list()
        inputs = sources + caches + [stdlib._list_json, sdk.root_file]
        payload = {
            "version": "phebs-t451a-projection-input-v1",
            "owner": str(target.label),
            "roots": [], "embeds": [], "archives": [],
            "sdk": {"version": sdk.version, "root": sdk.root_file.dirname,
                    "mode": {"goos": mode.goos, "goarch": mode.goarch, "cgo": not mode.pure, "tags": mode.tags},
                    "prefix": RULES_GO_STDLIB_PREFIX, "list": _file(stdlib._list_json),
                    "caches": [_file(f) for f in caches], "sources": [f.path for f in sources]},
        }
    if payload:
        manifest = ctx.actions.declare_file(ctx.label.name + ".phebs-plan-input.json")
        out = ctx.actions.declare_file(ctx.label.name + ".phebs-plan.json")
        ctx.actions.write(manifest, json.encode(payload))
        ctx.actions.run(
            executable = ctx.executable._helper,
            arguments = ["__plan_helper", manifest.path, out.path],
            inputs = depset([manifest] + inputs),
            outputs = [out],
            mnemonic = "PhebsPlan",
            use_default_shell_env = False,
        )
        outputs.append(out)
    files = depset(outputs, transitive = transitive)
    return [PhebsPlanInfo(files = files), OutputGroupInfo(phebs_plan = files)]

phebs_plan = aspect(
    implementation = _impl,
    attr_aspects = ["*"],
    attrs = {
        "_helper": attr.label(default = Label("//phebs_plan:helper"), executable = True, cfg = "exec"),
        "_sdk_helper_srcs": attr.label_list(default = [Label("@phebs_sdk//:src/" + p) for p in _SDK_HELPER_SOURCES], allow_files = [".go"]),
    },
    toolchains = [GO_TOOLCHAIN_LABEL],
)
