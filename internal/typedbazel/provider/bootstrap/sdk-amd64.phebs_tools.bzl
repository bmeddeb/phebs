"""Executable labels for digest-admitted offline rules_go bootstrap tools."""

def _impl(ctx):
    out = ctx.actions.declare_file(ctx.label.name)
    ctx.actions.symlink(output = out, target_file = ctx.file.src, is_executable = True)
    return [DefaultInfo(executable = out)]

pinned_tool = rule(
    implementation = _impl,
    attrs = {"src": attr.label(allow_single_file = True, mandatory = True)},
    executable = True,
)
