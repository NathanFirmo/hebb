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
```

Aliases:

- `remember` -> `encode`
- `recall` -> `retrieve`
- `link` -> `associate`

