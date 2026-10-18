"""Own a private Chrome session and finite Playwright client; no downloads."""
import argparse
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time

import qualify
import requalification_checks as checks
import score


class OwnedChild:
    def __init__(self, argv, directory, name):
        self.log = (directory / (name + ".log")).open("w")
        try:
            self.child = subprocess.Popen(argv, stdout=self.log, stderr=self.log, start_new_session=True)
        except BaseException:
            self.log.close()
            raise

    def stop(self):
        mode = "already_exited"
        if self.child.poll() is None:
            mode = "sigterm"
            self.child.terminate()
            try:
                self.child.wait(timeout=3)
            except subprocess.TimeoutExpired:
                mode = "sigkill"
                try:
                    os.killpg(self.child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                self.child.wait(timeout=3)
        else:
            self.child.wait()
        drained = qualify.serve.receipts._drain_group(self.child.pid, False)
        if drained != "clean":
            try:
                os.killpg(self.child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            drained = qualify.serve.receipts._drain_group(self.child.pid, True)
        self.log.close()
        score.require(drained == "clean" and mode != "sigkill", "owned browser/client group required forced cleanup or survived: " + mode + "/" + drained)
        return {"exit_code": self.child.returncode, "teardown": drained, "termination_mode": mode}


def run(binding, root, document, timeout):
    root.mkdir()
    profile = root / "private-chrome-profile"
    chrome, client = None, None
    failure = None
    value = {}
    try:
        chrome = OwnedChild([binding["tools"]["chrome"]["path"], "--headless=new", "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--user-data-dir=" + str(profile), "about:blank"], root, "chrome")
        deadline = time.monotonic() + min(5, timeout())
        active = profile / "DevToolsActivePort"
        while not active.exists():
            score.require(chrome.child.poll() is None, "installed Chrome exited before control readiness")
            score.require(time.monotonic() < deadline, "Chrome control readiness deadline exceeded")
            timeout(); time.sleep(0.02)
        port = int(active.read_text().splitlines()[0])
        score.require(0 < port < 65536, "Chrome control port invalid")
        document.update(cdp="http://127.0.0.1:" + str(port), node_modules=binding["node_modules"], out=str(root / "result.json"), screens=str(root / "screens"))
        private = root / "private-input.json"; private.write_text(json.dumps(document)); private.chmod(0o600)
        client = OwnedChild([binding["tools"]["node"]["path"], str(qualify.HERE / "requalification_browser.cjs"), str(private)], root, "client")
        client.child.wait(timeout=timeout())
        score.require(client.child.returncode == 0, "Playwright source UI/preflight failed")
        value = checks.bounded_json(root / "result.json", 65536)
        score.require(value["outcome"] == "passed", "source UI/preflight result unavailable")
    except BaseException as error:
        failure = error
    finally:
        for name, owned in (("client", client), ("chrome", chrome)):
            if owned:
                try:
                    value[name + "_terminal"] = owned.stop()
                except BaseException as error:
                    failure = failure or error
                    value[name + "_cleanup_failure"] = str(error)
        try:
            (root / "private-input.json").unlink(missing_ok=True)
            if profile.exists():
                shutil.rmtree(profile)
        except BaseException as error:
            failure = failure or error
            value["custody_cleanup_failure"] = str(error)
        (root / "joined-teardown.json").write_text(json.dumps(value, indent=2) + "\n")
    if failure:
        raise failure
    value["chrome_path"] = str(Path(binding["tools"]["chrome"]["path"]).resolve())
    return value


def main():
    parser = argparse.ArgumentParser(description="One private installed-Chrome preflight, before the native product clock")
    parser.add_argument("--chrome", required=True)
    parser.add_argument("--node", required=True)
    parser.add_argument("--node-modules", required=True)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--execute-reviewed-preflight", action="store_true")
    args = parser.parse_args()
    score.require(args.execute_reviewed_preflight, "preflight requires reviewed code and assigned native slot")
    score.require(not args.root.exists() and not args.out.exists(), "preflight output exists; no silent retry")
    limit = checks.Deadline(15)
    value = run({"tools": {"chrome": {"path": args.chrome}, "node": {"path": args.node}}, "node_modules": args.node_modules}, args.root, {"preflight": True}, lambda: limit.timeout(10))
    value["seconds"] = limit.elapsed()
    limit.remaining()
    args.out.write_text(json.dumps(value, indent=2) + "\n")


if __name__ == "__main__":
    main()
