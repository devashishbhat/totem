"""The LangChain callback handler.

It sees three things the store cannot: the prompt that was actually sent, the
tool calls the model chose, and the tool output that comes back. Those are what
turn a pile of memories into an attributed decision.
"""

from __future__ import annotations

import json
import logging
import re
from typing import Any
from uuid import UUID

from langchain_core.callbacks import BaseCallbackHandler
from langchain_core.messages import BaseMessage
from langchain_core.outputs import ChatGeneration, LLMResult

from ._payload import build_payload, message_texts, messages_to_openai, model_of

log = logging.getLogger("totem_ai")

_URL = re.compile(r"https?://[^\s\"'<>]+")


class CallbackHandler(BaseCallbackHandler):
    """Records model calls, decisions and tool output.

    Pass it in the LangChain config::

        graph.invoke(inputs, config={"callbacks": [tm.CallbackHandler()]})
    """

    #: A callback that raises would take the agent down with it.
    raise_error = False
    run_inline = True

    def __init__(self) -> None:
        self._prompts: dict[UUID, _Prompt] = {}
        self._tools: dict[UUID, tuple[str, str]] = {}

    # -- model calls -----------------------------------------------------

    def on_chat_model_start(
        self,
        serialized: dict[str, Any],
        messages: list[list[BaseMessage]],
        *,
        run_id: UUID,
        **kwargs: Any,
    ) -> None:
        try:
            batch = messages[0] if messages else []
            params = kwargs.get("invocation_params") or {}
            openai_messages = messages_to_openai(batch)
            self._prompts[run_id] = _Prompt(
                messages=openai_messages,
                model=model_of(params, serialized),
                params=params,
            )
            self._record_prompt_sources(batch)
        except Exception:
            log.debug("totem: on_chat_model_start failed", exc_info=True)

    def on_llm_end(self, response: LLMResult, *, run_id: UUID, **kwargs: Any) -> None:
        from . import recorder

        rec = recorder()
        prompt = self._prompts.pop(run_id, None)
        if rec is None or prompt is None:
            return
        try:
            payload = build_payload(prompt.messages, prompt.model, prompt.params)
            call_id = rec.record_llm_call(
                message_texts(prompt.messages),
                model=prompt.model,
                payload=payload,
            )
            self._record_decisions(rec, response, call_id)
        except Exception:
            log.debug("totem: on_llm_end failed", exc_info=True)

    def on_llm_error(self, error: BaseException, *, run_id: UUID, **kwargs: Any) -> None:
        self._prompts.pop(run_id, None)

    # -- tools -----------------------------------------------------------

    def on_tool_start(
        self,
        serialized: dict[str, Any],
        input_str: str,
        *,
        run_id: UUID,
        **kwargs: Any,
    ) -> None:
        name = _tool_name(serialized, kwargs)
        self._tools[run_id] = (name, _tool_input(input_str, kwargs))
        from . import current_run

        ctx = current_run()
        if ctx is not None and ctx.step is not None:
            ctx.step.tool = name

    def on_tool_end(self, output: Any, *, run_id: UUID, **kwargs: Any) -> None:
        from . import recorder

        rec = recorder()
        name, tool_input = self._tools.pop(run_id, ("", ""))
        if rec is None:
            return
        try:
            text = _tool_output_text(output)
            if not text:
                return
            origin = name
            if name in rec.config.external_tools:
                # A fetched document's origin is where it came from, which is
                # what the taint path should name. Ordinary tool output keeps
                # the tool's name, because that is what the trusted-tool lists
                # are written against.
                kind, trust = "external_document", None
                origin = _first_url(tool_input) or name
            elif name in rec.config.trusted_tools:
                kind, trust = "tool_output", 2
            else:
                kind, trust = "tool_output", None
            rec.record_source(kind, text, origin=origin, trust=trust)
        except Exception:
            log.debug("totem: on_tool_end failed", exc_info=True)

    def on_tool_error(self, error: BaseException, *, run_id: UUID, **kwargs: Any) -> None:
        self._tools.pop(run_id, None)

    # -- helpers ---------------------------------------------------------

    def _record_prompt_sources(self, messages: list[BaseMessage]) -> None:
        """Records the user's words as sources.

        The system message is deliberately not recorded as a source. Agents
        assemble it, which means it often contains recalled memories rendered
        into it; recording it would then make a trusted system prompt look like
        the origin of a value that in fact came from an untrusted memory. The
        system message is still kept verbatim in the recorded payload, and an
        agent that wants its static configuration tracked can record it itself
        with ``record_source("system_config", ...)``.

        Repeats are dropped by the run context, so re-sending the same
        conversation on every step does not pile up sources.
        """
        from . import recorder

        rec = recorder()
        if rec is None:
            return
        for message in messages:
            if message.type != "human":
                continue
            text = message.content if isinstance(message.content, str) else ""
            if text.strip():
                rec.record_source("user_message", text)

    def _record_decisions(self, rec: Any, response: LLMResult, call_id: str | None) -> None:
        for generations in response.generations:
            for generation in generations:
                if not isinstance(generation, ChatGeneration):
                    if generation.text.strip():
                        rec.record_decision(
                            "", {"content": generation.text}, side_effect=False, call_id=call_id
                        )
                    continue
                message = generation.message
                calls = getattr(message, "tool_calls", None) or []
                for call in calls:
                    name = call.get("name", "") if isinstance(call, dict) else ""
                    args = call.get("args", {}) if isinstance(call, dict) else {}
                    rec.record_decision(name, args, call_id=call_id)
                if calls:
                    continue
                text = message.content if isinstance(message.content, str) else ""
                if text.strip():
                    rec.record_decision("", {"content": text}, side_effect=False, call_id=call_id)


class _Prompt:
    """One prompt in flight, kept until its response arrives."""

    __slots__ = ("messages", "model", "params")

    def __init__(self, messages: list[dict[str, Any]], model: str, params: dict[str, Any]) -> None:
        self.messages = messages
        self.model = model
        self.params = params


def _tool_name(serialized: dict[str, Any], kwargs: dict[str, Any]) -> str:
    name = kwargs.get("name") or (serialized or {}).get("name") or ""
    if not name:
        path = (serialized or {}).get("id") or []
        if isinstance(path, list) and path:
            name = str(path[-1])
    return str(name)


def _tool_input(input_str: str, kwargs: dict[str, Any]) -> str:
    inputs = kwargs.get("inputs")
    if isinstance(inputs, dict):
        return json.dumps(inputs, default=str)
    return input_str or ""


def _first_url(text: str) -> str | None:
    found = _URL.search(text)
    return found.group(0).rstrip("\"',") if found else None


def _tool_output_text(output: Any) -> str:
    content = getattr(output, "content", None)
    if isinstance(content, str):
        return content
    if isinstance(output, str):
        return output
    return str(output)
