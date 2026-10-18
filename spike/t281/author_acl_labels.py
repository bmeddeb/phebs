"""Serialize explicitly reviewed safe-token choices; never copy ACL context.

This is a source-literal label helper, not the future streaming ACL reader.
Credential tokens are neither located nor retained by this authoring helper.
"""
from pathlib import Path
import hashlib
import json
import tarfile

ROOT=Path(__file__).resolve().parent
CHOICES={
    'tests/assets/nodefaultuser.acl':[
        ('alice',5,10,[('~*',21,23)]),
        ('bob',35,38,[('~*',49,51)]),
    ],
    'tests/assets/user.acl':[
        ('alice',5,10,[]),
        ('bob',49,52,[('~set*',73,78)]),
        ('default',92,99,[('~*',110,112)]),
    ],
    'tests/assets/userwithselectors.acl':[
        ('alice',5,10,[('~rw*',20,24)]),
        ('bob',31,34,[('%W~w*',44,49),('%R~r*',57,62)]),
    ],
}

def span(raw,text,start,end):
    assert raw[start:end]==text.encode()
    return dict(token=text,start=start,end=end,sha256=hashlib.sha256(raw[start:end]).hexdigest())

def main():
    target=ROOT/'labels';target.mkdir(exist_ok=True)
    with tarfile.open(ROOT/'corpus/redis.tar.gz') as source:
        files=[]
        for path,choices in CHOICES.items():
            raw=source.extractfile(path).read();principals=[];pairs=[]
            for name,start,end,patterns in choices:
                principal=span(raw,name,start,end);principals.append(principal)
                for pattern,begin,stop in patterns:
                    permission='R' if pattern.startswith('%R~') else 'W' if pattern.startswith('%W~') else 'RW'
                    pairs.append(dict(principal=principal,pattern=span(raw,pattern,begin,stop),authorization_intent=permission))
            files.append(dict(source_path=path,source_bytes=len(raw),source_sha256=hashlib.sha256(raw).hexdigest(),principals=principals,pairs=pairs))
    out=dict(format='t281-acl-source-labels-v1',repository='redis/redis',commit='316753259b4db132cf494292a1b3a702d9e9ddb2',scope='Complete three-file public ACL census; only explicit username/pattern token spans, no line context/grants/credentials or credential hashes',principal_occurrences=7,principal_pattern_pairs=7,credential_output_allowance=0,public_credential_payload_username_collisions=2,collision_rule='Complete credential-token bytes and credential-origin citations forbidden; identical allowed username bytes remain permitted. Distinct synthetic secrets additionally require payload absence.',files=files,recognizer_runs=0)
    (target/'public-acl.json').write_text(json.dumps(out,indent=2,ensure_ascii=False)+'\n')
    print('safe ACL labels:',out['principal_occurrences'],'principals,',out['principal_pattern_pairs'],'pairs')

if __name__=='__main__':main()
