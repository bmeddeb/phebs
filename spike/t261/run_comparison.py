#!/usr/bin/env python3
"""Joined four-lane comparison. SQL readers cannot access evaluator labels.

The input checkpoint and common contract precede all outcomes. This runner never
changes them. First build/output/error files use exclusive creation; corrections
require a distinct explicitly named lineage.
"""
from pathlib import Path, PurePosixPath
import argparse
import ctypes
import hashlib
import json
import os
import platform
import posixpath
import signal
import subprocess
import sys
import tempfile
import time

ROOT=Path(__file__).resolve().parent
CHECKPOINT="1f8970fe58f82bc648bf0e9fe1b7dd5b4443b76c"
PREREG="791585bd54e5a03ccdfbda033884229a48c2006e9ac822ad7265091605e962e9"
CONTRACT="9568cf9cce51f30bfd38c131d4ba8a6502ad9c0a777d743e9b5b6cd330d7031c"
HISTORY="ad65ad9aa90313cfc371662a3a37cf528c190212d971ebac2bb443c595f5d056"

def sha(data):return hashlib.sha256(data).hexdigest()
def read_json(path):return json.loads(path.read_text())
def write_new(path,value):
    path.parent.mkdir(parents=True,exist_ok=True)
    with path.open("x") as f:json.dump(value,f,indent=2,ensure_ascii=False,sort_keys=True);f.write("\n")

def verify_inputs():
    assert sha((ROOT/"preregistration.json").read_bytes())==PREREG
    inventory=read_json(ROOT/"preregistration.json")["files"]
    for item in inventory:
        raw=(ROOT/item["path"]).read_bytes()
        assert len(raw)==item["bytes"] and sha(raw)==item["sha256"],item["path"]
    assert sha((ROOT/"comparison-contract.json").read_bytes())==CONTRACT
    assert sha(Path("/Users/ben/phebs.com/history.txt").read_bytes())==HISTORY
    return dict(files=len(inventory),bytes=sum(i["bytes"] for i in inventory),preregistration_sha256=PREREG,contract_sha256=CONTRACT,history_sha256=HISTORY)

def manifest_inputs(pop,path):
    """Read only the exact committed one-entry sqlc metadata subset used here.

    No application source or SQL syntax participates. Unexpected indentation,
    duplicate engines or missing roles stop preparation rather than guess roots.
    """
    text=(ROOT/"corpus"/pop/path).read_text()
    import re
    engines=re.findall(r'^\s+(?:-\s+)?engine:\s*["\']?(postgresql|mysql)["\']?\s*$',text,re.M)
    assert len(engines)==1,path
    result={"engine":engines[0]}
    for role in ("schema","queries"):
        found=re.findall(r'^(?:    |  - )'+role+r':[ \t]*(.*)$',text,re.M);assert len(found)==1,(path,role)
        if found[0]:values=[found[0].strip().strip("\"'")]
        else:
            block=text.split("    "+role+":",1)[1].splitlines()[1:];values=[]
            for line in block:
                if not line.strip() or line.lstrip().startswith("#"):continue
                if not line.startswith("      - "):break
                values.append(line[8:].strip().strip("\"'"))
        paths=[]
        for value in values:
            p=posixpath.normpath(str(PurePosixPath(path).parent/value))
            assert not p.startswith("../") and not p.startswith("/") and "\\" not in p
            paths.append(p)
        assert paths,(path,role);result[role]=paths
    return result

