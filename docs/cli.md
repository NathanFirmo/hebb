# CLI

Primary commands:

```bash
hebb init
hebb doctor
hebb encode
hebb retrieve
hebb associate
hebb reinforce
hebb inhibit
hebb forget
hebb consolidate
hebb inspect
hebb maintain
hebb mcp
```

Examples:

```bash
hebb init
hebb doctor
hebb encode --kind decision --title "Use sqlite-vec" --body "Hebb stores vectors in SQLite with sqlite-vec." --entity Hebb --scope /repo
hebb retrieve "how does Hebb search memory?" --scope /repo --limit 10
hebb associate 1 2 --relation supports
hebb reinforce 1 --reason "used_in_answer"
hebb inhibit 1 --reason "stale_or_noisy"
hebb forget 1 --soft
hebb consolidate --scope /repo
hebb inspect trace 1
hebb inspect entity Hebb
hebb maintain embed --pending
hebb maintain decay --dry-run
hebb mcp
hebb agent install --agent codex --apply
hebb agent install --agent claude --apply
```

Aliases:

- `remember` -> `encode`
- `recall` -> `retrieve`
- `link` -> `associate`

## Agent Setup

Use `hebb agent install` to configure supported agents for proactive memory use.

```bash
hebb agent install --agent codex
hebb agent install --agent claude
hebb agent install --agent all
```

Add `--apply` to write changes. Without `--apply`, Hebb prints the plan.

Codex setup registers MCP and adds managed instructions. Claude setup registers MCP, adds lifecycle hooks and adds managed instructions. Hooks call `hebb agent hook ...` internally.
