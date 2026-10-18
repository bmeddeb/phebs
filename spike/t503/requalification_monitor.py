"""Joined ownership for the original Monitor's observer and host samples."""
import json
import os
import re
import select
import signal
import subprocess
import threading
import time

import qualify
import score


def finish_child(child):
    """Terminate only an owned child; join even when forced cleanup is needed."""
    forced = False
    if child.poll() is None:
        child.terminate()
        try:
            child.wait(timeout=0.5)
        except subprocess.TimeoutExpired:
            forced = True
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            child.wait(timeout=1)
    else:
        child.wait()
    return forced


class JoinedMonitor(qualify.Monitor):
    def __init__(self, *args):
        self.host_lock = threading.Lock()
        self.host_child = None
        self.host_terminals = []
        self.joined = False
        self.ownership = {}
        # Keep the original observer/protocol/cadences and measurement bounds.
        # Override its run/stop only to give host subprocesses bounded ownership.
        try:
            super().__init__(*args)
        except BaseException:
            if hasattr(self, "child"):
                self.child.kill()
                self.child.wait(timeout=1)
                for pipe in (self.child.stdout, self.child.stdin):
                    pipe.close()
            if hasattr(self, "stderr"):
                self.stderr.close()
            raise

    def host_sample(self):
        child = subprocess.Popen(["/usr/bin/vm_stat"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, start_new_session=True)
        with self.host_lock:
            self.host_child = child
        forced = False
        try:
            raw, _ = child.communicate(timeout=2)
            score.require(child.returncode == 0 and len(raw) <= 65536, "physical measurement unavailable or oversized")
            size = int(re.search(r"page size of (\d+) bytes", raw).group(1))
            counts = {name: int(re.search(r"Pages " + name + r":\s+(\d+)\.", raw).group(1)) for name in ("free", "inactive", "speculative")}
            return {"available_physical_bytes": size * sum(counts.values()), "page_size": size, "pages": counts, "formula": "page_size*(free+inactive+speculative); compressor and swap excluded"}
        finally:
            forced = finish_child(child)
            for pipe in (child.stdout, child.stderr):
                if pipe is not None:
                    pipe.close()
            with self.host_lock:
                self.host_terminals.append({"exit_code": child.returncode, "joined": True, "forced": forced})
                self.host_child = None

    def allocated_sample(self):
        total = 0
        for path, directories, files in os.walk(self.data, followlinks=False):
            if self.done.is_set():
                return None
            for name in directories + files:
                if self.done.is_set():
                    return None
                entry = qualify.Path(path) / name
                score.require(not entry.is_symlink(), "derived data measurement encountered a symlink")
                total += entry.stat().st_blocks * 512
        return total

    def run(self):
        try:
            last_host = 0
            while not self.done.is_set():
                self.child.stdin.write(str(self.pid) + "\n")
                self.child.stdin.flush()
                score.require(select.select([self.child.stdout], [], [], 2)[0], "native process measurement unavailable")
                sample = json.loads(self.child.stdout.readline())
                score.require(not sample.get("unavailable") and sample.get("rss_bytes", 0) > 0, "native process measurement unavailable")
                score.require(sample["rss_bytes"] <= self.bounds["process_tree_sampled_max_rss_bytes"], "sampled RSS bound exceeded")
                self.samples.append(sample)
                if self.done.is_set():
                    break
                if time.monotonic() - last_host >= 1:
                    host = self.host_sample()
                    score.require(host["available_physical_bytes"] >= self.bounds["minimum_available_physical_headroom_bytes"], "actual physical headroom below frozen bound")
                    self.headroom.append(host)
                    size = self.allocated_sample()
                    if size is None:
                        break
                    score.require(size <= self.bounds["derived_allocated_disk_bytes"], "observed allocated derived data exceeds frozen bound")
                    self.disk.append(size)
                    last_host = time.monotonic()
                self.done.wait(0.2)
        except BaseException as error:
            self.failure = str(error)
            self.done.set()

    def stop(self):
        self.done.set()
        forced_sampling = False
        observer_forced = False
        self.thread.join(timeout=4.5)
        if self.thread.is_alive():
            forced_sampling = True
            self.failure = self.failure or "sampling thread required forced cancellation"
            with self.host_lock:
                host = self.host_child
            if host is not None:
                finish_child(host)
            if self.child.poll() is None:
                observer_forced = True
                self.child.kill()  # exact frozen observer does not spawn children
                self.child.wait(timeout=1)
            self.thread.join(timeout=2)
        thread_joined = not self.thread.is_alive()
        self.ownership = {"sampling_thread_joined": thread_joined, "host_children": self.host_terminals, "forced_sampling_cancellation": forced_sampling}
        score.require(thread_joined, "sampling thread did not join; retain derived custody")
        try:
            self.child.stdin.close()
        except OSError as error:
            self.failure = self.failure or str(error)
        try:
            self.child.wait(timeout=1)
        except subprocess.TimeoutExpired:
            observer_forced = True
            self.failure = self.failure or "observer required forced cleanup"
            self.child.kill()
            self.child.wait(timeout=1)
        self.ownership["observer"] = {"exit_code": self.child.returncode, "joined": True, "forced": observer_forced}
        self.joined = self.host_child is None or self.host_child.poll() is not None
        # Already joined all child/thread activity. Reuse the frozen metrics
        # projection and required-sample checks without its unsafe wait path.
        try:
            score.require(self.child.returncode == 0, "observer exited unsuccessfully")
            return {**super().stop(), "ownership": self.ownership}
        finally:
            for pipe in (self.child.stdout, self.child.stdin, self.stderr):
                pipe.close()
