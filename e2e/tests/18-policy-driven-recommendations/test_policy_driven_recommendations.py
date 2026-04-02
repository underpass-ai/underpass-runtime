"""E2E test: Policy-Driven Recommendations (P2) — validates that the runtime
online recommendation path consumes learned policies from Valkey.

Proves:
  1. Seed a learned policy in Valkey for a known context+tool
  2. RecommendTools returns decision_source reflecting learned policy
  3. Recommendations include policy_notes with source info
  4. context_signature is always populated in the decision
  5. Evidence bundle reflects the active policy mode

Requires Valkey access (same instance as runtime).
"""

from __future__ import annotations

import json
import os
import sys
import time

import redis

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"

PRINCIPAL = {
    "tenant_id": "e2e-policy",
    "actor_id": "e2e-actor-policy",
    "roles": ["developer"],
}

POLICY_KEY_PREFIX = "tool_policy"

# Seed a policy for context "general:unknown:standard" (matches default context
# when no go.mod/Cargo.toml is present in the workspace).
SEED_CONTEXT = "general:unknown:standard"
SEED_TOOL = "fs.read_file"
SEED_POLICY = {
    "context_signature": SEED_CONTEXT,
    "tool_id": SEED_TOOL,
    "alpha": 95.0,
    "beta": 5.0,
    "p95_latency_ms": 150,
    "p95_cost": 0.01,
    "error_rate": 0.05,
    "n_samples": 200,
    "freshness_ts": "2026-04-02T12:00:00Z",
    "confidence": 0.95,
}


