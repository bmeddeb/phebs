"""Serialize hand-labelled sampled migration statements; no SQL recognition."""
import hashlib
import json
from author_pg_labels import Source,ROOT,COMMIT

# Each tuple: full statement lines, explicit Goose branch, syntax class,
# predicate/entity literal/token line (or a frozen refusal reason), extra data.
MIGRATIONS={
"20240606145243_v0_31_0.sql":[
(3,3,"up","create-enum","MIGRATION_CREATES_TYPE",'"LimitResource"',3,{}),
(5,5,"up","create-enum","MIGRATION_CREATES_TYPE",'"TenantResourceLimitAlertType"',5,{}),
(7,7,"up","alter-add-column","MIGRATION_ADDS_COLUMN",'"enableTenantResourceLimitAlerts"',7,dict(relation="TenantAlertingSettings",columns=["enableTenantResourceLimitAlerts"])),
(9,9,"up","create-table","MIGRATION_CREATES_TABLE",'"TenantResourceLimit"',9,{}),
(11,11,"up","create-index","MIGRATION_CREATES_INDEX",'"TenantResourceLimit_id_key"',11,dict(relation="TenantResourceLimit",columns=["id"])),
(13,13,"up","create-index","MIGRATION_CREATES_INDEX",'"TenantResourceLimit_tenantId_resource_key"',13,dict(relation="TenantResourceLimit",columns=["tenantId","resource"])),
(15,15,"up","create-table","MIGRATION_CREATES_TABLE",'"TenantResourceLimitAlert"',15,{}),
(17,17,"up","create-index","MIGRATION_CREATES_INDEX",'"TenantResourceLimitAlert_id_key"',17,dict(relation="TenantResourceLimitAlert",columns=["id"])),
],
"20240331162333_v0_18_1.sql":[
(3,6,"up","create-table","MIGRATION_CREATES_TABLE",'"WorkerSemaphore"',3,{}),
(9,9,"up","create-index","MIGRATION_CREATES_INDEX",'"WorkerSemaphore_workerId_key"',9,dict(relation="WorkerSemaphore",columns=["workerId"])),
(12,12,"up","alter-add-fk","MIGRATION_ADDS_FOREIGN_KEY",'"WorkerSemaphore_workerId_fkey"',12,dict(relation="WorkerSemaphore",columns=["workerId"],target_relation="Worker",target_columns=["id"])),
],
"20251216190909_v1_0_61.sql":[
(3,3,"up","drop-table","MIGRATION_DROPS_TABLE","v1_payload_wal",3,{}),
(4,4,"up","drop-type","MIGRATION_DROPS_TYPE","v1_payload_wal_operation",4,{}),
(5,5,"up","drop-table","MIGRATION_DROPS_TABLE","v1_payload_cutover_queue_item",5,{}),
(6,6,"up","drop-function","unsupported-statement","",6,{}),
(7,7,"up","drop-function","unsupported-statement","",7,{}),
(12,12,"down","create-enum","MIGRATION_CREATES_TYPE","v1_payload_wal_operation",12,{}),
(14,26,"down","create-table","MIGRATION_CREATES_TABLE","v1_payload_wal",14,{}),
(28,28,"down","create-index","MIGRATION_CREATES_INDEX","v1_payload_wal_payload_lookup_idx",28,dict(relation="v1_payload_wal",columns=["payload_id","payload_inserted_at","payload_type","tenant_id"])),
(29,29,"down","create-index","MIGRATION_CREATES_INDEX","v1_payload_wal_poll_idx",29,dict(relation="v1_payload_wal",columns=["tenant_id","offload_at"])),
(31,31,"down","select-function-call","unsupported-statement","",31,{}),
(33,41,"down","create-table","MIGRATION_CREATES_TABLE","v1_payload_cutover_queue_item",33,{}),
(43,43,"down","select-function-call","unsupported-statement","",43,{}),
(45,72,"down","create-function","procedural-sql","",45,{}),
(74,102,"down","create-function","procedural-sql","",74,{}),
],
"20250509123031_v1_0_18.sql":[
(3,4,"up","alter-add-column","MIGRATION_ADDS_COLUMN","retry_count",4,dict(relation="v1_log_line",columns=["retry_count"])),
(9,10,"down","alter-drop-column","MIGRATION_DROPS_COLUMN","retry_count",10,dict(relation="v1_log_line",columns=["retry_count"])),
],
"20260316185928_v1_0_90.sql":[
(3,3,"up","create-enum","MIGRATION_CREATES_TYPE","v1_otel_span_kind",3,{}),
(4,4,"up","create-enum","MIGRATION_CREATES_TYPE","v1_otel_status_code",4,{}),
(6,26,"up","create-table","MIGRATION_CREATES_TABLE","v1_otel_trace",6,{}),
(28,30,"up","create-index","MIGRATION_CREATES_INDEX","idx_v1_otel_trace_task_lookup",28,dict(relation="v1_otel_trace",columns=["tenant_id","task_run_external_id"])),
(32,34,"up","create-index","MIGRATION_CREATES_INDEX","idx_v1_otel_trace_workflow_lookup",32,dict(relation="v1_otel_trace",columns=["tenant_id","workflow_run_external_id"])),
(36,36,"up","select-function-call","unsupported-statement","",36,{}),
(37,37,"up","select-function-call","unsupported-statement","",37,{}),
(39,46,"up","create-table","MIGRATION_CREATES_TABLE","v1_otel_trace_lookup_table",39,{}),
(48,48,"up","select-function-call","unsupported-statement","",48,{}),
(49,49,"up","select-function-call","unsupported-statement","",49,{}),
(52,52,"down","drop-table","MIGRATION_DROPS_TABLE","v1_otel_trace_lookup_table",52,{}),
(53,53,"down","drop-table","MIGRATION_DROPS_TABLE","v1_otel_trace",53,{}),
(54,54,"down","drop-type","MIGRATION_DROPS_TYPE","v1_otel_status_code",54,{}),
(55,55,"down","drop-type","MIGRATION_DROPS_TYPE","v1_otel_span_kind",55,{}),
],
"20260707141853_v1_0_125.sql":[
(3,3,"up","create-index-concurrently","MIGRATION_CREATES_INDEX","ix_user_session_cre_at_exp_at",3,dict(relation="UserSession",columns=["createdAt"],note="INCLUDE expiresAt remains source modifier, not ordered index key.")),
(7,7,"down","drop-index-concurrently","MIGRATION_DROPS_INDEX","ix_user_session_cre_at_exp_at",7,{}),
],
}


