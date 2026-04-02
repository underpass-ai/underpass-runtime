"""LLM provider abstraction — config-driven via llm_config.yaml.

Supports two modes per provider:
- tool_calling: true  → native function calling (industry standard)
- tool_calling: false → free-text JSON (legacy fallback)
"""

from __future__ import annotations

import json
import os
import re
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any

import yaml


def _load_config() -> dict[str, Any]:
    cfg_path = Path(__file__).parent / "llm_config.yaml"
    if not cfg_path.exists():
        cfg_path = Path("/app/llm_config.yaml")
    with open(cfg_path) as f:
        return yaml.safe_load(f)


_CONFIG = _load_config()


# ── Tool schemas for native tool calling ─────────────────────────────────

AGENT_TOOLS_SCHEMA = [
    {
        "type": "function",
        "function": {
            "name": "fs_write_file",
            "description": "Write content to a file in the workspace.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "File path relative to workspace root"},
                    "content": {"type": "string", "description": "Full file content to write"},
                },
                "required": ["path", "content"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "fs_read_file",
            "description": "Read the content of a file in the workspace.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "File path relative to workspace root"},
                },
                "required": ["path"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "fs_list",
            "description": "List files and directories in the workspace.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Directory path relative to workspace root"},
                },
                "required": ["path"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "done",
            "description": "Signal that the task is complete.",
            "parameters": {
                "type": "object",
                "properties": {
                    "summary": {"type": "string", "description": "Brief summary of what was accomplished"},
                },
                "required": ["summary"],
            },
        },
    },
]

# Anthropic uses a different schema format
AGENT_TOOLS_ANTHROPIC = [
    {
        "name": "fs_write_file",
        "description": "Write content to a file in the workspace.",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string", "description": "File path"},
                "content": {"type": "string", "description": "Full file content"},
            },
            "required": ["path", "content"],
        },
    },
    {
        "name": "fs_read_file",
        "description": "Read the content of a file.",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string", "description": "File path"},
            },
            "required": ["path"],
        },
    },
    {
        "name": "fs_list",
        "description": "List files and directories.",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string", "description": "Directory path"},
            },
            "required": ["path"],
        },
    },
    {
        "name": "done",
        "description": "Signal that the task is complete.",
        "input_schema": {
            "type": "object",
            "properties": {
                "summary": {"type": "string", "description": "Brief summary"},
            },
            "required": ["summary"],
        },
    },
]

# Map tool_call function names back to runtime tool names
TOOL_NAME_MAP = {
    "fs_write_file": "fs.write_file",
    "fs_read_file": "fs.read_file",
    "fs_list": "fs.list",
}


# ── Agent decision dataclass ─────────────────────────────────────────────

class AgentDecision:
    """Unified decision from any LLM provider."""

    def __init__(
        self,
        *,
        done: bool = False,
        tool: str = "",
        args: dict[str, Any] | None = None,
        approved: bool = False,
        summary: str = "",
        thinking: str = "",
    ):
        self.done = done
        self.tool = tool
        self.args = args or {}
        self.approved = approved
        self.summary = summary
        self.thinking = thinking


# ── Provider ─────────────────────────────────────────────────────────────

