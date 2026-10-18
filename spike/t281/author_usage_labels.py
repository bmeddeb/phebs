"""Serialize source-reviewed method/argument choices, never discover grammar.

The complete seven-file purposive sample was fixed before this label pass.
Every line, method, argument and disposition below is an explicit author choice.
No Go parser, Redis recognizer, command interpreter or candidate output is used.
"""
from label_literals import blob,metadata,literal,save

RDB='internal/rdb/rdb.go'
INSPECT='internal/rdb/inspect.go'
MEMORY='flow/agent/react/memory_example/memory/redis.go'
MAIN='quickstart/eino_assistant/cmd/knowledgeindexing/main.go'
PKG='quickstart/eino_assistant/pkg/redis/redis.go'
RETRIEVER='quickstart/eino_assistant/eino/einoagent/retriever.go'
INDEXER='quickstart/eino_assistant/eino/knowledgeindexing/indexer.go'

# line, exact method name, exact core command, key expression (None=no key spec)
CORE={
 RDB:[(62,'Ping','PING',None),
      (118,'SAdd','SADD','base.AllQueues'),(205,'SAdd','SADD','base.AllQueues'),(295,'SAdd','SADD','base.AllQueues'),(653,'SAdd','SADD','base.AllQueues'),(718,'SAdd','SADD','base.AllQueues'),(778,'SAdd','SADD','base.AllQueues'),(840,'SAdd','SADD','base.AllQueues'),
      (1137,'SMembers','SMEMBERS','base.AllGroups(qname)'),(1312,'ZScore','ZSCORE','base.AllAggregationSets(qname)'),(1477,'ZAddXX','ZADD','base.LeaseKey(qname)'),
      (1519,'ZAdd','ZADD','base.AllServers'),(1522,'ZAdd','ZADD','base.AllWorkers'),(1541,'ZRem','ZREM','base.AllServers'),(1544,'ZRem','ZREM','base.AllWorkers'),(1575,'ZAdd','ZADD','base.AllSchedulers'),(1587,'ZRem','ZREM','base.AllSchedulers'),
      (1590,'Del','DEL','key'),(1600,'Subscribe','SUBSCRIBE',None),(1614,'Publish','PUBLISH',None),(1656,'Del','DEL','key'),(1667,'HSet','HSET','taskKey')],
 INSPECT:[(22,'SMembers','SMEMBERS','base.AllQueues'),(422,'Info','INFO',None),(431,'ClusterInfo','CLUSTER INFO',None),(822,'SIsMember','SISMEMBER','base.AllQueues'),(1934,'SRem','SREM','base.AllQueues'),(1968,'Get','GET','key'),(2002,'HVals','HVALS','key'),(2037,'LRange','LRANGE','key'),(2055,'ZRevRangeWithScores','ZREVRANGE','key'),(2077,'SetNX','SETNX','key'),(2090,'Del','DEL','key'),(2103,'ClusterKeySlot','CLUSTER KEYSLOT',None),(2112,'ClusterSlots','CLUSTER SLOTS',None)],
 MEMORY:[(43,'Set','SET','sessionID'),(48,'Get','GET','sessionID')],
}
SCRIPT_RDB={
 'Run':[66,74,217,224,370,1104,1295,1414,1446],
 'runScriptWithErrorCode':[132,311,669,736,792,856,1256],
 'runScript':[477,479,587,589,618,948,1050,1347,1372,1525,1547,1579,1648],
}
SCRIPT_INSPECT=[171,355,400,511,597,698,850,980,1058,1111,1204,1259,1351,1416,1497,1581,1687,1731,1772,1924,1958,1992,2027]
# Frozen exact core-spec vocabulary. Expected kind is a separate authored table.
FLAGS={'SADD':['RW','INSERT'],'SMEMBERS':['RO','ACCESS'],'ZSCORE':['RO','ACCESS'],'ZADD':['RW','UPDATE'],'ZREM':['RW','DELETE'],'DEL':['RM','DELETE'],'HSET':['RW','UPDATE'],'SISMEMBER':['RO'],'SREM':['RW','DELETE'],'GET':['RO','ACCESS'],'HVALS':['RO','ACCESS'],'LRANGE':['RO','ACCESS'],'ZREVRANGE':['RO','ACCESS'],'SETNX':['OW','INSERT'],'SET':['OW','UPDATE']}
KINDS={'SADD':'set','SMEMBERS':'set','SISMEMBER':'set','SREM':'set','ZADD':'sorted-set','ZSCORE':'sorted-set','ZREM':'sorted-set','ZREVRANGE':'sorted-set','HSET':'hash','HVALS':'hash','GET':'string','SET':'string','SETNX':'string','LRANGE':'list'}

