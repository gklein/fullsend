#!/usr/bin/env python3
"""Claude Code PreToolUse hook: tool call allowlist enforcement.

Blocks tool calls outside the agent's authorized tool set. If the agent
attempts to call Bash, WebFetch, or any other out-of-role tool, this
hook blocks the call.

Protocol: reads JSON from stdin (tool_name, tool_input),
writes JSON to stdout if blocking. Exit 0 = allow, exit 1 = block.

Environment variables:
  FULLSEND_TOOL_ALLOWLIST: Comma-separated list of allowed tool names.
                            If unset, uses the default triage agent allowlist.
                            If set to empty string "", all tools are blocked.
"""

from __future__ import annotations

import json
import os
import sys
from datetime import UTC, datetime

FINDINGS_PATH = "/tmp/workspace/.security/findings.jsonl"

_ERR_MALFORMED = '{"decision":"block","reason":"ALLOWLIST_HOOK_ERROR: malformed JSON input"}'
_ERR_UNEXPECTED = (
    '{"decision":"block","reason":"ALLOWLIST_HOOK_ERROR: unexpected error reading input"}'
)

DEFAULT_TRIAGE_ALLOWLIST: frozenset[str] = frozenset(
    {
        "mcp__github__issue_read",
        "mcp__github__issue_comment",
        "mcp__github__label_add",
        "mcp__github__label_remove",
        "mcp__github__search_issues",
    }
)


def log_finding(name: str, severity: str, detail: str, action: str) -> None:
    trace_id = os.environ.get("FULLSEND_TRACE_ID", "")
    finding = {
        "trace_id": trace_id,
        "timestamp": datetime.now(UTC).isoformat(),
        "phase": "hook_pretool",
        "scanner": "tool_allowlist_pretool",
        "name": name,
        "severity": severity,
        "detail": detail,
        "action": action,
    }
    try:
        with open(FINDINGS_PATH, "a") as f:
            f.write(json.dumps(finding) + "\n")
    except OSError:
        pass


def _parse_allowlist(env_value: str | None) -> frozenset[str]:
    if env_value is None:
        return DEFAULT_TRIAGE_ALLOWLIST
    tools = {t.strip() for t in env_value.split(",") if t.strip()}
    return frozenset(tools)


def main() -> None:
    try:
        raw = sys.stdin.read()
        if not raw.strip():
            sys.exit(0)
        hook_input = json.loads(raw)
    except json.JSONDecodeError:
        sys.stdout.write(_ERR_MALFORMED)
        sys.exit(1)
    except Exception:  # noqa: BLE001
        sys.stdout.write(_ERR_UNEXPECTED)
        sys.exit(1)

    tool_name = hook_input.get("tool_name", "")
    if not tool_name:
        sys.exit(0)

    env_value = os.environ.get("FULLSEND_TOOL_ALLOWLIST")
    allowed_tools = _parse_allowlist(env_value)

    if tool_name in allowed_tools:
        sys.exit(0)

    reason = f"Tool '{tool_name}' is NOT in the allowlist ({', '.join(sorted(allowed_tools))})"
    log_finding("tool_blocked", "critical", reason, "block")
    json.dump({"decision": "block", "reason": reason}, sys.stdout)
    sys.exit(1)


if __name__ == "__main__":
    main()
