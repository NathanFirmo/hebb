# MCP

Hebb is MCP-first for agent integrations.

Example configuration:

```json
{
  "mcpServers": {
    "hebb": {
      "command": "hebb",
      "args": ["mcp"]
    }
  }
}
```

Planned tools:

- `hebb_encode_trace`
- `hebb_retrieve_context`
- `hebb_associate_traces`
- `hebb_reinforce_trace`
- `hebb_inhibit_trace`
- `hebb_consolidate_memory`
- `hebb_inspect_trace`
- `hebb_memory_stats`

The MVP does not expose hard delete over MCP. Hard delete is CLI-only and must require explicit confirmation.

