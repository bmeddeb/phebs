"""Fences for the operating-groundwork helpers; the full rehearsal needs the real binary."""
import os
import subprocess
import sys
import time
import unittest

import caller_operating


class HelperTests(unittest.TestCase):
    def test_session_rss_counts_only_the_session(self):
        child = subprocess.Popen([sys.executable, '-c', 'import time; x = bytearray(32 << 20); time.sleep(3)'],
                                 start_new_session=True)
        try:
            time.sleep(0.5)
            with caller_operating.SessionRSS(child.pid, interval=0.1) as rss:
                time.sleep(0.5)
            summary = rss.summary()
            self.assertGreater(summary['peak_bytes'], 32 << 20)
            self.assertEqual(summary['peak_processes'], 1)
        finally:
            child.kill()
            child.wait()
        with caller_operating.SessionRSS(os.getpid() + 10_000_000, interval=0.05) as empty:
            time.sleep(0.2)
        self.assertEqual(empty.summary(), {'samples': 0})

    def test_percentiles(self):
        stats = caller_operating.percentiles([i / 1000 for i in range(1, 101)])
        self.assertEqual((stats['n'], stats['p50_ms'], stats['p95_ms'], stats['max_ms']), (100, 51.0, 96.0, 100.0))


if __name__ == '__main__':
    unittest.main()
