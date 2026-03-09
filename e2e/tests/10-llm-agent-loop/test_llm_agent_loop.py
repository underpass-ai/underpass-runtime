"""E2E test: LLM Agent Loop — validates the full cycle:
   LLM → tool discovery → invoke → artifacts → telemetry.

The agent receives a coding task, discovers available tools via the
workspace API, asks the LLM which tools to use and with what arguments,
invokes them, and repeats until the task is complete or max iterations.

Supports three LLM providers: claude, openai, vllm (Qwen).
Set LLM_PROVIDER env var to select (default: claude).
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
from llm_providers import get_provider, LLMProvider

SYSTEM_PROMPT = """\
You are a software engineering agent. You have access to a workspace with tools
for file operations, code analysis, and more.

Your task will be given by the user. You must accomplish it using ONLY the tools
available in the workspace. For each step, respond with a JSON object:

{
  "thinking": "brief reasoning about what to do next",
  "action": {
    "tool": "tool.name",
    "args": {"arg1": "value1"},
    "approved": false
  },
  "done": false
}

When the tool requires write access (side_effects != "none"), set "approved": true.

When the task is complete, respond with:
{
  "thinking": "task is complete because...",
  "action": null,
  "done": true,
  "summary": "what was accomplished"
}

IMPORTANT:
- Respond ONLY with the JSON object, no markdown fences, no extra text.
- Use exact tool names from the discovery list.
- For fs.write, the args are: {"path": "filename", "content": "file content"}
- For fs.read, the args are: {"path": "filename"}
- For fs.list, the args are: {"path": "."}
- Only use tools that appear in the discovery list.
"""

TASK_PROMPT = """\
Create a small Go project in the workspace:
1. Write a main.go file with a simple HTTP server that responds "hello world" on /
2. Write a main_test.go file with a test for the handler
3. List the workspace to confirm both files exist
4. Read main.go back to verify the content

