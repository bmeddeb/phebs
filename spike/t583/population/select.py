"""T58.3a selection (protocol section 6), applied in candidate order."""
import datetime, json, pathlib, subprocess, sys

WT = '/home/ben/phebs-wt/t583'
ORACLE = '/tmp/claude-1000/-home-ben-phebs/ac53f306-9a01-4739-8e4b-f5aa2b549ac6/scratchpad/t583/oracle'
excluded = {r['name'].lower() for r in json.load(open(f'{WT}/spike/t581/corpus.lock.json'))['repos']}
excluded |= {r['name'].lower() for r in json.load(open(f'{WT}/spike/t472/corpus.lock.json'))['repos']}
excluded |= {'grpc/grpc-go', 'hashicorp/go-plugin'}
seen, info = [], {}
for i in range(1, 5):
    for hit in json.load(open(f'search/q{i}.json')):
        r = hit['repository']; n = r['nameWithOwner']
        if n not in info:
            info[n] = r; seen.append(n)
json.dump([{'name': n, 'fork': info[n].get('isFork', False)} for n in seen], open('search/candidates.json', 'w'), indent=1)
out, repos, sites = [], 0, 0
for name in seen:
    if repos >= 25 or (repos >= 10 and sites >= 500):
        break
    rec = {'name': name}
    if name.lower() in excluded:
        rec['drop'] = 'excluded: T58.1/T47.2 population, grpc-go, or dry-run repository'; out.append(rec); continue
    meta = json.loads(subprocess.run(['gh', 'repo', 'view', name, '--json', 'isArchived,isFork'], capture_output=True, text=True).stdout or '{}')
    if not meta:
        rec['drop'] = 'unreachable'; out.append(rec); continue
    if meta['isFork'] or meta['isArchived']:
        rec['drop'] = 'fork' if meta['isFork'] else 'archived'; out.append(rec); continue
    head = subprocess.run(['gh', 'api', f'repos/{name}/commits/HEAD', '--jq', '.sha'], capture_output=True, text=True).stdout.strip()
    tree = json.loads(subprocess.run(['gh', 'api', f'repos/{name}/git/trees/{head}?recursive=1'], capture_output=True, text=True).stdout or '{}')
    blobs = [e for e in tree.get('tree', []) if e.get('type') == 'blob']
    size = sum(e.get('size', 0) for e in blobs)
    gofiles = sum(1 for e in blobs if e['path'].endswith('.go'))
    rec.update(commit=head, checkout_mib=round(size / 2**20, 1), go_files=gofiles)
    if not any(e['path'] == 'go.mod' for e in blobs):
        rec['drop'] = 'no root go.mod'; out.append(rec); continue
    if not head or tree.get('truncated') or size > 200 * 2**20 or gofiles > 5000:
        rec['drop'] = 'exceeds host envelope'; out.append(rec); continue
    d = 'history/' + name.replace('/', '__')
    r = subprocess.run(['git', 'clone', '-q', '--depth', '1', f'https://github.com/{name}.git', d], capture_output=True, text=True)
    if r.returncode or subprocess.run(['git', '-C', d, 'rev-parse', 'HEAD'], capture_output=True, text=True).stdout.strip() != head:
        rec['drop'] = 'clone failed or HEAD moved'; out.append(rec); continue
    rec['dir'] = d.split('/')[1]
    tracked = [f for f in subprocess.run(['git', '-C', d, 'ls-files', '--', '*.go'], capture_output=True, text=True).stdout.splitlines() if f]
    try:
        o = subprocess.run([ORACLE, '-repo', d, '-name', name], capture_output=True, text=True, timeout=900)
    except subprocess.TimeoutExpired:
        rec['drop'] = 'oracle exceeded 15 minutes'; out.append(rec); continue
    rows = [json.loads(l) for l in o.stdout.splitlines() if l.strip()]
    summ = next((x for x in rows if x.get('summary')), None)
    if o.returncode or summ is None:
        rec['drop'] = 'oracle failed: ' + o.stderr.strip()[-160:]; out.append(rec); continue
    loaded = summ['loaded_files'] / max(1, len(tracked))
    resolved = sum(1 for x in rows if not x.get('summary') and x['state'] == 'resolved')
    rec.update(tracked_go_files=len(tracked), loaded_fraction=round(loaded, 4), oracle_resolved_sites=resolved)
    if loaded < 0.8:
        rec['drop'] = 'oracle loads under 80% of tracked .go files'; out.append(rec); continue
    if resolved == 0:
        rec['drop'] = 'oracle finds no resolved site'; out.append(rec); continue
    repos += 1; sites += resolved; rec['eligible'] = repos; out.append(rec)
    print(name, resolved, sites, flush=True)
json.dump({'selected_utc': datetime.date.today().isoformat(), 'eligible_repositories': repos, 'oracle_resolved_sites': sites, 'records': out},
          open('selection.json', 'w'), indent=1)
for r in out:
    print(r['name'], r.get('eligible') or r.get('drop'), r.get('oracle_resolved_sites', ''), r.get('loaded_fraction', ''))
