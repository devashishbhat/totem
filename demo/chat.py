"""An assistant you can talk to, instrumented with Totem.

This is the agent to use on your own conversations. It has a memory that
outlasts the conversation, it can read real web pages, and it can take one
irreversible action (sending email, simulated). Everything it reads, writes,
recalls and decides is recorded, so the dashboard fills in as you talk.

    make chat

The whole conversation is one Totem run, which is what a conversation is: the
timeline reads top to bottom as the thing you just had.

For the staged prompt-injection story instead, see crm_agent.py.
"""

from __future__ import annotations

import argparse
import getpass
import json
import os
import sys
import warnings
from pathlib import Path
from typing import Any

import totem_ai as tm
from langchain_core.language_models import BaseChatModel
from langchain_core.messages import AIMessage, BaseMessage, HumanMessage, SystemMessage
from langgraph.prebuilt import create_react_agent
from langgraph.store.base import BaseStore
from langgraph.store.memory import InMemoryStore

from chat_tools import (
    EXTERNAL_TOOLS,
    MEMORY_NAMESPACE,
    SIDE_EFFECT_TOOLS,
    TRUSTED_TOOLS,
    Outbox,
    build_tools,
)

AGENT_ID = "assistant"

#: Where memories are kept between conversations. Gitignored.
MEMORY_FILE = Path(__file__).parent / ".memory.json"

SYSTEM_PROMPT = """You are a personal assistant with a memory that lasts \
beyond this conversation.

You can read web pages, remember and forget facts, and send email. Work from
what you know.

Guidelines:
- When you learn something that will matter later, a name, an address, a
  deadline, a preference, call remember() with it before you reply. Anything
  you only say out loud is forgotten when this conversation ends.
- Use what you remember. Do not ask again for something you have been told.
- When you send an email, use the address you remember for that person or
  company.
- Keep replies to a few sentences.
"""

HELP = """Commands:
  /memory          what the assistant currently remembers
  /forget <key>    delete one memory
  /reset           delete every memory
  /run             the dashboard link for this conversation
  /help            this
  /quit            leave (Ctrl-D also works)
"""


# -- memory that outlives the process -------------------------------------
#
# These read and write the store directly, before Totem wraps it, so that
# reloading yesterday's memories is not recorded as the agent writing them
# today.


def load_memories(store: BaseStore, path: Path) -> int:
    """Puts saved memories back into the store. Returns how many."""
    if not path.exists():
        return 0
    try:
        saved = json.loads(path.read_text(encoding="utf-8")).get("memories") or {}
    except (OSError, ValueError):
        return 0
    for key, value in saved.items():
        if isinstance(value, str):
            store.put(MEMORY_NAMESPACE, key, {"value": value})
    return len(saved)


def save_memories(store: BaseStore, path: Path) -> None:
    """Writes the store's memories out, so the next conversation has them."""
    memories = {item.key: item.value.get("value", "") for item in store.search(MEMORY_NAMESPACE)}
    try:
        path.write_text(json.dumps({"memories": memories}, indent=2), encoding="utf-8")
    except OSError as err:
        print(dim(f"  (could not save memories: {err})"))


def current_memories(store: BaseStore) -> dict[str, str]:
    """What the assistant remembers right now."""
    return {item.key: item.value.get("value", "") for item in store.search(MEMORY_NAMESPACE)}


# -- the agent -------------------------------------------------------------


def build_prompt(store: BaseStore):
    """Builds the prompt function, which is where memory reaches the model.

    Recall happens here rather than through a tool, which is how an assistant
    actually works: it does not decide to trust a memory, it simply finds the
    memory already in its prompt. That is also what makes a poisoned memory
    worth tracing.
    """

    def prompt(state: dict) -> list[BaseMessage]:
        # Each memory is rendered with its key. A bare value is ambiguous: an
        # address on its own does not say whose it is, so the model stops to
        # label it instead of acting on it, and the same prompt yields a
        # different decision from one sample to the next. The key also keeps
        # the counterfactual honest, because removing the value leaves the
        # label behind and the agent knows to look the address up again.
        remembered = [
            (item.key, item.value.get("value", "")) for item in store.search(MEMORY_NAMESPACE)
        ]
        text = SYSTEM_PROMPT
        if remembered:
            lines = "\n".join(f"- {key}: {value}" for key, value in remembered)
            text += f"\nWhat you remember:\n{lines}\n"
        return [SystemMessage(text), *state["messages"]]

    return prompt


