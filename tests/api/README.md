# api

Folded into [`../integration/`](../integration/) rather than duplicated
here: `test_full_stack.py` already asserts REST contract shape (status
codes, response JSON fields) as part of driving a real running stack
end-to-end — the same live pair a dedicated API-contract suite would also
need to stand up, so there was no reason to spin up a second one against
the same processes. If API-contract testing ever needs to grow
independently of the full-stack integration flow (a dedicated schema/
OpenAPI-diff check, say), it belongs here as its own suite; until then,
this directory is intentionally empty.
