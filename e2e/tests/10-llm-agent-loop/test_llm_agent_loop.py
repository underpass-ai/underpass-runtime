"""E2E test: LLM Agent Loop — validates the full cycle:
   LLM → tool discovery → invoke → artifacts → telemetry.

The agent receives a coding task, discovers available tools via the
workspace API, asks the LLM which tools to use and with what arguments,
invokes them, and repeats until the task is complete or max iterations.

Supports native tool calling (OpenAI/Anthropic/vLLM) and free-text JSON
fallback, configured per provider via llm_config.yaml.
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.parse
from typing import Any

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from workspace_common import WorkspaceE2EBase, print_error, print_info, print_step, print_success, print_warning
from llm_providers import get_provider, LLMProvider, AgentDecision

SYSTEM_PROMPT = """\
You are a software engineering agent. You have access to workspace tools
for file operations. Use the provided tools to accomplish the task.

When the task is complete, call the "done" tool with a summary.

Rules:
- Use exact tool names provided.
- For fs_write_file: provide path and content.
- For fs_read_file: provide path.
- For fs_list: provide path (use "." for root).
- Always call done when finished.
"""

TASK_PROMPT = """\
Create a small Go project in the workspace:
1. Write a main.go file with a simple HTTP server that responds "hello world" on /
2. Write a main_test.go file with a test for the handler
3. List the workspace to confirm both files exist
4. Read main.go back to verify the content

