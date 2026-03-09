"""E2E test: Session lifecycle — create, close, metadata, idempotency."""

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


class SessionLifecycleE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="02-session-lifecycle",
            run_id_prefix="e2e-session",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-02-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            # --- Step 1: Create and close a session ---
            print_step(1, "Create session and verify fields")
            sid = self.create_session(payload={"principal": PRINCIPAL})
            self.record_step("create_and_close", "session_created", {"session_id": sid})

            # Verify fields via evidence (create_session already validated id)
            status, body = self.request("DELETE", f"/v1/sessions/{sid}")
            if status != 200:
                raise RuntimeError(f"close session: expected 200, got {status}")
            if not body.get("closed"):
                raise RuntimeError(f"close session: expected closed=true, got {body}")
            self.record_step("create_and_close", "passed")
            print_success(f"Session {sid} created and closed")

            # --- Step 2: Create with metadata ---
            print_step(2, "Create session with metadata")
            meta = {"project": "underpass", "env": "e2e"}
            sid2 = self.create_session(payload={"principal": PRINCIPAL, "metadata": meta})
            self.record_step("metadata", "passed", {"session_id": sid2})
            print_success(f"Session with metadata created: {sid2}")

            # --- Step 3: Close idempotent (nonexistent session) ---
            print_step(3, "Close nonexistent session is idempotent")
            status, body = self.request("DELETE", "/v1/sessions/does-not-exist-e2e")
            if status != 200:
                raise RuntimeError(f"idempotent close: expected 200, got {status}")
            self.record_step("close_idempotent", "passed")
            print_success("Close nonexistent session returned 200")

            # --- Step 4: POST to sessions required, GET returns 405 ---
            print_step(4, "GET /v1/sessions returns 405")
            status, _ = self.request("GET", "/v1/sessions")
            if status != 405:
                raise RuntimeError(f"method not allowed: expected 405, got {status}")
            self.record_step("method_not_allowed", "passed")
            print_success("GET /v1/sessions -> 405")

            # --- Step 5: Two concurrent sessions are independent ---
            print_step(5, "Multiple sessions are independent")
            sid_a = self.create_session(payload={"principal": PRINCIPAL})
            sid_b = self.create_session(payload={"principal": PRINCIPAL})
            if sid_a == sid_b:
                raise RuntimeError("two sessions have same ID")
            self.record_step("multiple_independent", "passed", {"a": sid_a, "b": sid_b})
            print_success(f"Two independent sessions: {sid_a}, {sid_b}")

            # --- Step 6: Double close is idempotent ---
            print_step(6, "Double close same session")
            sid_c = self.create_session(payload={"principal": PRINCIPAL})
            s1, _ = self.request("DELETE", f"/v1/sessions/{sid_c}")
            s2, _ = self.request("DELETE", f"/v1/sessions/{sid_c}")
            if s1 != 200 or s2 != 200:
                raise RuntimeError(f"double close: expected 200/200, got {s1}/{s2}")
            self.record_step("double_close", "passed")
            print_success("Double close returned 200 both times")

            # --- Step 7: Create with explicit session ID ---
            print_step(7, "Create session with explicit ID")
            explicit_id = f"e2e-explicit-{int(time.time())}"
            sid_d = self.create_session(
                payload={"principal": PRINCIPAL, "session_id": explicit_id}
            )
            if sid_d != explicit_id:
                raise RuntimeError(f"explicit ID: expected {explicit_id}, got {sid_d}")
            self.record_step("explicit_id", "passed", {"session_id": sid_d})
            print_success(f"Explicit session ID honoured: {sid_d}")

            final_status = "passed"
            print_success("All session lifecycle tests passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)


def main() -> int:
    return SessionLifecycleE2E().run()


if __name__ == "__main__":
    sys.exit(main())