def main():
    for filename,choices in MIGRATIONS.items():
        s=Source("cmd/hatchet-migrate/migrate/migrations/"+filename)
        statements=[]
        for first,last,direction,syntax,predicate,literal,line,extra in choices:
            disposition="migration-event" if predicate.startswith("MIGRATION_") else predicate
            events=[]
            if disposition=="migration-event":
                events.append(dict(predicate=predicate,name=literal.strip('"'),quoted=literal.startswith('"'),**s.literal(line,literal),**extra))
            note="Explicit source history occurrence in Goose "+direction+" branch; never execute, fold branches or emit DECLARES_* objects. Stable identity includes source path, statement byte offset and predicate."
            if disposition!="migration-event":note+=" Function/procedural syntax or partition-creation calls are frozen gaps, never evidence of dynamically created tables or current shape."
            statements.append(dict(id="migration-L"+str(first),**s.line_span(first,last),syntax_class=syntax,expected_disposition=disposition,direction=direction,objects=[],sites=[],events=events,note=note))
        result=dict(format="t261-source-labels-v1",dialect="postgresql",repository="hatchet-dev/hatchet",commit=COMMIT,source_path=s.path,source_sha256=hashlib.sha256(s.raw).hexdigest(),plane="sql-migration-event",statements=statements,inventory_notes=["Entire selected file, both Goose branches, labelled before parser execution. No accuracy inference to 208 unsampled migration files.","Lane derives from committed sqlc PostgreSQL engine metadata under explicit spike migration-root admission, never grammar sniffing."])
        path=ROOT/"labels"/("hatchet-migration-"+filename[:-4]+".json")
        path.write_text(json.dumps(result,indent=2,ensure_ascii=False)+"\n")
        print(json.dumps(dict(path=str(path),statements=len(statements),events=sum(len(x["events"]) for x in statements),sha256=hashlib.sha256(path.read_bytes()).hexdigest())))


if __name__=="__main__":main()
