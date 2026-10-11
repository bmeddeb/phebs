#!/usr/bin/env python3
"""Inventory Go modules at the admitted corpus pins, without Phebs output."""
import json
import os
from pathlib import Path
import subprocess


def enclosing_module(path, modules):
    return max((rel for rel in modules if not rel or path.startswith(rel + "/")), key=len)


def main():
    here = Path(__file__).resolve().parent
    corpus = Path(os.environ.get("CORPUS_DIR", here.parent / "corpus"))
    root = Path(os.environ["T472_ROOT"])
    lock = json.loads((here.parent / "corpus.lock.json").read_text())
    report = {}
    for repo in lock["repos"]:
        if not repo["derived"]["corpus_admitted"]:
            continue
        short = repo["name"].split("/")[1]
        raw = subprocess.check_output([
            "git", "-C", str(corpus / short), "ls-tree", "-rz", "--name-only", repo["commit"],
        ])
        files = sorted(raw.decode().rstrip("\0").split("\0"))
        module_rels = sorted(p[:-len("/go.mod")] if p != "go.mod" else ""
                             for p in files if p == "go.mod" or p.endswith("/go.mod"))
        if "" not in module_rels:
            raise SystemExit(short + ": no root module")
        per = {rel: [] for rel in module_rels}
        for path in files:
            per[enclosing_module(path, module_rels)].append(path)
        modules = [{
            "rel": rel,
            "files": len(paths),
            "go_files": sum(p.endswith(".go") for p in paths),
            "go_file_list": [p for p in paths if p.endswith(".go")],
        } for rel, paths in per.items()]
        report[short] = {"pin": repo["commit"], "modules": modules, "total_files": len(files)}
        print(short, "modules", len(modules), "runs", sum(m["go_files"] > 0 for m in modules))
    (root / "modules.json").write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
