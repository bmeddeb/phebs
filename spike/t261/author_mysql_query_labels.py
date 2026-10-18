"""Serialize source-only, manually enumerated query labels; no SQL parser."""
from pathlib import Path
import hashlib
import json

ROOT = Path(__file__).resolve().parent
RAW = (ROOT / "corpus/monitor/query.sql").read_bytes()
LINES = RAW.splitlines(keepends=True)
OFFSETS = [0]
for line in LINES:
    OFFSETS.append(OFFSETS[-1] + len(line))


def span(first, last):
    start, end = OFFSETS[first-1], OFFSETS[last]
    return dict(start=start,end=end,sha256=hashlib.sha256(RAW[start:end]).hexdigest())


# Name, exact block lines, syntax class, explicitly selected relation sites.
# r/w/rw refer to physical relation read/write semantics, never SQL row results.
QUERIES = [
 ("GetMonitorsTLSName",1,5,"select",[(2,"monitors","r")]),
 ("GetMonitorTLSNameIP",7,18,"select-join",[(11,"monitors","r"),(12,"accounts","r")]),
 ("GetServer",20,21,"select",[(21,"servers","r")]),
 ("GetServerIP",23,24,"select",[(24,"servers","r")]),
 ("GetServerScore",26,30,"select",[(27,"server_scores","r")]),
 ("UpdateServerScore",32,36,"update",[(33,"server_scores","rw")]),
 ("UpdateServerScoreQueue",38,45,"update",[(39,"server_scores","rw")]),
 ("InsertServerScore",47,52,"insert-on-duplicate",[(48,"server_scores","rw")]),
 ("UpdateServerScoreStatus",54,57,"update",[(55,"server_scores","rw")]),
 ("UpdateServerScoreStratum",59,62,"update",[(60,"server_scores","rw")]),
 ("UpdateServer",64,70,"update",[(65,"servers","rw")]),
 ("UpdateServerStratum",72,79,"update",[(73,"servers","rw")]),
 ("GetScorers",81,88,"select-comma-join",[(84,"monitors","r"),(84,"scorer_status","r")]),
 ("GetScorerStatus",90,92,"select-comma-join",[(91,"scorer_status","r"),(91,"monitors","r")]),
 ("UpdateScorerStatus",94,97,"update",[(95,"scorer_status","rw")]),
 ("InsertScorerStatus",99,102,"insert-values",[(100,"scorer_status","w")]),
 ("InsertScorer",104,112,"insert-values",[(105,"monitors","w")]),
 ("GetMinLogScoreID",114,116,"select",[(116,"log_scores","r")]),
 ("GetScorerLogScores",118,128,"select-index-hint",[(120,"log_scores","r"),(121,"monitors","r")]),
 ("GetScorerNextLogScoreID",130,144,"select-index-hint",[(137,"log_scores","r"),(138,"monitors","r")]),
 ("GetScorerRecentScores",146,166,"select-derived-join",[(148,"log_scores","r"),(151,"log_scores","r"),(152,"monitors","r"),(153,"server_scores","r")]),
 ("UpdateMonitorSeen",168,171,"update",[(169,"monitors","rw")]),
 ("UpdateMonitorSubmit",173,176,"update",[(174,"monitors","rw")]),
 ("UpdateMonitorVersion",178,181,"update",[(179,"monitors","rw")]),
 ("InsertLogScore",183,186,"insert-values",[(184,"log_scores","w")]),
 ("GetServers",188,206,"select-join",[(190,"servers","r"),(191,"server_scores","r")]),
 ("GetMonitorPriority",208,232,"select-join",[(221,"log_scores","r"),(222,"monitors","r"),(223,"server_scores","r"),(224,"accounts","r")]),
 ("GetServersMonitorReview",234,238,"select",[(235,"servers_monitor_review","r")]),
 ("UpdateServersMonitorReview",240,243,"update",[(241,"servers_monitor_review","rw")]),
 ("UpdateServersMonitorReviewChanged",245,248,"update",[(246,"servers_monitor_review","rw")]),
 ("GetSystemSetting",250,251,"select",[(251,"system_settings","r")]),
 ("UpdateServerScoreConstraintViolation",253,257,"update",[(254,"server_scores","rw")]),
 ("ClearServerScoreConstraintViolation",259,265,"update",[(260,"server_scores","rw")]),
 ("UpdateServerScorePauseReason",267,271,"update",[(268,"server_scores","rw")]),
 ("UpdateServerScoreLastConstraintCheck",273,276,"update",[(274,"server_scores","rw")]),
 ("DeleteServerScore",279,282,"delete",[(281,"server_scores","rw")]),
]


def main():
    statements=[]
    access=dict(r="read",w="write",rw="read-write")
    for name,first,last,syntax,locations in QUERIES:
        assert ("-- name: " + name + " ").encode() in LINES[first-1]
        sites=[]
        for line,table,mode in locations:
            literal=table.encode()
            # The author chose occurrence zero on this exact line. Later same-
            # spelling column qualifiers do not supply additional table sites.
            assert LINES[line-1].count(literal)>=1,(name,line,table)
            start=OFFSETS[line-1]+LINES[line-1].index(literal)
            end=start+len(literal)
            sites.append(dict(name=table,quoted=False,start=start,end=end,sha256=hashlib.sha256(RAW[start:end]).hexdigest(),access=access[mode],expected_binding="unique",declaration_relation=table))
        note="Exact authored relation sites only; sqlc placeholders/functions and column qualifiers are not table sites. One committed manifest and one matching current declaration root provide unique binding."
        if syntax=="insert-on-duplicate":
            note+=" One target occurrence is read-write for ON DUPLICATE KEY UPDATE; VALUES(score_raw) does not introduce another relation."
        if syntax=="select-derived-join":
            note+=" Derived alias g is not a physical relation; outer and inner log_scores occurrences remain distinct."
        if syntax=="select-index-hint":
            note+=" USE INDEX(primary) is a source modifier, not another relation."
        if name=="DeleteServerScore":
            note+=" Final statement has no semicolon; EOF terminates it."
        statements.append(dict(id=name,**span(first,last),syntax_class=syntax,expected_disposition="query",objects=[],sites=sites,note=note))
    result=dict(format="t261-source-labels-v1",dialect="mysql",repository="ntppool/monitor",commit="e03c40a06ae8f9bd4906001c2ede0c7296ec8e96",source_path="query.sql",source_sha256=hashlib.sha256(RAW).hexdigest(),plane="sql-query",statements=statements,inventory_notes=["All 36 authored named query blocks and all physical relation-literal occurrences are labelled, without sampling.","Generated ntpdb/query.sql.go is a separately retained duplicate-negative population; it supplies no primary usage sites.","No query was executed; labels describe committed source read/write intent, not runtime table access or query results."])
    path=ROOT/"labels/monitor-query.json"
    path.parent.mkdir(exist_ok=True)
    path.write_text(json.dumps(result,indent=2,ensure_ascii=False)+"\n")
    print(json.dumps(dict(path=str(path),queries=len(statements),sites=sum(len(s["sites"]) for s in statements),sha256=hashlib.sha256(path.read_bytes()).hexdigest())))


if __name__=="__main__":
    main()
