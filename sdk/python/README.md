# totem-ai

The Python SDK for [Totem](../../README.md). It records what an agent read,
what reached the model, and what the agent then did, so a decision can be
traced back to the memory and the source that caused it.

```python
import totem_ai as tm

tm.init(agent_id="crm-assistant", side_effect_tools={"send_email"})
store = tm.wrap_store(InMemoryStore())
handler = tm.CallbackHandler()

with tm.run(scope="user:42"):
    graph.invoke(inputs, config={"callbacks": [handler]})

tm.shutdown()
```

Every value Totem records is redacted before it leaves the process, and nothing
in the SDK raises into the agent.
