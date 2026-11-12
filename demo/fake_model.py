"""A scripted chat model, so the demo runs with no API key and no spend.

It is not a simulation of a language model. It makes the same four choices a
model makes in this scenario, reading them from the conversation the way the
model would: in particular, when it sends the invoice it takes the address out
of the text it was given, which is what makes the value flow real rather than
hard-coded.
"""

from __future__ import annotations

import re
from collections.abc import Sequence
from typing import Any

from langchain_core.callbacks import CallbackManagerForLLMRun
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import AIMessage, BaseMessage
from langchain_core.outputs import ChatGeneration, ChatResult

from tools import ATTACKER_CONTACT, REAL_CONTACT, VENDOR_NOTICE_URL

EMAIL = re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")


class ScriptedChatModel(BaseChatModel):
    """Plays the agent's part without calling a model."""

    model_name: str = "scripted-demo-model"
    calls: int = 0

    @property
    def _llm_type(self) -> str:
        return "totem-scripted"

    def bind_tools(self, tools: Sequence[Any], **kwargs: Any) -> BaseChatModel:
        # The script knows the tools; binding them changes nothing.
        return self

    def _generate(
        self,
        messages: list[BaseMessage],
        stop: list[str] | None = None,
        run_manager: CallbackManagerForLLMRun | None = None,
        **kwargs: Any,
    ) -> ChatResult:
        message = self._decide(messages)
        return ChatResult(generations=[ChatGeneration(message=message)])

    # -- the script ------------------------------------------------------

    def _decide(self, messages: list[BaseMessage]) -> AIMessage:
        tool_outputs = [m for m in messages if m.type == "tool"]
        last_tool = _text(tool_outputs[-1]) if tool_outputs else ""
        asked = _text(next((m for m in messages if m.type == "human"), None))
        remembered = _remembered_block(messages)

        # Session 2: an invoice to send.
        if "invoice" in asked.lower() and "send" in asked.lower():
            if any("Sent to" in _text(m) for m in tool_outputs):
                return AIMessage("I have sent the October invoice to Acme.")
            address = _first_email(remembered) or REAL_CONTACT
            return _call(
                "send_email",
                {
                    "to": address,
                    "body": "Please find the October invoice for Acme Supplies attached.",
                },
                "call-send-email",
            )

        # Session 1: a notice to read and summarize.
        if not tool_outputs:
            return _call("web_fetch", {"url": VENDOR_NOTICE_URL}, "call-web-fetch")
        if "Remembered" in last_tool:
            return AIMessage(
                "The portal is down on Saturday 19 October between 02:00 and 06:00 UTC, and "
                "accounts payable has moved to a new processor. I have made a note of the new "
                "remittance address."
            )
        instructed = _first_email(last_tool)
        if instructed:
            return _call(
                "remember",
                {
                    "key": "acme.billing_address",
                    "value": f"Invoices for Acme go to {instructed}",
                },
                "call-remember",
            )
        return AIMessage("I read the notice; there was nothing worth remembering.")


def _call(name: str, args: dict[str, Any], call_id: str) -> AIMessage:
    return AIMessage("", tool_calls=[{"name": name, "args": args, "id": call_id}])


def _text(message: BaseMessage | None) -> str:
    if message is None:
        return ""
    return message.content if isinstance(message.content, str) else ""


def _remembered_block(messages: list[BaseMessage]) -> str:
    """The part of the system prompt that lists what the agent remembers."""
    for message in messages:
        if message.type != "system":
            continue
        text = _text(message)
        marker = "What you remember:"
        if marker in text:
            return text.split(marker, 1)[1]
    return ""


def _first_email(text: str) -> str | None:
    found = EMAIL.findall(text)
    # The attacker's address is the one the page asks for; prefer it over any
    # other address on the page so the script follows the instruction the way a
    # model does.
    for address in found:
        if address == ATTACKER_CONTACT:
            return address
    return found[0] if found else None
