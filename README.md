# meltiply-cli

An agent-first CLI for remote SaaS applications backed by OpenAPI, built in Go.

## Architecture & Research

Comprehensive architecture research and blueprints derived from CircleCI CLI (`circleci-cli`) and Cloudflare CLI (`cf`):
- Read the complete technical document: [RESEARCH_AGENT_FIRST_CLI.md](./RESEARCH_AGENT_FIRST_CLI.md)

## Core Capabilities for AI Agents

1. **Self-Introspection**: `melt tools` exports full CLI command surfaces as Model Context Protocol (MCP) tool schemas.
2. **Schema Introspection**: `melt schema <resource> <action>` dumps exact JSON Schema for parameters and request bodies.
3. **Intent Search**: `melt search "<query>"` maps natural language intents to exact CLI commands without crawling help text.
4. **Structured Error Recovery**: Errors output structured JSON to `stderr` with actionable `suggestions[]` for automated self-healing.
5. **Context Window Efficiency**: Pure JSON outputs with built-in `--jq` expression evaluation to filter payloads inside the Go binary.
6. **Mutation Safeguards**: Universal `--dry-run` to preview HTTP method, URL, and body prior to execution.
7. **Input Flexibility**: Typed flags + `--body <json|@file|->` bypass for nested OpenAPI payloads.
