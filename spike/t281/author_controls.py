"""Author neutral inputs and explicit expectations before first recognition.

Fixtures are inert source text. This helper never parses Go, Redis or ACL. ACL
secrets are generated into input-only fixtures and never copied into labels.
"""
from pathlib import Path
import base64
import hashlib
import json
from label_literals import ROOT,span,save

CASES=[]
def go(name,body,*,version='v9.14.1',major='v9',keys=(),declarations='',**expected):
    text='package neutral\nimport ("context"; "fmt"; redis "github.com/redis/go-redis/'+major+'")\n'+(declarations+'\n' if declarations else '')+'func example(ctx context.Context,c *redis.Client,key,tenant string,id int) {\n'+body+'\n}\n'
    CASES.append(dict(id=name,source_class='go-source',version=version,source=text.encode(),key_choices=list(keys),expected=expected))

go('literal-raw-get','c.Do(ctx,"GET","plain:key")',keys=[('"plain:key"',b'plain:key')],recognized=1,key_expressions=1,resolved_keys=1,access=[['RO','ACCESS']],expected_kinds=['string'])
go('typed-get-exact-second-version','c.Get(ctx,"plain:key")',version='v9.17.2',keys=[('"plain:key"',b'plain:key')],recognized=1,key_expressions=1,resolved_keys=1,access=[['RO','ACCESS']],expected_kinds=['string'])
go('same-file-constant','const local="hash:key"\nc.HSet(ctx,local,"field","value")',keys=[('"hash:key"',b'hash:key')],recognized=1,key_expressions=1,resolved_keys=1,access=[['RW','UPDATE']],expected_kinds=['hash'])
go('bounded-format','c.SAdd(ctx,fmt.Sprintf("set:%s",tenant),"member")',recognized=1,key_expressions=1,resolved_structures=1,format_segments=[{'kind':'literal','bytes_base64':'c2V0Og=='},{'kind':'parameter','name':'tenant','format':'%s'}],access=[['RW','INSERT']],expected_kinds=['set'])
go('dynamic-concatenation','c.Get(ctx,"prefix:"+key)',recognized=1,key_expressions=1,resolved_keys=0,gaps=['dynamic-concatenation'])
go('opaque-key-helper','c.Get(ctx,buildKey(key))',recognized=1,key_expressions=1,resolved_keys=0,gaps=['opaque-helper'])
go('runtime-namespace','c.Get(ctx,key)',recognized=1,key_expressions=1,resolved_keys=0,gaps=['runtime-namespace'])
go('unknown-client-major','c.Get(ctx,"k")',major='v8',version='v8.11.5',recognized=0,gaps=['unknown-client-adapter'])
go('unsupported-exact-v9-version','c.Get(ctx,"k")',version='v9.7.0',recognized=0,gaps=['unknown-client-adapter'])
go('core-after-pin','c.Do(ctx,"HEXPIRE","h",10,"FIELDS",1,"f")',recognized=0,gaps=['unsupported-core-command-after-pin'])
go('unadmitted-module-command','c.Do(ctx,"JSON.GET","doc:key")',recognized=0,gaps=['unsupported-module-command'])
go('incomplete-spec-fixed-primary','c.Do(ctx,"SORT","list:key","BY","object:*")',keys=[('"list:key"',b'list:key')],recognized=1,key_expressions=1,resolved_keys=1,access=[['RO','ACCESS']],gaps=['incomplete-key-specification','unknown-expected-value-kind'],omitted_unknown_key_positions=True)
go('set-variable-option','args:=[]interface{}{"SET","s:key","value",option()}\nc.Do(ctx,args...)',keys=[('"s:key"',b's:key')],recognized=1,key_expressions=1,resolved_keys=1,access=[None],gaps=['variable-access-mode'],expected_kinds=['string'])
go('set-get-branch','c.Do(ctx,"SET","s:key","value","GET")',keys=[('"s:key"',b's:key')],recognized=1,resolved_keys=1,access=[['RW','ACCESS','UPDATE']],expected_kinds=['string'])
go('set-no-get-branch','c.Do(ctx,"SET","s:key","value")',keys=[('"s:key"',b's:key')],recognized=1,resolved_keys=1,access=[['OW','UPDATE']],expected_kinds=['string'])
go('unknown-expected-kind','c.Del(ctx,"any:key")',keys=[('"any:key"',b'any:key')],recognized=1,resolved_keys=1,access=[['RM','DELETE']],gaps=['unknown-expected-value-kind'])
go('eval-explicit-keys','c.Eval(ctx,"return 1",[]string{"script:one","script:two"})',keys=[('"script:one"',b'script:one'),('"script:two"',b'script:two')],recognized=1,script_passthrough_rows=2,key_access_rows=0,expected_kind_rows=0)
go('fcall-explicit-keys','c.FCall(ctx,"neutral",[]string{"function:one"})',keys=[('"function:one"',b'function:one')],recognized=1,script_passthrough_rows=1,key_access_rows=0,expected_kind_rows=0)
go('opaque-script-key-list','c.Eval(ctx,"return 1",unknownKeys())',recognized=1,script_passthrough_rows=0,key_cardinality='unmeasured',gaps=['opaque-helper'])
go('script-run-unproved-wrapper','script.Run(ctx,c,[]string{"hidden"})',recognized=0,script_passthrough_rows=0,gaps=['opaque-helper'])
go('overlapping-prefix-families','c.Do(ctx,"FT.CREATE","idxA","ON","HASH","PREFIX",1,"doc:","SCHEMA","name","TEXT")\nc.Do(ctx,"FT.CREATE","idxB","ON","HASH","PREFIX",1,"doc:","SCHEMA","name","TEXT")\nc.Get(ctx,"doc:key")',keys=[('"doc:key"',b'doc:key')],recognized=3,native_declarations=2,resolved_keys=1,unique_family_bindings=0,gaps=['ambiguous-key-family'],note='Native index prefixes supply indexed-projection candidates only; no complete value model or deferred neutral manifest.')
go('ft-static-projection','c.Do(ctx,"FT.CREATE","index","ON","HASH","PREFIX",1,"doc:","SCHEMA","name","TEXT","weight","NUMERIC")',recognized=1,native_declarations=1,index_fields=2,complete_value_models=0)
go('ft-partial-atomic','c.Do(ctx,"FT.CREATE","index","ON","HASH","PREFIX",1,"doc:","SCHEMA","v","VECTOR","FLAT",6,"TYPE","FLOAT32","DIM",id,"DISTANCE_METRIC","COSINE")',recognized=1,native_declarations=0,index_fields=0,gaps=['runtime-namespace'])
go('ft-unsupported-shape-atomic','c.Do(ctx,"FT.CREATE","index","ON","HASH","SCHEMA","v","VECTOR","FLAT",99,"TYPE","FLOAT32")',recognized=1,native_declarations=0,index_fields=0,gaps=['unsupported-module-command'])
go('ts-create','c.Do(ctx,"TS.CREATE","temperature","RETENTION",1000,"LABELS","sensor","neutral")',recognized=1,native_declarations=1,native_kind='series',entry_fields=0)
go('ts-rule','c.Do(ctx,"TS.CREATERULE","temperature","temperature:avg","AGGREGATION","avg",60000)',recognized=1,native_declarations=1,native_kind='series-rule',resource_references=2,entry_fields=0)
go('ts-rule-partial-atomic','c.Do(ctx,"TS.CREATERULE","temperature",key,"AGGREGATION","avg",60000)',recognized=1,native_declarations=0,resource_references=0,gaps=['runtime-namespace'])
go('xgroup-create','c.Do(ctx,"XGROUP","CREATE","events","processors","0","MKSTREAM")',keys=[('"events"',b'events')],recognized=1,native_declarations=1,native_kind='stream-group',resolved_keys=1,access=[['RW','INSERT']],expected_kinds=['stream'],entry_fields=0)
go('array-mutation-not-literal-proof','args:=[]interface{}{"GET","initial"}\nargs[0]=key\nc.Do(ctx,args...)',recognized=0,resolved_keys=0,gaps=['opaque-helper'])
go('unlisted-client-method','c.CustomGet(ctx,"k")',recognized=0,gaps=['unknown-client-adapter'])
go('unknown-format-specifier','c.Get(ctx,fmt.Sprintf("key:%q",tenant))',recognized=1,resolved_keys=0,gaps=['opaque-helper'])
go('binary-safe-key','c.Get(ctx,"binary:\\x00\\xff")',keys=[('"binary:\\x00\\xff"',b'binary:\x00\xff')],recognized=1,resolved_keys=1,access=[['RO','ACCESS']],expected_kinds=['string'])
go('literal-hash-tags','c.Get(ctx,"a{tag}z")\nc.Get(ctx,"a{}{later}")\nc.Get(ctx,"a{{tag}z")\nc.Get(ctx,"a}b{yes}")',keys=[('"a{tag}z"',b'a{tag}z'),('"a{}{later}"',b'a{}{later}'),('"a{{tag}z"',b'a{{tag}z'),('"a}b{yes}"',b'a}b{yes}')],recognized=4,resolved_keys=4,hash_tag_bytes_base64=[base64.b64encode(x).decode() for x in [b'tag',b'a{}{later}',b'{tag',b'yes']],cluster_claims=0)
go('format-brace-uncertainty','c.Get(ctx,fmt.Sprintf("doc:{%s}",tenant))',recognized=1,resolved_structures=1,hash_tag_state='argument-dependent; parameter may contain closing brace',cluster_claims=0)
go('negative-script-count','c.Do(ctx,"EVAL","return 1",-1,"k")',recognized=1,script_passthrough_rows=0,gaps=['incomplete-key-specification'])
go('malformed-go','c.Get(ctx,"unterminated)',decision='refuse',boundary_reason='malformed-input',all_fact_rows=0)
go('depth-bound','x:='+('('*65)+'1'+(')'*65),decision='refuse',boundary_reason='resource-limit',all_fact_rows=0)
CASES.append(dict(id='input-byte-bound',source_class='go-source',version='v9.14.1',source=b'x'*262145,key_choices=[],expected=dict(decision='refuse',boundary_reason='resource-limit',all_fact_rows=0)))