def requests():
    corpus=read_json(ROOT/"corpus-files.json")["files"]
    manifests={"hatchet":"pkg/repository/sqlcv1/sqlc.yaml","monitor":"sqlc.yaml"}
    metadata={p:manifest_inputs(p,m) for p,m in manifests.items()}
    allpaths={i["population"]+"/"+i["path"] for i in corpus}
    for pop,meta in metadata.items():
        for role in ("schema","queries"):
            assert all(pop+"/"+p in allpaths for p in meta[role])
    out=[]
    for item in corpus:
        pop=item["population"];path=item["path"];meta=metadata[pop]
        if path in meta["schema"]:cls="captured-dump" if pop=="monitor" else "authored-schema"
        elif path in meta["queries"]:cls="authored-query"
        elif path.endswith(".sql.go"):cls="generated-sql-go"
        elif path==manifests[pop]:cls="manifest"
        elif path.startswith("cmd/hatchet-migrate/migrate/migrations/") and path.endswith(".sql"):cls="migration"
        else:raise AssertionError("unknown source class: "+path)
        out.append(dict(id=pop+"/"+path,path="corpus/"+pop+"/"+path,sha256=item["sha256"],dialect=meta["engine"],manifest_engine=meta["engine"],source_class=cls,repository=item["repository"],commit=item["commit"],lineage=item["repository"]+"\0"+manifests[pop]+"\0"+meta["engine"],source_role="sqlc-tooling-stub" if path.endswith("pg-stubs.sql") else "committed-source"))
    fixture=ROOT/"fixtures/requested-pg18.6-dump.sql"
    out.append(dict(id="generated/requested-pg18.6-dump.sql",path=str(fixture.relative_to(ROOT)),sha256=sha(fixture.read_bytes()),dialect="postgresql",manifest_engine="",source_class="captured-dump",lineage="generated-public-fixture\0requested-pg18.6-dump.sql\0postgresql",repository="generated-public-fixture",commit="",source_role="generated-fixture"))
    for c in read_json(ROOT/"controls.json")["cases"]:
        out.append(dict(id="control:"+c["id"],path=c["source_path"],sha256=c["source_sha256"],dialect=c["dialect"],manifest_engine=c.get("manifest_engine",""),source_class=c["source_class"],lineage="control:"+c["id"],repository="authored-neutral-control",commit="",declaration_sets=c.get("declaration_sets",[]),use_control_sets=True,projection_fault=c.get("projection_fault",""),source_role="authored-neutral-control"))
    rank={"authored-schema":0,"captured-dump":0}
    return sorted(out,key=lambda r:(rank.get(r["source_class"],1),r["id"]))

class Usage(ctypes.Structure):
    _fields_=[("uuid",ctypes.c_uint8*16)]+[(name,ctypes.c_uint64) for name in ("user","system","idle","interrupt","pageins","wired","resident","footprint","start","exit","child_user","child_system","child_idle","child_interrupt","child_pageins","child_elapsed","read","written")]

def run_child(argv,cwd,outpath,errpath,wall,rss_stop=None,cache=None):
    """Actual joined process group, never a goroutine timeout. Darwin native
    rusage_info_v2 supplies current RSS at20ms; wait4 supplies exact-child peak.
    Imported C runs in that child; no parser spawns a descendant.
    """
    assert sys.platform=="darwin","this receipt adapter is Darwin-only"
    lib=ctypes.CDLL("/usr/lib/libproc.dylib",use_errno=True)
    lib.proc_pid_rusage.argtypes=[ctypes.c_int,ctypes.c_int,ctypes.POINTER(Usage)]
    env=os.environ.copy();env["GOWORK"]="off"
    if cache:env["GOCACHE"]=str(cache)
    start=time.monotonic();peak=0;samples=0;sample_errors=0;stop=None
    with outpath.open("xb") as stdout,errpath.open("xb") as stderr:
        child=subprocess.Popen(argv,cwd=cwd,stdout=stdout,stderr=stderr,env=env,start_new_session=True)
        while True:
            waited,status,usage=os.wait4(child.pid,os.WNOHANG)
            if waited:
                child.returncode=os.waitstatus_to_exitcode(status);break
            current=Usage()
            if lib.proc_pid_rusage(child.pid,2,ctypes.byref(current))==0:
                peak=max(peak,current.resident);samples+=1
            else:sample_errors+=1
            elapsed=time.monotonic()-start
            if elapsed>wall:stop="wall-limit"
            if rss_stop and peak>rss_stop:stop="observed-rss-limit"
            if outpath.stat().st_size>64<<20 or errpath.stat().st_size>65536:stop="outer-output-limit"
            if stop:
                try:os.killpg(child.pid,signal.SIGKILL)
                except ProcessLookupError:pass
                _,status,usage=os.wait4(child.pid,0);child.returncode=os.waitstatus_to_exitcode(status);break
            time.sleep(.02)
    joined=child.wait()
    return dict(argv=argv,cwd=str(cwd),go_cache=str(cache) if cache else "inherited host cache; not a cold-build measurement",exit=joined,stop=stop,joined=True,wall_seconds=round(time.monotonic()-start,6),peak_observed_rss_bytes=peak,samples=samples,sample_errors=sample_errors,sampling_interval_seconds=.02,exact_child_wait4_maxrss_bytes=usage.ru_maxrss,rss_stop_bytes=rss_stop,rss_qualification="observed stop, not hard heap or allocation bound;20ms plus scheduling/measurement overshoot; wait4 peak may exceed sampled current RSS",stdout_bytes=outpath.stat().st_size,stderr_bytes=errpath.stat().st_size,stdout_sha256=sha(outpath.read_bytes()),stderr_sha256=sha(errpath.read_bytes()),wall_limit_seconds=wall,output_limit_bytes=64<<20,stderr_limit_bytes=65536)

