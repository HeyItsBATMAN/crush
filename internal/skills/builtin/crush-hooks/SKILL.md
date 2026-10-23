---
name: crush-hooks
description: Use when the user wants to add, write, debug, or configure a Crush hook — gating or blocking tool calls, approving or rewriting tool input before execution, injecting context into tool results, or troubleshooting hook behavior in crush.json.
---

# Crush Hooks

Hooks are user-defined commands in `crush.json` that fire at specific points
during execution, giving deterministic control over tool behavior. They run
**only on the top-level agent** — sub-agent calls (task tool, agentic_fetch,
etc.) are not intercepted, though the sub-agent tool call itself is. Tool hooks
run **before** permission checks; `Stop` hooks run after the turn ends.

For the full reference, see `docs/hooks/README.md`. This skill covers what you
need to author correct hooks.

## Supported Events

Three events are currently supported. Event names are case-insensitive and
accept snake_case (`PreToolUse`, `pretooluse`, `pre_tool_use` all work).

- **`PreToolUse`** — fires before a top-level tool call. Can block, allow,
  rewrite input, or inject context.
- **`Stop`** — fires once per top-level turn, right after the agent finishes
  responding (the same moment the "Crush is waiting..." notification fires).
  It is **observational**: the turn is over, so `decision`, `halt`, `context`,
  and `updated_input` are ignored. Use it for side effects such as
  notifications, sounds, logging, or kicking off a build. `Stop` has no tool
  to match, so `matcher` is ignored and every configured `Stop` hook runs.
- **`SessionStart`** — fires once, exactly after a new session's first user
  message is recorded and before the model responds. It is the place to
  inject session-long context. On exit 0, plain-text stdout is injected
  verbatim and a JSON envelope contributes its `context` field; empty output
  injects nothing. `matcher` is ignored and every configured `SessionStart`
  hook runs.

## Configuration

```jsonc
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "^bash$",              // regex against tool name (optional; omit to match all)
        "command": "./hooks/my-hook.sh",   // required: shell command to run
        "timeout": 10                     // optional: seconds, default 30
      }
    ],
    "Stop": [
      {
        // No matcher here: there is no tool to match against.
        "command": "./hooks/notify-finished.sh"
      }
    ]
  }
}
```

Project-level hooks take precedence over global. Matching hooks are deduped by
`command`, run in parallel, and aggregated in **config order** (not finish order).

## Language

`command` is a shell command, so hooks can be written in any language by
invoking the interpreter: `node ./hooks/h.js`, `python3 ./hooks/h.py`,
`./hooks/h.sh`, inline `echo '…'`, etc. The rest of this skill shows bash, but
the input/output contract is identical regardless of language.

## Input

**Environment variables:**

| Variable                     | Description                              |
| ---------------------------- | ---------------------------------------- |
| `CRUSH_EVENT`                | Event name (e.g. `PreToolUse`)           |
| `CRUSH_TOOL_NAME`            | Tool being called (e.g. `bash`), `PreToolUse` only |
| `CRUSH_SESSION_ID`           | Current session ID                       |
| `CRUSH_CWD`                  | Working directory                        |
| `CRUSH_PROJECT_DIR`          | Project root directory                   |
| `CRUSH_TOOL_INPUT_COMMAND`   | For `bash` calls: the shell command      |
| `CRUSH_TOOL_INPUT_FILE_PATH` | For file tools: the target file path     |
| `CRUSH_SESSION_TITLE`        | `Stop` only: the session's title         |
| `CRUSH_STOP_MESSAGE`         | `Stop` only: the turn's final message    |

Event-specific variables are only set for the events that use them.

**JSON on stdin:**

```json
{
  "event": "PreToolUse",
  "session_id": "313909e",
  "cwd": "/home/user/project",
  "tool_name": "bash",
  "tool_input": {"command": "rm -rf /"}
}
```

`Stop` carries the session title and the turn's final message instead of the
tool fields:

```json
{
  "event": "Stop",
  "session_id": "313909e",
  "session_title": "Fix the login redirect",
  "cwd": "/home/user/project",
  "message": "All tests pass."
}
```

## Output

Communicate back via exit code (+ stderr) or JSON on stdout.

| Exit Code | Meaning                                                       |
| --------- | ------------------------------------------------------------- |
| 0         | Success. Stdout is parsed as the JSON envelope below.         |
| 2         | Block this tool call. Stderr becomes the deny reason.         |
| 49        | Halt the whole turn. Stderr becomes the halt reason.          |
| Other     | Non-blocking error. Logged and ignored; tool call proceeds.   |

Exit 2 blocks one tool call (agent sees the reason and can try again); exit 49
ends the whole turn (user takes over). Default to deny — reach for halt only
when letting the agent retry is itself the problem (e.g. secrets detected,
policy violation).

**JSON envelope (exit 0):**