class LLMProvider:
    """Config-driven LLM provider with native tool calling support."""

    def __init__(self, name: str, cfg: dict[str, Any]) -> None:
        self.name = name
        self.provider_type = cfg["type"]
        self.model = os.getenv(f"{name.upper().replace('-','_')}_MODEL", cfg.get("model", ""))
        self.url = os.getenv(f"{name.upper().replace('-','_')}_URL", cfg.get("url", ""))
        self.max_tokens = int(cfg.get("max_tokens", 4096))
        self.temperature = float(cfg.get("temperature", 0.2))
        self.tool_calling = cfg.get("tool_calling", False)
        self.api_key = ""
        if cfg.get("api_key_env"):
            self.api_key = os.environ.get(cfg["api_key_env"], "")
        thinking_cfg = cfg.get("thinking", {})
        self.thinking_enabled = thinking_cfg.get("enabled", False) if isinstance(thinking_cfg, dict) else bool(thinking_cfg)
        self.thinking_strip = thinking_cfg.get("strip_tags", True) if isinstance(thinking_cfg, dict) else True

    def decide(self, messages: list[dict[str, Any]]) -> AgentDecision:
        """Send messages to LLM and return a structured decision."""
        if self.tool_calling:
            return self._decide_tool_calling(messages)
        return self._decide_freetext(messages)

    # ── Native tool calling ──────────────────────────────────────────────

    def _decide_tool_calling(self, messages: list[dict[str, Any]]) -> AgentDecision:
        if self.provider_type == "anthropic":
            return self._tc_anthropic(messages)
        return self._tc_openai_compat(messages)

    def _tc_openai_compat(self, messages: list[dict[str, Any]]) -> AgentDecision:
        """OpenAI / vLLM native tool calling."""
        endpoint = self.url.rstrip("/")
        if not endpoint.endswith("/chat/completions"):
            endpoint += "/v1/chat/completions"

        body: dict[str, Any] = {
            "model": self.model,
            "messages": messages,
            "max_tokens": self.max_tokens,
            "temperature": self.temperature,
            "tools": AGENT_TOOLS_SCHEMA,
            "tool_choice": "auto",
        }
        if self.thinking_enabled and self.provider_type == "vllm":
            body["chat_template_kwargs"] = {"enable_thinking": True}

        headers: dict[str, str] = {"Content-Type": "application/json"}
        key = os.getenv("VLLM_API_KEY", self.api_key)
        if key:
            headers["Authorization"] = f"Bearer {key}"

        data = self._http_post(endpoint, body, headers, timeout=300)
        msg = data["choices"][0]["message"]
        tool_calls = msg.get("tool_calls") or []

        if not tool_calls:
            # Model responded with text instead of tool call — parse as done
            content = msg.get("content") or ""
            if self.thinking_strip:
                content = re.sub(r"<think>.*?</think>", "", content, flags=re.DOTALL).strip()
            return AgentDecision(done=True, summary=content or "no tool call returned", thinking=content)

        tc = tool_calls[0]
        fn_name = tc["function"]["name"]
        fn_args = tc["function"].get("arguments", "{}")
        if isinstance(fn_args, str):
            fn_args = json.loads(fn_args)

        if fn_name == "done":
            return AgentDecision(done=True, summary=fn_args.get("summary", ""))

        runtime_tool = TOOL_NAME_MAP.get(fn_name, fn_name.replace("_", "."))
        needs_approval = runtime_tool in ("fs.write_file", "fs.delete", "git.push")

        return AgentDecision(
            tool=runtime_tool,
            args=fn_args,
            approved=needs_approval,
            thinking=msg.get("reasoning") or "",
        )

    def _tc_anthropic(self, messages: list[dict[str, Any]]) -> AgentDecision:
        """Anthropic native tool use."""
        system_msg = ""
        api_messages = []
        for m in messages:
            if m["role"] == "system":
                system_msg = m["content"]
            else:
                api_messages.append(m)

        body: dict[str, Any] = {
            "model": self.model,
            "max_tokens": self.max_tokens,
            "messages": api_messages,
            "tools": AGENT_TOOLS_ANTHROPIC,
        }
        if system_msg:
            body["system"] = system_msg

        headers = {
            "Content-Type": "application/json",
            "x-api-key": self.api_key,
            "anthropic-version": "2023-06-01",
        }

        data = self._http_post(self.url, body, headers, timeout=120)

        thinking = ""
        for block in data.get("content", []):
            if block.get("type") == "text":
                thinking = block["text"]
            if block.get("type") == "tool_use":
                fn_name = block["name"]
                fn_args = block.get("input", {})
                if fn_name == "done":
                    return AgentDecision(done=True, summary=fn_args.get("summary", ""), thinking=thinking)
                runtime_tool = TOOL_NAME_MAP.get(fn_name, fn_name.replace("_", "."))
                needs_approval = runtime_tool in ("fs.write_file", "fs.delete", "git.push")
                return AgentDecision(tool=runtime_tool, args=fn_args, approved=needs_approval, thinking=thinking)

        # No tool use — treat as done
        return AgentDecision(done=True, summary=thinking or "no tool call", thinking=thinking)

    # ── Free-text JSON fallback ──────────────────────────────────────────

    def _decide_freetext(self, messages: list[dict[str, Any]]) -> AgentDecision:
        """Legacy: parse JSON from free-text response."""
        if self.provider_type == "anthropic":
            text = self._chat_anthropic(messages)
        elif self.provider_type == "vllm":
            text = self._chat_vllm(messages)
        else:
            text = self._chat_openai(messages)

        cleaned = text.strip()
        if self.thinking_strip:
            cleaned = re.sub(r"<think>.*?</think>", "", cleaned, flags=re.DOTALL).strip()
        if cleaned.startswith("```"):
            lines = cleaned.split("\n")[1:]
            if lines and lines[-1].strip() == "```":
                lines = lines[:-1]
            cleaned = "\n".join(lines).strip()

        parsed = json.loads(cleaned)
        if parsed.get("done"):
            return AgentDecision(done=True, summary=parsed.get("summary", ""), thinking=parsed.get("thinking", ""))

        action = parsed.get("action", {})
        tool = action.get("tool", "")
        needs_approval = action.get("approved", False) or tool in ("fs.write_file", "fs.delete")
        return AgentDecision(
            tool=tool,
            args=action.get("args", {}),
            approved=needs_approval,
            thinking=parsed.get("thinking", ""),
        )

    # ── Raw chat methods (for freetext fallback) ─────────────────────────

    def _chat_anthropic(self, messages: list[dict[str, Any]]) -> str:
        system_msg = ""
        api_messages = []
        for m in messages:
            if m["role"] == "system":
                system_msg = m["content"]
            else:
                api_messages.append({"role": m["role"], "content": m["content"]})
        body: dict[str, Any] = {"model": self.model, "max_tokens": self.max_tokens, "messages": api_messages}
        if system_msg:
            body["system"] = system_msg
        headers = {"Content-Type": "application/json", "x-api-key": self.api_key, "anthropic-version": "2023-06-01"}
        data = self._http_post(self.url, body, headers, timeout=120)
        for block in data.get("content", []):
            if block.get("type") == "text":
                return block["text"]
        return ""

    def _chat_openai(self, messages: list[dict[str, Any]]) -> str:
        body = {"model": self.model, "messages": messages, "max_tokens": self.max_tokens, "temperature": self.temperature}
        headers = {"Content-Type": "application/json", "Authorization": f"Bearer {self.api_key}"}
        data = self._http_post(self.url + "/v1/chat/completions" if "/v1/" not in self.url else self.url, body, headers, timeout=120)
        return data["choices"][0]["message"]["content"]

    def _chat_vllm(self, messages: list[dict[str, Any]]) -> str:
        endpoint = self.url.rstrip("/") + "/v1/chat/completions"
        body: dict[str, Any] = {"model": self.model, "messages": messages, "max_tokens": self.max_tokens, "temperature": self.temperature}
        if not self.thinking_enabled:
            body["chat_template_kwargs"] = {"enable_thinking": False}
        headers: dict[str, str] = {"Content-Type": "application/json"}
        key = os.getenv("VLLM_API_KEY", self.api_key)
        if key:
            headers["Authorization"] = f"Bearer {key}"
        data = self._http_post(endpoint, body, headers, timeout=300)
        msg = data["choices"][0]["message"]
        content = msg.get("content") or msg.get("reasoning") or ""
        return content

    # ── HTTP helper ──────────────────────────────────────────────────────

    @staticmethod
    def _http_post(url: str, body: dict, headers: dict, timeout: int = 120) -> dict:
        req = urllib.request.Request(url, data=json.dumps(body).encode(), method="POST", headers=headers)
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode())


def get_provider(name: str) -> LLMProvider:
    """Factory — looks up provider by name in llm_config.yaml."""
    providers = _CONFIG.get("providers", {})
    if name == "vllm":
        url_override = os.getenv("VLLM_URL", "")
        model_override = os.getenv("VLLM_MODEL", "")
        if url_override or model_override:
            cfg = dict(providers.get("vllm", {}))
            if url_override:
                cfg["url"] = url_override
            if model_override:
                cfg["model"] = model_override
            return LLMProvider(name, cfg)
    if name not in providers:
        raise ValueError(f"Unknown LLM provider: {name}. Options: {list(providers.keys())}")
    return LLMProvider(name, providers[name])