# Prospective peer-review corrections, before any recognizer output. Public
# labels/source populations and the original43 authored fixture bytes stay exact.
go('receiver-shadowed-import','{ redis:=shadowFactory(); rc:=redis.NewClient(nil); rc.Get(ctx,"k") }',recognized=0,resolved_keys=0,gaps=['unknown-client-adapter'])
go('receiver-shadowed-local','{ c:=shadowFactory(); c.Get(ctx,"k") }',recognized=0,resolved_keys=0,gaps=['unknown-client-adapter'])
go('receiver-reassigned','c=clientFactory(); c.Get(ctx,"k")',recognized=0,resolved_keys=0,gaps=['unknown-client-adapter'])
go('receiver-ambiguous-field','var h Holder; h.Client.Get(ctx,"k")',declarations='type Holder struct { Client *redis.Client }\ntype Holder struct { Client *OtherClient }',recognized=0,resolved_keys=0,gaps=['unknown-client-adapter'])
go('receiver-unproved-copy','rc:=c; rc.Get(ctx,"k")',recognized=0,resolved_keys=0,gaps=['unknown-client-adapter'])
go('receiver-exact-constructor','rc:=redis.NewClient(nil); rc.Get(ctx,"k")',keys=[('"k"',b'k')],recognized=1,resolved_keys=1,access=[['RO','ACCESS']],expected_kinds=['string'])
go('ft-flat-vector-closed-positive','c.Do(ctx,"FT.CREATE","index","ON","HASH","PREFIX",1,"doc:","SCHEMA","v","VECTOR","FLAT",6,"TYPE","FLOAT32","DIM",3,"DISTANCE_METRIC","COSINE")',recognized=1,native_declarations=1,index_fields=1,complete_value_models=0)
go('ft-vector-distance-enum','c.Do(ctx,"FT.CREATE","index","ON","HASH","PREFIX",1,"doc:","SCHEMA","v","VECTOR","FLAT",6,"TYPE","FLOAT32","DIM",3,"DISTANCE_METRIC","OTHER")',recognized=1,native_declarations=0,index_fields=0,gaps=['unsupported-module-command'])
go('ft-vector-zero-dimension','c.Do(ctx,"FT.CREATE","index","ON","HASH","PREFIX",1,"doc:","SCHEMA","v","VECTOR","FLAT",6,"TYPE","FLOAT32","DIM",0,"DISTANCE_METRIC","COSINE")',recognized=1,native_declarations=0,index_fields=0,gaps=['unsupported-module-command'])
go('ft-vector-duplicate-attribute','c.Do(ctx,"FT.CREATE","index","ON","HASH","PREFIX",1,"doc:","SCHEMA","v","VECTOR","FLAT",6,"TYPE","FLOAT32","TYPE","FLOAT32","DIM",3)',recognized=1,native_declarations=0,index_fields=0,gaps=['unsupported-module-command'])
go('ft-vector-count-mismatch','c.Do(ctx,"FT.CREATE","index","ON","HASH","PREFIX",1,"doc:","SCHEMA","v","VECTOR","FLAT",4,"TYPE","FLOAT32","DIM",3,"DISTANCE_METRIC","COSINE")',recognized=1,native_declarations=0,index_fields=0,gaps=['unsupported-module-command'])
go('ft-hnsw-outside-narrow-subset','c.Do(ctx,"FT.CREATE","index","ON","HASH","PREFIX",1,"doc:","SCHEMA","v","VECTOR","HNSW",6,"TYPE","FLOAT32","DIM",3,"DISTANCE_METRIC","COSINE")',recognized=1,native_declarations=0,index_fields=0,gaps=['unsupported-module-command'])
go('ts-aggregator-outside-subset','c.Do(ctx,"TS.CREATERULE","source","destination","AGGREGATION","max",1000)',recognized=1,native_declarations=0,resource_references=0,gaps=['unsupported-module-command'])
go('ts-zero-bucket','c.Do(ctx,"TS.CREATERULE","source","destination","AGGREGATION","avg",0)',recognized=1,native_declarations=0,resource_references=0,gaps=['unsupported-module-command'])
go('ts-duplicate-label','c.Do(ctx,"TS.CREATE","series","LABELS","sensor","a","sensor","b")',recognized=1,native_declarations=0,gaps=['unsupported-module-command'])
go('ts-alignment-outside-subset','c.Do(ctx,"TS.CREATERULE","source","destination","AGGREGATION","avg",1000,42)',recognized=1,native_declarations=0,resource_references=0,gaps=['unsupported-module-command'])
go('ts-negative-retention','c.Do(ctx,"TS.CREATE","series","RETENTION",-1)',recognized=1,native_declarations=0,gaps=['unsupported-module-command'])

