"""LLM provider abstraction — Claude, OpenAI, vLLM (OpenAI-compatible)."""

from __future__ import annotations

import json
import os
import urllib.error
import urllib.request
from typing import Any


class LLMProvider:
    """Base class for LLM providers."""

    name: str = "base"

    def chat(self, messages: list[dict[str, str]], tools_schema: list[dict] | None = None) -> str:
        raise NotImplementedError


class ClaudeProvider(LLMProvider):
    """Anthropic Claude API provider."""

    name = "claude"

    def __init__(self) -> None:
        self.api_key = os.environ["ANTHROPIC_API_KEY"]
        self.model = os.getenv("CLAUDE_MODEL", "claude-sonnet-4-20250514")
        self.url = "https://api.anthropic.com/v1/messages"

    def chat(self, messages: list[dict[str, str]], tools_schema: list[dict] | None = None) -> str:
        system_msg = ""
        api_messages = []
        for m in messages:
            if m["role"] == "system":
                system_msg = m["content"]
            else:
                api_messages.append({"role": m["role"], "content": m["content"]})

        body: dict[str, Any] = {
            "model": self.model,
            "max_tokens": 4096,
            "messages": api_messages,
        }
        if system_msg:
            body["system"] = system_msg

        headers = {
            "Content-Type": "application/json",
            "x-api-key": self.api_key,
            "anthropic-version": "2023-06-01",
        }

        req = urllib.request.Request(
            self.url,
            data=json.dumps(body).encode(),
            method="POST",
            headers=headers,
        )
        with urllib.request.urlopen(req, timeout=120) as resp:
            data = json.loads(resp.read().decode())

        for block in data.get("content", []):
            if block.get("type") == "text":
                return block["text"]
        return ""


class OpenAIProvider(LLMProvider):
    """OpenAI API provider."""

    name = "openai"

    def __init__(self) -> None:
        self.api_key = os.environ["OPENAI_API_KEY"]
        self.model = os.getenv("OPENAI_MODEL", "gpt-4o-mini")
        self.url = "https://api.openai.com/v1/chat/completions"

    def chat(self, messages: list[dict[str, str]], tools_schema: list[dict] | None = None) -> str:
        body: dict[str, Any] = {
            "model": self.model,
            "messages": messages,
            "max_tokens": 4096,
            "temperature": 0.2,
        }

        headers = {
            "Content-Type": "application/json",
            "Authorization": f"Bearer {self.api_key}",
        }

        req = urllib.request.Request(
            self.url,
            data=json.dumps(body).encode(),
            method="POST",
            headers=headers,
        )
        with urllib.request.urlopen(req, timeout=120) as resp:
            data = json.loads(resp.read().decode())

        return data["choices"][0]["message"]["content"]


class VLLMProvider(LLMProvider):
    """vLLM OpenAI-compatible provider (local Qwen or other models)."""

    name = "vllm"

    def __init__(self) -> None:
        self.url = os.getenv("VLLM_URL", "http://vllm:8000") + "/v1/chat/completions"
        self.model = os.getenv("VLLM_MODEL", "Qwen/Qwen3-8B")

    def chat(self, messages: list[dict[str, str]], tools_schema: list[dict] | None = None) -> str:
        body: dict[str, Any] = {
            "model": self.model,
            "messages": messages,
            "max_tokens": 4096,
            "temperature": 0.2,
        }

        headers = {"Content-Type": "application/json"}

        # vLLM may require an API key in some configs
        api_key = os.getenv("VLLM_API_KEY", "")
        if api_key:
            headers["Authorization"] = f"Bearer {api_key}"

        req = urllib.request.Request(
            self.url,
            data=json.dumps(body).encode(),
            method="POST",
            headers=headers,
        )
        with urllib.request.urlopen(req, timeout=300) as resp:
            data = json.loads(resp.read().decode())

        return data["choices"][0]["message"]["content"]


def get_provider(name: str) -> LLMProvider:
    """Factory for LLM providers."""
    providers = {
        "claude": ClaudeProvider,
        "openai": OpenAIProvider,
        "vllm": VLLMProvider,
    }
    if name not in providers:
        raise ValueError(f"Unknown LLM provider: {name}. Options: {list(providers.keys())}")
    return providers[name]()
