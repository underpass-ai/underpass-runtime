"""E2E test: gRPC health check."""

from __future__ import annotations

import os
import sys
import time

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"


class HealthE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="01-health",
            run_id_prefix="e2e-health",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-01-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            # --- Step 1: gRPC Health.Check returns status ok ---
            print_step(1, "gRPC Health.Check returns status ok")
            status, body = self.request("GET", "/healthz")
            self.record_step("healthz", "request_sent")

            if status != 200:
                raise RuntimeError(f"expected 200, got {status}")
            if body.get("status") != "ok":
                raise RuntimeError(f"expected status=ok, got {body}")
            self.record_step("healthz", "passed", {"status": status, "body": body})
            print_success(f"Health.Check -> status=ok")

            final_status = "passed"
            print_success("All health tests passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.write_evidence(final_status, error_message)


def main() -> int:
    return HealthE2E().run()


if __name__ == "__main__":
    sys.exit(main())
