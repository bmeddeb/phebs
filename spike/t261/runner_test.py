"""Independent evaluator/metadata contract tests; never execute a SQL parser."""
import copy
import unittest
import run_comparison as run

class RunnerContract(unittest.TestCase):
    def test_manifest_populations_and_stripped_requests(self):
        pg=run.manifest_inputs("hatchet","pkg/repository/sqlcv1/sqlc.yaml")
        my=run.manifest_inputs("monitor","sqlc.yaml")
        self.assertEqual((len(pg["schema"]),len(pg["queries"])),(5,36))
        self.assertEqual((my["schema"],my["queries"]),(["schema.sql"],["query.sql"]))
        requests=run.requests()
        self.assertEqual(len(requests),296+1+60)
        self.assertEqual(len({r["id"] for r in requests}),len(requests))
        self.assertFalse(any(k.startswith("expected_") for r in requests for k in r))
        self.assertFalse(any(k in ("objects","sites","events","statements") for r in requests for k in r))

    def test_exact_witness_and_false_emissions(self):
        f=dict(start=2,end=5,sha256="one",name="a",quoted=False,kind="foreign_key",relation="child",columns=["a","b"],target_relation="parent",target_columns=["x","y"])
        self.assertTrue(run.exact_match(f,f))
        for key,value in (("start",3),("sha256","two"),("name","b"),("quoted",True),("columns",["b","a"]),("target_columns",["y","x"]),("relation","other")):
            altered=copy.deepcopy(f);altered[key]=value
            self.assertFalse(run.exact_match(f,altered),key)
        matched,missing,extra=run.compare([f],[f,f])
        self.assertEqual((len(matched),len(missing),len(extra)),(1,0,1))
        self.assertEqual(len(run.compare([f],[])[1]),1)

    def test_review_migration_direction_and_envelope(self):
        e=dict(start=12,end=15,sha256="token",name="tab",quoted=False,predicate="MIGRATION_CREATES_TABLE",direction="up",label_statement_start=5,label_statement_end=25)
        a=dict(start=12,end=15,sha256="token",name="tab",quoted=False,predicate="MIGRATION_CREATES_TABLE",direction="up",candidate_statement_start=7,candidate_statement_end=23)
        self.assertTrue(run.exact_match(e,a,event=True))
        for key,value in (("direction","down"),("candidate_statement_start",0),("candidate_statement_end",26)):
            bad=dict(a);bad[key]=value
            self.assertFalse(run.exact_match(e,bad,event=True),key)

    def test_review_known_bool_omitempty(self):
        e=dict(start=1,end=2,sha256="one",name="a",quoted=False,kind="table",schema="public",schema_quoted=False)
        a=dict(e);del a["schema_quoted"]
        self.assertTrue(run.exact_match(e,a))
        a["schema_quoted"]=True
        self.assertFalse(run.exact_match(e,a))

if __name__=="__main__":unittest.main()
