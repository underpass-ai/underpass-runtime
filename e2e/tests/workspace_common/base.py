"""Shared base helpers for underpass-runtime E2E tests."""

from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from typing import Any

from .console import print_info, print_warning


def _env_bool(name: str, default: bool = False) -> bool:
    """Read a boolean from an environment variable (1/true/yes)."""
    val = os.getenv(name, "").strip().lower()
    if not val:
        return default
    return val in ("1", "true", "yes")


class WorkspaceE2EBase:
    """Reusable base class for workspace E2E tests.

    Provides:
    - HTTP requests with optional trusted header auth
    - session lifecycle helpers
    - tool invocation tracking
    - evidence recording and emission

    Debug mode (E2E_DEBUG=1):
    - Logs every HTTP request/response
    - Skips session cleanup so workspaces can be inspected
    - Prints session IDs and invocation details inline
    """

    def __init__(
        self,
        *,
        test_id: str,
        run_id_prefix: str,
        workspace_url: str,
        evidence_file: str,
    ) -> None:
        self.test_id = test_id
        self.workspace_url = workspace_url.rstrip("/")
        self.evidence_file = evidence_file
        self.run_id = f"{run_id_prefix}-{int(time.time())}"

        self.debug = _env_bool("E2E_DEBUG")
        self.skip_cleanup = _env_bool("E2E_SKIP_CLEANUP") or self.debug

        self.sessions: list[str] = []
        self.invocation_counter = 0

        self.evidence: dict[str, Any] = {
            "test_id": self.test_id,
            "run_id": self.run_id,
            "status": "running",
            "started_at": self.now_iso(),
            "workspace_url": self.workspace_url,
            "debug": self.debug,
            "skip_cleanup": self.skip_cleanup,
            "steps": [],
            "sessions": [],
            "invocations": [],
        }

        if self.debug:
            print_info(f"DEBUG MODE — cleanup disabled, verbose logging")
            print_info(f"  test_id:       {self.test_id}")
            print_info(f"  run_id:        {self.run_id}")
            print_info(f"  workspace_url: {self.workspace_url}")

    @staticmethod
    def now_iso() -> str:
        return datetime.now(timezone.utc).isoformat()

    def request(
        self,
        method: str,
        path: str,
        payload: dict[str, Any] | None = None,
        timeout: int = 60,
    ) -> tuple[int, dict[str, Any]]:
        url = self.workspace_url + path
        data = None
        headers = {"Content-Type": "application/json"}
        auth_token = os.getenv("WORKSPACE_AUTH_TOKEN", "").strip()
        if auth_token:
            headers.update(
                {
                    os.getenv("WORKSPACE_AUTH_TOKEN_HEADER", "X-Workspace-Auth-Token"): auth_token,
                    os.getenv("WORKSPACE_AUTH_TENANT_HEADER", "X-Workspace-Tenant-Id"): os.getenv(
                        "WORKSPACE_AUTH_TENANT_ID", "e2e-tenant"
                    ),
                    os.getenv("WORKSPACE_AUTH_ACTOR_HEADER", "X-Workspace-Actor-Id"): os.getenv(
                        "WORKSPACE_AUTH_ACTOR_ID", "e2e-workspace"
                    ),
                    os.getenv("WORKSPACE_AUTH_ROLES_HEADER", "X-Workspace-Roles"): os.getenv(
                        "WORKSPACE_AUTH_ROLES", "developer,devops"
                    ),
                }
            )
        if payload is not None:
            data = json.dumps(payload).encode("utf-8")

        if self.debug:
            print_info(f"  >> {method} {path}")
            if payload is not None:
                print_info(f"     body: {json.dumps(payload, ensure_ascii=False)[:200]}")

        req = urllib.request.Request(url, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as response:
                body = response.read().decode("utf-8")
                code = response.getcode()
                try:
                    parsed = json.loads(body) if body else {}
                except (json.JSONDecodeError, ValueError):
                    parsed = {"raw": body}
                if self.debug:
                    print_info(f"  << {code} ({len(body)} bytes)")
                return code, parsed
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8")
            try:
                parsed = json.loads(body) if body else {}
            except Exception:
                parsed = {"raw": body}
            if self.debug:
                print_info(f"  << {exc.code} (error, {len(body)} bytes)")
            return exc.code, parsed

    def record_step(self, name: str, status: str, data: Any | None = None) -> None:
        entry: dict[str, Any] = {"at": self.now_iso(), "step": name, "status": status}
        if data is not None:
            entry["data"] = data
        self.evidence["steps"].append(entry)

    def extract_error(self, invocation: dict[str, Any] | None, body: dict[str, Any]) -> dict[str, Any]:
        if isinstance(invocation, dict) and isinstance(invocation.get("error"), dict):
            return invocation["error"]
        if isinstance(body.get("error"), dict):
            return body["error"]
        return {}

    def record_invocation(
        self,
        *,
        session_id: str,
        tool: str,
        http_status: int,
        invocation: dict[str, Any] | None,
        body: dict[str, Any],
    ) -> None:
        error = self.extract_error(invocation, body)
        self.evidence["invocations"].append(
            {
                "at": self.now_iso(),
                "session_id": session_id,
                "tool": tool,
                "http_status": http_status,
                "invocation_id": invocation.get("id") if isinstance(invocation, dict) else None,
                "invocation_status": invocation.get("status") if isinstance(invocation, dict) else None,
                "error_code": error.get("code"),
                "error_message": error.get("message"),
            }
        )

    def create_session(
        self,
        *,
        payload: dict[str, Any],
        session_record: dict[str, Any] | None = None,
    ) -> str:
        status, body = self.request("POST", "/v1/sessions", payload)
        if status != 201:
            raise RuntimeError(f"create session failed ({status}): {body}")

        session_id = str(body.get("session", {}).get("id", "")).strip()
        if not session_id:
            raise RuntimeError(f"create session missing id: {body}")

        self.sessions.append(session_id)
        entry: dict[str, Any] = {"at": self.now_iso(), "session_id": session_id}
        if session_record is not None:
            entry.update(session_record)
        else:
            entry["payload"] = payload

        # Capture workspace_path from response for debug inspection
        workspace_path = body.get("session", {}).get("workspace_path", "")
        if workspace_path:
            entry["workspace_path"] = workspace_path

        self.evidence["sessions"].append(entry)

        if self.debug:
            print_info(f"  SESSION created: {session_id}")
            if workspace_path:
                print_info(f"    workspace_path: {workspace_path}")
        return session_id

    def invoke(
        self,
        *,
        session_id: str,
        tool_name: str,
        args: dict[str, Any],
        approved: bool = False,
        timeout: int = 120,
    ) -> tuple[int, dict[str, Any], dict[str, Any] | None]:
        self.invocation_counter += 1
        payload = {
            "correlation_id": f"{self.run_id}-{self.invocation_counter:04d}",
            "approved": approved,
            "args": args,
        }
        status, body = self.request(
            "POST",
            f"/v1/sessions/{session_id}/tools/{tool_name}/invoke",
            payload,
            timeout=timeout,
        )
        invocation = body.get("invocation") if isinstance(body, dict) else None
        self.record_invocation(
            session_id=session_id,
            tool=tool_name,
            http_status=status,
            invocation=invocation if isinstance(invocation, dict) else None,
            body=body if isinstance(body, dict) else {},
        )

        if self.debug and isinstance(invocation, dict):
            inv_id = invocation.get("id", "?")
            inv_status = invocation.get("status", "?")
            duration = invocation.get("duration_ms", "?")
            print_info(f"  INVOKE {tool_name} -> {inv_status} (id={inv_id}, {duration}ms)")

        return status, body, invocation if isinstance(invocation, dict) else None

    def assert_invocation_succeeded(
        self,
        *,
        invocation: dict[str, Any] | None,
        body: dict[str, Any],
        label: str,
    ) -> None:
        if invocation is None:
            raise RuntimeError(f"{label}: missing invocation")
        status = str(invocation.get("status", "")).strip()
        if status != "succeeded":
            error = self.extract_error(invocation, body)
            code = str(error.get("code", "")).strip()
            raise RuntimeError(f"{label}: expected succeeded, got {status} ({code})")

    def assert_policy_denied(
        self,
        *,
        status: int,
        body: dict[str, Any],
        invocation: dict[str, Any] | None,
        label: str,
    ) -> None:
        if invocation is None:
            raise RuntimeError(f"{label}: missing invocation in response body")

        code = str(self.extract_error(invocation, body).get("code", "")).strip()
        if code != "policy_denied":
            raise RuntimeError(
                f"{label}: expected policy_denied, got code={code}, status={status}, body={body}"
            )

    def assert_not_policy_denied(
        self,
        *,
        status: int,
        body: dict[str, Any],
        invocation: dict[str, Any] | None,
        label: str,
    ) -> None:
        if invocation is None:
            raise RuntimeError(f"{label}: missing invocation in response body")

        code = str(self.extract_error(invocation, body).get("code", "")).strip()
        if code in ("policy_denied", "approval_required"):
            raise RuntimeError(f"{label}: unexpected policy block ({code})")

        inv_status = str(invocation.get("status", "")).strip()
        if inv_status != "succeeded":
            raise RuntimeError(
                f"{label}: expected succeeded invocation, got status={inv_status}, status={status}, body={body}"
            )

    def write_evidence(self, status: str, error_message: str = "") -> None:
        self.evidence["status"] = status
        self.evidence["ended_at"] = self.now_iso()
        if error_message:
            self.evidence["error_message"] = error_message

        if self.debug:
            self._print_debug_summary()

        try:
            with open(self.evidence_file, "w", encoding="utf-8") as handle:
                json.dump(self.evidence, handle, ensure_ascii=False, indent=2)
            print_warning(f"Evidence file: {self.evidence_file}")
        except Exception as exc:
            print_warning(f"Could not write evidence file: {exc}")

        print("EVIDENCE_JSON_START")
        print(json.dumps(self.evidence, ensure_ascii=False, indent=2))
        print("EVIDENCE_JSON_END")

    def _print_debug_summary(self) -> None:
        from .console import Colors
        sep = f"{Colors.BLUE}{'─' * 72}{Colors.NC}"
        print(f"\n{sep}")
        print(f"{Colors.BLUE}  DEBUG SUMMARY — {self.test_id} ({self.run_id}){Colors.NC}")
        print(sep)

        print(f"\n  Sessions ({len(self.sessions)}):")
        for s in self.evidence.get("sessions", []):
            sid = s.get("session_id", "?")
            wp = s.get("workspace_path", "(no path)")
            print(f"    {sid}  ->  {wp}")

        invocations = self.evidence.get("invocations", [])
        print(f"\n  Invocations ({len(invocations)}):")
        for inv in invocations:
            iid = inv.get("invocation_id") or "—"
            tool = inv.get("tool", "?")
            ist = inv.get("invocation_status") or inv.get("error_code") or "?"
            http = inv.get("http_status", "?")
            print(f"    {iid}  {tool:30s}  {ist:12s}  HTTP {http}")

        if self.skip_cleanup:
            print(f"\n  {Colors.YELLOW}Sessions left open for inspection.{Colors.NC}")
            print(f"  Inspect with:")
            print(f"    kubectl exec -n underpass-runtime deploy/valkey -- valkey-cli KEYS 'workspace:session:*'")
            print(f"    kubectl debug -n underpass-runtime <pod> --image=busybox --target=underpass-runtime --profile=general")

        print(f"\n{sep}\n")

    def cleanup_sessions(self) -> None:
        if self.skip_cleanup:
            print_warning(
                f"SKIP CLEANUP — {len(self.sessions)} session(s) left open for inspection: "
                + ", ".join(self.sessions)
            )
            return
        for session_id in self.sessions:
            try:
                self.request("DELETE", f"/v1/sessions/{session_id}")
            except Exception:
                pass
