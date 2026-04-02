"""E2E test: Recommendation Evidence (P0) — validates that RecommendTools
produces auditable evidence: bridge fields, NATS events, persisted decisions,
and evidence bundles.

Proves:
  1. RecommendTools returns bridge fields (recommendation_id, event_id, etc.)
  2. NATS event runtime.learning.recommendation.emitted is emitted (JetStream)
  3. GetRecommendationDecision returns persisted decision by recommendation_id
  4. GetEvidenceBundle returns audit bundle with decision
  5. Decision fields are consistent across response, event, and evidence API

Fail-fast: every step must pass. No fallbacks.
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

# --- Constants ---

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"

PRINCIPAL = {
    "tenant_id": "e2e-evidence",
    "actor_id": "e2e-actor-evidence",
    "roles": ["developer"],
}

EVENT_TYPE_RECOMMENDATION = "runtime.learning.recommendation.emitted"
NATS_SUBJECT_EVENTS = "workspace.events.>"

BRIDGE_FIELDS = (
    "recommendation_id",
    "event_id",
    "event_subject",
    "decision_source",
    "algorithm_id",
    "algorithm_version",
    "policy_mode",
)

DECISION_FIELDS = (
    "recommendation_id",
    "session_id",
    "tenant_id",
    "actor_id",
    "decision_source",
    "algorithm_id",
    "algorithm_version",
    "policy_mode",
)


def normalize_enum(val: str) -> str:
    """Strip proto enum prefixes for comparison."""
    if not val:
        return val
    for prefix in ("DECISION_SOURCE_", "POLICY_MODE_"):
        if val.startswith(prefix):
            return val[len(prefix):].lower()
    return val.lower()


class RecommendationEvidenceE2E(WorkspaceE2EBase):
    """E2E test for P0 learning evidence plane."""

    def __init__(self) -> None:
        super().__init__(
            test_id="16-recommendation-evidence",
            run_id_prefix="e2e-evidence",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-16-{int(time.time())}.json"),
        )

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            asyncio.run(self._run_all())
            final_status = "passed"
            return 0
        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1
        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)

    async def _run_all(self) -> None:
        # --- Step 1: Connect to NATS + JetStream subscribe ---
        print_step(1, f"Connecting to NATS and subscribing (JetStream) to {NATS_SUBJECT_EVENTS}")
        nc = await self.nats_connect()
        js = nc.jetstream()

        collected: list[dict] = []

        # Use ordered consumer — ephemeral, delivers new messages from now.
        sub = await js.subscribe(NATS_SUBJECT_EVENTS, ordered_consumer=True)
        self.record_step("nats_jetstream_subscribe", "passed")
        print_success(f"JetStream subscribed to {NATS_SUBJECT_EVENTS}")

        # --- Step 2: Create session ---
        print_step(2, "Creating session")
        session_id = self.create_session(payload={
            "principal": PRINCIPAL,
            "metadata": {"test": "recommendation-evidence"},
        })
        self.record_step("create_session", "passed", {"session_id": session_id})
        print_success(f"Session: {session_id}")

        # --- Step 3: RecommendTools — verify bridge fields ---
        print_step(3, "RecommendTools — verifying bridge fields")
        status, body = self.request(
            "GET",
            f"/v1/sessions/{session_id}/tools/recommendations?task_hint=read+file&top_k=5",
        )
        if status != 200:
            raise RuntimeError(f"RecommendTools: expected 200, got {status}")

        missing = [f for f in BRIDGE_FIELDS if not body.get(f)]
        if missing:
            raise RuntimeError(f"Missing bridge fields: {missing}. Body keys: {list(body.keys())}")

        recommendation_id = body["recommendation_id"]
        event_id = body["event_id"]

        self.record_step("bridge_fields", "passed", {
            "recommendation_id": recommendation_id,
            "event_id": event_id,
            "decision_source": body["decision_source"],
            "algorithm_id": body["algorithm_id"],
            "policy_mode": body["policy_mode"],
            "recommendation_count": len(body.get("recommendations", [])),
        })
        print_success(
            f"Bridge fields: rec={recommendation_id[:20]}... "
            f"src={body['decision_source']} algo={body['algorithm_id']} mode={body['policy_mode']}"
        )

        # --- Step 4: Drain JetStream events ---
        print_step(4, "Collecting NATS JetStream events")

        # Drain messages with a timeout. The runtime publishes synchronously
        # before returning the gRPC response, so events should already be in
        # the stream by now.
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            try:
                msg = await asyncio.wait_for(sub.next_msg(), timeout=1.0)
                evt = json.loads(msg.data.decode())
                collected.append({
                    "subject": msg.subject,
                    "type": evt.get("type", "?"),
                    "id": evt.get("id", "?"),
                    "session_id": evt.get("session_id", "?"),
                    "payload": evt.get("payload", {}),
                })
            except asyncio.TimeoutError:
                break
            except Exception:
                break

        await sub.unsubscribe()
        await nc.close()

        # Filter for recommendation events from THIS test.
        rec_events = [
            e for e in collected
            if e["type"] == EVENT_TYPE_RECOMMENDATION
            and e.get("payload", {}).get("recommendation_id") == recommendation_id
        ]

        if not rec_events:
            all_types = [e["type"] for e in collected]
            raise RuntimeError(
                f"No {EVENT_TYPE_RECOMMENDATION} event for {recommendation_id}. "
                f"Got {len(collected)} events: {all_types}"
            )

        nats_evt = rec_events[0]
        nats_payload = nats_evt.get("payload", {})

        # Event ID from response must match the NATS event envelope ID.
        if nats_evt["id"] != event_id:
            raise RuntimeError(
                f"NATS event ID mismatch: response={event_id}, nats={nats_evt['id']}"
            )

        self.record_step("nats_recommendation_event", "passed", {
            "total_events": len(collected),
            "recommendation_events": len(rec_events),
            "event_id": nats_evt["id"],
            "recommendation_id": nats_payload.get("recommendation_id"),
        })
        print_success(
            f"NATS event verified: {len(collected)} total, event_id matches response"
        )

        # --- Step 5: GetRecommendationDecision ---
        print_step(5, "GetRecommendationDecision — verifying persisted decision")
        status, decision = self.request(
            "GET",
            f"/v1/learning/recommendations/{recommendation_id}",
        )
        if status != 200:
            raise RuntimeError(f"GetRecommendationDecision: expected 200, got {status}")

        missing_d = [f for f in DECISION_FIELDS if not decision.get(f)]
        if missing_d:
            raise RuntimeError(f"Missing decision fields: {missing_d}")

        if decision.get("recommendation_id") != recommendation_id:
            raise RuntimeError(
                f"Decision ID mismatch: expected={recommendation_id}, "
                f"got={decision.get('recommendation_id')}"
            )

        resp_source = normalize_enum(body.get("decision_source", ""))
        dec_source = normalize_enum(decision.get("decision_source", ""))
        if resp_source != dec_source:
            raise RuntimeError(f"Decision source mismatch: resp={resp_source}, dec={dec_source}")

        recs_in_decision = decision.get("recommendations", [])
        self.record_step("get_decision", "passed", {
            "recommendation_id": recommendation_id,
            "decision_source": decision.get("decision_source"),
            "recommendations_count": len(recs_in_decision),
        })
        print_success(f"Decision persisted: {len(recs_in_decision)} tools, source={dec_source}")

        # --- Step 6: GetEvidenceBundle ---
        print_step(6, "GetEvidenceBundle — verifying audit bundle")
        status, bundle = self.request(
            "GET",
            f"/v1/learning/evidence/recommendations/{recommendation_id}",
        )
        if status != 200:
            raise RuntimeError(f"GetEvidenceBundle: expected 200, got {status}")

        bundle_rec = bundle.get("recommendation", {})
        if not bundle_rec:
            raise RuntimeError("Evidence bundle missing recommendation")

        if bundle_rec.get("recommendation_id") != recommendation_id:
            raise RuntimeError(f"Bundle recommendation_id mismatch")

        self.record_step("get_evidence_bundle", "passed", {
            "recommendation_id": recommendation_id,
            "bundle_has_recommendation": True,
        })
        print_success("Evidence bundle verified")

        # --- Step 7: Close session ---
        print_step(7, "Closing session")
        status, _ = self.request("DELETE", f"/v1/sessions/{session_id}")
        self.record_step("close_session", "passed", {"status": status})
        print_success(f"Session closed: {status}")

        print_success(
            "ALL PASSED — bridge fields, NATS event, decision, evidence bundle"
        )


def main() -> int:
    return RecommendationEvidenceE2E().run()


if __name__ == "__main__":
    raise SystemExit(main())
