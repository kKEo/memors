# Part IV: Exploratory testing

Automated tests check what someone thought to check. Exploration finds the rest. You use
memors-mcp the way an agent or a person would, watch what it does, and ask "is that right?".
Every surprising answer is either a bug, a [behaviour gap](../scenarios/gaps.md), or a
scenario waiting to be written.

- [A sandbox knowledge base](sandbox.md): a throwaway setup that cannot touch real data.
- [Probing retrieval](retrieval.md): why a result ranked where it did, and how to turn real
  questions into benchmark queries.
- [Checking the store](store.md): status, integrity, lint, history and the live UI after an
  experiment.

When you find something, write it down as a story (actors, steps over time, what you
observed, what you expected) and follow [Ideas worth a scenario](../scenarios/next.md#ideas-worth-a-scenario).
