"""Serialize manually selected exact PostgreSQL query sites; no SQL parser."""
import hashlib
import json
from author_pg_labels import Source,ROOT,COMMIT

# File -> (authored query name, exact source block lines, syntax, explicit sites).
# A site is (line, exact identifier token, physical relation access).
QUERIES={
"ticker.sql":[
("IsTenantAlertActive",1,23,"select-cte",[(6,'"TenantAlertingSettings"',"r"),(21,'"TenantAlertingSettings"',"r")]),
("CreateTicker",25,30,"insert-values",[(27,'"Ticker"',"w")]),
("ListNewlyStaleTickers",32,40,"select",[(35,'"Ticker"',"r")]),
("ListActiveTickers",42,50,"select",[(45,'"Ticker"',"r")]),
("SetTickersInactive",52,60,"update",[(54,'"Ticker"',"rw")]),
("ListTickers",62,76,"select",[(66,'"Ticker"',"r")]),
("DeactivateTicker",78,85,"update",[(80,'"Ticker"',"rw")]),
("UpdateTicker",87,94,"update",[(89,'"Ticker"',"rw")]),
("PollCronSchedules",96,160,"update-cte",[(107,'"WorkflowTriggerCronRef"',"r"),(109,'"WorkflowTriggers"',"r"),(111,'"WorkflowVersion"',"r"),(113,'"Tenant"',"r"),(120,'"Ticker"',"r"),(132,'"WorkflowVersion"',"r"),(150,'"WorkflowTriggerCronRef"',"rw")]),
("PollScheduledWorkflows",162,230,"update-cte",[(171,'"WorkflowVersion"',"r"),(182,'"WorkflowTriggerScheduledRef"',"r"),(184,'"WorkflowVersion"',"r"),(186,'"Workflow"',"r"),(188,'"Tenant"',"r"),(199,'"Ticker"',"r"),(205,'"WorkflowRunTriggeredBy"',"r"),(214,'"WorkflowTriggerScheduledRef"',"r"),(221,'"WorkflowTriggerScheduledRef"',"rw"),(226,'"WorkflowVersion"',"r"),(227,'"Workflow"',"r")]),
("PollTenantAlerts",232,279,"update-cte",[(238,'"TenantAlertingSettings"',"r"),(240,'"Tenant"',"r"),(254,'"WorkflowRun"',"r"),(270,'"TenantAlertingSettings"',"rw")]),
("PollExpiringTokens",282,315,"update-cte",[(287,'"APIToken"',"r"),(289,'"Tenant"',"r"),(304,'"APIToken"',"rw")]),
("PollTenantResourceLimitAlerts",317,392,"insert-select-cte",[(333,'"TenantResourceLimit"',"r"),(335,'"TenantAlertingSettings"',"r"),(339,'"Tenant"',"r"),(359,'"TenantResourceLimitAlert"',"r"),(367,'"TenantResourceLimitAlert"',"w")]),
],
"workflow_schedules.sql":[
("ListScheduledWorkflows",1,42,"select-join",[(12,'"WorkflowTriggerScheduledRef"',"r"),(13,'"WorkflowVersion"',"r"),(14,'"Workflow"',"r"),(15,'"WorkflowRunTriggeredBy"',"r"),(16,'"WorkflowRun"',"r")]),
("CountScheduledWorkflows",44,66,"select-join",[(46,'"WorkflowTriggerScheduledRef"',"r"),(47,'"WorkflowVersion"',"r"),(48,'"Workflow"',"r"),(49,'"WorkflowRunTriggeredBy"',"r"),(50,'"WorkflowRun"',"r")]),
("UpdateScheduledWorkflow",68,72,"update",[(69,'"WorkflowTriggerScheduledRef"',"rw")]),
("DeleteScheduledWorkflow",74,77,"delete",[(75,'"WorkflowTriggerScheduledRef"',"rw")]),
("GetScheduledWorkflowMetaByIds",79,93,"select-subquery",[(85,'"WorkflowRunTriggeredBy"',"r"),(88,'"WorkflowTriggerScheduledRef"',"r"),(89,'"WorkflowVersion"',"r"),(90,'"Workflow"',"r")]),
("BulkDeleteScheduledWorkflows",95,104,"delete-using",[(96,'"WorkflowTriggerScheduledRef"',"rw"),(97,'"WorkflowVersion"',"r"),(97,'"Workflow"',"r")]),
("BulkUpdateScheduledWorkflows",106,129,"update-cte",[(115,'"WorkflowTriggerScheduledRef"',"rw"),(117,'"WorkflowVersion"',"r"),(117,'"Workflow"',"r"),(126,'"WorkflowRunTriggeredBy"',"r")]),
("CreateWorkflowTriggerScheduledRefForWorkflow",131,161,"insert-values-cte",[(133,'"WorkflowVersion"',"r"),(140,'"WorkflowTriggers"',"r"),(145,'"WorkflowTriggerScheduledRef"',"w")]),
("ListCronWorkflows",163,213,"select-cte",[(170,'"WorkflowVersion"',"r"),(172,'"Workflow"',"r"),(190,'"WorkflowTriggers"',"r"),(192,'"WorkflowTriggerCronRef"',"r"),(194,'"Workflow"',"r")]),
("CountCronWorkflows",215,249,"select-cte",[(222,'"WorkflowVersion"',"r"),(224,'"Workflow"',"r"),(235,'"WorkflowTriggers"',"r"),(237,'"WorkflowTriggerCronRef"',"r"),(239,'"Workflow"',"r")]),
("DeleteWorkflowTriggerCronRef",251,254,"delete",[(252,'"WorkflowTriggerCronRef"',"rw")]),
],
"sync.sql":[
("SyncUpsertTenant",1,42,"insert-on-conflict",[(2,'"Tenant"',"rw")]),
("SyncUpdateTenant",44,53,"update",[(45,'"Tenant"',"rw")]),
("SyncSoftDeleteTenant",55,60,"update",[(56,'"Tenant"',"rw")]),
("SyncListUsers",62,69,"select",[(67,'"User"',"r")]),
("SyncUpsertUser",71,91,"insert-on-conflict",[(72,'"User"',"rw")]),
("SyncUpsertTenantInvite",93,121,"insert-on-conflict",[(94,'"TenantInviteLink"',"rw")]),
("SyncUpdateTenantInvite",123,130,"update",[(124,'"TenantInviteLink"',"rw")]),
("SyncUpsertTenantMember",132,150,"insert-on-conflict",[(133,'"TenantMember"',"rw")]),
("SyncUpdateTenantMember",152,158,"update",[(153,'"TenantMember"',"rw")]),
("SyncUpsertTenantAlertingSettings",160,166,"insert-on-conflict",[(161,'"TenantAlertingSettings"',"rw")]),
],
"pg_health.sql":[
("CheckPGStatStatementsEnabled",1,2,"select",[(2,"pg_available_extensions","r")]),
("CheckBloat",4,27,"select",[(18,"pg_stat_user_tables","r")]),
("CheckLongRunningQueries",29,46,"select",[(39,"pg_stat_activity","r")]),
("CheckQueryCaches",48,67,"select",[(62,"pg_statio_user_tables","r")]),
("LongRunningVacuum",69,95,"select-join",[(90,"pg_stat_progress_vacuum","r"),(91,"pg_stat_activity","r")]),
],
"sleep.sql":[
("CreateDurableSleep",1,18,"insert-select-cte",[(11,"v1_durable_sleep","w")]),
("PopDurableSleep",20,39,"delete-cte",[(25,"v1_durable_sleep","r"),(36,"v1_durable_sleep","rw")]),
],
"interval_settings.sql":[
("ListIntervalsByOperationId",1,7,"select",[(5,"v1_operation_interval_settings","r")]),
("UpsertInterval",9,22,"insert-on-conflict",[(10,"v1_operation_interval_settings","rw")]),
("ReadInterval",24,32,"select",[(28,"v1_operation_interval_settings","r")]),
],
}


