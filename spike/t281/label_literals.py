"""Source-only bounded archive and explicitly chosen literal-span helpers.

No language, command, key-specification or ACL recognizer lives here. Authors
choose every path/line/token; helpers only verify immutable bytes and serialize.
"""
from pathlib import Path
import hashlib
import json
import tarfile

ROOT=Path(__file__).resolve().parent
RECEIPT=json.loads((ROOT/'source-receipt.json').read_text())

def blob(pop,path):
    record=next(x for x in RECEIPT['records'] if x['name']==pop)
    expected=next(x for x in record['files'] if x['path']==path)
    with tarfile.open(ROOT/record['archive']) as source:
        member=source.getmember(path)
        assert member.isfile() and member.size==expected['bytes'] and member.size<=262144
        raw=source.extractfile(member).read()
    assert hashlib.sha256(raw).hexdigest()==expected['sha256']
    return raw

def metadata(pop,path):
    record=next(x for x in RECEIPT['records'] if x['name']==pop)
    item=next(x for x in record['files'] if x['path']==path)
    return dict(repository=record['repository'],commit=record['commit'],source_path=path,source_sha256=item['sha256'],source_bytes=item['bytes'])

def span(raw,start,end):
    assert 0<=start<end<=len(raw)
    return dict(start=start,end=end,sha256=hashlib.sha256(raw[start:end]).hexdigest())

def literal(raw,line,text,occurrence=0):
    lines=raw.splitlines(keepends=True);selected=lines[line-1];offset=sum(map(len,lines[:line-1]));token=text.encode();position=-1
    for _ in range(occurrence+1):
        position=selected.find(token,position+1);assert position>=0,(line,text)
    return span(raw,offset+position,offset+position+len(token))

def save(path,data):
    target=ROOT/path;target.parent.mkdir(parents=True,exist_ok=True);target.write_text(json.dumps(data,indent=2,ensure_ascii=False)+'\n')
