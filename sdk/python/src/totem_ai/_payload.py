"""Converting LangChain messages into an OpenAI Chat Completions request.

Replay has to be able to ask the model exactly what the agent asked it, so the
recorded payload is a request body that can be sent again unchanged. The
message order is also the coordinate system that in-context spans use, so this
module and the matcher must see the same list.
"""

from __future__ import annotations

import json
from typing import Any

#: Request parameters worth recording. Anything else a provider added is
#: dropped rather than guessed at.
_PARAMS = (
    "temperature",
    "top_p",
    "max_tokens",
    "max_completion_tokens",
    "frequency_penalty",
    "presence_penalty",
    "seed",
    "stop",
    "tools",
    "tool_choice",
    "response_format",
    "parallel_tool_calls",
)


def message_to_openai(message: Any) -> dict[str, Any]:
    """Converts one LangChain message to a Chat Completions message."""
    role = _role_of(message)
    content = message.content if isinstance(getattr(message, "content", None), str) else ""
    if not isinstance(content, str):
        content = _flatten_content(message.content)

    out: dict[str, Any] = {"role": role, "content": content}
    if role == "tool":
        out["tool_call_id"] = getattr(message, "tool_call_id", "") or ""
        return out
    calls = _tool_calls_of(message)
    if calls:
        out["tool_calls"] = calls
    return out


def messages_to_openai(messages: list[Any]) -> list[dict[str, Any]]:
    """Converts a list of LangChain messages."""
    return [message_to_openai(m) for m in messages]


def message_texts(openai_messages: list[dict[str, Any]]) -> list[str]:
    """The text of each message, in the same order as the request.

    This is what the matcher searches, so a span's message index addresses the
    recorded payload directly.
    """
    return [m.get("content") or "" for m in openai_messages]


def build_payload(
    openai_messages: list[dict[str, Any]],
    model: str,
    invocation_params: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Assembles the request body to record."""
    payload: dict[str, Any] = {"model": model, "messages": openai_messages}
    for key, value in (invocation_params or {}).items():
        if key in _PARAMS and value is not None:
            payload[key] = value
    return payload


def model_of(invocation_params: dict[str, Any] | None, serialized: dict[str, Any] | None) -> str:
    """The model name, from wherever LangChain put it."""
    for source in (invocation_params or {}, (serialized or {}).get("kwargs", {})):
        for key in ("model", "model_name", "deployment_name"):
            value = source.get(key)
            if isinstance(value, str) and value:
                return value
    return ""


def _role_of(message: Any) -> str:
    message_type = getattr(message, "type", "") or ""
    return {
        "human": "user",
        "ai": "assistant",
        "system": "system",
        "tool": "tool",
        "function": "tool",
    }.get(message_type, "user")


def _flatten_content(content: Any) -> str:
    """Reduces multi-part content to the text parts, joined by newlines."""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for item in content:
            if isinstance(item, str):
                parts.append(item)
            elif isinstance(item, dict) and isinstance(item.get("text"), str):
                parts.append(item["text"])
        return "\n".join(parts)
    return ""


def _tool_calls_of(message: Any) -> list[dict[str, Any]]:
    calls = getattr(message, "tool_calls", None) or []
    out = []
    for call in calls:
        name = call.get("name") if isinstance(call, dict) else getattr(call, "name", "")
        args = call.get("args") if isinstance(call, dict) else getattr(call, "args", {})
        call_id = call.get("id") if isinstance(call, dict) else getattr(call, "id", "")
        out.append(
            {
                "id": call_id or "",
                "type": "function",
                "function": {"name": name or "", "arguments": json.dumps(args or {}, default=str)},
            }
        )
    return out