def main():
    files=[]
    for pop,path in [('asynq',RDB),('asynq',INSPECT),('eino',MEMORY),('eino',MAIN),('eino',PKG),('eino',RETRIEVER),('eino',INDEXER)]:
        raw=blob(pop,path);nested=pop=='eino' and path.startswith('quickstart/')
        module='quickstart/eino_assistant/go.mod' if nested else 'go.mod'
        version='v9.7.0' if nested else 'v9.14.1' if pop=='asynq' else 'v9.17.2'
        item=dict(metadata(pop,path),population=pop,module_path=module,module_sha256=metadata(pop,module)['source_sha256'],actual_client='github.com/redis/go-redis/v9',actual_version=version,sites=[])
        def site(line,method,shape,**extra):
            value=dict(id=f'L{line}-{method}',method=method,method_token=literal(raw,line,method),shape=shape,**extra);item['sites'].append(value);return value
        for line,method,command,key in CORE.get(path,[]):
            row=site(line,method,'admitted-go-redis-core-api',expected_command=command,expected_command_recognized=True,expected_unresolved=None,keys=[])
            if key:
                row['keys'].append(dict(go_argument_index=1,expression_span=literal(raw,line,key),expected_resolution='unresolved',expected_reason='opaque-helper' if key.startswith('base.') else 'runtime-namespace',expected_access_flags=FLAGS[command],expected_kind=KINDS.get(command),expected_kind_reason=None if command in KINDS else 'unknown-expected-value-kind',expected_binding='unresolved',note='Imported helper/constant is outside same-file constant evaluation.' if key.startswith('base.') else 'Parameter/local runtime spelling is not followed across helper/iteration/function boundaries.'))
            if method=='SetNX':row['branch']='literal expiration0 maps to SETNX, not SET NX'
            if method=='Set':row['branch']='Set API has no GET branch; only expiry options vary; flags OW/UPDATE under documented SET variable-flags rule'
            if method=='ClusterKeySlot':row['note']='Routing operand is not a Redis data-key specification in the pinned core metadata; no usage key fact.'
        if path in (RDB,INSPECT):
            choices=SCRIPT_RDB if path==RDB else {'Run':SCRIPT_INSPECT}
            for method,lines in choices.items():
                for line in lines:site(line,method,'opaque-script-wrapper',expected_command=None,expected_command_recognized=False,expected_unresolved='opaque-helper',keys=[],key_cardinality='unmeasured behind wrapper; not zero keys',expected_script_passthrough_rows=0,note='No Lua body analysis. Script.Run may choose EVALSHA/EVAL; application wrappers are unproved. No inherited access/kind or key-cardinality inference.')
        if path in (MAIN,PKG):
            for line,method in ([(148,'Ping'),(155,'Do'),(180,'Do'),(185,'Do')] if path==MAIN else [(72,'Ping'),(79,'Do'),(104,'Do'),(109,'Do')]):
                row=site(line,method,'unsupported-exact-client-version',expected_command=None,expected_command_recognized=False,expected_unresolved='unknown-client-adapter',keys=[],note='Nearest nested module is v9.7.0; root v9.17.2 must not be inherited.')
                if method=='Do':row['source_command_intent']='FT.CREATE' if line in (180,104) else 'FT.INFO';row['vector_shape']='same-file-array' if line in (180,104) else 'literal-command'
        if path in (RETRIEVER,INDEXER):
            line,method=(76,'NewRetriever') if path==RETRIEVER else (81,'NewIndexer')
            site(line,method,'unproved-client-library-wrapper',expected_command=None,expected_command_recognized=False,expected_unresolved='opaque-helper',keys=[],note='The redis identifier denotes an Eino extension package, not go-redis. No method-to-command or model-class implementation inferred.')
        item['sites'].sort(key=lambda s:s['method_token']['start']);files.append(item)
    sites=[s for f in files for s in f['sites']];keys=[k for s in sites for k in s['keys']]
    assert len(sites)==99 and len(keys)==30
    out=dict(format='t281-usage-source-labels-v1',scope='Complete command/frontier census in seven prospectively fixed files; no generalization to other Go files, hidden Lua/script operations or runtime Redis',files=files,denominators=dict(command_or_unresolved_sites=99,admitted_core_api_sites=37,opaque_script_wrapper_sites=52,unknown_client_version_sites=8,unproved_extension_wrapper_sites=2,known_key_argument_expressions=30,expected_resolved_key_expressions=0,provable_access_mode_opportunities=30,expected_mode_resolved=30,expected_kind_opportunities=30,expected_kind_known=27,unknown_expected_kind=3,explicit_script_key_passthrough_opportunities=0,unmeasured_script_wrapper_sites=52,uniquely_bindable_spelling_or_family_opportunities=0),recognizer_runs=0,quality_qualification='Source expectations only, not measured candidate success. Resolved access/kind describes the admitted command argument position even when its actual key bytes remain unproved; no concrete key usage fact is manufactured.')
    save('labels/public-usage.json',out);print('source-only usage labels:99sites,30known-key-expression opportunities; parser runs0')

if __name__=='__main__':main()