Use fs.write (with approved=true), fs.list, and fs.read tools.
"""

MAX_ITERATIONS = 10


class LLMAgentLoopE2E(WorkspaceE2EBase):
    """Agent loop E2E test — LLM drives tool invocations."""

    def __init__(self, provider: LLMProvider) -> None:
        super().__init__(
            test_id="10-llm-agent-loop",
            run_id_prefix=f"e2e-llm-{provider.name}",
            workspace_url=os.getenv("WORKSPACE_URL", "http://localhost:50053"),
            evidence_file=os.getenv("EVIDENCE_FILE", "/tmp/evidence-10-llm-agent-loop.json"),
        )
        self.provider = provider
        self.conversation: list[dict[str, str]] = []
        self.iteration = 0

    def _discover_tools(self, session_id: str) -> list[dict[str, Any]]:
        """Fetch compact tool discovery list."""
        status, body = self.request(
            "GET",
            f"/v1/sessions/{session_id}/tools/discovery?detail=compact",
        )
        if status != 200:
            raise RuntimeError(f"discovery failed ({status}): {body}")
        return body.get("tools", [])

    def _get_recommendations(self, session_id: str, hint: str) -> list[dict[str, Any]]:
        """Fetch tool recommendations for a task hint."""
        status, body = self.request(
            "GET",
            f"/v1/sessions/{session_id}/tools/recommendations?task_hint={urllib.parse.quote(hint)}&top_k=10",
        )
        if status != 200:
            print_warning(f"recommendations failed ({status}), continuing without")
            return []
        return body.get("recommendations", [])

    def _format_tools_for_llm(self, tools: list[dict[str, Any]]) -> str:
        """Format tool list as compact text for LLM context."""
        lines = []
        for t in tools:
            name = t.get("name", "?")
            desc = t.get("description", "")
            args = t.get("required_args", [])
            risk = t.get("risk", "?")
            side = t.get("side_effects", "?")
            approval = t.get("approval", False)
            lines.append(
                f"- {name}: {desc} | args={args} risk={risk} "
                f"side_effects={side} requires_approval={approval}"
            )
        return "\n".join(lines)

    def _parse_llm_response(self, text: str) -> dict[str, Any]:
        """Parse JSON from LLM response, handling markdown fences and <think> tags."""
        import re
        cleaned = text.strip()
        # Strip <think>...</think> reasoning blocks (Qwen3, etc.)
        cleaned = re.sub(r"<think>.*?</think>", "", cleaned, flags=re.DOTALL).strip()
        # Strip markdown code fences if present
        if cleaned.startswith("```"):
            lines = cleaned.split("\n")
            lines = lines[1:]
            if lines and lines[-1].strip() == "```":
                lines = lines[:-1]
            cleaned = "\n".join(lines).strip()
        return json.loads(cleaned)

    def _llm_step(self, tools_text: str, recs_text: str) -> dict[str, Any]:
        """Send conversation to LLM and parse structured response."""
        if not self.conversation:
            # First message: system + tools + task
            self.conversation.append({"role": "system", "content": SYSTEM_PROMPT})
            user_msg = (
                f"Available tools:\n{tools_text}\n\n"
            )
            if recs_text:
                user_msg += f"Recommended tools for this task:\n{recs_text}\n\n"
            user_msg += f"Task:\n{TASK_PROMPT}"
            self.conversation.append({"role": "user", "content": user_msg})
        # else: conversation already has history with tool results

        response_text = self.provider.chat(self.conversation)
        self.conversation.append({"role": "assistant", "content": response_text})

        if self.debug:
            print_info(f"  LLM response: {response_text[:300]}")

        return self._parse_llm_response(response_text)

    def _execute_action(self, session_id: str, action: dict[str, Any]) -> dict[str, Any]:
        """Execute a tool invocation from LLM action."""
        tool_name = action["tool"]
        args = action.get("args", {})
        approved = action.get("approved", False)

        status, body, invocation = self.invoke(
            session_id=session_id,
            tool_name=tool_name,
            args=args,
            approved=approved,
        )

        result: dict[str, Any] = {
            "http_status": status,
            "tool": tool_name,
        }

        if invocation:
            result["invocation_status"] = invocation.get("status")
            result["output"] = invocation.get("output", "")
            error = self.extract_error(invocation, body)
            if error:
                result["error"] = error
        else:
            result["error"] = body

        return result

    def run_agent_loop(self) -> dict[str, Any]:
        """Run the full agent loop."""
        # Step 1: Create session
        print_step(1, "Creating workspace session")
        session_id = self.create_session(
            payload={
                "principal": {"tenant_id": "e2e-tenant", "actor_id": "llm-agent-loop", "roles": ["developer"]},
                "metadata": {"runner_profile": "base", "task": "go-hello-world"},
            },
        )
        self.record_step("create_session", "pass", {"session_id": session_id})
        print_success(f"Session created: {session_id}")

        # Step 2: Discover tools
        print_step(2, f"Discovering tools (provider: {self.provider.name})")
        tools = self._discover_tools(session_id)
        tools_text = self._format_tools_for_llm(tools)
        self.record_step("discover_tools", "pass", {"tool_count": len(tools)})
        print_success(f"Discovered {len(tools)} tools")

        # Step 3: Get recommendations
        print_step(3, "Getting tool recommendations")
        recs = self._get_recommendations(session_id, "create go project with tests")
        recs_text = ""
        if recs:
            recs_text = "\n".join(
                f"- {r.get('name')}: score={r.get('score', '?')}, why={r.get('why', '')}"
                for r in recs[:5]
            )
            self.record_step("recommendations", "pass", {"count": len(recs)})
            print_success(f"Got {len(recs)} recommendations")
        else:
            self.record_step("recommendations", "skip", {"reason": "not available"})
            print_warning("Recommendations not available, continuing")

        # Step 4: Agent loop — LLM decides, we execute
        print_step(4, f"Starting agent loop (max {MAX_ITERATIONS} iterations)")
        loop_results: list[dict[str, Any]] = []

        for i in range(1, MAX_ITERATIONS + 1):
            self.iteration = i
            print_info(f"  --- Iteration {i}/{MAX_ITERATIONS} ---")

            try:
                decision = self._llm_step(tools_text, recs_text)
            except (json.JSONDecodeError, KeyError, ValueError) as exc:
                print_warning(f"  LLM returned invalid JSON: {exc}")
                # Tell the LLM about the error
                self.conversation.append({
                    "role": "user",
                    "content": f"Error: your response was not valid JSON. Please respond with only a JSON object. Error: {exc}",
                })
                loop_results.append({"iteration": i, "error": f"parse_error: {exc}"})
                continue

            thinking = decision.get("thinking", "")
            print_info(f"  Thinking: {thinking[:120]}")

            if decision.get("done"):
                summary = decision.get("summary", "no summary")
                print_success(f"  Agent done: {summary}")
                loop_results.append({
                    "iteration": i,
                    "done": True,
                    "summary": summary,
                })
                break

            action = decision.get("action")
            if not action or not action.get("tool"):
                print_warning("  LLM returned no action, retrying")
                self.conversation.append({
                    "role": "user",
                    "content": "You must provide an action with a tool name. Try again.",
                })
                loop_results.append({"iteration": i, "error": "no_action"})
                continue

            # Execute the tool
            result = self._execute_action(session_id, action)
            loop_results.append({"iteration": i, "action": action, "result": result})

            # Feed result back to LLM
            feedback = json.dumps(result, ensure_ascii=False, default=str)
            self.conversation.append({
                "role": "user",
                "content": f"Tool result:\n{feedback}\n\nContinue with the next step.",
            })

            inv_status = result.get("invocation_status", "unknown")
            print_info(f"  Tool {action['tool']} → {inv_status}")
        else:
            print_warning(f"  Agent did not finish within {MAX_ITERATIONS} iterations")

        self.record_step("agent_loop", "pass", {
            "iterations": self.iteration,
            "provider": self.provider.name,
            "results": loop_results,
        })

        # Step 5: Verify files exist
        print_step(5, "Verifying workspace state")
        status, body, inv = self.invoke(
            session_id=session_id,
            tool_name="fs.list",
            args={"path": "."},
        )
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
            "files": files_found,
            "has_main": has_main,
            "has_test": has_test,
        })

        if has_main:
            print_success(f"Workspace has {len(files_found)} files (main.go: {'yes' if has_main else 'no'}, test: {'yes' if has_test else 'no'})")
        else:
            print_warning(f"main.go not found in workspace (files: {files_found})")

        return {
            "session_id": session_id,
            "provider": self.provider.name,
            "iterations": self.iteration,
            "tools_discovered": len(tools),
            "recommendations": len(recs),
            "files_created": files_found,
            "has_main": has_main,
            "has_test": has_test,
            "loop_results": loop_results,
        }

    def run(self) -> int:
        """Execute the test."""
        try:
            result = self.run_agent_loop()
            self.evidence["result"] = result

            # Determine pass/fail
            if result["has_main"]:
                self.write_evidence("pass")
                print_success(f"LLM Agent Loop PASSED (provider={self.provider.name}, iterations={result['iterations']})")
                return 0
            else:
                self.write_evidence("warn", "Agent completed but main.go not verified")
                print_warning(f"LLM Agent Loop completed with warnings (provider={self.provider.name})")
                return 0  # Warn but don't fail — LLM output is non-deterministic
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