def acl(name,raw,principals,patterns,**expected):
    CASES.append(dict(id=name,source_class='acl',version=None,source=raw,key_choices=[],principal_choices=principals,pattern_choices=patterns,expected=expected))

distinct=hashlib.sha256(b't281-neutral-distinct-input-only-credential').hexdigest().encode()
acl('acl-adjacent-credentials',b'user neutral on >'+distinct+b' #'+hashlib.sha256(distinct).hexdigest().encode()+b' ~safe:* +@all\n',['neutral'],['~safe:*'],principal_pattern_pairs=1,credential_token_output=0,distinct_credential_payload_output=0,line_context_output=0,grant_output=0)
acl('acl-selector-permissions',b'user neutral on nopass (%R~read:* +get) (%W~write:* +set) %RW~both:*\n',['neutral'],['%R~read:*','%W~write:*','%RW~both:*'],principal_pattern_pairs=3,credential_token_output=0,line_context_output=0,grant_output=0)
acl('acl-pattern-looking-secret',b'user neutral on >~'+distinct+b' ~safe:*\n',['neutral'],['~safe:*'],principal_pattern_pairs=1,credential_token_output=0,distinct_credential_payload_output=0)
acl('acl-malformed-quote',b'user neutral on >"unterminated ~safe:*\n',[],[],decision='refuse',boundary_reason='malformed-input',all_fact_rows=0)
acl('acl-token-bound',b'user neutral on >'+b'x'*4097+b' ~safe:*\n',[],[],decision='refuse',boundary_reason='resource-limit',all_fact_rows=0)

