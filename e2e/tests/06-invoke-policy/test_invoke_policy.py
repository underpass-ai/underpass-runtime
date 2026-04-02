"""E2E test: Invocation policy enforcement — approval, path traversal."""

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


class InvokePolicyE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="06-invoke-policy",
            run_id_prefix="e2e-policy",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-06-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            sid = self.create_session(payload={"principal": PRINCIPAL})

            # --- Step 1: Write without approval -> denied ---
            print_step(1, "fs.write_file without approved=true -> approval_required")
            http_status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.write_file",
                args={"path": "test.txt", "content": "hello"},
                approved=False,
            )
            if inv is None:
                raise RuntimeError("expected invocation in response")
            inv_status = inv.get("status", "")
            if inv_status != "denied":
                raise RuntimeError(f"expected denied, got {inv_status}")
            error = self.extract_error(inv, body)
            if error.get("code") != "approval_required":
                raise RuntimeError(f"expected approval_required, got {error.get('code')}")
            self.record_step("approval_required", "passed")
            print_success("Write without approval -> denied (approval_required)")

            # --- Step 2: Write with approval -> succeeded ---
            print_step(2, "fs.write_file with approved=true -> succeeded")
            http_status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.write_file",
                args={"path": "e2e-policy-test.txt", "content": "e2e content"},
                approved=True,
            )
            self.assert_invocation_succeeded(invocation=inv, body=body, label="write_with_approval")
            self.record_step("write_approved", "passed", {"invocation_id": inv["id"] if inv else None})
            print_success("Write with approval -> succeeded")

            # Verify: read the file back
            http_status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.read_file",
                args={"path": "e2e-policy-test.txt"},
            )
            self.assert_invocation_succeeded(invocation=inv, body=body, label="read_back")
            self.record_step("read_back_verified", "passed")
            print_success("File content verified via read_file")

            # --- Step 3: Path traversal -> policy_denied ---
            print_step(3, "fs.list with path traversal -> policy_denied")
            http_status, body, inv = self.invoke(
                session_id=sid,
                tool_name="fs.list",
                args={"path": "../../etc"},
            )
            if inv is None:
                raise RuntimeError("expected invocation in response")
            inv_status = inv.get("status", "")
            if inv_status != "denied":
                raise RuntimeError(f"expected denied, got {inv_status}")
            error = self.extract_error(inv, body)
            if error.get("code") != "policy_denied":
                raise RuntimeError(f"expected policy_denied, got {error.get('code')}")
            self.record_step("path_traversal", "passed")
            print_success("Path traversal -> denied (policy_denied)")

            final_status = "passed"
            print_success("All policy enforcement tests passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)


def main() -> int:
    return InvokePolicyE2E().run()


if __name__ == "__main__":
    sys.exit(main())