def build_model(model_name: str) -> BaseChatModel:
    """The model the assistant runs on."""
    from langchain_openai import ChatOpenAI

    return ChatOpenAI(model=model_name, temperature=0)


# -- terminal --------------------------------------------------------------

_COLOUR = sys.stdout.isatty() and os.getenv("NO_COLOR") is None


def dim(text: str) -> str:
    return f"\033[2m{text}\033[0m" if _COLOUR else text


def bold(text: str) -> str:
    return f"\033[1m{text}\033[0m" if _COLOUR else text


def explain(err: Exception) -> str:
    """A sentence a person can act on, for the failures that actually happen."""
    message = str(err)
    lowered = message.lower()
    if "invalid_api_key" in lowered or "incorrect api key" in lowered or " 401" in lowered:
        return "The model rejected the API key. Check OPENAI_API_KEY in .env, then start again."
    if "insufficient_quota" in lowered or "exceeded your current quota" in lowered:
        return (
            "The API key has no credit left. Add a payment method at "
            "platform.openai.com, or wait for a free-tier reset."
        )
    if "rate limit" in lowered or " 429" in lowered:
        return f"The model is rate limiting this key. Wait a moment and try again. ({message})"
    if "model_not_found" in lowered or "does not exist" in lowered:
        return f"That model is not available to this key. Try --model gpt-4o-mini. ({message})"
    return f"The model call failed: {message}"


def describe_tool_calls(messages: list[BaseMessage]) -> list[str]:
    """One dim line per tool the agent called, in order."""
    lines = []
    for message in messages:
        if not isinstance(message, AIMessage):
            continue
        for call in message.tool_calls or []:
            args = call.get("args") or {}
            detail = ""
            if "url" in args:
                detail = str(args["url"])
            elif "key" in args:
                detail = str(args["key"])
            elif "to" in args:
                detail = str(args["to"])
            lines.append(f"  · {call.get('name', '?')} {detail}".rstrip())
    return lines


# -- the loop --------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="An assistant with a traced memory.")
    parser.add_argument(
        "--model",
        default=os.getenv("TM_CHAT_MODEL", os.getenv("TM_REPLAY_MODEL", "gpt-4o-mini")),
        help="the OpenAI model to use (default: %(default)s)",
    )
    parser.add_argument(
        "--endpoint",
        default=os.getenv("TM_ENDPOINT", "http://localhost:8080"),
        help="totem-server base URL",
    )
    parser.add_argument(
        "--dashboard",
        default=os.getenv("TM_DASHBOARD_URL", "http://localhost:5173"),
        help="dashboard base URL, for the links this prints",
    )
    parser.add_argument(
        "--scope",
        default=os.getenv("TM_CHAT_SCOPE", ""),
        help="who the conversation is for, as recorded (default: your username)",
    )
    parser.add_argument(
        "--memory-file",
        default=os.getenv("TM_CHAT_MEMORY_FILE", str(MEMORY_FILE)),
        help="where memories are kept between conversations",
    )
    parser.add_argument(
        "--fresh",
        action="store_true",
        help="start with an empty memory, leaving the saved one untouched",
    )
    args = parser.parse_args(argv)

    # LangGraph 1.0 moved create_react_agent and warns when it is called.
    # Migrating means taking on the langchain v1 package and re-verifying that
    # the staged demo still falls for the poisoned page, so it is a change of
    # its own (docs/decisions.md). Until then the warning is addressed to
    # whoever maintains this file, not to whoever is reading this terminal.
    warnings.filterwarnings("ignore", message=".*create_react_agent has been moved.*")

    if not os.getenv("OPENAI_API_KEY"):
        print(
            "OPENAI_API_KEY is not set, and this assistant needs a real model to talk to.\n"
            "Put your key in .env (see .env.example) and start again.",
            file=sys.stderr,
        )
        return 2

    scope = args.scope or f"user:{getpass.getuser()}"
    memory_path = Path(args.memory_file)

    # Prompts are recorded as the agent sent them, which is what lets a replay
    # reproduce a decision. Everything stays in the local Postgres.
    redact = os.getenv("TM_REDACT_PAYLOADS", "false") != "false"
    tm.init(
        endpoint=args.endpoint,
        agent_id=AGENT_ID,
        side_effect_tools=SIDE_EFFECT_TOOLS,
        external_tools=EXTERNAL_TOOLS,
        trusted_tools=TRUSTED_TOOLS,
        redact_payloads=redact,
    )

    inner = InMemoryStore()
    loaded = 0 if args.fresh else load_memories(inner, memory_path)
    store = tm.wrap_store(inner)
    outbox = Outbox()

    try:
        model = build_model(args.model)
    except Exception as err:
        print(explain(err), file=sys.stderr)
        tm.shutdown()
        return 1

    agent = create_react_agent(
        model,
        build_tools(store, outbox),
        store=store,
        prompt=build_prompt(store),
    )
    handler = tm.CallbackHandler()

    print()
    print(bold("Totem assistant") + dim(f"  ·  {args.model}  ·  recording to {args.endpoint}"))
    print(dim(f"  remembers {loaded} thing{'' if loaded == 1 else 's'} from before"))
    if not redact:
        print(dim("  prompts are stored as sent, so decisions can be replayed"))
    print(dim("  /help for commands"))

    status = 0
    try:
        with tm.run(scope=scope) as ctx:
            run_url = f"{args.dashboard.rstrip('/')}/runs/{ctx.run_id}"
            print(dim(f"  this conversation: {run_url}"))
            print()
            status = converse(
                agent=agent,
                handler=handler,
                store=store,
                inner=inner,
                outbox=outbox,
                memory_path=memory_path,
                run_url=run_url,
            )
    finally:
        save_memories(inner, memory_path)
        print(dim("\nflushing to the dashboard…"))
        tm.shutdown()
    return status


