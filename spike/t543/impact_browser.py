"""Finite Impact UI client using the reviewed T503 owned-child lifecycle."""
import json
from pathlib import Path
import shutil
import time

import requalification_browser as owned
import requalification_checks as checks
import score

HERE = Path(__file__).resolve().parent


def run(binding, root, document, remaining):
    root.mkdir()
    profile, private = root / "private-chrome-profile", root / "private-input.json"
    children, terminals, failure, result = [], {}, None, {}
    clock = time.monotonic()
    limit = clock + min(25, remaining())
    def timeout():
        value = min(limit - time.monotonic(), remaining())
        score.require(value > 0, "Impact browser deadline exceeded")
        return value
    try:
        chrome = owned.OwnedChild([binding["tools"]["chrome"]["path"], "--headless=new", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--user-data-dir=" + str(profile), "about:blank"], root, "chrome")
        children.append(("chrome", chrome))
        ready = time.monotonic() + min(5, timeout())
        active = profile / "DevToolsActivePort"
        while not active.exists():
            score.require(chrome.child.poll() is None and time.monotonic() < ready, "Impact Chrome control readiness refused")
            timeout(); time.sleep(0.02)
        port = int(active.read_text().splitlines()[0])
        score.require(0 < port < 65536, "Impact Chrome control port invalid")
        document.update(cdp="http://127.0.0.1:" + str(port), node_modules=binding["node_modules"], axe_source=binding["axe_source"]["path"], out=str(root / "result.json"), screens=str(root / "screens"))
        private.write_text(json.dumps(document)); private.chmod(0o600)
        client = owned.OwnedChild([binding["tools"]["node"]["path"], str(HERE / "impact_browser.cjs"), str(private)], root, "client")
        children.append(("client", client))
        client.child.wait(timeout=timeout())
        score.require(client.child.returncode == 0, "Impact actual UI client failed")
        result = checks.bounded_json(root / "result.json", 65536)
        score.require(result["outcome"] == "passed", "Impact UI result unavailable")
    except BaseException as error:
        failure = error
    finally:
        for name, child in reversed(children):
            try:
                terminals[name] = child.stop()
            except BaseException as error:
                terminals[name] = {"cleanup_failure": str(error)}
                failure = failure or error
        joined = all(row.get("teardown") == "clean" and row.get("termination_mode") != "sigkill" for row in terminals.values())
        removed = False
        if joined:
            try:
                private.unlink(missing_ok=True)
                if profile.exists():
                    shutil.rmtree(profile)
                for name, _ in children:
                    (root / (name + ".log")).unlink(missing_ok=True)
                removed = True
            except BaseException as error:
                failure = failure or error
        record = {"joined": joined, "terminals": terminals, "seconds": round(time.monotonic() - clock, 6), "private_input_profile_logs_removed": removed}
        (root / "joined-teardown.json").write_text(json.dumps(record, indent=2) + "\n")
        if joined:
            images = list((root / "screens").glob("*.png")) if (root / "screens").exists() else []
            try:
                score.require(len(images) <= 3 and all(p.is_file() and not p.is_symlink() and p.stat().st_size <= 4194304 for p in images), "Impact private UI capture bound exceeded")
                receipts = list(root.glob("*.json"))
                score.require(len(receipts) <= 2 and all(p.is_file() and not p.is_symlink() and p.stat().st_size <= 65536 for p in receipts), "Impact private UI JSON bound exceeded")
                score.require({p.name for p in root.iterdir()} <= {"screens", "result.json", "joined-teardown.json"}, "Impact UI retained unexpected private scratch")
                timeout()
            except BaseException as error:
                failure = failure or error
    if failure:
        raise failure
    return {**result, "ownership": record, "capture_scope": "Actual signed native public-source UI, no interception; private screenshots are not canonical fixture baselines"}
