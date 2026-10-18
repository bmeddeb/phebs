"""Serialize manually chosen source lines/literals. This is not a SQL parser.

All semantic names, members, kinds and statement boundaries below were authored
from the pinned source before parser execution. No syntax discovery is allowed.
"""
from pathlib import Path
import hashlib
import json

ROOT = Path(__file__).resolve().parent
RAW = (ROOT / "corpus/monitor/schema.sql").read_bytes()
LINES = RAW.splitlines(keepends=True)
OFFSETS = [0]
for line in LINES:
    OFFSETS.append(OFFSETS[-1] + len(line))


def span(first, last=None):
    last = first if last is None else last
    start, end = OFFSETS[first - 1], OFFSETS[last]
    return dict(start=start, end=end, sha256=hashlib.sha256(RAW[start:end]).hexdigest())


def obj(kind, name, line, relation=None, **extra):
    quoted = not name.startswith("@")
    if quoted:
        literal = ("`" + name + "`").encode()
    else:
        literal = {"primary_key": b"PRIMARY KEY", "unique": b"UNIQUE", "check": b"CHECK", "foreign_key": b"FOREIGN KEY"}[kind]
    assert literal in LINES[line-1], (line,name)
    start = OFFSETS[line-1] + LINES[line-1].index(literal)
    end = start + len(literal)
    if not quoted:
        name = f"@{start}"
    result = dict(kind=kind, name=name, quoted=quoted, start=start, end=end, sha256=hashlib.sha256(RAW[start:end]).hexdigest())
    if relation is not None:
        result["relation"] = relation
    result.update(extra)
    return result


# (exact relation spelling, CREATE line, closing engine line, exact columns).
TABLES = [
    ("account_invites",26,45,"id account_id email status user_id sent_by_id code expires_on created_on modified_on"),
    ("account_subscriptions",55,70,"id account_id stripe_subscription_id status name max_zones max_devices created_on ended_on modified_on"),
    ("account_users",80,87,"account_id user_id"),
    ("accounts",97,113,"id id_token name organization_name organization_url public_profile url_slug flags created_on modified_on stripe_customer_id"),
    ("api_keys",123,141,"id account_id user_id api_key grants audience token_lookup token_hashed last_seen created_on modified_on"),
    ("api_keys_monitors",151,158,"api_key_id monitor_id"),
    ("combust_cache",168,180,"id type created purge_key data metadata serialized expire"),
    ("combust_secrets",190,197,"secret_ts expires_ts type secret"),
    ("dns_roots",207,215,"id origin vendor_available general_use ns_list"),
    ("log_scores",225,241,"id monitor_id server_id ts score step offset rtt attributes"),
    ("log_scores_archive_status",251,260,"id archiver log_score_id modified_on"),
    ("logs",270,289,"id account_id server_id user_id vendor_zone_id type message changes created_on"),
    ("monitor_registrations",299,321,"id monitor_id request_token verification_token ip4 ip6 tls_name hostname location_code account_id client status last_seen created_on"),
    ("monitors",331,362,"id id_token type user_id account_id hostname location ip ip_version tls_name api_key status config client_version last_seen last_submit created_on deleted_on is_current"),
    ("oidc_public_keys",393,405,"id kid public_key algorithm created_at expires_at active"),
    ("schema_revision",415,419,"revision schema_name"),
    ("scorer_status",429,439,"id scorer_id log_score_id modified_on"),
    ("server_alerts",449,456,"server_id last_score first_email_time last_email_time"),
    ("server_notes",466,477,"id server_id name note created_on modified_on"),
    ("server_scores",487,510,"id monitor_id server_id score_ts score_raw stratum status queue_ts created_on modified_on constraint_violation_type constraint_violation_since last_constraint_check pause_reason"),
    ("server_urls",520,527,"id server_id url"),
    ("server_verifications",537,553,"id server_id user_id user_ip indirect_ip verified_on token created_on modified_on"),
    ("server_verifications_history",563,577,"id server_id user_id user_ip indirect_ip verified_on created_on modified_on"),
    ("server_zones",587,594,"server_id zone_id"),
    ("servers",604,630,"id ip ip_version user_id account_id hostname stratum in_pool in_server_list netspeed netspeed_target created_on updated_on score_ts score_raw deletion_on flags"),
    ("servers_monitor_review",640,649,"server_id last_review next_review last_change config"),
    ("system_settings",659,667,"id key value created_on modified_on"),
    ("user_equipment_applications",677,686,"id user_id application contact_information status"),
    ("user_identities",696,709,"id profile_id user_id provider data email created_on modified_on"),
    ("user_privileges",719,728,"user_id see_all_servers vendor_admin equipment_admin support_staff monitor_admin"),
    ("user_sessions",738,749,"id user_id token_lookup token_hashed last_seen created_on"),
    ("user_tasks",759,771,"id user_id task status traceid execute_on created_on modified_on"),
    ("users",781,793,"id id_token email name username public_profile deletion_on"),
    ("vendor_zones",803,832,"id id_token zone_name status user_id organization_name client_type contact_information request_information device_information device_count opensource opensource_info rt_ticket approved_on created_on modified_on dns_root_id account_id"),
    ("zone_server_counts",842,854,"id zone_id ip_version date count_active count_registered netspeed_active"),
    ("zones",864,874,"id name description parent_id dns"),
]

