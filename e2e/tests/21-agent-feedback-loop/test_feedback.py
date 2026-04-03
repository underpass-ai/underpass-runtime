"""E2E test: Agent feedback loop — AcceptRecommendation / RejectRecommendation.

Validates:
1. Create session
2. Get recommendation (RecommendTools)
3. Accept recommendation (positive feedback → domain event)
4. Reject recommendation (negative feedback → domain event)
5. Verify decision still accessible
6. Close session

Fail-fast: every step must pass.
"""

from __future__ import annotations

import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

# Import generated proto stubs
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))
from gen.underpass.runtime.v1 import runtime_pb2 as pb

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"

PRINCIPAL = {
    "tenant_id": "e2e-feedback",
    "actor_id": "e2e-agent-feedback",
    "roles": ["developer"],
}


class FeedbackLoopE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="21-agent-feedback-loop",
            run_id_prefix="e2e-feedback",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-21-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            sid = self.create_session(payload={"principal": PRINCIPAL})

            # Step 1: Get recommendation
            print_step(1, "Get recommendation")
            req = pb.RecommendToolsRequest(session_id=sid, task_hint="read a config file", top_k=3)
            resp = self._catalog_stub.RecommendTools(req, metadata=self._metadata)
            rec_id = resp.recommendation_id
            recs = list(resp.recommendations)
            assert rec_id, "missing recommendation_id"
            assert len(recs) > 0, "no recommendations"
            top_tool = recs[0].name
            print_success(f"recommendation_id={rec_id}, top_tool={top_tool}")
            self.record_step("get_recommendation", "passed", data={"recommendation_id": rec_id, "top_tool": top_tool})

            # Step 2: Accept recommendation
            print_step(2, f"Accept recommendation {rec_id}")
            accept_req = pb.AcceptRecommendationRequest(
                session_id=sid, recommendation_id=rec_id, selected_tool_id=top_tool,
            )
            accept_resp = self._catalog_stub.AcceptRecommendation(accept_req, metadata=self._metadata)
            print_success(f"accepted, event_id={accept_resp.event_id}")
            self.record_step("accept_recommendation", "passed", data={"event_id": accept_resp.event_id})

            # Step 3: Reject recommendation
            print_step(3, f"Reject recommendation {rec_id}")
            reject_req = pb.RejectRecommendationRequest(
                session_id=sid, recommendation_id=rec_id, reason="tool output not useful for my task",
            )
            reject_resp = self._catalog_stub.RejectRecommendation(reject_req, metadata=self._metadata)
            print_success(f"rejected, event_id={reject_resp.event_id}")
            self.record_step("reject_recommendation", "passed", data={"event_id": reject_resp.event_id})

            # Step 4: Verify decision still accessible via learning evidence API
            print_step(4, "Verify decision persisted")
            from gen.underpass.runtime.learning.v1 import learning_pb2 as lpb
            dec_req = lpb.GetRecommendationDecisionRequest(recommendation_id=rec_id)
            dec_resp = self._learning_stub.GetRecommendationDecision(dec_req, metadata=self._metadata)
            assert dec_resp.decision.recommendation_id == rec_id, "decision ID mismatch"
            print_success(f"decision verified: {dec_resp.decision.recommendation_id}")
            self.record_step("verify_decision", "passed")

            final_status = "passed"
        except Exception as exc:
            error_message = str(exc)
            print_error(error_message)
            self.record_step("error", "failed", data={"error": error_message})
        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)
        return 0 if final_status == "passed" else 1


if __name__ == "__main__":
    sys.exit(FeedbackLoopE2E().run())
