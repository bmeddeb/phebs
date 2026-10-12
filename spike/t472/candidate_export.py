"""Private, read-only exact-run export and replay-ledger reconciliation.

No truth labels, randomness, scores, or protocol seal are created here.
Retained rows never prove withholding: only an exact matching replay does.
"""
from __future__ import annotations
import base64
import copy
import hashlib
import json
from pathlib import Path
from urllib.request import Request, urlopen

import frames
import source_outcomes as outcomes

MAX_EXPORT = 64 << 20


def query(root: Path, sql: str):
    runtime = outcomes.load(root / 'data/.surreal-runtime.json')
    request = Request(runtime['endpoint'].replace('ws://', 'http://') + '/sql',
                      data=sql.encode(), method='POST', headers={
                          'Authorization': 'Basic ' + base64.b64encode(('root:' + runtime['pass']).encode()).decode(),
                          'surreal-ns': 'phebs', 'surreal-db': 'phebs',
                          'Accept': 'application/json', 'Content-Type': 'text/plain'})
    with urlopen(request, timeout=30) as response:
        raw = response.read(MAX_EXPORT + 1)
    outcomes.require(len(raw) <= MAX_EXPORT, 'store export exceeds bound')
    result = json.loads(raw)
    outcomes.require(len(result) == 1 and result[0]['status'] == 'OK', 'store export query failed')
    return result[0]['result']


def read_export(root: Path, controls: dict) -> dict:
    selected = query(root, "SELECT * FROM extraction_domain_root WHERE domain = 'grpc-caller' LIMIT 4;")
    outcomes.require(len(selected) == 3, 'incomplete current caller roots')
    expected = {(c['repository'], c['run_id']): c for c in controls['controls'] if c['domain'] == 'grpc-caller'}
    assertions, atoms, occurrences, chunks = [], {}, [], []
    for current in selected:
        key = (current['repository'], current['run_id'])
        outcomes.require(key in expected, 'current authority differs from retained controls')
        control = expected[key]
        outcomes.require(json.loads(current['root']) == control['root'] and
                         json.loads(current['plan']) == control['plan'], 'authority identity mismatch')
        scope = 'run_id = ' + json.dumps(current['run_id'])
        # Root reservations bound this population; every query carries an overflow sentinel.
        limit = max(current['facts'], current['references'], current['rows']) + 1
        for table, target in [('assertion', assertions), ('snapshot_evidence', occurrences), ('evidence_chunk', chunks)]:
            rows = query(root, f'SELECT * FROM {table} WHERE {scope} LIMIT {limit};')
            outcomes.require(len(rows) < limit, 'root row census overflow')
            for row in rows:
                row.pop('id', None)
            target.extend(rows)
        refs = {a for row in assertions if row['run_id'] == current['run_id']
                for a in row['supporting'] + (row.get('contradicting') or [])}
        if refs:
            rows = query(root, 'SELECT * FROM evidence_atom WHERE atom_id IN ' + json.dumps(sorted(refs)) + f' LIMIT {len(refs)+1};')
            outcomes.require(len(rows) == len(refs), 'missing or duplicate evidence atoms')
            for row in rows:
                row.pop('id', None)
                outcomes.require(row['atom_id'] not in atoms or atoms[row['atom_id']] == row, 'inconsistent shared atom')
                atoms[row['atom_id']] = row
    return {'authorities': selected, 'assertions': assertions, 'atoms': atoms,
            'occurrences': occurrences, 'chunks': chunks}


