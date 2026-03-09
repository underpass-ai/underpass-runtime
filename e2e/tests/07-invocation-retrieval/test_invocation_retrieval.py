"""E2E test: Invocation retrieval — get by ID, logs, artifacts, not found."""

from __future__ import annotations

import os
import sys
import time

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

DEFAULT_URL = "http://underpass-runtime.underpass-runtime.svc.cluster.local:50053"

PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-actor",
    "roles": ["developer"],
}


class InvocationRetrievalE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="07-invocation-retrieval",
            run_id_prefix="e2e-retrieval",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-07-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            sid = self.create_session(payload={"principal": PRINCIPAL})

            # Create an invocation to retrieve
            http_status, body, inv = self.invoke(
                session_id=sid, tool_name="fs.list", args={"path": "."}
            )
            self.assert_invocation_succeeded(invocation=inv, body=body, label="setup")
            inv_id = inv["id"]

            # --- Step 1: Get invocation by ID ---
            print_step(1, "GET /v1/invocations/{id} returns invocation")
            status, body = self.request("GET", f"/v1/invocations/{inv_id}")
            if status != 200:
                raise RuntimeError(f"get invocation: expected 200, got {status}")
            ret_inv = body.get("invocation", {})
            if ret_inv.get("id") != inv_id:
                raise RuntimeError(f"ID mismatch: expected {inv_id}, got {ret_inv.get('id')}")
            if ret_inv.get("session_id") != sid:
                raise RuntimeError(f"session_id mismatch: expected {sid}")
            if ret_inv.get("tool_name") != "fs.list":
                raise RuntimeError(f"tool_name mismatch: expected fs.list")
            if ret_inv.get("status") != "succeeded":
                raise RuntimeError(f"status mismatch: expected succeeded")
            self.record_step("get_by_id", "passed", {"invocation_id": inv_id})
            print_success(f"Retrieved invocation {inv_id}")

            # --- Step 2: Get invocation logs ---
            print_step(2, "GET /v1/invocations/{id}/logs returns log structure")
            status, body = self.request("GET", f"/v1/invocations/{inv_id}/logs")
            if status != 200:
                raise RuntimeError(f"get logs: expected 200, got {status}")
            logs = body.get("logs")
            if not isinstance(logs, list):
                raise RuntimeError(f"expected logs array, got {type(logs)}")
            self.record_step("get_logs", "passed", {"log_count": len(logs)})
            print_success(f"Retrieved {len(logs)} log entries")

            # --- Step 3: Get invocation artifacts ---
            print_step(3, "GET /v1/invocations/{id}/artifacts returns artifact structure")
            status, body = self.request("GET", f"/v1/invocations/{inv_id}/artifacts")
            if status != 200:
                raise RuntimeError(f"get artifacts: expected 200, got {status}")
            artifacts = body.get("artifacts")
            if not isinstance(artifacts, list):
                raise RuntimeError(f"expected artifacts array, got {type(artifacts)}")
            self.record_step("get_artifacts", "passed", {"artifact_count": len(artifacts)})
            print_success(f"Retrieved {len(artifacts)} artifacts")

            # --- Step 4: Invocation not found ---
            print_step(4, "GET /v1/invocations/nonexistent returns 404")
            status, _ = self.request("GET", "/v1/invocations/nonexistent-e2e-id")
            if status != 404:
                raise RuntimeError(f"expected 404, got {status}")
            self.record_step("invocation_not_found", "passed")
            print_success("Nonexistent invocation -> 404")

            # --- Step 5: Logs not found ---
            print_step(5, "GET /v1/invocations/nonexistent/logs returns 404")
            status, _ = self.request("GET", "/v1/invocations/nonexistent-e2e-id/logs")
            if status != 404:
                raise RuntimeError(f"expected 404, got {status}")
            self.record_step("logs_not_found", "passed")
            print_success("Nonexistent invocation logs -> 404")

            # --- Step 6: Artifacts not found ---
            print_step(6, "GET /v1/invocations/nonexistent/artifacts returns 404")
            status, _ = self.request("GET", "/v1/invocations/nonexistent-e2e-id/artifacts")
            if status != 404:
                raise RuntimeError(f"expected 404, got {status}")
            self.record_step("artifacts_not_found", "passed")
            print_success("Nonexistent invocation artifacts -> 404")

            final_status = "passed"
            print_success("All invocation retrieval tests passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)


def main() -> int:
    return InvocationRetrievalE2E().run()


if __name__ == "__main__":
    sys.exit(main())
