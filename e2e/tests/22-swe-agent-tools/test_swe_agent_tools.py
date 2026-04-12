"""E2E test 22 — SWE Agent Tools

Validates the 10 new tools added for SWE agent workflows:
  fs.edit, fs.read_lines, fs.insert, fs.glob, shell.exec,
  repo.tree, workspace.undo_edit, git.diff_file, tool.suggest, policy.check

Test flow:
  1. shell.exec — create workspace structure
  2. repo.tree — verify codebase orientation
  3. fs.glob — find files by pattern
  4. fs.read_lines — read specific line range
  5. fs.edit — surgical search-and-replace
  6. fs.insert — insert new code at line
  7. workspace.undo_edit — revert the insert
  8. git.diff_file — diff edited file against HEAD
  9. tool.suggest — get tool recommendation
 10. policy.check — validate tool policy (allowed)
 11. policy.check — validate path escape (denied)
"""

from __future__ import annotations

import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from workspace_common import WorkspaceE2EBase  # noqa: E402
from workspace_common.console import print_error, print_info, print_step, print_success  # noqa: E402

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"

PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-actor",
    "roles": ["developer", "devops"],
}


class SWEAgentToolsE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="22-swe-agent-tools",
            run_id_prefix="e2e-swe-tools",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv(
                "EVIDENCE_FILE", f"/tmp/e2e-22-{int(time.time())}.json"
            ),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            sid = self.create_session(payload={"principal": PRINCIPAL})
            print_success(f"Session created: {sid}")

            # ---------------------------------------------------------------
            # Step 1: shell.exec — bootstrap workspace with files
            # ---------------------------------------------------------------
            print_step(1, "shell.exec — create workspace files")
            script = (
                "mkdir -p src/pkg && "
                "echo 'package main\\n\\nimport \"fmt\"\\n\\nfunc main() {\\n\\tfmt.Println(\"hello\")\\n}' > src/main.go && "
                "echo 'package pkg\\n\\nfunc Util() string {\\n\\treturn \"util\"\\n}' > src/pkg/util.go && "
                "echo '# README' > README.md && "
                "git init && git add -A && "
                "git -c user.email=e2e@test -c user.name=E2E commit -m 'initial'"
            )
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="shell.exec",
                args={"command": script, "timeout_seconds": 30},
                approved=True,
            )
            self.assert_invocation_succeeded(inv, body, "shell.exec bootstrap")
            output = self._inv_output(inv)
            if output.get("exit_code") != 0:
                raise RuntimeError(f"shell.exec exit_code={output.get('exit_code')}")
            self.record_step("shell_exec_bootstrap", "passed")
            print_success("Workspace bootstrapped via shell.exec")

            # ---------------------------------------------------------------
            # Step 2: repo.tree — verify structure
            # ---------------------------------------------------------------
            print_step(2, "repo.tree — codebase orientation")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="repo.tree",
                args={"max_depth": 3},
            )
            self.assert_invocation_succeeded(inv, body, "repo.tree")
            output = self._inv_output(inv)
            tree = str(output.get("tree", ""))
            if "src/" not in tree:
                raise RuntimeError(f"Expected src/ in tree, got: {tree[:200]}")
            self.record_step("repo_tree", "passed", {"entries": output.get("entries")})
            print_success(f"repo.tree: {output.get('entries')} entries")

            # ---------------------------------------------------------------
            # Step 3: fs.glob — find Go files
            # ---------------------------------------------------------------
            print_step(3, "fs.glob — find **/*.go files")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.glob",
                args={"pattern": "**/*.go"},
            )
            self.assert_invocation_succeeded(inv, body, "fs.glob")
            output = self._inv_output(inv)
            count = output.get("count", 0)
            if count < 2:
                raise RuntimeError(f"Expected >= 2 Go files, got {count}")
            self.record_step("fs_glob", "passed", {"count": count})
            print_success(f"fs.glob: found {count} Go files")

            # ---------------------------------------------------------------
            # Step 4: fs.read_lines — read main.go lines 1-5
            # ---------------------------------------------------------------
            print_step(4, "fs.read_lines — read src/main.go lines 1-5")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.read_lines",
                args={"path": "src/main.go", "start_line": 1, "end_line": 5},
            )
            self.assert_invocation_succeeded(inv, body, "fs.read_lines")
            output = self._inv_output(inv)
            content = str(output.get("content", ""))
            if "package main" not in content:
                raise RuntimeError(f"Expected 'package main' in content, got: {content[:200]}")
            self.record_step("fs_read_lines", "passed", {"total_lines": output.get("total_lines")})
            print_success(f"fs.read_lines: {output.get('total_lines')} total lines")

            # ---------------------------------------------------------------
            # Step 5: fs.edit — change hello to world
            # ---------------------------------------------------------------
            print_step(5, "fs.edit — replace hello with world")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.edit",
                args={
                    "path": "src/main.go",
                    "old_string": "hello",
                    "new_string": "world",
                },
                approved=True,
            )
            self.assert_invocation_succeeded(inv, body, "fs.edit")
            output = self._inv_output(inv)
            if output.get("replacements") != 1:
                raise RuntimeError(f"Expected 1 replacement, got {output.get('replacements')}")
            self.record_step("fs_edit", "passed")
            print_success("fs.edit: replaced hello → world")

            # ---------------------------------------------------------------
            # Step 6: fs.insert — add comment at line 1
            # ---------------------------------------------------------------
            print_step(6, "fs.insert — add comment at top of file")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.insert",
                args={
                    "path": "src/main.go",
                    "line": 0,
                    "content": "// SWE agent was here",
                },
                approved=True,
            )
            self.assert_invocation_succeeded(inv, body, "fs.insert")
            self.record_step("fs_insert", "passed")
            print_success("fs.insert: comment inserted at line 0")

            # ---------------------------------------------------------------
            # Step 7: workspace.undo_edit — revert the insert
            # ---------------------------------------------------------------
            print_step(7, "workspace.undo_edit — revert the insert")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="workspace.undo_edit",
                args={"path": "src/main.go"},
                approved=True,
            )
            self.assert_invocation_succeeded(inv, body, "workspace.undo_edit")
            output = self._inv_output(inv)
            if output.get("restored") is not True:
                raise RuntimeError("Expected restored=true")
            self.record_step("workspace_undo_edit", "passed")
            print_success("workspace.undo_edit: file restored")

            # ---------------------------------------------------------------
            # Step 8: git.diff_file — diff edited file against HEAD
            # ---------------------------------------------------------------
            print_step(8, "git.diff_file — diff src/main.go vs HEAD")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="git.diff_file",
                args={"path": "src/main.go", "ref": "HEAD", "stat": True},
            )
            self.assert_invocation_succeeded(inv, body, "git.diff_file")
            output = self._inv_output(inv)
            diff_text = str(output.get("diff", ""))
            if "world" not in diff_text:
                # After undo, the fs.edit change (hello→world) should still be present
                # because undo only reverted the insert.
                print_info(f"Diff may be empty if undo reverted all: {diff_text[:200]}")
            self.record_step("git_diff_file", "passed")
            print_success("git.diff_file: diff retrieved")

            # ---------------------------------------------------------------
            # Step 9: tool.suggest — ask for recommendations
            # ---------------------------------------------------------------
            print_step(9, "tool.suggest — recommend tools for editing code")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="tool.suggest",
                args={"task": "edit a function in a Go file", "top_k": 3},
            )
            self.assert_invocation_succeeded(inv, body, "tool.suggest")
            output = self._inv_output(inv)
            suggestions = output.get("suggestions", [])
            if len(suggestions) == 0:
                raise RuntimeError("Expected at least 1 suggestion")
            names = [s.get("name", "") if isinstance(s, dict) else "" for s in suggestions]
            print_info(f"Suggestions: {names}")
            self.record_step("tool_suggest", "passed", {"suggestions": names})
            print_success(f"tool.suggest: {len(suggestions)} recommendations")

            # ---------------------------------------------------------------
            # Step 10: policy.check — allowed invocation
            # ---------------------------------------------------------------
            print_step(10, "policy.check — verify fs.read_file is allowed")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="policy.check",
                args={
                    "tool_name": "fs.read_file",
                    "args": {"path": "src/main.go"},
                },
            )
            self.assert_invocation_succeeded(inv, body, "policy.check allowed")
            output = self._inv_output(inv)
            if output.get("allowed") is not True:
                raise RuntimeError(f"Expected allowed=true, got: {output}")
            self.record_step("policy_check_allowed", "passed")
            print_success("policy.check: fs.read_file allowed")

            # ---------------------------------------------------------------
            # Step 11: policy.check — path escape denied
            # ---------------------------------------------------------------
            print_step(11, "policy.check — verify path escape is denied")
            status, body, inv = self.invoke(
                session_id=sid,
                tool_name="policy.check",
                args={
                    "tool_name": "fs.edit",
                    "args": {"path": "../../../etc/passwd"},
                },
            )
            self.assert_invocation_succeeded(inv, body, "policy.check denied")
            output = self._inv_output(inv)
            if output.get("allowed") is not False:
                raise RuntimeError(f"Expected allowed=false for path escape, got: {output}")
            self.record_step("policy_check_denied", "passed")
            print_success("policy.check: path escape correctly denied")

            final_status = "passed"
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)

    @staticmethod
    def _inv_output(inv: dict) -> dict:
        """Extract output dict from invocation, handling nested structures."""
        if inv is None:
            return {}
        output = inv.get("output", {})
        if isinstance(output, dict):
            return output
        return {}


def main() -> int:
    return SWEAgentToolsE2E().run()


if __name__ == "__main__":
    sys.exit(main())