def source_inventory():
    paths=sorted((ROOT/"reader").rglob("*.go"))+sorted((ROOT/"reader").rglob("go.mod"))+sorted((ROOT/"reader").rglob("go.sum"))+[Path(__file__),ROOT/"comparison-contract.json"]
    return [dict(path=str(p.relative_to(ROOT)),bytes=p.stat().st_size,sha256=sha(p.read_bytes())) for p in sorted(set(paths))]

def main():
    parser=argparse.ArgumentParser();parser.add_argument("--lineage",required=True);parser.add_argument("--build-only",action="store_true");parser.add_argument("--run-existing",action="store_true");parser.add_argument("--cold-cache",action="store_true");parser.add_argument("--score-only",action="store_true");parser.add_argument("--score-version");args=parser.parse_args()
    assert args.lineage and "/" not in args.lineage and ".." not in args.lineage
    result=ROOT/"results"/args.lineage
    if args.score_only:
        verify_inputs();score(result,args.score_version);return
    if args.run_existing:
        prepared=read_json(result/"build-receipt.json");bins={k:Path(v["binary"]) for k,v in prepared["lanes"].items()}
    else:
        result.mkdir(parents=True,exist_ok=False);runtime=Path(tempfile.mkdtemp(prefix="phebs-t261-"));bins={};prepared=dict(input_checkpoint=CHECKPOINT,input_verification=verify_inputs(),source_at_build_start=source_inventory(),host=dict(system=platform.platform(),machine=platform.machine()),go_version=subprocess.check_output(["go","version"],text=True).strip(),go_binary_sha256=sha(Path(subprocess.check_output(["which","go"],text=True).strip()).read_bytes()),git_head_at_build=subprocess.check_output(["git","rev-parse","HEAD"],cwd=ROOT,text=True).strip(),git_qualification="source inventory includes uncommitted evaluator bytes; HEAD is checkpoint lineage, not false immutable evaluator provenance",lanes={})
        for dialect in ("postgresql","mysql"):
            for kind in ("subset","imported"):
                lane=dialect+"-"+kind;binary=runtime/lane;bins[lane]=binary
                cwd=ROOT/"reader" if kind=="subset" else ROOT/"reader/candidates"/dialect
                argv=["go","build","-mod=mod","-trimpath","-buildvcs=false","-o",str(binary)]
                if kind=="subset":argv += ["-ldflags=-X main.Dialect="+dialect,"./cmd/subset"]
                else:argv += ["."]
                cache=runtime/(lane+"-gocache") if args.cold_cache else None
                measurement=run_child(argv,cwd,result/(lane+"-build.stdout"),result/(lane+"-build.stderr"),240,cache=cache)
                measurement.update(binary=str(binary),binary_bytes=binary.stat().st_size if binary.exists() else None,binary_sha256=sha(binary.read_bytes()) if binary.exists() else None)
                if binary.exists():measurement["go_build_info"]=subprocess.check_output(["go","version","-m",str(binary)],text=True)
                prepared["lanes"][lane]=measurement
                print(lane,"build",measurement["exit"],measurement["wall_seconds"],flush=True)
                if measurement["exit"]!=0 or measurement["stop"]:
                    write_new(result/"build-receipt.json",prepared);raise RuntimeError("first build stopped; preserved; no automatic retry")
        prepared["source_after_build"]=source_inventory();write_new(result/"build-receipt.json",prepared)
    if args.build_only:return
    expected_source={i["path"]:i["sha256"] for i in prepared["source_after_build"] if i["path"].startswith("reader/")}
    current_source={i["path"]:i["sha256"] for i in source_inventory() if i["path"].startswith("reader/")}
    assert expected_source==current_source,"reader source changed after binary build; create separate lineage"
    inputs=verify_inputs();requests_path=result/"requests.json";write_new(requests_path,requests())
    receipt=dict(input_checkpoint=CHECKPOINT,inputs_before=inputs,source_inventory=source_inventory(),lanes={})
    for lane,binary in bins.items():
        lane_runs=[]
        for number in (1,2):
            out=result/f"{lane}-pass{number}.json";err=result/f"{lane}-pass{number}.stderr"
            measured=run_child([str(binary),str(ROOT),str(requests_path)],ROOT,out,err,30,1<<30);lane_runs.append(measured)
            print(lane,"pass",number,measured["exit"],measured["wall_seconds"],flush=True)
            if measured["exit"]!=0 or measured["stop"]:break
        same=len(lane_runs)==2 and lane_runs[0]["stdout_sha256"]==lane_runs[1]["stdout_sha256"]
        receipt["lanes"][lane]=dict(runs=lane_runs,canonical_bytes_identical=same,binary_sha256=sha(binary.read_bytes()))
    receipt["inputs_after"]=verify_inputs();receipt["source_after"]=source_inventory();write_new(result/"run-receipt.json",receipt)
    score(result)

