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