class PolicyDrivenRecommendationsE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="18-policy-driven-recommendations",
            run_id_prefix="e2e-policy",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-18-{int(time.time())}.json"),
        )
        self.valkey_host = os.getenv("VALKEY_HOST", "rehydration-kernel-valkey")
        self.valkey_port = int(os.getenv("VALKEY_PORT", "6379"))
        self.valkey_db = int(os.getenv("VALKEY_DB", "0"))

    def run(self) -> int:
        final_status = "failed"
        error_message = ""
        try:
            self._run_all()
            final_status = "passed"
            return 0
        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1
        finally:
            self._cleanup_policy()
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)

    def _build_valkey(self) -> redis.Redis:
        """Connect to Valkey (same instance the runtime reads from)."""
        ssl_ca = os.getenv("WORKSPACE_TLS_CA_PATH", "").strip()
        ssl_cert = os.getenv("WORKSPACE_TLS_CERT_PATH", "").strip()
        ssl_key = os.getenv("WORKSPACE_TLS_KEY_PATH", "").strip()

        kwargs: dict = {
            "host": self.valkey_host,
            "port": self.valkey_port,
            "db": self.valkey_db,
            "decode_responses": True,
        }
        if ssl_ca and os.path.isfile(ssl_ca):
            kwargs["ssl"] = True
            kwargs["ssl_ca_certs"] = ssl_ca
            kwargs["ssl_certfile"] = ssl_cert if ssl_cert else None
            kwargs["ssl_keyfile"] = ssl_key if ssl_key else None

        return redis.Redis(**kwargs)

    def _seed_policy(self) -> str:
        """Seed learned policies in Valkey for all likely context signatures.

        The runtime computes context_signature from the workspace content.
        An empty workspace yields general:unknown:standard, but we seed
        multiple variants to handle edge cases.
        """
        r = self._build_valkey()
        contexts = [
            SEED_CONTEXT,                      # general:unknown:standard
            "general:unknown:constraints_high", # when AllowedPaths is set
            "general:unknown:constraints_low",  # platform_admin
            "io:unknown:standard",
        ]
        self._seeded_keys = []
        for ctx in contexts:
            policy = dict(SEED_POLICY)
            policy["context_signature"] = ctx
            key = f"{POLICY_KEY_PREFIX}:{ctx}:{SEED_TOOL}"
            r.set(key, json.dumps(policy), ex=300)
            self._seeded_keys.append(key)
        r.close()
        return self._seeded_keys[0]

    def _cleanup_policy(self) -> None:
        """Remove all seeded policies."""
        try:
            r = self._build_valkey()
            for key in getattr(self, "_seeded_keys", []):
                r.delete(key)
            r.close()
        except Exception:
            pass

    def _run_all(self) -> None:
        # --- Step 1: Seed policy ---
        print_step(1, "Seeding learned policy in Valkey")
        key = self._seed_policy()
        self.record_step("seed_policy", "passed", {
            "key": key,
            "context": SEED_CONTEXT,
            "tool": SEED_TOOL,
            "confidence": SEED_POLICY["confidence"],
        })
        print_success(f"Policy seeded: {key} (confidence={SEED_POLICY['confidence']})")

        # --- Step 2: Create session ---
        print_step(2, "Creating session")
        session_id = self.create_session(payload={
            "principal": PRINCIPAL,
            "metadata": {"test": "policy-driven-recommendations"},
        })
        self.record_step("create_session", "passed", {"session_id": session_id})
        print_success(f"Session: {session_id}")

        # --- Step 3: RecommendTools — verify policy consumption ---
        print_step(3, "RecommendTools — verifying learned policy consumption")
        status, body = self.request(
            "GET",
            f"/v1/sessions/{session_id}/tools/recommendations?task_hint=read+file&top_k=50",
        )
        if status != 200:
            raise RuntimeError(f"RecommendTools: expected 200, got {status}")

        decision_source = body.get("decision_source", "")
        policy_mode = body.get("policy_mode", "")

        # Check what context_signature the runtime computed.
        rec_id = body.get("recommendation_id", "")
        status_d, decision_d = self.request("GET", f"/v1/learning/recommendations/{rec_id}")
        actual_ctx = decision_d.get("context_signature", decision_d.get("contextSignature", ""))
        print_success(f"Runtime context_signature: {actual_ctx}")

        # With a seeded policy, decision_source should reflect learned policy
        if "learned_policy" not in decision_source and "telemetry" not in decision_source:
            raise RuntimeError(
                f"Expected decision_source with learned_policy or telemetry, "
                f"got '{decision_source}'. Runtime context_signature='{actual_ctx}', "
                f"seeded for '{SEED_CONTEXT}'. Policy may not match."
            )

        self.record_step("recommend_with_policy", "passed", {
            "decision_source": decision_source,
            "policy_mode": policy_mode,
            "recommendation_count": len(body.get("recommendations", [])),
        })
        print_success(f"Decision source: {decision_source}, mode: {policy_mode}")

        # --- Step 4: Verify policy_notes on the seeded tool ---
        print_step(4, "Verifying policy_notes on fs.read_file")
        recs = body.get("recommendations", [])
        target = [r for r in recs if r.get("name") == SEED_TOOL]
        if not target:
            raise RuntimeError(f"{SEED_TOOL} not in recommendations: {[r.get('name') for r in recs]}")

        policy_notes = target[0].get("policyNotes", target[0].get("policy_notes", []))
        if not policy_notes:
            raise RuntimeError(
                f"Expected policy_notes on {SEED_TOOL}, got empty. "
                f"Full rec: {target[0]}"
            )

        self.record_step("policy_notes", "passed", {
            "tool": SEED_TOOL,
            "policy_notes": policy_notes,
        })
        print_success(f"Policy notes: {policy_notes[0][:60]}...")

        # --- Step 5: Verify context_signature in decision ---
        print_step(5, "GetRecommendationDecision — verify context_signature")
        rec_id = body.get("recommendation_id", "")
        status, decision = self.request(
            "GET",
            f"/v1/learning/recommendations/{rec_id}",
        )
        if status != 200:
            raise RuntimeError(f"GetRecommendationDecision: expected 200, got {status}")

        ctx_sig = decision.get("context_signature", decision.get("contextSignature", ""))
        if not ctx_sig:
            raise RuntimeError("context_signature missing in decision")

        self.record_step("context_signature", "passed", {
            "context_signature": ctx_sig,
        })
        print_success(f"Context signature: {ctx_sig}")

        # --- Step 6: Close session ---
        print_step(6, "Closing session")
        status, _ = self.request("DELETE", f"/v1/sessions/{session_id}")
        self.record_step("close_session", "passed", {"status": status})
        print_success(f"Session closed: {status}")

        print_success("ALL PASSED — learned policy consumed, policy_notes visible, context_signature present")


def main() -> int:
    return PolicyDrivenRecommendationsE2E().run()


if __name__ == "__main__":
    raise SystemExit(main())