def converse(
    *,
    agent: Any,
    handler: Any,
    store: BaseStore,
    inner: BaseStore,
    outbox: Outbox,
    memory_path: Path,
    run_url: str,
) -> int:
    """The read, invoke, print loop. Returns the exit status."""
    history: list[BaseMessage] = []
    while True:
        try:
            line = input(bold("you  ") if _COLOUR else "you  ").strip()
        except (EOFError, KeyboardInterrupt):
            print()
            return 0
        if not line:
            continue

        if line.startswith("/"):
            if handle_command(line, store=store, inner=inner, run_url=run_url):
                return 0
            continue

        history.append(HumanMessage(line))
        before_messages = len(history)
        before_sent = len(outbox.sent)
        try:
            result = agent.invoke(
                {"messages": history},
                config={"callbacks": [handler], "recursion_limit": 24},
            )
        except KeyboardInterrupt:
            print(dim("  (interrupted)"))
            continue
        except Exception as err:
            print(dim("  " + explain(err)))
            continue

        history = list(result["messages"])
        for detail in describe_tool_calls(history[before_messages:]):
            print(dim(detail))
        for email in outbox.sent[before_sent:]:
            print(dim(f"  [outbox] to={email['to']} subject={email['subject']}"))
        answer = history[-1].content if history else ""
        print(f"\n{answer if isinstance(answer, str) else str(answer)}\n")
        # Saved every turn, so a crash or a Ctrl-C does not lose the memory.
        save_memories(inner, memory_path)


def handle_command(line: str, *, store: BaseStore, inner: BaseStore, run_url: str) -> bool:
    """Runs a slash command. Returns True when the loop should end."""
    command, _, rest = line.partition(" ")
    rest = rest.strip()
    if command in ("/quit", "/exit"):
        return True
    if command == "/help":
        print(HELP)
        return False
    if command == "/run":
        print(dim(f"  {run_url}"))
        return False
    if command == "/memory":
        # Read the unwrapped store: listing memories here is the operator
        # looking, not the agent recalling, and recording it would put a step
        # in the timeline that the model never saw.
        memories = current_memories(inner)
        if not memories:
            print(dim("  nothing remembered yet"))
        for key, value in sorted(memories.items()):
            print(f"  {key}: {value}")
        return False
    if command == "/forget":
        if not rest:
            print(dim("  /forget <key>"))
            return False
        store.delete(MEMORY_NAMESPACE, rest)
        print(dim(f"  forgot {rest}"))
        return False
    if command == "/reset":
        for key in current_memories(inner):
            store.delete(MEMORY_NAMESPACE, key)
        print(dim("  memory emptied"))
        return False
    print(dim(f"  {command} is not a command. /help lists them."))
    return False


if __name__ == "__main__":
    raise SystemExit(main())
