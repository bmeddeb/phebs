"""Serialize the complete independently source-reviewed native-site choices."""
from label_literals import blob,metadata,literal,save

SITES=[
    ('quickstart/eino_assistant/cmd/knowledgeindexing/main.go',167,180,152,169,176,'runtime-format',None),
    ('quickstart/eino_assistant/pkg/redis/redis.go',91,104,76,93,100,'same-file-constant-format','eino:doc:vector_index'),
]

def main():
    rows=[]
    module='quickstart/eino_assistant/go.mod';module_meta=metadata('eino',module)
    for path,line,call,name_line,prefix_line,dimension_line,name_shape,name in SITES:
        raw=blob('eino',path)
        item=dict(metadata('eino',path),command='FT.CREATE',kind='index',command_token=literal(raw,line,'"FT.CREATE"'),invocation_token=literal(raw,call,'Do'),vector_shape='same-file ordered []interface{} expanded into go-redis Do',module_path=module,module_sha256=module_meta['source_sha256'],actual_client='github.com/redis/go-redis/v9',actual_version='v9.7.0',expected_disposition='gap',expected_reason='unknown-client-adapter',resource_name_shape=name_shape,resource_name_value=name,resource_name_evidence=literal(raw,name_line,'fmt.Sprintf'),prefix_argument=literal(raw,prefix_line,'config.RedisKeyPrefix' if name is None else 'RedisPrefix'),runtime_dimension_argument=literal(raw,dimension_line,'config.Dimension'),source_only_atomic_shape_complete=False,structural_gap_independent_of_version='Runtime dimension cannot be evaluated from the command-vector construction. The first site additionally has runtime name/prefix. No partial index/schema facts may publish.',expected_declaration_facts=0)
        rows.append(item)
    save('labels/native-declarations.json',dict(format='t281-native-source-labels-v1',scope='Complete known named declaration census across the frozen Go-source scope; literal/name search plus full source review of every matched file and Redis Do/NewCmd/Process shape. Script bodies are excluded, not claimed declaration-free.',known_native_sites=2,source_identity_static_sites=1,source_identity_dynamic_sites=1,source_atomic_complete_sites=0,admitted_version_sites=0,index_sites=rows,series_sites=[],series_rule_sites=[],stream_group_sites=[],unknown_dynamic_raw_command_sites=[],unknown_dynamic_raw_command_site_count=0,raw_redis_calls=6,raw_nonredis_Do_negative_sites=5,coverage_limit='Unknown Redis operations hidden by unproved application/script helpers are separately labelled opaque-helper usage sites; this named-site census does not discover or infer their declaration contents.',recognizer_runs=0))
    print('source-only native labels:2 known index sites;0 complete/admitted declarations')

if __name__=='__main__':main()
