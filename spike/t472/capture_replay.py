"""Create a private observational Go overlay; never edit production source.

The ledger describes a replay, not retroactive telemetry. Its decisions may
bind a retained run only after every ordered chunk/result matches that run.
"""
from pathlib import Path
import json
import hashlib


def prepare_overlay(repo: Path, output: Path) -> dict:
    replacements = {}
    sources = {
        'fallback.go': ('case *ast.CallExpr:\n\t\t\t\tselector, ok := typedNode.Fun.(*ast.SelectorExpr)',
                       'case *ast.CallExpr:\n\t\t\t\tif err = t472Observe(ctx, filePath, tokenFile.Offset(typedNode.Fun.Pos()), tokenFile.Offset(typedNode.Fun.End()), "syntax"); err != nil { return false }\n\t\t\t\tselector, ok := typedNode.Fun.(*ast.SelectorExpr)'),
        'gocaller.go': ('\t\t\tif typedSpans[document.path] == nil {',
                       '\t\t\tif err := t472Observe(ctx, document.path, start, end, "scip"); err != nil { return len(unresolved), typedSpans, err }\n\t\t\tif typedSpans[document.path] == nil {'),
    }
    hashes = {}
    for name, (old, new) in sources.items():
        source = repo / 'internal/extract/extractors/gocaller' / name
        text = source.read_text()
        if text.count(old) != 1:
            raise ValueError('observational injection point changed: ' + name)
        changed = output / name
        changed.write_text(text.replace(old, new))
        replacements[str(source)] = str(changed)
        hashes[name] = {'original': 'sha256:' + hashlib.sha256(source.read_bytes()).hexdigest(),
                        'overlay': 'sha256:' + hashlib.sha256(changed.read_bytes()).hexdigest()}
    hook = output / 'observer.go'
    hook.write_text('''package gocaller
import("context";"encoding/json";"os";"fmt")
func t472Observe(ctx context.Context, path string, start,end int, lane string) error {
 if err:=ctx.Err();err!=nil{return err}
 file,err:=os.OpenFile(os.Getenv("PHEBS_T472_TRACE"),os.O_WRONLY|os.O_APPEND,0600)
 if err!=nil{return err}
 info,err:=file.Stat();if err!=nil{file.Close();return err}
 if info.Size()>128<<20{file.Close();return fmt.Errorf("trace exceeds bound")}
 err=json.NewEncoder(file).Encode(map[string]any{"path":path,"start_byte":start,"end_byte":end,"lane":lane,"repository":os.Getenv("PHEBS_T472_TRACE_REPOSITORY")})
 closeErr:=file.Close();if err!=nil{return err};return closeErr
}
''')
    replacements[str(repo / 'internal/extract/extractors/gocaller/t472_observer.go')] = str(hook)
    replacements[str(repo / 'internal/extract/t472_replay_test.go')] = str(repo / 'spike/t472/capture_replay.go.txt')
    overlay = output / 'overlay.json'
    overlay.write_text(json.dumps({'Replace': replacements}, indent=2) + '\n')
    return hashes


# T47.2o: the Caller Map leaf lane. The observer is in-memory, sticky and
# fail-closed: it never assigns the walk's shared err, and the replay program
# refuses the whole run if any observation failed. Bytes are pre-formatted so
# the compiled overlay is exactly what this function writes.
LEAF_FALLBACK_ANCHOR = 'case *ast.CallExpr:\n\t\t\t\tselector, ok := typedNode.Fun.(*ast.SelectorExpr)'
LEAF_FALLBACK_INJECTION = (
    'case *ast.CallExpr:\n'
    '\t\t\t\tif t472Observe(ctx, filePath, tokenFile.Offset(typedNode.Fun.Pos()), tokenFile.Offset(typedNode.Fun.End())) != nil {\n'
    '\t\t\t\t\treturn false\n'
    '\t\t\t\t}\n'
    '\t\t\t\tselector, ok := typedNode.Fun.(*ast.SelectorExpr)')
LEAF_OBSERVER = '''//go:build t472replay

package gocaller

import (
	"context"
	"errors"
	"sync"
)

// T472Visit is one observed call-expression visit of the shared syntax walk.
type T472Visit struct {
	Path  string `json:"path"`
	Start int    `json:"start_byte"`
	End   int    `json:"end_byte"`
}

const t472MaxVisits = 1 << 21

var t472 struct {
	mu      sync.Mutex
	visits  []T472Visit
	failure error
}

func t472Observe(ctx context.Context, path string, start, end int) error {
	t472.mu.Lock()
	defer t472.mu.Unlock()
	if t472.failure == nil {
		if err := ctx.Err(); err != nil {
			t472.failure = err
		} else if len(t472.visits) >= t472MaxVisits {
			t472.failure = errors.New("t472 visit trace exceeds bound")
		} else {
			t472.visits = append(t472.visits, T472Visit{Path: path, Start: start, End: end})
		}
	}
	return t472.failure
}

// T472Drain returns and clears the visits; any observation failure is sticky.
func T472Drain() ([]T472Visit, error) {
	t472.mu.Lock()
	defer t472.mu.Unlock()
	visits := t472.visits
	t472.visits = nil
	return visits, t472.failure
}
'''


def prepare_leaf_overlay(repo: Path, output: Path) -> dict:
    source = repo / 'internal/extract/extractors/gocaller/fallback.go'
    text = source.read_text()
    if text.count(LEAF_FALLBACK_ANCHOR) != 1:
        raise ValueError('observational injection point changed: fallback.go')
    files = {'fallback.go': text.replace(LEAF_FALLBACK_ANCHOR, LEAF_FALLBACK_INJECTION),
             'observer.go': LEAF_OBSERVER}
    targets = {'fallback.go': source,
               'observer.go': repo / 'internal/extract/extractors/gocaller/t472_observer.go'}
    hashes = {'fallback.go.original': 'sha256:' + hashlib.sha256(source.read_bytes()).hexdigest()}
    replacements = {}
    for name, body in files.items():
        (output / name).write_text(body)
        replacements[str(targets[name])] = str(output / name)
        hashes[name] = 'sha256:' + hashlib.sha256(body.encode()).hexdigest()
    overlay = output / 'overlay.json'
    overlay.write_text(json.dumps({'Replace': replacements}, indent=2, sort_keys=True) + '\n')
    hashes['overlay.json'] = 'sha256:' + hashlib.sha256(overlay.read_bytes()).hexdigest()
    return hashes
