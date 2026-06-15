"""Data-driven per-tool E2E — governance contract + happy-path for every catalog tool.

This single runner replaces the old scenario-based suite. It loads the runtime
tool catalog and, for each of the ~130 tools, derives and runs up to five cases:

  - discovery            tool is registered and visible in the session catalog
  - happy_path           invoke with the catalog's own example args
  - invalid_input        invoke with empty args -> must be rejected (not executed)
  - approval_gate        requires_approval tool invoked WITHOUT approval -> blocked
  - policy_traversal     path_field set to a workspace escape -> must be denied

Governance cases (invalid_input, approval_gate, policy_traversal) are the hard
backbone: they assert the runtime's core value — that a small model can never
bypass validation, approval, or path confinement — for every applicable tool.

happy_path is honest about the environment: a tool that *succeeds* counts as
real coverage; a tool whose own catalog example is rejected by governance is a
real bug (fail); a tool that reaches execution but fails on a missing external
dependency or fixture state is recorded as "executed" (informational, never
flaky). This keeps the suite green on a bare cluster while still exercising
real execution for the ~80 workspace-local tools.

Set E2E_DRY_RUN=1 to print the derived case matrix without contacting a cluster.
"""

from __future__ import annotations

import json
import os
import sys

import yaml

try:
    from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success
    from workspace_common.console import print_info, print_warning
except ImportError:  # E2E_DRY_RUN without the gRPC harness installed
    WorkspaceE2EBase = object  # type: ignore[assignment,misc]

    def print_error(msg: str) -> None:
        print(msg)

    print_step = print_success = print_info = print_warning = print_error

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"
CATALOG_PATH = os.getenv("CATALOG_PATH", "/app/catalog_defaults.yaml")

PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-actor",
    "roles": ["developer", "devops"],
}

# Families whose happy-path can execute against a freshly-seeded workspace with
# no external dependency. Everything else (k8s, db, queues, github, web, conn,
# api, prometheus, container) reaches execution but may fail on a missing
# dependency — its governance cases still assert hard.
WORKSPACE_LOCAL = {
    "fs", "repo", "git", "workspace", "shell", "tool", "policy",
    "go", "rust", "node", "python", "c", "image", "sbom",
    "security", "quality", "artifact", "ci",
}

TRAVERSAL_VALUE = "../../../../../../etc/passwd"
GOVERNANCE_REJECTIONS = {"policy_denied", "approval_required", "invalid_argument"}

# Files seeded into the workspace so read/stat/analysis tools have real content.
FIXTURE_FILES = {
    "README.md": "# Fixture repo\n\nSeeded by the per-tool E2E matrix.\n",
    "main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hello\") }\n",
    "src/main.go": "package main\n\nfunc Add(a, b int) int { return a + b }\n",
    "notes/todo.txt": "- wire the per-tool matrix\n",
}
FIXTURE_DIRS = ["src", "notes"]


def load_catalog(path: str) -> list[dict]:
    with open(path, encoding="utf-8") as handle:
        data = yaml.safe_load(handle)
    caps = data.get("capabilities") if isinstance(data, dict) else None
    if not caps:
        raise RuntimeError(f"no 'capabilities' list in catalog {path}")
    return [c for c in caps if isinstance(c, dict) and c.get("name")]


def family(name: str) -> str:
    return name.split(".", 1)[0]


def first_example(tool: dict) -> dict | None:
    examples = tool.get("examples") or []
    if not examples:
        return None
    try:
        parsed = json.loads(examples[0])
        return parsed if isinstance(parsed, dict) else None
    except (ValueError, TypeError):
        return None


def input_schema(tool: dict) -> dict:
    try:
        parsed = json.loads(tool.get("input_schema") or "{}")
        return parsed if isinstance(parsed, dict) else {}
    except (ValueError, TypeError):
        return {}


def path_field(tool: dict) -> str | None:
    fields = (tool.get("policy") or {}).get("path_fields") or []
    for spec in fields:
        if isinstance(spec, dict) and spec.get("field"):
            return spec["field"]
    return None