def fact_key(f,site=False,event=False):
    fields=["start","end","sha256","name","quoted"]
    if site:fields += ["access"]
    elif event:fields += ["predicate"]
    else:fields += ["kind"]
    return tuple(f.get(k) for k in fields)

def exact_match(expected,actual,site=False,event=False):
    if fact_key(expected,site,event)!=fact_key(actual,site,event):return False
    if event:
        if expected.get("direction")!=actual.get("direction"):return False
        if "label_statement_start" in expected:
            if not(expected["label_statement_start"]<=actual.get("candidate_statement_start",-1)<=actual["start"]<actual["end"]<=actual.get("candidate_statement_end",-1)<=expected["label_statement_end"]):return False
    for key in ("relation","schema","schema_quoted","columns","target_relation","target_columns","prefix_lengths","canonical_name","source_role"):
        value=actual.get(key,False) if key=="schema_quoted" else actual.get(key)
        if key in expected and expected[key]!=value:return False
    return True

def compare(expected,actual,site=False,event=False):
    used=set();matched=[];miss=[]
    for e in expected:
        match=next((i for i,a in enumerate(actual) if i not in used and exact_match(e,a,site,event)),None)
        if match is None:miss.append(e)
        else:used.add(match);matched.append((e,actual[match]))
    return matched,miss,[a for i,a in enumerate(actual) if i not in used]

