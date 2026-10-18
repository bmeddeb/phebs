"""Literal-only hand labels for the entire measured1311-byte PG18.6 dump."""
from pathlib import Path
import hashlib
import json

ROOT=Path(__file__).resolve().parent
PATH="fixtures/requested-pg18.6-dump.sql"
RAW=(ROOT/PATH).read_bytes()
assert hashlib.sha256(RAW).hexdigest()=="712e9380d8b580971cc891b5cfbf3f2b0817b84782e3d67df3118a9877e0d2b2"
LINES=RAW.splitlines(keepends=True)
OFFSETS=[0]
for line in LINES:OFFSETS.append(OFFSETS[-1]+len(line))


def span(first,last):
    start,end=OFFSETS[first-1],OFFSETS[last]
    return dict(start=start,end=end,sha256=hashlib.sha256(RAW[start:end]).hexdigest())


def obj(kind,name,line,literal,**extra):
    token=literal.encode();assert token in LINES[line-1]
    start=OFFSETS[line-1]+LINES[line-1].index(token)
    return dict(kind=kind,name=name,quoted=literal.startswith('"'),start=start,end=start+len(token),sha256=hashlib.sha256(token).hexdigest(),schema="public",schema_quoted=False,**extra)


statements=[]
for line in [5,*range(10,21),22,50]:
    statements.append(dict(id="wrapper-L"+str(line),**span(line,line),syntax_class="dump-wrapper",expected_disposition="wrapper",objects=[],note="Exact PG18.6 capture/restrict/session wrapper; no query usage/history or execution in the reader."))
objects=[obj("table","Dispatcher",28,'"Dispatcher"')]
for line,name,literal,type_ in [(29,"id","id","uuid"),(30,"createdAt",'"createdAt"',"timestamp(3) without time zone"),(31,"updatedAt",'"updatedAt"',"timestamp(3) without time zone"),(32,"deletedAt",'"deletedAt"',"timestamp(3) without time zone"),(33,"lastHeartbeatAt",'"lastHeartbeatAt"',"timestamp(3) without time zone"),(34,"isActive",'"isActive"',"boolean")]:
    objects.append(obj("column",name,line,literal,relation="Dispatcher",type_spelling=type_))
statements.append(dict(id="table-public-Dispatcher",**span(28,35),syntax_class="create-table",expected_disposition="declaration",objects=objects))
statements.append(dict(id="pk-public-Dispatcher",**span(42,43),syntax_class="alter-table-add-pk",expected_disposition="declaration",objects=[obj("primary_key","Dispatcher_pkey",43,'"Dispatcher_pkey"',relation="Dispatcher",columns=["id"])],note="Captured-dump current constraint, not a migration event; explicit public qualifier retained separately from exact quoted table name."))
statements.sort(key=lambda x:x["start"])
result=dict(format="t261-source-labels-v1",dialect="postgresql",repository="phebs-t261-public-dispatcher-fixture",commit=None,source_origin="generated-fixture",evaluation_commit="The evaluator must bind this exact source hash/path to the lead's immutable preregistration Git checkpoint; no self-referential future commit is fabricated inside its own input.",source_path=PATH,source_sha256=hashlib.sha256(RAW).hexdigest(),plane="sql-schema",statements=statements,inventory_notes=["Entire measured PG18.6 schema-only output completely hand-labelled after authorized generation and before any SQL parser candidate/grammar run.","One table/six columns/one named PK;14 explicit wrappers. Public schema qualifier retained as typed metadata, not delimiter-split identity.","Derived from the exact licensed pinned Hatchet Dispatcher statement; this dump is not falsely attributed to an upstream Hatchet Git blob. Exact upstream derivation lives in pg-generator-input.json; exact runtime/double-dump/teardown receipt in pg-generation-receipt-accepted.json.","One mechanical supplementary capture only; no live/runtime/vendor/schema accuracy claim."])
dest=ROOT/"labels/generated-pg-dump.json"
dest.write_text(json.dumps(result,indent=2,ensure_ascii=False)+"\n")
print(json.dumps(dict(path=str(dest),statements=len(statements),objects=sum(len(s["objects"]) for s in statements),sha256=hashlib.sha256(dest.read_bytes()).hexdigest())))
