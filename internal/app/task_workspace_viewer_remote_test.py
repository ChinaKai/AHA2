import os
import sys
import unittest
from unittest.mock import patch

import task_workspace_viewer_remote as worker


class WorkspaceViewerRemoteGitTest(unittest.TestCase):
    @patch.object(worker, "run_git_command", return_value=(b"", 0, False))
    def test_worker_honors_caller_timeout_and_keeps_the_existing_ceiling(self, run):
        request = {"args": ["for-each-ref", "--", "refs/remotes/origin/topic"], "limit": 8192}
        for timeout_ms, seconds in [(None, 12), (3500, 3.5), (15000, 12)]:
            with self.subTest(timeout_ms=timeout_ms):
                options = {"timeout_ms": timeout_ms} if timeout_ms is not None else {}
                worker.run_git({**request, **options}, ".git")
                argv, env, limit = run.call_args.args
                self.assertEqual(argv[-3:], request["args"])
                self.assertEqual(env["GIT_ALLOW_PROTOCOL"], "")
                self.assertEqual(env["GIT_OPTIONAL_LOCKS"], "0")
                self.assertEqual(limit, request["limit"])
                self.assertEqual(run.call_args.kwargs["timeout"], seconds)

    def test_killed_command_fails_and_budgeted_output_is_marked_truncated(self):
        data, exit_code, truncated = worker.run_git_command(
            [sys.executable, "-I", "-c", "import time; time.sleep(5)"], os.environ, 16, timeout=0.1,
        )
        self.assertEqual(data, b"")
        self.assertNotEqual(exit_code, 0)
        self.assertFalse(truncated)

        data, _, truncated = worker.run_git_command(
            [sys.executable, "-I", "-c", "print('a' * 100)"], os.environ, 16, timeout=5,
        )
        self.assertEqual(data, b"a" * 16)
        self.assertTrue(truncated)


if __name__ == "__main__":
    unittest.main()
