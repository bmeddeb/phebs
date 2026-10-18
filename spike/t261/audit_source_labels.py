"""Check frozen-author span accounting; never recognize or parse SQL syntax."""
from pathlib import Path
import hashlib
import json

ROOT=Path(__file__).resolve().parent


def audit(path):
    data=json.loads(path.read_text())
    if data.get("source_origin")=="generated-fixture":
        raw=(ROOT/data["source_path"]).read_bytes()
    else:
        corpus="hatchet" if data["repository"]=="hatchet-dev/hatchet" else "monitor"
        raw=(ROOT/"corpus"/corpus/data["source_path"]).read_bytes()
    assert hashlib.sha256(raw).hexdigest()==data["source_sha256"],path
    covered=bytearray(len(raw))
    objects=sites=0
    for stmt in data["statements"]:
        for item in [stmt,*stmt.get("objects",[]),*stmt.get("sites",[]),*stmt.get("events",[])]:
            first,last=item["start"],item["end"]
            assert 0<=first<last<=len(raw),(path,item)
            assert hashlib.sha256(raw[first:last]).hexdigest()==item["sha256"],(path,item)
            assert stmt["start"]<=first and last<=stmt["end"],(path,item)
            raw[first:last].decode("utf-8")
            if item is not stmt:
                token=raw[first:last].decode("utf-8")
                if item["name"].startswith("@"):
                    assert item["name"]=="@"+str(first),(path,item)
                    assert token in ["PRIMARY KEY","UNIQUE","CHECK","FOREIGN KEY"],(path,item)
                else:
                    expected=item["name"]
                    if item.get("quoted"):
                        quote='`' if data["dialect"]=="mysql" else '"'
                        expected=quote+expected+quote
                    assert token==expected,(path,item,token)
        first,last=stmt["start"],stmt["end"]
        assert not any(covered[first:last]),(path,stmt["id"],"overlap")
        covered[first:last]=b"\1"*(last-first)
        objects+=len(stmt.get("objects",[]))
        sites+=len(stmt.get("sites",[]))
    remainder=bytes(32 if covered[i] else b for i,b in enumerate(raw))
    # Remove only line comments/empty lines from already-author-selected envelope
    # gaps. This is a complete byte-accounting check, not SQL interpretation.
    uncovered=[]
    for number,line in enumerate(remainder.splitlines(),1):
        line=line.strip()
        if line and not line.startswith(b"--"):
            uncovered.append(dict(line=number,bytes=line.decode("utf-8")))
    return dict(path=str(path.relative_to(ROOT)),source_bytes=len(raw),statements=len(data["statements"]),objects=objects,sites=sites,all_hash_spans_valid=True,uncovered_noncomment_lines=uncovered)


if __name__=="__main__":
    reports=[audit(p) for p in sorted((ROOT/"labels").glob("*.json"))]
    print(json.dumps(reports,indent=2,ensure_ascii=False))
    assert all(not r["uncovered_noncomment_lines"] for r in reports)
