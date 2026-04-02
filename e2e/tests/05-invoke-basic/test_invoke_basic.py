"""E2E test: Basic tool invocation — fs.list, correlation ID, error cases."""

from __future__ import annotations

import os
import sys
import time

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"

PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-actor",
    "roles": ["developer"],
}


class InvokeBasicE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="05-invoke-basic",
            run_id_prefix="e2e-invoke",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-05-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            sid = self.create_session(payload={"principal": PRINCIPAL})

            # --- Step 1: Invoke fs.list succeeds ---
            print_step(1, "Invoke fs.list on workspace root")
            http_status, body, inv = self.invoke(
                session_id=sid, tool_name="fs.list", args={"path": "."}
            )
            if http_status != 200:
                raise RuntimeError(f"fs.list: expected 200, got {http_status}")
            self.assert_invocation_succeeded(invocation=inv, body=body, label="fs.list")
            if not inv or not inv.get("id"):
                raise RuntimeError("fs.list: invocation missing id")
            self.record_step("invoke_fs_list", "passed", {"invocation_id": inv["id"]})
            print_success(f"fs.list succeeded, invocation={inv['id']}")

            # --- Step 2: Correlation ID echoed ---
            print_step(2, "Invoke with correlation_id")
            http_status, body, inv = self.invoke(
                session_id=sid, tool_name="fs.list", args={"path": "."}
            )
            if http_status != 200:
                raise RuntimeError(f"correlation: expected 200, got {http_status}")
            # Our base class auto-generates correlation_id; _normalize_dict handles camelCase
            corr = inv.get("correlation_id", "") if inv else ""
            if not corr:
                raise RuntimeError("correlation_id not echoed in invocation")
            self.record_step("correlation_id", "passed", {"correlation_id": corr})
            print_success(f"Correlation ID echoed: {corr}")

            # --- Step 3: Tool not found ---
            print_step(3, "Invoke nonexistent tool")
            http_status, body, inv = self.invoke(
                session_id=sid, tool_name="nonexistent.tool", args={}
            )
            # Should return error (404 or error in body)
            if http_status == 200 and inv and inv.get("status") == "succeeded":
                raise RuntimeError("nonexistent tool should not succeed")
            self.record_step("tool_not_found", "passed", {"http_status": http_status})
            print_success(f"Nonexistent tool -> {http_status}")

            # --- Step 4: Session not found ---
            print_step(4, "Invoke on nonexistent session returns 404")
            http_status, body = self.request(
                "POST",
                "/v1/sessions/does-not-exist/tools/fs.list/invoke",
                {"args": {"path": "."}, "approved": False},
            )
            if http_status != 404:
                raise RuntimeError(f"expected 404, got {http_status}")
            self.record_step("session_not_found", "passed")
            print_success("Nonexistent session -> 404")

            final_status = "passed"
            print_success("All basic invocation tests passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)


def main() -> int:
    return InvokeBasicE2E().run()


if __name__ == "__main__":
    sys.exit(main())
