| Field | Value |
|---|---|
| **Type** | ADR |
| **Date** | 2026-03-23 |
| **Deciders** | Core team |
| **Last synced** | 2026-03-23 |

# ADR-004 — Passive capture as primary recording mechanism

## Status
Accepted

## Context
Opax needs to record agent sessions. The recording mechanism determines how much cooperation is required from agent platforms and how reliably sessions are captured.

## Options Considered

### Option A — Agent-side integration (MCP tools, API calls)
- Pros: real-time capture, richer metadata.
- Cons: requires each agent platform to actively call Opax. Web-only platforms may support this, but CLI-based agents (Claude Code, Codex) already write session data to disk. Adoption friction: every platform must integrate.

### Option B — Passive capture via git hooks
- Pros: zero agent cooperation. Hooks detect sessions and read agent-native storage after the fact. Works with any agent that writes session files to disk. Agent-specific readers know where each platform stores data (Claude Code's JSONL files, Codex session logs).
- Cons: capture happens after the session, not during. Some metadata may be lost if the agent doesn't persist it. Doesn't work for platforms without local file access.

### Option C — Both, with passive as primary
- Pros: passive capture handles the common case (CLI agents with file access). MCP provides a fallback for web-only platforms (Claude web, ChatGPT).
- Cons: two code paths to maintain.

## Decision
Option C. Passive capture is the primary mechanism. MCP is a complement for platforms without shell access.

Hooks fire on commit, detect active agent sessions, read transcripts from disk, and create save records. This is the Entire.io pattern — it works because the major CLI agent platforms already write structured session data to local files.

## Consequences

### Positive
- Works today with Claude Code and Codex without any changes to those platforms
- Zero adoption friction for agents
- Adding a new agent platform means writing a reader, not negotiating an integration

### Negative
- Capture is post-hoc, not real-time
- Web-only platforms require MCP fallback path

### Follow-up
- Agent-specific reader implementations (Claude, Codex, Cursor, Gemini CLI)
- MCP server tool design