# Explicit handwritten key/index/FK choices. p/u/i/f = PK/unique/index/FK.
# Anonymous PK name is assigned its literal source offset, not an invented name.
KEYS = {
"account_invites":[(37,"p","","id"),(38,"u","account_id","account_id email"),(39,"u","code","code"),(40,"i","account_invites_user_fk","user_id"),(41,"i","account_invites_sent_by_fk","sent_by_id"),(42,"f","account_invites_account_fk","account_id","accounts","id"),(43,"f","account_invites_sent_by_fk","sent_by_id","users","id"),(44,"f","account_invites_user_fk","user_id","users","id")],
"account_subscriptions":[(66,"p","","id"),(67,"u","stripe_subscription_id","stripe_subscription_id"),(68,"i","account_subscriptions_account_fk","account_id"),(69,"f","account_subscriptions_account_fk","account_id","accounts","id")],
"account_users":[(83,"p","","account_id user_id"),(84,"i","account_users_user_fk","user_id"),(85,"f","account_users_account_fk","account_id","accounts","id"),(86,"f","account_users_user_fk","user_id","users","id")],
"accounts":[(109,"p","","id"),(110,"u","url_slug_idx","url_slug"),(111,"u","stripe_customer_id","stripe_customer_id"),(112,"u","id_token","id_token")],
"api_keys":[(135,"p","","id"),(136,"u","api_key","api_key"),(137,"i","api_keys_account_fk","account_id"),(138,"i","api_keys_user_fk","user_id"),(139,"f","api_keys_account_fk","account_id","accounts","id"),(140,"f","api_keys_user_fk","user_id","users","id")],
"api_keys_monitors":[(154,"p","","api_key_id monitor_id"),(155,"i","api_keys_monitors_monitors_fk","monitor_id"),(156,"f","api_keys_monitors_api_keys_fk","api_key_id","api_keys","id"),(157,"f","api_keys_monitors_monitors_fk","monitor_id","monitors","id")],
"combust_cache":[(177,"p","","id type"),(178,"i","expire_idx","expire"),(179,"i","purge_idx","purge_key")],
"combust_secrets":[(195,"p","","type secret_ts"),(196,"i","expires_ts","expires_ts")],
"dns_roots":[(213,"p","","id"),(214,"u","origin","origin")],
"log_scores":[(235,"p","","id"),(236,"i","log_scores_server_ts_idx","server_id ts"),(237,"i","ts","ts"),(238,"i","log_score_monitor_id_fk","monitor_id"),(239,"f","log_score_monitor_id_fk","monitor_id","monitors","id"),(240,"f","log_scores_server","server_id","servers","id")],
"log_scores_archive_status":[(256,"p","","id"),(257,"u","archiver","archiver"),(258,"i","log_score_id","log_score_id"),(259,"f","log_score_id","log_score_id","log_scores","id")],
"logs":[(280,"p","","id"),(281,"i","server_id","server_id type"),(282,"i","server_logs_user_id","user_id"),(283,"i","logs_vendor_zone_id","vendor_zone_id"),(284,"i","account_id_idx","account_id"),(285,"f","account_id_idx","account_id","accounts","id"),(286,"f","logs_vendor_zone_id","vendor_zone_id","vendor_zones","id"),(287,"f","server_logs_server_id","server_id","servers","id"),(288,"f","server_logs_user_id","user_id","users","id")],
"monitor_registrations":[(314,"p","","id"),(315,"u","request_token","request_token"),(316,"u","verification_token","verification_token"),(317,"i","monitor_registrations_monitor_id_fk","monitor_id"),(318,"i","monitor_registrations_account_fk","account_id"),(319,"f","monitor_registrations_account_fk","account_id","accounts","id"),(320,"f","monitor_registrations_monitor_id_fk","monitor_id","monitors","id")],
"monitors":[(351,"p","","id"),(352,"u","api_key","api_key"),(353,"u","monitors_tls_name","tls_name ip_version"),(354,"u","token_id","id_token"),(355,"u","id_token","id_token"),(356,"u","ip","ip is_current"),(357,"i","monitors_user_id","user_id"),(358,"i","monitors_account_fk","account_id"),(359,"i","type_status","type status"),(360,"f","monitors_account_fk","account_id","accounts","id"),(361,"f","monitors_user_id","user_id","users","id")],
"oidc_public_keys":[(401,"p","","id"),(402,"u","kid","kid"),(403,"i","idx_kid","kid"),(404,"i","idx_active_expires","active expires_at")],
"schema_revision":[(418,"p","","schema_name")],
"scorer_status":[(434,"p","","id"),(435,"i","scorer_log_score_id","log_score_id"),(436,"i","scores_status_monitor_id_fk","scorer_id"),(437,"f","scorer_log_score_id","log_score_id","log_scores","id"),(438,"f","scores_status_monitor_id_fk","scorer_id","monitors","id")],
"server_alerts":[(454,"p","","server_id"),(455,"f","server_alerts_server","server_id","servers","id")],
"server_notes":[(473,"p","","id"),(474,"u","server","server_id name"),(475,"i","name","name"),(476,"f","server_notes_ibfk_1","server_id","servers","id")],
"server_scores":[(502,"p","","id"),(503,"u","server_id","server_id monitor_id"),(504,"i","monitor_id","monitor_id server_id"),(505,"i","monitor_id_2","monitor_id score_ts"),(506,"i","idx_constraint_violation","constraint_violation_type constraint_violation_since"),(507,"i","idx_paused_monitors","status last_constraint_check pause_reason"),(508,"f","server_score_monitor_fk","monitor_id","monitors","id"),(509,"f","server_score_server_id","server_id","servers","id")],
"server_urls":[(524,"p","","id"),(525,"i","server","server_id"),(526,"f","server_urls_server","server_id","servers","id")],
"server_verifications":[(547,"p","","id"),(548,"u","server","server_id"),(549,"u","token","token"),(550,"i","server_verifications_ibfk_2","user_id"),(551,"f","server_verifications_ibfk_1","server_id","servers","id"),(552,"f","server_verifications_ibfk_2","user_id","users","id")],
"server_verifications_history":[(572,"p","","id"),(573,"i","server_verifications_history_ibfk_1","server_id"),(574,"i","server_verifications_history_ibfk_2","user_id"),(575,"f","server_verifications_history_ibfk_1","server_id","servers","id"),(576,"f","server_verifications_history_ibfk_2","user_id","users","id")],
"server_zones":[(590,"p","","server_id zone_id"),(591,"i","locations_zone","zone_id"),(592,"f","locations_server","server_id","servers","id"),(593,"f","locations_zone","zone_id","zones","id")],
"servers":[(622,"p","","id"),(623,"u","ip","ip"),(624,"i","admin","user_id"),(625,"i","score_ts","score_ts"),(626,"i","deletion_on","deletion_on"),(627,"i","server_account_fk","account_id"),(628,"f","server_account_fk","account_id","accounts","id"),(629,"f","servers_user_ibfk","user_id","users","id")],
"servers_monitor_review":[(646,"p","","server_id"),(647,"i","next_review","next_review"),(648,"f","server_monitor_review_server_id_fk","server_id","servers","id")],
"system_settings":[(665,"p","","id"),(666,"u","key","key")],
"user_equipment_applications":[(683,"p","","id"),(684,"i","user_equipment_applications_user_id","user_id"),(685,"f","user_equipment_applications_user_id","user_id","users","id")],
"user_identities":[(705,"p","","id"),(706,"u","profile_id","profile_id"),(707,"i","user_identities_user_id","user_id"),(708,"f","user_identities_user_id","user_id","users","id")],
"user_privileges":[(726,"p","","user_id"),(727,"f","user_privileges_user","user_id","users","id")],
"user_sessions":[(745,"p","","id"),(746,"i","user_sessions_user_fk","user_id"),(747,"i","token_lookup","token_lookup"),(748,"f","user_sessions_user_fk","user_id","users","id")],
"user_tasks":[(768,"p","","id"),(769,"i","user_tasks_user_fk","user_id"),(770,"f","user_tasks_user_fk","user_id","users","id")],
"users":[(789,"p","","id"),(790,"u","email","email"),(791,"u","username","username"),(792,"u","id_token","id_token")],
"vendor_zones":[(823,"p","","id"),(824,"u","zone_name","zone_name dns_root_id"),(825,"u","id_token","id_token"),(826,"i","vendor_zones_user_id","user_id"),(827,"i","dns_root_fk","dns_root_id"),(828,"i","vendor_zone_account_fk","account_id"),(829,"f","dns_root_fk","dns_root_id","dns_roots","id"),(830,"f","vendor_zone_account_fk","account_id","accounts","id"),(831,"f","vendor_zones_user_id","user_id","users","id")],
"zone_server_counts":[(850,"p","","id"),(851,"u","zone_date","zone_id date ip_version"),(852,"i","date_idx","date zone_id"),(853,"f","zone_server_counts","zone_id","zones","id")],
"zones":[(870,"p","","id"),(871,"u","name","name"),(872,"i","parent","parent_id"),(873,"f","zones_parent","parent_id","zones","id")],
}


