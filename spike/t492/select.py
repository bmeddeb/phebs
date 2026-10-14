"""T49.2 selection (protocol section 4), applied in candidate order."""
import datetime, json, subprocess, sys
sys.path.insert(0, '/home/ben/phebs-wt/t492/spike/t492/oracle')
import oracle  # noqa: E402

cands = json.load(open('search/candidates.json'))
out, repos, decls = [], 0, 0
for c in cands:
    if repos >= 30 or (repos >= 10 and decls >= 1000):
        break
    rec = {'name': c['name']}
    if c['drop']:
        rec['drop'] = c['drop']; out.append(rec); continue
    meta = json.loads(subprocess.run(['gh', 'repo', 'view', c['name'], '--json', 'isArchived,isFork'],
                                     capture_output=True, text=True).stdout or '{}')
    if not meta:
        rec['drop'] = 'unreachable'; out.append(rec); continue
    if meta['isFork'] or meta['isArchived']:
        rec['drop'] = 'fork' if meta['isFork'] else 'archived'; out.append(rec); continue
    d = 'history/' + c['name'].replace('/', '__')
    # Exact checkout bytes from the default-branch tree, without fetching blobs.
    head = subprocess.run(['gh', 'api', f"repos/{c['name']}/commits/HEAD", '--jq', '.sha'], capture_output=True, text=True).stdout.strip()
    tree = json.loads(subprocess.run(['gh', 'api', f"repos/{c['name']}/git/trees/{head}?recursive=1"],
                                     capture_output=True, text=True).stdout or '{}')
    blobs = [e for e in tree.get('tree', []) if e.get('type') == 'blob']
    size = sum(e.get('size', 0) for e in blobs)
    thrift_files = sum(1 for e in blobs if e['path'].endswith('.thrift'))
    rec.update(dir=d.split('/')[1], commit=head, checkout_mib=round(size / 2**20, 1), thrift_files=thrift_files)
    if not head or tree.get('truncated') or size > 200 * 2**20 or thrift_files > 2000:
        rec['drop'] = 'exceeds host envelope' + (' (tree listing truncated)' if tree.get('truncated') else ''); out.append(rec); continue
    r = subprocess.run(['git', 'clone', '-q', '--depth', '1', f"https://github.com/{c['name']}.git", d], capture_output=True, text=True)
    actual = subprocess.run(['git', '-C', d, 'rev-parse', 'HEAD'], capture_output=True, text=True).stdout.strip()
    if r.returncode or actual != head:
        rec['drop'] = 'clone failed or HEAD moved'; out.append(rec); continue
    rows = oracle.run(__import__('pathlib').Path(d), c['name'])
    ok = [x for x in rows if x['status'] == 'ok']
    rec.update(oracle_files_ok=len(ok), oracle_files_rejected=len(rows) - len(ok),
               oracle_declarations=sum(len(x['declarations']) for x in ok))
    if not ok:
        rec['drop'] = 'oracle compiles none of its .thrift files'; out.append(rec); continue
    repos += 1; decls += rec['oracle_declarations']; rec['eligible'] = repos; out.append(rec)
    print(c['name'], rec['oracle_declarations'], decls, flush=True)
json.dump({'selected_utc': datetime.date.today().isoformat(), 'eligible_repositories': repos,
           'oracle_declarations': decls, 'records': out}, open('selection.json', 'w'), indent=1)
for r in out:
    print(r['name'], r.get('eligible') or r.get('drop'), r.get('checkout_mib', ''), r.get('oracle_declarations', ''))