def native_proof(export: dict) -> dict:
    assertions, evidence, repositories, extractors = [], [], [], []
    for source in export['assertions']:
        fields = ('predicate','subject','object','tier','code_role','repo','supporting','run_id','detail')
        row = {k: source[k] for k in fields}
        row['id'] = source['assertion_id']
        for key in ('lineage','contradicting'):
            if source.get(key): row[key] = source[key]
        assertions.append(row)
    assertions.sort(key=lambda a: (a['repo'],a['run_id'],a['id']))
    keys = sorted({(a['repo'],a['run_id'],atom) for a in assertions
                   for atom in a['supporting'] + a.get('contradicting',[])})
    occurrences_by_atom = {}
    for row in export['occurrences']:
        occurrences_by_atom.setdefault((row['repo'],row['run_id'],row['atom_id']),[]).append(row)
    for repository, run, atom in keys:
        occurrence = occurrences_by_atom.get((repository,run,atom),[])
        outcomes.require(bool(occurrence), 'assertion lacks a snapshot occurrence')
        evidence.append({'repository':repository,'run_id':run,'atom':{k:v for k,v in export['atoms'][atom].items() if k != 'retention_revision'},
                         'occurrences':[{k:v for k,v in r.items() if k not in ('association_key','atom_record')} for r in sorted(occurrence,key=lambda r:r['association_key'])]})
    # This certificate's published state is exact partition-root publication,
    # not the legacy extraction_run.status. Root/plan bytes are separately bound.
    for authority in sorted(export['authorities'],key=lambda r:r['repository']):
        plan = json.loads(authority['plan'])
        repo, run = authority['repository'], authority['run_id']
        row_assertions = [a for a in assertions if a['repo']==repo and a['run_id']==run]
        row_evidence = [e for e in evidence if e['repository']==repo and e['run_id']==run]
        commits = {o['commit'] for e in row_evidence for o in e['occurrences']}
        outcomes.require(len(commits)==1, 'caller evidence commit census mismatch')
        commit = next(iter(commits))
        repositories.append({'repository':repo,'indexed_commit':commit,'scope_posture':'whole-repository',
                             'scip_index':'present','runs':[{
                                 'domain':'grpc-caller','status':'published','run_id':run,
                                 'extractor':'grpc-caller@1.6.0','commit':commit,'fresh':True,
                                 'protocols':['grpc','partitioned-work-counters-unavailable'], 'failures':['partitioned_work_counters_unavailable'], 'source_scope_digest':authority['root_digest'], 'corpus_file_count':0,'candidate_file_count':0,
                                 'read_file_count':0,'read_bytes':0,'unresolved_count':sum(a['predicate']=='UNRESOLVED_CALLER' for a in row_assertions),
                                 'assertion_count':len(row_assertions),'atom_count':len(row_evidence)}]})
        extractors.append({'repository':repo,'domain':'grpc-caller','run_id':run,'extractor':'grpc-caller@1.6.0'})
    visible = 'sha256:' + hashlib.sha256(json.dumps([r['repository'] for r in repositories],separators=(',',':')).encode()).hexdigest()
    return {'schema_version':'proof-bundle-v1','query':{'kind':'t472_exact_partitioned_caller_export','domains':['grpc-caller']},
            'assertions':assertions,'evidence':evidence,'coverage':{'schema_version':'coverage-certificate-v3',
            'domains':['grpc-caller'],'repository_count':3,'repositories':repositories,'digest':''},
            'extractor_versions':extractors,'visibility_context':{'principal':'Ben Meddeb',
            'authorization_provider':'operator-owned-public-corpus','permission_snapshot':'exact-retained-run-controls',
            'visible_repository_set_digest':visible},'caveat':
            'Provisional exact partition-root export; legacy run status remains staged. Zero legacy coverage work counters are unavailable, not measured zero. Complete root/plan controls and processing receipts are bound separately. No absence, compatibility, migration-complete, decommissioning or quality conclusion.'}


def project_native(envelope: dict, universe: dict, aliases: dict) -> dict:
    adapted = copy.deepcopy(envelope)
    for assertion in adapted['bundle']['assertions']:
        assertion['repo'] = aliases[assertion['repo']]
    for item in adapted['bundle']['evidence']:
        item['repository'] = aliases[item['repository']]
        item['atom']['id'] = item['atom']['atom_id']
        for occurrence in item['occurrences']:
            occurrence['repo'] = aliases[occurrence['repo']]
    return frames.project_candidate_frames(adapted, universe)


def reconcile_replay(export: dict, replay: list[dict], results: dict) -> None:
    expected = {(r['repository'], r['ordinal']): r['spec'] for r in replay if r['kind']=='result'}
    outcomes.require(expected == results, 'replay result specs differ from retained partitions')
    def chunks(rows):
        return sorted((r['run_id'],r['chunk_id'],r['fact_count']) for r in rows)
    run_ids = {r['repository']:r['run_id'] for r in export['authorities']}
    captured = [{**r,'run_id':run_ids[r['repository']]} for r in replay if r['kind']=='chunk']
    outcomes.require(chunks(captured)==chunks(export['chunks']), 'replay ordered chunk population differs from retained run')
    original = {(r['repo'],r['run_id'],r['assertion_id']):(r['predicate'],r['subject'],r['object'],r.get('lineage') or '',r['tier'],r['code_role'],r['detail']) for r in export['assertions']}
    emitted = {(r['repo'],r['run_id'],r['id']):(r['predicate'],r['subject'],r['object'],r.get('lineage') or '',r['tier'],r['code_role'],r['detail']) for c in captured for r in c['assertions']}
    outcomes.require(original == emitted, 'replay assertion attributes differ from retained run')


def decision_ledger(universe: dict, projected: dict, trace: list[dict], aliases: dict,
                    basis: str = 'matching_partition_replay_observed_ast_call_or_bound_scip_reference') -> list[dict]:
    citations = {}
    for site in universe['sites']:
        if frames.recall_eligibility(site)[0]:
            for start,end in site['citation_spans']:
                citations[(site['repository'],site['path'],start,end)] = site['site_id']
    considered = set()
    for event in trace:
        key = (aliases[event['repository']],event['path'],event['start_byte'],event['end_byte'])
        if key in citations: considered.add(citations[key])
    reported = {r['site_id']:r for f in projected.values() for r in f['rows']}
    outcomes.require(set(reported)<=considered, 'reported site missing an observed consideration')
    return [{'site_id':s['site_id'],'stratum':s['stratum'],
             'state':'reported' if s['site_id'] in reported else 'withheld' if s['site_id'] in considered else 'not_considered',
             'basis':basis if s['site_id'] in considered else 'outside_observed_consideration_set'}
            for s in sorted(universe['sites'],key=lambda r:r['site_id']) if frames.recall_eligibility(s)[0]]