def main():
    statements = []
    for line in [1, *range(8,18)]:
        statements.append(dict(id=f"wrapper-L{line}", **span(line), syntax_class="dump-wrapper", expected_disposition="wrapper", objects=[], note="Version/sandbox/session metadata only; never execute."))
    kinds = dict(p="primary_key", u="unique", i="index", f="foreign_key")
    for name, first, last, columns in TABLES:
        assert ("CREATE TABLE `" + name + "`").encode() in LINES[first - 1]
        objects = [obj("table", name, first)]
        for line, column in enumerate(columns.split(), first + 1):
            assert LINES[line - 1].startswith(("  `" + column + "`").encode())
            objects.append(obj("column", column, line, relation=name))
        for line, kind, key, members, *target in KEYS[name]:
            key = key or f"@{OFFSETS[line-1]}"
            extra = dict(columns=members.split())
            for member in members.split():
                assert ("`" + member + "`").encode() in LINES[line-1]
            if target:
                extra.update(target_relation=target[0], target_columns=target[1].split())
            objects.append(obj(kinds[kind], key, line, relation=name, **extra))
        statements.append(dict(id="table-" + name, **span(first,last), syntax_class="create-table", expected_disposition="declaration", objects=objects, note="Engine/charset/collation are committed syntax, not runtime identity; no implicit extra index objects."))
        for line in [first-3,first-2,first-1,last+1]:
            statements.append(dict(id=f"wrapper-L{line}", **span(line), syntax_class="dump-wrapper", expected_disposition="wrapper", objects=[], note="Dump DROP/SET metadata; not migration history."))
    for first,last,kind,note in [
        (369,369,"dump-wrapper","Stand-in cleanup, not a table declaration."),
        (370,370,"dump-wrapper","Stand-in cleanup, not migration history."),
        (371,371,"dump-wrapper","Session metadata only."),
        (372,372,"dump-wrapper","Session metadata only."),
        (373,383,"temporary-view-placeholder","Ten constant stand-in columns; explicit dump scaffold, no current declaration objects and no authored query sites."),
        (384,384,"dump-wrapper","Session metadata only."),
    ]:
        statements.append(dict(id=f"wrapper-L{first}", **span(first,last), syntax_class=kind, expected_disposition="wrapper", objects=[], note=note))
    for line in [*range(881,888),*range(891,895),*range(896,903)]:
        statements.append(dict(id=f"wrapper-L{line}", **span(line), syntax_class="dump-wrapper", expected_disposition="wrapper", objects=[], note="Final-view/session restore wrapper; never execute."))
    final_objects = [obj("view", "monitors_data", 890)]
    # Explicit ten final AS output aliases, located by unique exact literal bytes.
    final = LINES[889]
    for name in "id account_id type name ip ip_version status client_version last_seen last_submit".split():
        literal = (" AS `" + name + "`").encode()
        assert final.count(literal) == 1
        start = OFFSETS[889] + final.index(literal) + 4
        end = start + len(name.encode()) + 2
        final_objects.append(dict(kind="view_column",name=name,quoted=True,start=start,end=end,sha256=hashlib.sha256(RAW[start:end]).hexdigest(),relation="monitors_data"))
    statements.append(dict(id="view-monitors_data-final", **span(888,890), syntax_class="versioned-create-view", expected_disposition="declaration", objects=final_objects, note="One final current view. The earlier constant stand-in is labelled separately and contributes no current declarations. FROM monitors belongs to schema definition, not authored query plane."))
    statements.sort(key=lambda s:s["start"])
    result = dict(format="t261-source-labels-v1",dialect="mysql",repository="ntppool/monitor",commit="e03c40a06ae8f9bd4906001c2ede0c7296ec8e96",source_path="schema.sql",source_sha256=hashlib.sha256(RAW).hexdigest(),plane="sql-schema",statements=statements,inventory_notes=["Complete pinned dump; no candidate parser/recognizer executed to derive labels.","Names/backticks are exact; no lower_case_table_names assumption.","MariaDB versioned comments and engine/charset/collation retained; no runtime host/database/environment claims.","No data-bearing section appears in this source. Authored negative data controls are a separate population."])
    path = ROOT / "labels/monitor-schema.json"
    path.parent.mkdir(exist_ok=True)
    path.write_text(json.dumps(result,indent=2,ensure_ascii=False)+"\n")
    counts = {}
    for statement in statements:
        for item in statement["objects"]:
            counts[item["kind"]] = counts.get(item["kind"],0)+1
    print(json.dumps(dict(path=str(path),statements=len(statements),declarations=counts,total=sum(counts.values()),sha256=hashlib.sha256(path.read_bytes()).hexdigest())))


if __name__ == "__main__":
    main()
