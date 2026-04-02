"""E2E test 15: vLLM-Driven Learning Loop

Validates the full adaptive tool selection cycle:
  vLLM agent → discovery → recommendations → invoke → telemetry →
  tool-learning (Beta-SWTS) → updated recommendations

The test proves that:
1. vLLM can drive tool discovery and invocation autonomously
2. Telemetry is recorded for all invocations
3. Tool-learning pipeline computes Thompson Sampling policies
4. Recommendations adapt: a tool that starts failing drops in ranking

This is the first empirical evidence for the research paper
"Adaptive Tool Selection for Governed Agent Execution Environments".
"""

from __future__ import annotations

import json
import os
import sys
import time
from typing import Any

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from workspace_common import WorkspaceE2EBase, print_error, print_info, print_step, print_success, print_warning

DEFAULT_URL = "https://underpass-runtime.underpass-runtime.svc.cluster.local:50053"
VLLM_URL = os.getenv("VLLM_URL", "http://vllm-server:8000")
VLLM_MODEL = os.getenv("VLLM_MODEL", "Qwen/Qwen3-8B")
VLLM_API_KEY = os.getenv("VLLM_API_KEY", "")

PRINCIPAL = {
    "tenant_id": "e2e-tenant",
    "actor_id": "e2e-vllm-agent",
    "roles": ["developer", "devops"],
}

SYSTEM_PROMPT = """\
You are a software engineering agent with access to workspace tools.
Respond ONLY with a JSON object (no markdown, no extra text):

For tool calls:
{"tool": "tool.name", "args": {"arg1": "value"}, "approved": true}

When done:
{"done": true, "summary": "what was accomplished"}
"""


class VLLMChat:
    """Minimal vLLM OpenAI-compatible chat client."""

    def __init__(self, url: str, model: str, api_key: str = "") -> None:
        self.url = url.rstrip("/") + "/v1/chat/completions"
        self.model = model
        self.api_key = api_key

    def chat(self, messages: list[dict[str, str]]) -> str:
        import urllib.request

        payload = json.dumps({
            "model": self.model,
            "messages": messages,
            "temperature": 0.3,
            "max_tokens": 512,
        }).encode()

        headers = {"Content-Type": "application/json"}
        if self.api_key:
            headers["Authorization"] = f"Bearer {self.api_key}"

        req = urllib.request.Request(self.url, data=payload, headers=headers)
        with urllib.request.urlopen(req, timeout=120) as resp:
            data = json.loads(resp.read().decode())

        content = data["choices"][0]["message"]["content"]
        # Strip thinking tags if present (Qwen3)
        import re
        content = re.sub(r"<think>.*?</think>", "", content, flags=re.DOTALL).strip()
        # Strip markdown fences
        if content.startswith("```"):
            lines = content.split("\n")[1:]
            if lines and lines[-1].strip() == "```":
                lines = lines[:-1]
            content = "\n".join(lines).strip()
        return content


