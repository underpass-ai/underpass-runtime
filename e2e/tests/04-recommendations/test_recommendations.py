"""E2E test: Tool recommendations — task hints, top_k, scoring."""

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


class RecommendationsE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="04-recommendations",
            run_id_prefix="e2e-recommend",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-04-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            sid = self.create_session(payload={"principal": PRINCIPAL})

            # --- Step 1: Recommendations with task hint ---
            print_step(1, "Recommendations with task_hint=run+tests, top_k=5")
            status, body = self.request(
                "GET",
                f"/v1/sessions/{sid}/tools/recommendations?task_hint=run+tests&top_k=5",
            )
            if status != 200:
                raise RuntimeError(f"recommendations: expected 200, got {status}")

            recs = body.get("recommendations", [])
            if len(recs) > 5:
                raise RuntimeError(f"expected at most 5 recommendations, got {len(recs)}")

            # Verify sorted descending by score
            scores = [r.get("score", 0) for r in recs]
            for i in range(len(scores) - 1):
                if scores[i] < scores[i + 1]:
                    raise RuntimeError(f"recommendations not sorted: {scores}")

            # Verify task_hint echoed back
            if body.get("task_hint") != "run tests":
                raise RuntimeError(f"task_hint not echoed: {body.get('task_hint')}")

            self.record_step("recommendations_task_hint", "passed", {
                "count": len(recs),
                "top_scores": scores[:3],
            })
            print_success(f"Recommendations: {len(recs)} results, sorted by score")

            # --- Step 2: Default top_k ---
            print_step(2, "Recommendations with default top_k")
            status, body = self.request(
                "GET",
                f"/v1/sessions/{sid}/tools/recommendations?task_hint=build",
            )
            if status != 200:
                raise RuntimeError(f"default top_k: expected 200, got {status}")

            recs = body.get("recommendations", [])
            if len(recs) < 1:
                raise RuntimeError("expected at least 1 recommendation")
            self.record_step("recommendations_default_topk", "passed", {"count": len(recs)})
            print_success(f"Default top_k: {len(recs)} recommendations")

            # --- Step 3: Invalid session returns 404 ---
            print_step(3, "Recommendations with invalid session returns 404")
            status, _ = self.request(
                "GET",
                "/v1/sessions/does-not-exist/tools/recommendations?task_hint=test",
            )
            if status != 404:
                raise RuntimeError(f"expected 404, got {status}")
            self.record_step("recommendations_invalid_session", "passed")
            print_success("Invalid session -> 404")

            final_status = "passed"
            print_success("All recommendation tests passed")
            return 0

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)


def main() -> int:
    return RecommendationsE2E().run()


if __name__ == "__main__":
    sys.exit(main())