def main():
    out=[];target=ROOT/'fixtures/controls';target.mkdir(parents=True,exist_ok=True)
    for original in CASES:
        c=original.copy();raw=c.pop('source');ext='.acl' if c['source_class']=='acl' else '.go';path=target/(c['id']+ext);path.write_bytes(raw)
        c.update(source_path=str(path.relative_to(ROOT)),source_bytes=len(raw),source_sha256=hashlib.sha256(raw).hexdigest(),population='authored-neutral; separate from public denominators')
        keys=[]
        for token,value in c.pop('key_choices'):
            at=raw.index(token.encode());keys.append(dict(source_token=span(raw,at,at+len(token.encode())),bytes_base64=base64.b64encode(value).decode()))
        c['key_labels']=keys
        for name,label_name in [('principal_choices','principal_tokens'),('pattern_choices','pattern_tokens')]:
            chosen=c.pop(name,[]);safe=[]
            for value in chosen:
                at=raw.index(value.encode());safe.append(dict(token=value,**span(raw,at,at+len(value.encode()))))
            if c['source_class']=='acl':c[label_name]=safe
        out.append(c)
    admission=[dict(id='data-bearing-role',source_path='fixtures/controls/literal-raw-get.go',source_class='rdb-data',expected=dict(decision='refuse',boundary_reason='data-bearing-input',all_fact_rows=0),note='Inert text assigned an explicitly prohibited input role; no real RDB/AOF data is created or read.'),dict(id='missing-input',source_path='fixtures/controls/intentionally-missing.go',source_class='go-source',expected=dict(decision='refuse',boundary_reason='artifact-missing',all_fact_rows=0))]
    save('controls.json',dict(format='t281-neutral-controls-v1',recognizer_runs=0,cases=out,admission_controls=admission,scope='Inert authored source/ACL inputs and source-literal expectations only. Generated credential tokens remain in input ACL fixtures; labels contain only allowed safe tokens, no secret/context/grants. No Redis/application execution.'))
    print('authored neutral controls',len(out),'no candidate runs')

if __name__=='__main__':main()