def score(result,version=None):
    if version:assert "/" not in version and ".." not in version
    labels=[read_json(p) for p in sorted((ROOT/"labels").glob("*.json"))]
    controls=read_json(ROOT/"controls.json")["cases"];summary=dict(format="t261-comparison-results-v1",input_checkpoint=CHECKPOINT,scope="finite preregistered populations; no cross-dialect blend, native library positions, independent population accuracy or production adoption",lanes={})
    for lane in read_json(result/"run-receipt.json")["lanes"]:
        path=result/(lane+"-pass1.json")
        try:outputs=read_json(path)
        except (ValueError,FileNotFoundError):summary["lanes"][lane]=dict(status="no complete parseable output");continue
        dialect=lane.split("-")[0];by_path={o["path"]:o for o in outputs};stats=dict(declarations={},migration_statements={},query_sites={},bindings={},controls=[],source_classes={},frontend={},projector_gaps={},boundary_refusals={},unexpected_emissions=[],span_failures=[])
        for o in outputs:
            if o["dialect"]!=dialect:continue
            cls=stats["source_classes"].setdefault(o["source_class"],dict(blobs=0,admit=0,gap=0,refuse=0));cls["blobs"]+=1;cls[o["decision"]]+=1
            if o["decision"]=="refuse":stats["boundary_refusals"][o.get("reason")]=stats["boundary_refusals"].get(o.get("reason"),0)+1
            data=(ROOT/o["path"]).read_bytes()
            for s in o["statements"]:
                witness=s["frontend"]+":"+s.get("frontend_category","");stats["frontend"][witness]=stats["frontend"].get(witness,0)+1
                if s.get("gap"):stats["projector_gaps"][s["gap"]]=stats["projector_gaps"].get(s["gap"],0)+1
                for key in ("objects","sites","events"):
                    for f in (s[key] or []):
                        if not(0<=s["start"]<=f["start"]<f["end"]<=s["end"]<=len(data)) or sha(data[f["start"]:f["end"]])!=f["sha256"]:stats["span_failures"].append(dict(id=o["id"],fact=f))
        for label in labels:
            if label["dialect"]!=dialect:continue
            generated=label.get("source_origin")=="generated-fixture"
            pop="generated_pg_dump" if generated else "public_manifest" if label["plane"]=="sql-schema" else "public_query" if label["plane"]=="sql-query" else "selected_migration"
            prefix="fixtures/" if generated else "corpus/hatchet/" if label["repository"]=="hatchet-dev/hatchet" else "corpus/monitor/"
            source=label["source_path"] if generated else prefix+label["source_path"]
            out=by_path[source];assert out["sha256"]==label["source_sha256"],source
            actual={key:[f for s in out["statements"] for f in (s[key] or [])] for key in ("objects","sites","events")}
            expected={key:[f for s in label["statements"] for f in s.get(key,[])] for key in actual}
            actual["events"]=[dict(f,candidate_statement_start=s["start"],candidate_statement_end=s["end"]) for s in out["statements"] for f in (s["events"] or [])]
            expected["events"]=[dict(f,direction=s["direction"],label_statement_start=s["start"],label_statement_end=s["end"]) for s in label["statements"] for f in s.get("events",[])]
            for kind in sorted({f["kind"] for f in expected["objects"]}):
                es=[f for f in expected["objects"] if f["kind"]==kind];ac=[f for f in actual["objects"] if f["kind"]==kind];matched,miss,extra=compare(es,ac)
                item=stats["declarations"].setdefault(pop+":"+kind,dict(expected=0,matched=0,unresolved=0,unexpected=0,sources=[]));item["expected"]+=len(es);item["matched"]+=len(matched);item["unresolved"]+=len(miss);item["unexpected"]+=len(extra);item["sources"].append(dict(path=source,expected=len(es),matched=len(matched),unresolved=len(miss),unexpected=len(extra)))
            for key,is_site,is_event in (("objects",False,False),("sites",True,False),("events",False,True)):
                matched,miss,extra=compare(expected[key],actual[key],is_site,is_event)
                stats["unexpected_emissions"] += [dict(path=source,plane=key,fact=f) for f in extra]
                if is_site:
                    item=stats["query_sites"].setdefault(pop,dict(expected=0,matched=0,unresolved=0,unexpected=0));item["expected"]+=len(expected[key]);item["matched"]+=len(matched);item["unresolved"]+=len(miss);item["unexpected"]+=len(extra)
                    bindings=stats["bindings"].setdefault(pop,dict(expected=0,unique=0,name_only=0,missing=0,ambiguous=0,unrecognized_site=0));bindings["expected"]+=len(expected[key]);bindings["unrecognized_site"]+=len(miss)
                    for e,a in matched:bindings[a.get("binding","name-only").replace("-","_")]+=1
            if label["plane"]=="sql-migration-event":
                for stmt in label["statements"]:
                    key=stmt["syntax_class"]+":"+stmt.get("direction","unspecified");item=stats["migration_statements"].setdefault(key,dict(expected=0,recognized=0,gaps=0,expected_events=0,matched_events=0));item["expected"]+=1;events=[dict(f,direction=stmt["direction"],label_statement_start=stmt["start"],label_statement_end=stmt["end"]) for f in stmt.get("events",[])];matches=compare(events,actual["events"],event=True)[0];recognized=bool(events) and len(matches)==len(events);item["recognized"]+=int(recognized);item["gaps"]+=int(not recognized);item["expected_events"]+=len(events);item["matched_events"]+=len(matches)
        # Zero is an explicit unmeasured population, never a vacuous accuracy.
        if dialect=="mysql":stats["migration_population"]=dict(expected=0,status="unmeasured; no real corpus migration files")
        for c in controls:
            if c["dialect"]!=dialect:continue
            o=by_path[c["source_path"]];actual={k:[f for s in o["statements"] for f in (s[k] or [])] for k in ("objects","sites","events")}
            obj=compare(c["expected_objects"],actual["objects"]);sites=compare(c["expected_sites"],actual["sites"],site=True)
            decision=o["decision"]==c["expected_decision"] and (not c["expected_reason"] or o.get("reason")==c["expected_reason"])
            binding=all(e["expected_binding"]==a.get("binding") for e,a in sites[0]);expected_unresolved=c.get("expected_unresolved_sites",[])
            unresolved_ok=sorted(expected_unresolved)==sorted(f["name"] for f in sites[2] if f.get("binding")=="missing")
            other_sites=[f for f in sites[2] if f.get("binding")!="missing"]
            exact=decision and not obj[1] and not obj[2] and not sites[1] and not other_sites and unresolved_ok and binding and not actual["events"]
            stats["controls"].append(dict(id=c["id"],expected_decision=c["expected_decision"],decision=o["decision"],reason=o.get("reason"),exact_contract_pass=exact,missing_objects=len(obj[1]),extra_objects=len(obj[2]),missing_sites=len(sites[1]),extra_sites=len(other_sites),bindings_match=binding,unresolved_match=unresolved_ok))
        # Every unlabelled SQL file still enters classification/frontends and
        # panic/positions/bounds. Its facts are not scored against absent labels.
        stats["unlabelled_outputs_qualification"]="full-corpus safety traversal only; no accuracy denominator or false-positive oracle outside frozen labels"
        stats["controls_passed"]=sum(c["exact_contract_pass"] for c in stats["controls"])
        stats["panic_count"]=sum(o.get("reason")=="panic" for o in outputs)
        summary["lanes"][lane]=stats
    score_name="scores"+("-"+version if version else "")+".json";receipt_name="score-receipt"+("-"+version if version else "")+".json"
    write_new(result/score_name,summary)
    write_new(result/receipt_name,dict(scorer_sha256=sha(Path(__file__).read_bytes()),inputs=verify_inputs(),output_sha256=sha((result/score_name).read_bytes()),child_outputs={p.name:sha(p.read_bytes()) for p in sorted(result.glob("*-pass*.json"))},qualification="Original candidate outputs retained; review scorer adds required Goose direction/statement-envelope matching and statement-contained span validation. Frozen inputs/labels and parser quality semantics unchanged. Original unqualified scores remain separate." if version else "Scorer-only null-as-empty collection correction after first evaluator stop; candidate outputs/run receipts unchanged; no labels, thresholds or semantic matching changed"))

if __name__=="__main__":main()