```json
{
  "version": 1,
  "decision": "allow",
  "halt": false,
  "reason": "...",
  "context": "Extra info for the model",
  "updated_input": {"command": "rewritten"}
}
```

- **`decision`**: `"allow"`, `"deny"`, or omit. `"allow"` is **affirmative
  pre-approval** — it bypasses the permission prompt entirely. Omit it
  (or `null`) when you only want to inject context or rewrite input without
  also auto-approving the call.
- **`halt: true`**: ends the turn (same as exit 49).
- **`reason`**: shown to the model on deny; to model and user on halt.
- **`context`**: string **or array of strings**. Appended to what the model
  sees. Empty entries are dropped.
- **`updated_input`**: **shallow-merge patch** against `tool_input`, not a
  replacement. Keys you include overwrite; keys you don't are preserved.
  Nested objects are replaced wholesale, not deep-merged. Ignored on deny/halt.

## Aggregation (Multiple Hooks)

Composed in **config order**:

- `deny` > `allow` > no opinion. First deny decides; subsequent allows don't override.
- `halt` is sticky: any hook halting ends the turn.
- `reason` and `context` concatenate in config order (newline-joined).
- `updated_input` patches shallow-merge sequentially; later patches win on colliding keys.

## Canonical Examples

### Block destructive commands

```bash
#!/usr/bin/env bash
set -euo pipefail

if echo "$CRUSH_TOOL_INPUT_COMMAND" | grep -qE 'rm\s+-(rf|fr)\s+/'; then
  echo "Refusing to run rm -rf against root" >&2
  exit 2
fi
```

Config: `{"matcher": "^bash$", "command": "./hooks/no-rm-rf.sh"}`

### Auto-approve read-only tools (inline, no script)

```jsonc
{"matcher": "^(view|ls|grep|glob)$", "command": "echo '{\"decision\":\"allow\"}'"}
```

Every `view`/`ls`/`grep`/`glob` call now runs without prompting.

### Inject context without auto-approving

Emit only `context` — omit `decision` so the normal permission flow still runs.

```bash
#!/usr/bin/env bash
set -euo pipefail

if [[ "$CRUSH_TOOL_INPUT_FILE_PATH" == *.go ]]; then
  echo '{"context": "Remember: run gofumpt after editing Go files."}'
else
  echo '{}'
fi
```

Config: `{"matcher": "^(edit|write|multiedit)$", "command": "./hooks/go-context.sh"}`

### Rewrite tool input (shallow merge)

```bash
#!/usr/bin/env bash
set -euo pipefail

read -r input
rewritten=$(echo "$input" | jq -r '.tool_input.command' | some-rewriter)

cat <<EOF
{
  "context": "Rewrote command",
  "updated_input": {"command": "$rewritten"}
}
EOF
```

If the original call was `{"command": "npm test", "timeout": 60000}`, the
tool runs with `{"command": "<rewritten>", "timeout": 60000}` — `timeout` is
preserved.

### Notify when the agent stops

```bash
#!/usr/bin/env bash
set -euo pipefail

notify-send "Crush finished" "$CRUSH_SESSION_TITLE"
```

Config: `{"command": "./hooks/notify-finished.sh"}` under `hooks.Stop`. No
matcher: `Stop` hooks always run. Side effects only; anything printed is
ignored.

## Authoring Checklist

1. Add `#!/usr/bin/env bash` and `set -euo pipefail` (for shell scripts).
2. `chmod +x` the script.
3. Add the entry under `hooks.PreToolUse` (with a matcher) or
   `hooks.Stop`/`hooks.SessionStart` (no matcher) in `crush.json`.
4. For `PreToolUse`, decide intent: inject context (omit `decision`),
   auto-approve (`"allow"`), block (`exit 2`), or halt (`exit 49`).
5. For `Stop`, do side effects only — output is ignored because the turn has
   already ended.
6. For `SessionStart`, print the context to inject as plain text (or emit
   `context` via the JSON envelope); empty output injects nothing.
7. If rewriting input, remember `updated_input` is a shallow merge — only
   include the keys you want to change.

## Debugging

- Timeouts kill the hook silently and the tool call proceeds. Bump `timeout` if needed.
- Non-zero exit codes other than 2/49 are logged but don't block — check Crush logs.
- `Stop` hooks ignore exit 2/49 and their stdout: there is nothing left to block.
- `SessionStart` ignores exit 2/49 as well; only its stdout/context matters.
- Use `echo "debug info" >&2` for logging without corrupting stdout JSON.
- `matcher` is a regex against the tool name. Use `^bash$` (not `bash`) if you
  don't also want to match `mcp_something_bash`. It is ignored for `Stop` and `SessionStart`.

## Claude Code Compatibility

Crush also accepts Claude Code's `hookSpecificOutput` envelope. One intentional
divergence: Crush treats `updated_input` as shallow-merge, Claude Code replaces.
Existing Claude Code hooks work without modification for the matcher/decision
parts; revisit any that relied on `updatedInput` fully replacing tool input.
