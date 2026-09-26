def _flag_impl(ctx):
    return []

neutral_flag = rule(implementation = _flag_impl, build_setting = config.bool(flag = True))

def _transition_impl(settings, attr):
    return {"a": {"//transition:variant": False}, "b": {"//transition:variant": True}}

_split = transition(implementation = _transition_impl, inputs = [], outputs = ["//transition:variant"])

def _impl(ctx):
    return [DefaultInfo()]

phebs_split = rule(
    implementation = _impl,
    attrs = {
        "deps": attr.label(cfg = _split, mandatory = True),
        "_allowlist_function_transition": attr.label(default = "@bazel_tools//tools/allowlists/function_transition_allowlist"),
    },
)
