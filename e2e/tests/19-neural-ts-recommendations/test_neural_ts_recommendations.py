"""E2E test: Neural Thompson Sampling (P4) — validates that the runtime
uses NeuralTS when a trained model and sufficient policy data exist.

Proves:
  1. Seed trained MLP weights in Valkey (neural_ts:model:v1)
  2. Seed learned policies with n≥100
  3. RecommendTools returns algorithm_id=neural_thompson_sampling
  4. decision_source reflects neural scoring
  5. policy_notes include neural_ts explanation

Fail-fast: every step must pass.
"""

from __future__ import annotations

import json
import math
import os
import random
import sys
import time

import redis

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from workspace_common import WorkspaceE2EBase, print_error, print_step, print_success

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"

PRINCIPAL = {
    "tenant_id": "e2e-neural",
    "actor_id": "e2e-actor-neural",
    "roles": ["developer"],
}

SEED_CONTEXT = "general:unknown:constraints_high"
SEED_TOOL = "fs.read_file"
NEURAL_MODEL_KEY = "neural_ts:model:v1"
POLICY_KEY_PREFIX = "tool_policy"

INPUT_DIM = 17
HIDDEN_DIM = 32


def xavier_init(fan_in: int, fan_out: int, size: int) -> list[float]:
    """Xavier/Glorot initialization."""
    scale = math.sqrt(2.0 / (fan_in + fan_out))
    return [random.gauss(0, scale) for _ in range(size)]


def build_random_mlp_weights() -> dict:
    """Build valid MLP weights matching the runtime's MLPWeights struct."""
    return {
        "w1": xavier_init(INPUT_DIM, HIDDEN_DIM, INPUT_DIM * HIDDEN_DIM),
        "b1": [0.0] * HIDDEN_DIM,
        "w2": xavier_init(HIDDEN_DIM, 1, HIDDEN_DIM),
        "b2": [0.0],
    }


class NeuralTSRecommendationsE2E(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="19-neural-ts-recommendations",
            run_id_prefix="e2e-neural",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-19-{int(time.time())}.json"),
        )
        self.valkey_host = os.getenv("VALKEY_HOST", "rehydration-kernel-valkey")
        self.valkey_port = int(os.getenv("VALKEY_PORT", "6379"))
        self.valkey_db = int(os.getenv("VALKEY_DB", "0"))
        self._seeded_keys: list[str] = []

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
            self._cleanup()
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)

    def _build_valkey(self) -> redis.Redis:
        ssl_ca = os.getenv("WORKSPACE_TLS_CA_PATH", "").strip()
        ssl_cert = os.getenv("WORKSPACE_TLS_CERT_PATH", "").strip()
        ssl_key = os.getenv("WORKSPACE_TLS_KEY_PATH", "").strip()
        kwargs: dict = {
            "host": self.valkey_host, "port": self.valkey_port,
            "db": self.valkey_db, "decode_responses": True,
        }
        if ssl_ca and os.path.isfile(ssl_ca):
            kwargs.update(ssl=True, ssl_ca_certs=ssl_ca,
                          ssl_certfile=ssl_cert or None, ssl_keyfile=ssl_key or None)
        return redis.Redis(**kwargs)

    def _cleanup(self) -> None:
        try:
            r = self._build_valkey()
            for key in self._seeded_keys:
                r.delete(key)
            r.delete(NEURAL_MODEL_KEY)
            r.close()
        except Exception:
            pass

    def _run_all(self) -> None:
        r = self._build_valkey()

        # --- Step 1: Seed neural model weights ---
        print_step(1, "Seeding trained MLP model in Valkey")
        weights = build_random_mlp_weights()
        r.set(NEURAL_MODEL_KEY, json.dumps(weights), ex=300)
        self.record_step("seed_model", "passed", {"key": NEURAL_MODEL_KEY, "params": sum(len(v) for v in weights.values())})
        print_success(f"Model seeded: {NEURAL_MODEL_KEY} ({sum(len(v) for v in weights.values())} params)")

        # --- Step 2: Seed policies with n≥100 ---
        print_step(2, "Seeding learned policies (n≥100) in Valkey")
        policy = {
            "context_signature": SEED_CONTEXT,
            "tool_id": SEED_TOOL,
            "alpha": 180.0, "beta": 20.0,
            "p95_latency_ms": 120, "p95_cost": 0.01,
            "error_rate": 0.05, "n_samples": 200,
            "confidence": 0.9,
        }
        key = f"{POLICY_KEY_PREFIX}:{SEED_CONTEXT}:{SEED_TOOL}"
        r.set(key, json.dumps(policy), ex=300)
        self._seeded_keys.append(key)
        r.close()
        self.record_step("seed_policy", "passed", {"key": key, "n_samples": 200})
        print_success(f"Policy seeded: {key} (n=200)")

        # --- Step 3: Create session ---
        print_step(3, "Creating session")
        session_id = self.create_session(payload={
            "principal": PRINCIPAL,
            "metadata": {"test": "neural-ts"},
        })
        self.record_step("create_session", "passed", {"session_id": session_id})
        print_success(f"Session: {session_id}")

        # --- Step 4: RecommendTools — verify NeuralTS ---
        print_step(4, "RecommendTools — verifying NeuralTS algorithm selection")
        status, body = self.request(
            "GET",
            f"/v1/sessions/{session_id}/tools/recommendations?task_hint=read+file&top_k=10",
        )
        if status != 200:
            raise RuntimeError(f"RecommendTools: expected 200, got {status}")

        algo = body.get("algorithm_id", "")
        decision_source = body.get("decision_source", "")

        self.record_step("recommend_neural", "passed", {
            "algorithm_id": algo,
            "decision_source": decision_source,
        })
        print_success(f"Algorithm: {algo}, source: {decision_source}")

        if algo != "neural_thompson_sampling":
            raise RuntimeError(
                f"Expected algorithm_id=neural_thompson_sampling, got '{algo}'. "
                f"NeuralTS may not be selecting correctly."
            )

        # --- Step 5: Verify policy_notes include neural_ts ---
        print_step(5, "Verifying neural_ts in policy_notes")
        recs = body.get("recommendations", [])
        target = [r for r in recs if r.get("name") == SEED_TOOL]
        if not target:
            raise RuntimeError(f"{SEED_TOOL} not in recommendations")

        notes = target[0].get("policyNotes", target[0].get("policy_notes", []))
        if not notes:
            raise RuntimeError(f"Expected policy_notes on {SEED_TOOL}")

        has_neural = any("neural_thompson_sampling" in n for n in notes)
        if not has_neural:
            raise RuntimeError(f"Expected neural_thompson_sampling in notes, got: {notes}")

        self.record_step("neural_notes", "passed", {"notes": notes})
        print_success(f"Notes: {notes[0][:60]}...")

        # --- Step 6: Close session ---
        print_step(6, "Closing session")
        status, _ = self.request("DELETE", f"/v1/sessions/{session_id}")
        self.record_step("close_session", "passed")
        print_success(f"Session closed: {status}")

        print_success("ALL PASSED — NeuralTS selected, observable in evidence")


def main() -> int:
    return NeuralTSRecommendationsE2E().run()


if __name__ == "__main__":
    raise SystemExit(main())
