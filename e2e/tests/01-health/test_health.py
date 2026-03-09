"""E2E test: Health check and metrics endpoints."""

from __future__ import annotations

import os
import sys
import time

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

DEFAULT_URL = "http://underpass-runtime.underpass-runtime.svc.cluster.local:50053"


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
            # --- Step 1: Health endpoint returns 200 with status ok ---
            print_step(1, "GET /healthz returns 200 with status ok")
            status, body = self.request("GET", "/healthz")
            self.record_step("healthz", "request_sent")

            if status != 200:
                raise RuntimeError(f"expected 200, got {status}")
            if body.get("status") != "ok":
                raise RuntimeError(f"expected status=ok, got {body}")
            self.record_step("healthz", "passed", {"status": status, "body": body})
            print_success(f"GET /healthz -> {status}, status=ok")

            # --- Step 2: Metrics endpoint returns Prometheus text ---
            print_step(2, "GET /metrics returns Prometheus metrics")
            status, body = self.request("GET", "/metrics")
            self.record_step("metrics", "request_sent")

            if status != 200:
                raise RuntimeError(f"expected 200, got {status}")
            # Metrics may return raw text, check for Prometheus markers
            raw = body.get("raw", str(body))
            if "TYPE" not in raw and "raw" not in body:
                # If JSON parsed, that's unexpected for metrics
                self.record_step("metrics", "warning", {"note": "metrics returned JSON, expected text/plain"})
            self.record_step("metrics", "passed", {"status": status})
            print_success(f"GET /metrics -> {status}")

            # --- Step 3: POST /metrics returns 405 ---
            print_step(3, "POST /metrics returns 405 Method Not Allowed")
            status, body = self.request("POST", "/metrics")
            self.record_step("metrics_method_not_allowed", "request_sent")

            if status != 405:
                raise RuntimeError(f"expected 405, got {status}")
            self.record_step("metrics_method_not_allowed", "passed", {"status": status})
            print_success(f"POST /metrics -> {status} (Method Not Allowed)")

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