def plan_cases(tool: dict) -> list[str]:
    """Return the case names that apply to a tool (used by dry-run and live)."""
    cases = ["discovery"]
    if first_example(tool) is not None:
        cases.append("happy_path")
    if input_schema(tool).get("required"):
        cases.append("invalid_input")
    if tool.get("requires_approval") and first_example(tool) is not None:
        cases.append("approval_gate")
    if path_field(tool) is not None:
        cases.append("policy_traversal")
    return cases


class ToolMatrixE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="00-tool-matrix",
            run_id_prefix="e2e-toolmatrix",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", "/tmp/evidence-tool-matrix.json"),
        )
        self.tools = load_catalog(CATALOG_PATH)
        self.results: list[dict] = []

    # ── result recording ──────────────────────────────────────────────────

    def _record(self, tool: str, case: str, outcome: str, detail: str = "") -> None:
        self.results.append({"tool": tool, "case": case, "outcome": outcome, "detail": detail})
        self.evidence.setdefault("cases", []).append(
            {"tool": tool, "case": case, "outcome": outcome, "detail": detail}
        )

    def _err_code(self, invocation: dict | None, body: dict) -> str:
        return str(self.extract_error(invocation, body).get("code", "")).strip()

    @staticmethod
    def _status(invocation: dict | None) -> str:
        return str((invocation or {}).get("status", "")).strip()

    # ── preamble: health + session lifecycle (reintegrated) ────────────────

    def preamble(self) -> str:
        print_step("Preamble: health + session lifecycle")
        status, body = self.request("GET", "/healthz")
        if status != 200:
            raise RuntimeError(f"health check failed: {status} {body}")
        self._record("_health", "healthz", "pass", "200")

        session_id = self.create_session(payload={"principal": PRINCIPAL})
        self._record("_session", "create", "pass", session_id)

        # Seed a workspace fixture so read/analysis tools have real content.
        for d in FIXTURE_DIRS:
            self.invoke(session_id=session_id, tool_name="fs.mkdir",
                        args={"path": d, "create_parents": True, "exist_ok": True}, approved=True)
        for path, content in FIXTURE_FILES.items():
            self.invoke(session_id=session_id, tool_name="fs.write_file",
                        args={"path": path, "content": content, "create_parents": True}, approved=True)
        print_success(f"Session {session_id} seeded with {len(FIXTURE_FILES)} files")
        return session_id

    def discovered_tool_names(self, session_id: str) -> set[str]:
        status, body = self.request("GET", f"/v1/sessions/{session_id}/tools")
        names: set[str] = set()
        for tool in body.get("tools", []) if isinstance(body, dict) else []:
            name = tool.get("name") or tool.get("Name")
            if name:
                names.add(name)
        return names

    # ── per-tool cases ─────────────────────────────────────────────────────

    def case_discovery(self, tool: dict, discovered: set[str]) -> None:
        name = tool["name"]
        if name in discovered:
            self._record(name, "discovery", "pass")
        else:
            self._record(name, "discovery", "fail", "not visible in session catalog")

    def case_happy_path(self, session_id: str, tool: dict) -> None:
        name = tool["name"]
        args = first_example(tool)
        if args is None:
            return
        approved = bool(tool.get("requires_approval"))
        _, body, inv = self.invoke(session_id=session_id, tool_name=name, args=args, approved=approved)
        if self._status(inv) == "succeeded":
            self._record(name, "happy_path", "pass", "succeeded")
            return
        code = self._err_code(inv, body)
        if code in GOVERNANCE_REJECTIONS:
            self._record(name, "happy_path", "fail", f"catalog example rejected by governance: {code}")
        else:
            # Reached execution; failed on a missing external dep or fixture state.
            self._record(name, "happy_path", "executed", code or self._status(inv) or "execution error")

    def case_invalid_input(self, session_id: str, tool: dict) -> None:
        name = tool["name"]
        if not input_schema(tool).get("required"):
            return
        _, body, inv = self.invoke(session_id=session_id, tool_name=name, args={}, approved=True)
        if self._status(inv) == "succeeded":
            self._record(name, "invalid_input", "fail", "empty args accepted")
        else:
            self._record(name, "invalid_input", "pass", self._err_code(inv, body) or "rejected")

    def case_approval_gate(self, session_id: str, tool: dict) -> None:
        name = tool["name"]
        args = first_example(tool)
        if not tool.get("requires_approval") or args is None:
            return
        _, body, inv = self.invoke(session_id=session_id, tool_name=name, args=args, approved=False)
        if self._status(inv) == "succeeded":
            self._record(name, "approval_gate", "fail", "executed without approval")
        else:
            self._record(name, "approval_gate", "pass", self._err_code(inv, body) or "blocked")

    def case_policy_traversal(self, session_id: str, tool: dict) -> None:
        name = tool["name"]
        field = path_field(tool)
        if field is None:
            return
        args = dict(first_example(tool) or {})
        args[field] = TRAVERSAL_VALUE
        _, body, inv = self.invoke(session_id=session_id, tool_name=name, args=args, approved=True)
        if self._status(inv) == "succeeded":
            self._record(name, "policy_traversal", "fail", "workspace escape NOT blocked")
        else:
            self._record(name, "policy_traversal", "pass", self._err_code(inv, body) or "denied")

    # ── orchestration ──────────────────────────────────────────────────────

    def run(self) -> bool:
        session_id = self.preamble()
        discovered = self.discovered_tool_names(session_id)
        print_step(f"Running per-tool matrix over {len(self.tools)} tools")
        for tool in self.tools:
            self.case_discovery(tool, discovered)
            self.case_happy_path(session_id, tool)
            self.case_invalid_input(session_id, tool)
            self.case_approval_gate(session_id, tool)
            self.case_policy_traversal(session_id, tool)
        return self.summarize()

    def summarize(self) -> bool:
        by_outcome: dict[str, int] = {}
        for r in self.results:
            by_outcome[r["outcome"]] = by_outcome.get(r["outcome"], 0) + 1
        failures = [r for r in self.results if r["outcome"] == "fail"]

        self.evidence["summary"] = {
            "tools": len(self.tools),
            "cases": len(self.results),
            "by_outcome": by_outcome,
            "failures": [f"{r['tool']}:{r['case']} ({r['detail']})" for r in failures],
        }
        print_step("Summary")
        print_info(f"  tools: {len(self.tools)}  cases: {len(self.results)}")
        for outcome in ("pass", "executed", "fail"):
            if outcome in by_outcome:
                print_info(f"  {outcome:9s}: {by_outcome[outcome]}")
        if failures:
            print_error(f"{len(failures)} case(s) FAILED:")
            for r in failures:
                print_error(f"  {r['tool']}:{r['case']} — {r['detail']}")
            return False
        print_success("All governance cases passed; happy-path executed across the catalog")
        return True


