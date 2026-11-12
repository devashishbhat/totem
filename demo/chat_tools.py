"""The tools the interactive assistant has.

Unlike the staged demo, ``web_fetch`` here really fetches. That is the point of
this agent: you point it at a page you chose, and Totem shows you what the
agent took from it and what it later did with it.

``send_email`` is still a stand-in. It records the message and prints it, and
nothing leaves the machine, because an agent that mails strangers on someone's
first run would be a bad thing to ship.
"""

from __future__ import annotations

import ipaddress
import socket
from dataclasses import dataclass, field
from urllib.parse import urlparse

import httpx
from langchain_core.tools import tool
from langgraph.store.base import BaseStore

from tools import html_to_text

#: Where the assistant's memories live.
MEMORY_NAMESPACE = ("assistant", "memories")

#: How much of a page to read. Enough for an article, far short of a download.
MAX_BYTES = 200_000

#: How much of the extracted text to hand the model. The whole conversation
#: is re-sent on every turn, so an uncapped page would cost more with each
#: reply and eventually exceed the context window.
MAX_TEXT_CHARS = 8_000

#: How long to wait for a page.
TIMEOUT_SECONDS = 10.0

#: How many redirects to follow. Each hop is re-checked.
MAX_REDIRECTS = 3

#: What the fetcher calls itself. Deliberately not anyone's address.
USER_AGENT = "totem-assistant/0.1 (+https://github.com/devashishbhat/totem)"

#: Content types worth handing to a language model.
READABLE = ("text/html", "text/plain", "application/xhtml")


@dataclass
class Outbox:
    """Messages the agent tried to send. Nothing leaves the process."""

    sent: list[dict[str, str]] = field(default_factory=list)


def _public_address_problem(url: str) -> str | None:
    """Why this URL must not be fetched, or ``None`` when it is fine.

    An agent reading an attacker's page can be told to fetch another URL, so
    the fetcher refuses anything that is not a public host: no loopback, no
    private range, and in particular no cloud metadata service.
    """
    parsed = urlparse(url)
    if parsed.scheme not in ("http", "https"):
        return f"{url} is not an http or https URL, so it was not fetched."
    host = parsed.hostname
    if not host:
        return f"{url} has no host, so it was not fetched."
    try:
        infos = socket.getaddrinfo(host, parsed.port or (443 if parsed.scheme == "https" else 80))
    except socket.gaierror:
        return f"{host} does not resolve, so nothing was fetched."
    for info in infos:
        address = ipaddress.ip_address(info[4][0])
        if not address.is_global:
            return (
                f"{host} resolves to {address}, which is not a public address. "
                "This tool only reads public pages."
            )
    return None


def fetch_text(url: str) -> str:
    """The readable text of a public page, or a sentence saying why not.

    Errors come back as text rather than exceptions because the agent is the
    one that has to deal with them, and a sentence is something it can act on.
    """
    with httpx.Client(
        timeout=TIMEOUT_SECONDS,
        follow_redirects=False,
        headers={"user-agent": USER_AGENT, "accept": "text/html,text/plain;q=0.9,*/*;q=0.5"},
    ) as client:
        for _ in range(MAX_REDIRECTS + 1):
            problem = _public_address_problem(url)
            if problem:
                return problem
            try:
                with client.stream("GET", url) as response:
                    if response.is_redirect:
                        location = response.headers.get("location", "")
                        if not location:
                            return f"{url} redirected without saying where to."
                        url = str(response.url.join(location))
                        continue
                    if response.status_code >= 400:
                        return f"{url} returned {response.status_code}."
                    content_type = response.headers.get("content-type", "")
                    if not any(kind in content_type for kind in READABLE):
                        return (
                            f"{url} served {content_type or 'an unknown content type'}, "
                            "which this tool cannot read. It reads HTML and plain text."
                        )
                    chunks: list[bytes] = []
                    total = 0
                    for chunk in response.iter_bytes():
                        chunks.append(chunk)
                        total += len(chunk)
                        if total >= MAX_BYTES:
                            break
                    body = b"".join(chunks).decode(response.encoding or "utf-8", errors="replace")
                    text = body.strip() if "text/plain" in content_type else html_to_text(body)
                    return _clip(text)
            except httpx.HTTPError as err:
                return f"{url} could not be fetched: {err}."
    return f"{url} redirected more than {MAX_REDIRECTS} times, so it was not read."


def _clip(text: str) -> str:
    """Trims page text to what is worth sending, saying so when it trims."""
    if len(text) <= MAX_TEXT_CHARS:
        return text
    return text[:MAX_TEXT_CHARS] + f"\n\n[the page continues past {MAX_TEXT_CHARS} characters]"


def build_tools(store: BaseStore, outbox: Outbox) -> list:
    """The assistant's tools, bound to this store and outbox."""

    @tool
    def web_fetch(url: str) -> str:
        """Read a web page and return its text.

        Use this for any URL the person gives you, and whenever answering
        needs something you can only get from a page.
        """
        return fetch_text(url)

    @tool
    def remember(key: str, value: str) -> str:
        """Store one fact so that later conversations know it.

        Use a short, dotted key such as ``alice.email`` or ``project.deadline``,
        and write the value as a full sentence.
        """
        store.put(MEMORY_NAMESPACE, key, {"value": value})
        return f"Remembered {key}."

    @tool
    def forget(key: str) -> str:
        """Delete a fact you were asked to forget."""
        store.delete(MEMORY_NAMESPACE, key)
        return f"Forgot {key}."

    @tool
    def send_email(to: str, subject: str, body: str) -> str:
        """Send an email. This cannot be undone."""
        outbox.sent.append({"to": to, "subject": subject, "body": body})
        return f"Sent to {to}."

    return [web_fetch, remember, forget, send_email]


#: Tools that change the world. An alert is about a decision that calls one of
#: these on something the agent only knows from an untrusted source.
SIDE_EFFECT_TOOLS = frozenset({"send_email"})

#: Tools whose output is a document from outside, and so untrusted.
EXTERNAL_TOOLS = frozenset({"web_fetch"})

#: Tools whose output is the agent's own bookkeeping rather than anyone
#: else's data: "Remembered x", "Sent to y". Unlisted tool output defaults to
#: untrusted, which is the right default for a tool that fetches or queries
#: something, but wrong for these: left unlisted, the agent's own confirmation
#: strings would drag the trust of every later decision down and every send
#: would raise an alert. web_fetch is deliberately not here.
TRUSTED_TOOLS = frozenset({"remember", "forget", "send_email"})