def main():
    access=dict(r="read",w="write",rw="read-write")
    for filename,queries in QUERIES.items():
        s=Source("pkg/repository/sqlcv1/"+filename)
        statements=[]
        for name,first,last,syntax,choices in queries:
            assert ("-- name: "+name+" ").encode() in s.lines[first-1]
            sites=[]
            for line,literal,mode in choices:
                sites.append(dict(name=literal.strip('"'),quoted=literal.startswith('"'),**s.literal(line,literal),access=access[mode],expected_binding="unique",declaration_relation=literal.strip('"')))
            note="Each physical relation-literal occurrence retained; CTE/derived aliases, sqlc macros, functions and column qualifiers are not additional table sites. Explicit SELECT sources are read, UPDATE/DELETE target and conflict-update target read-write; plain INSERT target write. No query executes."
            if filename=="pg_health.sql":note+=" Unique binding is only to explicitly marked committed sqlc tooling stubs; no pg_catalog/live-server identity is inferred."
            statements.append(dict(id=name,**s.line_span(first,last),syntax_class=syntax,expected_disposition="query",objects=[],sites=sites,note=note))
        result=dict(format="t261-source-labels-v1",dialect="postgresql",repository="hatchet-dev/hatchet",commit=COMMIT,source_path=s.path,source_sha256=hashlib.sha256(s.raw).hexdigest(),plane="sql-query",statements=statements,inventory_notes=["Entire selected file labelled under preregistered metadata-only stratified sample; no inference to the 30 unsampled query files.","Exactly one committed sqlc manifest selects all five declarations inputs; names stay quoted/unquoted dialect-specific. Generated sql.go supplies no primary usage."])
        path=ROOT/"labels"/("hatchet-query-"+filename[:-4]+".json")
        path.write_text(json.dumps(result,indent=2,ensure_ascii=False)+"\n")
        print(json.dumps(dict(path=str(path),queries=len(statements),sites=sum(len(x["sites"]) for x in statements),sha256=hashlib.sha256(path.read_bytes()).hexdigest())))


if __name__=="__main__":main()