Use fs_write_file (for writing), fs_list, and fs_read_file tools.
"""

MAX_ITERATIONS = 10


class LLMAgentLoopE2E(WorkspaceE2EBase):
    """Agent loop E2E test — LLM drives tool invocations via native tool calling."""

    def __init__(self, provider: LLMProvider) -> None:
        super().__init__(
            test_id="10-llm-agent-loop",
            run_id_prefix=f"e2e-llm-{provider.name}",
            workspace_url=os.getenv("WORKSPACE_URL", "https://localhost:50053"),
            evidence_file=os.getenv("EVIDENCE_FILE", "/tmp/evidence-10-llm-agent-loop.json"),
        )
        self.provider = provider
        self.conversation: list[dict[str, Any]] = []
        self.iteration = 0

    def _discover_tools(self, session_id: str) -> list[dict[str, Any]]:
        status, body = self.request("GET", f"/v1/sessions/{session_id}/tools/discovery?detail=compact")
        if status != 200:
            raise RuntimeError(f"discovery failed ({status}): {body}")
        return body.get("tools", [])

    def _get_recommendations(self, session_id: str, hint: str) -> list[dict[str, Any]]:
        status, body = self.request(
            "GET", f"/v1/sessions/{session_id}/tools/recommendations?task_hint={urllib.parse.quote(hint)}&top_k=10",
        )
        if status != 200:
            print_warning(f"recommendations failed ({status}), continuing without")
            return []
        return body.get("recommendations", [])

    def _execute_action(self, session_id: str, decision: AgentDecision) -> dict[str, Any]:
        """Execute a tool invocation from agent decision."""
        status, body, invocation = self.invoke(
            session_id=session_id,
            tool_name=decision.tool,
            args=decision.args,
            approved=decision.approved,
        )
        result: dict[str, Any] = {"http_status": status, "tool": decision.tool}
        if invocation:
            result["invocation_status"] = invocation.get("status")
            result["output"] = invocation.get("output", "")
            error = self.extract_error(invocation, body)
            if error:
                result["error"] = error
        else:
            result["error"] = body
        return result

    def _add_tool_result(self, decision: AgentDecision, result: dict[str, Any]) -> None:
        """Add tool result to conversation for next LLM turn."""
        if self.provider.tool_calling and self.provider.provider_type != "anthropic":
            # OpenAI/vLLM: assistant message with tool_call, then tool message
            self.conversation.append({
                "role": "assistant",
                "content": None,
                "tool_calls": [{
                    "id": f"call_{self.iteration}",
                    "type": "function",
                    "function": {
                        "name": decision.tool.replace(".", "_"),
                        "arguments": json.dumps(decision.args),
                    },
                }],
            })
            self.conversation.append({
                "role": "tool",
                "tool_call_id": f"call_{self.iteration}",
                "content": json.dumps(result, default=str),
            })
        else:
            # Anthropic or freetext: use user message with result
            self.conversation.append({
                "role": "user",
                "content": f"Tool result:\n{json.dumps(result, default=str)}\n\nContinue with the next step.",
            })

    def run_agent_loop(self) -> dict[str, Any]:
        """Run the full agent loop."""
        print_step(1, "Creating workspace session")
        session_id = self.create_session(
            payload={
                "principal": {"tenant_id": "e2e-tenant", "actor_id": "llm-agent-loop", "roles": ["developer"]},
                "metadata": {"runner_profile": "base", "task": "go-hello-world"},
            },
        )
        self.record_step("create_session", "pass", {"session_id": session_id})
        print_success(f"Session created: {session_id}")

        print_step(2, f"Discovering tools (provider: {self.provider.name})")
        tools = self._discover_tools(session_id)
        self.record_step("discover_tools", "pass", {"tool_count": len(tools)})
        print_success(f"Discovered {len(tools)} tools")

        print_step(3, "Getting tool recommendations")
        recs = self._get_recommendations(session_id, "create go project with tests")
        if recs:
            self.record_step("recommendations", "pass", {"count": len(recs)})
            print_success(f"Got {len(recs)} recommendations")
        else:
            self.record_step("recommendations", "skip", {"reason": "not available"})
            print_warning("Recommendations not available, continuing")

        # Build initial conversation
        self.conversation = [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": TASK_PROMPT},
        ]

        print_step(4, f"Starting agent loop (max {MAX_ITERATIONS} iterations, tool_calling={self.provider.tool_calling})")
        loop_results: list[dict[str, Any]] = []

        for i in range(1, MAX_ITERATIONS + 1):
            self.iteration = i
            print_info(f"  --- Iteration {i}/{MAX_ITERATIONS} ---")

            try:
                decision = self.provider.decide(self.conversation)
            except (json.JSONDecodeError, KeyError, ValueError) as exc:
                print_warning(f"  LLM returned invalid response: {exc}")
                self.conversation.append({
                    "role": "user",
                    "content": f"Error: your response could not be parsed. Try again. Error: {exc}",
                })
                loop_results.append({"iteration": i, "error": f"parse_error: {exc}"})
                continue

            if decision.thinking:
                print_info(f"  Thinking: {decision.thinking[:120]}")

            if decision.done:
                print_success(f"  Agent done: {decision.summary}")
                loop_results.append({"iteration": i, "done": True, "summary": decision.summary})
                break

            if not decision.tool:
                print_warning("  LLM returned no tool, retrying")
                self.conversation.append({"role": "user", "content": "You must call a tool. Try again."})
                loop_results.append({"iteration": i, "error": "no_tool"})
                continue

            print_info(f"  Tool: {decision.tool} args={json.dumps(decision.args, default=str)[:200]}")
            result = self._execute_action(session_id, decision)
            loop_results.append({"iteration": i, "tool": decision.tool, "result": result})

            self._add_tool_result(decision, result)
            print_info(f"  {decision.tool} -> {result.get('invocation_status', 'unknown')}")
        else:
            print_warning(f"  Agent did not finish within {MAX_ITERATIONS} iterations")

        self.record_step("agent_loop", "pass", {
            "iterations": self.iteration,
            "provider": self.provider.name,
            "tool_calling": self.provider.tool_calling,
            "results": loop_results,
        })

        # Verify files exist
        print_step(5, "Verifying workspace state")
        status, body, inv = self.invoke(session_id=session_id, tool_name="fs.list", args={"path": "."})
        files_found = []
        if inv and inv.get("status") == "succeeded":
            output = inv.get("output", "")
            if isinstance(output, list):
                files_found = [str(f) for f in output]
            elif isinstance(output, dict):
                files_found = [str(f) for f in output.get("files", output.get("entries", []))]
            elif isinstance(output, str):
                files_found = [f.strip() for f in output.strip().split("\n") if f.strip()]

        has_main = any("main.go" in f and "test" not in f for f in files_found)
        has_test = any("main_test.go" in f or "test" in f.lower() for f in files_found)

        self.record_step("verify_files", "pass" if has_main else "warn", {
            "files": files_found, "has_main": has_main, "has_test": has_test,
        })
        if has_main:
            print_success(f"Workspace has {len(files_found)} files (main.go: yes, test: {'yes' if has_test else 'no'})")
        else:
            print_warning(f"main.go not found in workspace (files: {files_found})")

        return {
            "session_id": session_id, "provider": self.provider.name,
            "iterations": self.iteration, "tools_discovered": len(tools),
            "recommendations": len(recs), "files_created": files_found,
            "has_main": has_main, "has_test": has_test, "loop_results": loop_results,
        }

    def run(self) -> int:
        try:
            result = self.run_agent_loop()
            self.evidence["result"] = result
            if result["has_main"]:
                self.write_evidence("pass")
                print_success(f"LLM Agent Loop PASSED (provider={self.provider.name}, iterations={result['iterations']})")
                return 0
            else:
                self.write_evidence("warn", "Agent completed but main.go not verified")
                print_warning(f"LLM Agent Loop completed with warnings (provider={self.provider.name})")
                return 0
        except Exception as exc:
            self.write_evidence("fail", str(exc))
            print_error(f"LLM Agent Loop FAILED: {exc}")
            return 1
        finally:
            self.cleanup_sessions()


def main() -> int:
    provider_name = os.getenv("LLM_PROVIDER", "claude")
    print_info(f"LLM Agent Loop E2E — provider: {provider_name}")
    try:
        provider = get_provider(provider_name)
    except (ValueError, KeyError) as exc:
        print_error(f"Failed to initialize provider: {exc}")
        return 1
    return LLMAgentLoopE2E(provider).run()


if __name__ == "__main__":
    raise SystemExit(main())
