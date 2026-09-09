from __future__ import annotations

import json
import re
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Callable, Iterable
from uuid import uuid4

ToolFn = Callable[..., Any]


@dataclass
class Tool:
    name: str
    description: str
    parameters: dict[str, Any]
    fn: ToolFn


@dataclass
class HermesResult:
    content: str
    messages: list[dict[str, Any]] = field(default_factory=list)
    turns: int = 0


class HermesKernel:
    """OpenAI tool-calling loop: complete → tools → observe, until the model stops."""

    def __init__(
        self,
        complete: Callable[[list[dict[str, Any]], list[dict[str, Any]]], dict[str, Any]],
        tools: Iterable[Tool],
        max_turns: int = 8,
    ) -> None:
        self.complete = complete
        self.tools = {tool.name: tool for tool in tools}
        self.max_turns = max_turns

    def schemas(self) -> list[dict[str, Any]]:
        return [
            {
                "type": "function",
                "function": {
                    "name": tool.name,
                    "description": tool.description,
                    "parameters": tool.parameters,
                },
            }
            for tool in self.tools.values()
        ]

    def run(self, system: str, user: str) -> HermesResult:
        messages: list[dict[str, Any]] = [
            {"role": "system", "content": system},
            {"role": "user", "content": user},
        ]
        for turn in range(1, self.max_turns + 1):
            assistant = self.complete(messages, self.schemas())
            assistant = _normalize_assistant(assistant)
            messages.append(assistant)
            calls = assistant.get("tool_calls") or []
            if not calls:
                return HermesResult(content=str(assistant.get("content") or ""), messages=messages, turns=turn)
            for call in calls:
                name = ((call.get("function") or {}).get("name")) or ""
                raw_args = (call.get("function") or {}).get("arguments") or "{}"
                tool_id = call.get("id") or f"call_{uuid4().hex[:8]}"
                try:
                    args = json.loads(raw_args) if isinstance(raw_args, str) else dict(raw_args)
                    if name not in self.tools:
                        payload: Any = {"error": f"unknown tool {name}"}
                    else:
                        payload = self.tools[name].fn(**args)
                except Exception as exc:
                    payload = {"error": str(exc)}
                messages.append(
                    {
                        "role": "tool",
                        "tool_call_id": tool_id,
                        "content": json.dumps(payload, ensure_ascii=False, default=str),
                    }
                )
        return HermesResult(content="max_turns", messages=messages, turns=self.max_turns)


def grok2api_complete(
    base_url: str,
    api_key: str,
    model: str,
    timeout: int = 120,
) -> Callable[[list[dict[str, Any]], list[dict[str, Any]]], dict[str, Any]]:
    endpoint = base_url.rstrip("/") + "/chat/completions"

    def complete(messages: list[dict[str, Any]], tools: list[dict[str, Any]]) -> dict[str, Any]:
        body = {
            "model": model,
            "messages": messages,
            "tools": tools,
            "tool_choice": "auto",
            "temperature": 0,
            "stream": False,
        }
        request = urllib.request.Request(
            endpoint,
            data=json.dumps(body).encode("utf-8"),
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {api_key}",
            },
            method="POST",
        )
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                payload = json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")[:800]
            raise RuntimeError(f"grok2api {exc.code}: {detail}") from exc
        choices = payload.get("choices") or []
        if not choices:
            raise RuntimeError("grok2api 没有返回 choices")
        message = choices[0].get("message") or {}
        if "role" not in message:
            message["role"] = "assistant"
        return message

    return complete


_TOOL_CALL_BLOCK = re.compile(r"(?is)<tool_calls\s*>(.*?)</tool_calls\s*>")
_TOOL_CALL = re.compile(r"(?is)<tool_call\s*>(.*?)</tool_call\s*>")
_TOOL_NAME = re.compile(r"(?is)<tool_name\s*>(.*?)</tool_name\s*>")
_TOOL_PARAMS = re.compile(r"(?is)<parameters\s*>(.*?)</parameters\s*>")


def _normalize_assistant(message: dict[str, Any]) -> dict[str, Any]:
    if message.get("tool_calls"):
        return message
    content = str(message.get("content") or "")
    block = _TOOL_CALL_BLOCK.search(content)
    if not block:
        return message
    calls = []
    for index, chunk in enumerate(_TOOL_CALL.findall(block.group(1)), start=1):
        name_match = _TOOL_NAME.search(chunk)
        params_match = _TOOL_PARAMS.search(chunk)
        if not name_match:
            continue
        calls.append(
            {
                "id": f"call_{index}",
                "type": "function",
                "function": {
                    "name": name_match.group(1).strip(),
                    "arguments": (params_match.group(1).strip() if params_match else "{}"),
                },
            }
        )
    if calls:
        message = dict(message)
        message["tool_calls"] = calls
        message["content"] = None
    return message
