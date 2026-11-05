# RagCode MCP v3

Clean rewrite with the same features as v2 and about half the code. Design,
decisions and work order:
[docs/plans/2026-11-04-v3-simplification-proposal.md](../docs/plans/2026-11-04-v3-simplification-proposal.md).

Separate Go module (`github.com/doITmagic/rag-code-mcp/v3`). It does not import
v2 packages and nothing is copied over; v2 is read as a reference only and stays
frozen and shippable until v3 replaces it. Packages are added in the plan's
order: parser, store, embed, index, engine, tools, cmd, ops.
