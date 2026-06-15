"""E2E test: gRPC health check."""

from __future__ import annotations

import os
import re
import sys
import time

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"
PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-health",
    "roles": ["developer"],
}


def metric_value(metrics_text: str, metric_name: str, labels: str = "") -> float:
    suffix = labels if labels else ""
    pattern = rf"^{re.escape(metric_name)}{re.escape(suffix)}\s+([0-9]+(?:\.[0-9]+)?)$"
    for line in metrics_text.splitlines():
        match = re.match(pattern, line.strip())
        if match:
            return float(match.group(1))
    return 0.0


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

            # --- Step 2: Capture metrics baseline before generating activity ---
            print_step(2, "Capturing /metrics baseline")
            status, metrics_before = self.get_metrics()
            if status != 200:
                raise RuntimeError(f"expected /metrics 200, got {status}")
            baseline = {
                "sessions_created_total": metric_value(metrics_before, "workspace_sessions_created_total"),
                "invocations_total": metric_value(
                    metrics_before,
                    "invocations_total",
                    '{tool="fs.list",status="succeeded"}',
                ),
                "duration_count": metric_value(
                    metrics_before,
                    "duration_ms_count",
                    '{tool="fs.list"}',
                ),
            }
            self.record_step("metrics_baseline", "passed", baseline)
            print_success("/metrics baseline captured")

            # --- Step 3: Create session + invoke tool to emit OTel metrics ---
            print_step(3, "Creating session and invoking fs.list to emit metrics")
            session_id = self.create_session(payload={"principal": PRINCIPAL})
            status, body, inv = self.invoke(session_id=session_id, tool_name="fs.list", args={"path": "."})
            if status != 200 or not inv or inv.get("status") != "succeeded":
                raise RuntimeError(f"fs.list failed: status={status} invocation={inv}")
            self.record_step("emit_metrics", "passed", {
                "session_id": session_id,
                "invocation_id": inv["id"],
            })
            print_success(f"fs.list succeeded, invocation={inv['id']}")

            # --- Step 4: Verify OTel-backed metrics increased on /metrics ---
            print_step(4, "Verifying /metrics exposes OTel-backed runtime metrics")
            expected = {
                "sessions_created_total": baseline["sessions_created_total"] + 1,
                "invocations_total": baseline["invocations_total"] + 1,
                "duration_count": baseline["duration_count"] + 1,
            }
            deadline = time.monotonic() + 10
            observed = {}
            while time.monotonic() < deadline:
                status, metrics_after = self.get_metrics()
                if status != 200:
                    time.sleep(0.5)
                    continue
                observed = {
                    "sessions_created_total": metric_value(metrics_after, "workspace_sessions_created_total"),
                    "invocations_total": metric_value(
                        metrics_after,
                        "invocations_total",
                        '{tool="fs.list",status="succeeded"}',
                    ),
                    "duration_count": metric_value(
                        metrics_after,
                        "duration_ms_count",
                        '{tool="fs.list"}',
                    ),
                }
                if (
                    observed["sessions_created_total"] >= expected["sessions_created_total"]
                    and observed["invocations_total"] >= expected["invocations_total"]
                    and observed["duration_count"] >= expected["duration_count"]
                ):
                    self.record_step("metrics_otlp_runtime", "passed", {
                        "baseline": baseline,
                        "observed": observed,
                    })
                    print_success("/metrics reflects the new session and invocation")
                    break
                time.sleep(0.5)
            else:
                raise RuntimeError(
                    f"/metrics did not advance as expected; baseline={baseline} observed={observed} expected>={expected}"
                )

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