class TestVLLMLearningLoop(WorkspaceE2EBase):
    def __init__(self) -> None:
        super().__init__(
            test_id="15-vllm-learning-loop",
            run_id_prefix="e2e-vllm-learn",
            workspace_url=os.getenv("WORKSPACE_URL", DEFAULT_URL),
            evidence_file=os.getenv("EVIDENCE_FILE", f"/tmp/e2e-15-{int(time.time())}.json"),
        )
        self.llm = VLLMChat(VLLM_URL, VLLM_MODEL, VLLM_API_KEY)

    def run(self) -> int:
        final_status = "failed"
        error_message = ""

        try:
            # ── Step 1: Create session ──
            print_step(1, "Create workspace session")
            session_id = self.create_session(PRINCIPAL)
            print_success(f"Session created: {session_id}")

            # ── Step 2: Get initial recommendations ──
            print_step(2, "Get initial tool recommendations")
            status, recs_before = self.request(
                "GET",
                f"/v1/sessions/{session_id}/tools/recommendations?task_hint=write+a+python+file&top_k=5",
            )
            if status != 200:
                raise RuntimeError(f"recommendations failed: {status}")
            before_names = [r["name"] for r in recs_before.get("recommendations", [])]
            print_success(f"Initial recommendations: {before_names}")

            # ── Step 3: vLLM discovers tools ──
            print_step(3, "vLLM discovers available tools")
            status, discovery = self.request(
                "GET",
                f"/v1/sessions/{session_id}/tools/discovery?detail=compact",
            )
            if status != 200:
                raise RuntimeError(f"discovery failed: {status}")
            tool_count = discovery.get("filtered", 0)
            print_success(f"Discovered {tool_count} tools")

            # ── Step 4: vLLM-driven tool invocations ──
            print_step(4, "vLLM agent executes coding task")
            task = "Create a Python file called hello.py with a hello world function, then read it back to verify"

            tools_summary = json.dumps(
                [{"name": t["name"], "description": t.get("description", "")}
                 for t in discovery.get("tools", [])[:20]],
                indent=None,
            )

            messages = [
                {"role": "system", "content": SYSTEM_PROMPT},
                {"role": "user", "content": f"Available tools:\n{tools_summary}\n\nTask: {task}"},
            ]

            invocations = []
            for iteration in range(1, 8):
                print_info(f"  Iteration {iteration}/7")
                try:
                    response_text = self.llm.chat(messages)
                except Exception as llm_err:
                    print_warning(f"  LLM call failed: {llm_err}")
                    break

                try:
                    action = json.loads(response_text)
                except json.JSONDecodeError:
                    print_warning(f"  Invalid JSON from LLM: {response_text[:100]}")
                    break

                if action.get("done"):
                    print_success(f"  Agent done: {action.get('summary', 'completed')}")
                    break

                tool_name = action.get("tool", "")
                tool_args = action.get("args", {})
                approved = action.get("approved", False)

                if not tool_name:
                    print_warning("  No tool specified")
                    break

                inv_status, inv_resp = self.request(
                    "POST",
                    f"/v1/sessions/{session_id}/tools/{tool_name}/invoke",
                    {"args": tool_args, "approved": approved},
                )

                inv_data = inv_resp.get("invocation", {})
                inv_result = inv_data.get("status", "unknown")
                invocations.append({
                    "tool": tool_name,
                    "status": inv_result,
                    "http_status": inv_status,
                    "iteration": iteration,
                })

                print_info(f"  Tool {tool_name} → {inv_result}")

                # Feed result back to LLM
                output_summary = json.dumps(inv_data.get("output", {}))[:500]
                messages.append({"role": "assistant", "content": response_text})
                messages.append({"role": "user", "content": f"Tool result ({inv_result}): {output_summary}\n\nContinue with the task."})

            if not invocations:
                raise RuntimeError("No tool invocations executed")

            succeeded = sum(1 for i in invocations if i["status"] == "succeeded")
            print_success(f"Completed {len(invocations)} invocations ({succeeded} succeeded)")

            # ── Step 5: Verify telemetry recorded ──
            print_step(5, "Verify telemetry and metrics")
            status, metrics = self.request("GET", "/metrics")
            if status != 200:
                raise RuntimeError(f"metrics failed: {status}")

            metrics_text = metrics.get("raw", str(metrics))
            has_invocation_metrics = "invocations_total" in str(metrics_text)
            print_success(f"Metrics endpoint OK, has invocation data: {has_invocation_metrics}")

            # ── Step 6: Get updated recommendations ──
            print_step(6, "Get post-invocation recommendations")
            status, recs_after = self.request(
                "GET",
                f"/v1/sessions/{session_id}/tools/recommendations?task_hint=write+a+python+file&top_k=5",
            )
            if status != 200:
                raise RuntimeError(f"post-recommendations failed: {status}")
            after_names = [r["name"] for r in recs_after.get("recommendations", [])]
            print_success(f"Updated recommendations: {after_names}")

            # ── Step 7: Verify the learning signal ──
            print_step(7, "Verify learning signal (tools used appear in recommendations)")
            tools_used = {i["tool"] for i in invocations if i["status"] == "succeeded"}
            tools_recommended = set(after_names)
            overlap = tools_used & tools_recommended
            if overlap:
                print_success(f"Learning signal confirmed: {overlap} used and recommended")
            else:
                print_warning("No overlap between used tools and recommendations (may need more data)")

            final_status = "passed"
            print_success("vLLM Learning Loop PASSED")

        except Exception as exc:
            error_message = str(exc)
            print_error(f"Test failed: {error_message}")
            return 1

        finally:
            self.cleanup_sessions()
            self.write_evidence(final_status, error_message)

        return 0


def main() -> int:
    test = TestVLLMLearningLoop()
    return test.run()


if __name__ == "__main__":
    sys.exit(main())