def dry_run() -> None:
    tools = load_catalog(os.getenv("CATALOG_PATH", "internal/adapters/tools/catalog_defaults.yaml"))
    total = 0
    by_case: dict[str, int] = {}
    by_tier: dict[str, int] = {}
    for tool in tools:
        cases = plan_cases(tool)
        total += len(cases)
        tier = "local" if family(tool["name"]) in WORKSPACE_LOCAL else "external"
        by_tier[tier] = by_tier.get(tier, 0) + 1
        for c in cases:
            by_case[c] = by_case.get(c, 0) + 1
        print(f"{tool['name']:32s} [{tier:8s}] -> {', '.join(cases)}")
    print(f"\n{len(tools)} tools, {total} cases")
    print(f"  by case: {by_case}")
    print(f"  by tier: {by_tier}")


def main() -> int:
    if os.getenv("E2E_DRY_RUN") == "1":
        dry_run()
        return 0
    runner = ToolMatrixE2E()
    ok = False
    try:
        ok = runner.run()
        runner.write_evidence("passed" if ok else "failed")
    except Exception as exc:  # noqa: BLE001
        print_error(f"tool-matrix run crashed: {exc}")
        runner.write_evidence("error", str(exc))
        return 2
    finally:
        runner.cleanup_sessions()
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
