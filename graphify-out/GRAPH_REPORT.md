# Graph Report - .  (2026-04-22)

## Corpus Check
- Corpus is ~31,676 words - fits in a single context window. You may not need a graph.

## Summary
- 279 nodes · 392 edges · 18 communities detected
- Extraction: 88% EXTRACTED · 12% INFERRED · 0% AMBIGUOUS · INFERRED: 46 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Phase 2a Stubs & Tests|Phase 2a Stubs & Tests]]
- [[_COMMUNITY_claw-code Runtime Analysis|claw-code Runtime Analysis]]
- [[_COMMUNITY_Phase 2b Work Methods (panic TODO)|Phase 2b Work Methods (panic TODO)]]
- [[_COMMUNITY_AutoAgent + OpenClaw Channel Patterns|AutoAgent + OpenClaw Channel Patterns]]
- [[_COMMUNITY_CLI Wiring (main→cli→provider)|CLI Wiring (main→cli→provider)]]
- [[_COMMUNITY_Multi-Agent Communication (taskeventadvisor)|Multi-Agent Communication (task/event/advisor)]]
- [[_COMMUNITY_Haemil Project Structure|Haemil Project Structure]]
- [[_COMMUNITY_Goose MCP + AutoAgent Meta-Patterns|Goose MCP + AutoAgent Meta-Patterns]]
- [[_COMMUNITY_Task-Based Governance (Paperclip + SLA)|Task-Based Governance (Paperclip + SLA)]]
- [[_COMMUNITY_Domain Types + Roundtrip Tests|Domain Types + Roundtrip Tests]]
- [[_COMMUNITY_runtime Domain Types (message.go)|runtime Domain Types (message.go)]]
- [[_COMMUNITY_Hermes Memory & Skill System|Hermes Memory & Skill System]]
- [[_COMMUNITY_Anthropic API Gotchas & Security Rationale|Anthropic API Gotchas & Security Rationale]]
- [[_COMMUNITY_GoClaw Infrastructure Patterns|GoClaw Infrastructure Patterns]]
- [[_COMMUNITY_Anthropic SSE Scanner (stub)|Anthropic SSE Scanner (stub)]]
- [[_COMMUNITY_Runtime Turn Loop (conversation.go)|Runtime Turn Loop (conversation.go)]]
- [[_COMMUNITY_Session File Permissions (07000600)|Session File Permissions (0700/0600)]]
- [[_COMMUNITY_skeleton.md Design Doc|skeleton.md Design Doc]]

## God Nodes (most connected - your core abstractions)
1. `claw-code Platform` - 21 edges
2. `GoClaw Platform` - 17 edges
3. `Haemil Project` - 13 edges
4. `Hermes Platform` - 13 edges
5. `Run()` - 12 edges
6. `OpenClaw Platform` - 12 edges
7. `Paperclip Platform` - 12 edges
8. `internal/runtime package` - 11 edges
9. `24 AI Agent Platform Survey` - 11 edges
10. `Goose Platform` - 11 edges

## Surprising Connections (you probably didn't know these)
- `RedactAPIKey()` --semantically_similar_to--> `skeleton.md §9 gotchas (API key redaction, anthropic-version)`  [INFERRED] [semantically similar]
  internal/provider/provider.go → analysis/integration/skeleton.md
- `BashTool.Execute (panic TODO Phase 2b)` --semantically_similar_to--> `GoClaw 5-layer security architecture`  [INFERRED] [semantically similar]
  internal/tools/bash.go → analysis/platforms/goclaw.md
- `Runtime.RunTurn (panic TODO)` --semantically_similar_to--> `skeleton.md SSE event handling table`  [INFERRED] [semantically similar]
  internal/runtime/conversation.go → analysis/integration/skeleton.md
- `Default()` --future_source_of_tools--> `Goose MCP tool sourcing (future)`  [EXTRACTED]
  internal/tools/tool.go → analysis/platforms/goose.md
- `BLOCKED_PATTERNS regex list` --implements_spec--> `skeleton.md §8 BLOCKED_PATTERNS spec`  [EXTRACTED]
  internal/tools/bash.go → analysis/integration/skeleton.md

## Hyperedges (group relationships)
- **7 Source OSS Platform Corpus** — claw_code_platform, hermes_platform, openclaw_platform, paperclip_platform, goclaw_platform, goose_platform, autoagent_platform [EXTRACTED 1.00]
- **3-Layer Communication (Task/Event/Advisor)** — mac_layer1_task, mac_layer2_event, mac_layer3_advisor, paperclip_task_based_comm, goclaw_domain_event_bus [EXTRACTED 1.00]
- **Skeleton Layer 0 Design (single-agent)** — skel_import_graph, skel_provider_interface, skel_tool_interface, skel_run_turn_algorithm, skel_jsonl_session_format, skel_blocked_patterns [EXTRACTED 1.00]
- **Phase 2b Work Methods (all panic TODO)** — conversation_runturn, anthropic_chat, bash_execute, session_appenduser, session_appendassistant, session_messages, anthropic_ssescanner_next [EXTRACTED 1.00]
- **Phase 2a Real Assembly Constructors (no panic)** — session_newsession, conversation_new, bash_newbash, tool_default, repl_run, provider_new [EXTRACTED 1.00]
- **Phase 2a Regression Test Suite** — message_test_testmessagejsonroundtrip, bash_test_testbashspecschema, bash_test_testblockedpatternscompile, provider_test_testproviderfactory, provider_test_testredactapikey [EXTRACTED 1.00]

## Communities

### Community 0 - "Phase 2a Stubs & Tests"
Cohesion: 0.09
Nodes (26): anthropicProvider struct, anthropicProvider.Name, BashTool struct, NewBash(), BashTool.Spec method, bashSpecDescription constant, bashSpecSchema JSON Schema literal, TestBashSpecSchema() (+18 more)

### Community 1 - "claw-code Runtime Analysis"
Cohesion: 0.08
Nodes (30): compact.rs (auto-compaction), config.rs (multi-source merge), ConversationRuntime<C,T>, 4-Layer Security (bash+perms+policy+sandbox), hooks.rs (Pre/Post ToolUse), permissions.rs (5-mode gate), claw-code Platform, FNV1a Prompt Cache (+22 more)

### Community 2 - "Phase 2b Work Methods (panic TODO)"
Cohesion: 0.1
Nodes (21): anthropicAPIVersion constant (2023-06-01), anthropicProvider.Chat (panic TODO), anthropicMessagesURL endpoint, sseEvent struct, sseScanner.Next (panic TODO), BLOCKED_PATTERNS regex list, BashTool.Execute (panic TODO Phase 2b), TestBlockedPatternsCompile() (+13 more)

### Community 3 - "AutoAgent + OpenClaw Channel Patterns"
Cohesion: 0.11
Nodes (23): Failure-Driven Improvement (MAX_RETRY=3), GAIA/MultiHopRAG/Math500 Benchmarks, AutoAgent Platform, Plugin Registry (tools/agents/workflows), 24+ Messaging Channels, Channel Manager + Exponential Backoff, ChannelPlugin Contract, 2-Stage Channel Registry (bundled+external) (+15 more)

### Community 4 - "CLI Wiring (main→cli→provider)"
Cohesion: 0.13
Nodes (12): Config, main (cmd/haemil), New(), RedactAPIKey(), TestProviderFactory(), TestRedactAPIKey(), Run(), Session (+4 more)

### Community 5 - "Multi-Agent Communication (task/event/advisor)"
Cohesion: 0.1
Nodes (21): worker_boot.rs state machine, DomainEventBus, CostEvent (advisor tracking), Layer 2: Event Bus (real-time signals), Layer 3: Advisor (Anthropic advisor_20260301), OutboxRelay Worker (FOR UPDATE SKIP LOCKED), Why Outbox: task-event consistency guarantee, Why separate What/When/HowSmart (+13 more)

### Community 6 - "Haemil Project Structure"
Cohesion: 0.18
Nodes (18): cmd/haemil/main.go, Consumer Defines Interface Principle, Haemil Core Engine (Go), internal/cli package, internal/provider package, internal/runtime package, internal/tools package, Go Porting Difficulty Assessment (MED) (+10 more)

### Community 7 - "Goose MCP + AutoAgent Meta-Patterns"
Cohesion: 0.15
Nodes (17): Agent Creator (XML -> Python), Agent Former (NL -> XML), Meta-Agent Pattern, Tool Editor (auto tool gen), mcp_stdio.rs (JSON-RPC transport), mcp_tool_bridge.rs, Self-Evolution Guardrail, Built-in MCP Servers (in-process duplex) (+9 more)

### Community 8 - "Task-Based Governance (Paperclip + SLA)"
Cohesion: 0.16
Nodes (17): session.rs (JSONL + rotation), Auto-Approval Policy (threshold-based), Bidirectional Issue Flow (top-down + bottom-up), Layer 1: Task (Paperclip, persistent audit), proposed Status (new issue state), SLA Checker (4-stage graduated escalation), sla_policies schema, Approvals System (hire/budget/strategy) (+9 more)

### Community 9 - "Domain Types + Roundtrip Tests"
Cohesion: 0.18
Nodes (12): ToolCallRecord, TurnSummary, BlockType (text/tool_use/tool_result), ChatResponse struct, ContentBlock struct, Message struct, Role type (user/assistant/system), TestMessageJSONRoundtrip() (+4 more)

### Community 10 - "runtime Domain Types (message.go)"
Cohesion: 0.18
Nodes (10): BlockType, ChatRequest, ChatResponse, ContentBlock, Message, Provider, Role, Tool (+2 more)

### Community 11 - "Hermes Memory & Skill System"
Cohesion: 0.25
Nodes (11): 3-Tier Memory (L0 Working/L1 Episodic/L2 Semantic), SQLite FTS5 Session Search, Hermes as MCP Server, MEMORY.md + USER.md File Memory, Memory Nudging (10-turn interval), MemoryProvider ABC (prefetch/sync_turn), Hermes Platform, Review Agent Fork (background daemon) (+3 more)

### Community 12 - "Anthropic API Gotchas & Security Rationale"
Cohesion: 0.18
Nodes (11): SSE Parser, 5-Layer Security (Gateway->Owner), 31 Blocked Env Vars, 13 Anthropic API Pitfalls, Why BLOCKED_PATTERNS is not a security boundary, BLOCKED_PATTERNS regex guard, Per-Append fsync Policy, input_json_delta Accumulation (+3 more)

### Community 13 - "GoClaw Infrastructure Patterns"
Cohesion: 0.18
Nodes (11): ProviderClient enum (Anthropic/Xai/OpenAI), AES-256-GCM Encryption, Agent Router (tenant cache), Dialect Store Abstraction, InputGuard (prompt injection regex), Knowledge Vault (wikilinks+hybrid search), Lane-Based Scheduler (main/subagent/cron), Multi-Tenant Context (WithTenantID) (+3 more)

### Community 14 - "Anthropic SSE Scanner (stub)"
Cohesion: 0.2
Nodes (6): newSSEScanner(), sseScanner struct, GoClaw sse_reader.go (reference pattern), anthropicProvider, sseEvent, sseScanner

### Community 15 - "Runtime Turn Loop (conversation.go)"
Cohesion: 0.22
Nodes (4): Options, Runtime, ToolCallRecord, TurnSummary

### Community 16 - "Session File Permissions (0700/0600)"
Cohesion: 1.0
Nodes (1): 0700 dir / 0600 file permissions

### Community 17 - "skeleton.md Design Doc"
Cohesion: 1.0
Nodes (1): analysis/integration/skeleton.md design doc

## Knowledge Gaps
- **98 isolated node(s):** `Role`, `BlockType`, `ContentBlock`, `Message`, `ToolSpec` (+93 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **Thin community `Session File Permissions (0700/0600)`** (1 nodes): `0700 dir / 0600 file permissions`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.
- **Thin community `skeleton.md Design Doc`** (1 nodes): `analysis/integration/skeleton.md design doc`
  Too small to be a meaningful cluster - may be noise or needs more connections extracted.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `claw-code Platform` connect `claw-code Runtime Analysis` to `AutoAgent + OpenClaw Channel Patterns`, `Multi-Agent Communication (task/event/advisor)`, `Haemil Project Structure`, `Goose MCP + AutoAgent Meta-Patterns`, `Task-Based Governance (Paperclip + SLA)`, `Anthropic API Gotchas & Security Rationale`, `GoClaw Infrastructure Patterns`?**
  _High betweenness centrality (0.127) - this node is a cross-community bridge._
- **Why does `Haemil Project` connect `claw-code Runtime Analysis` to `AutoAgent + OpenClaw Channel Patterns`, `Haemil Project Structure`, `Goose MCP + AutoAgent Meta-Patterns`, `Task-Based Governance (Paperclip + SLA)`, `Hermes Memory & Skill System`, `GoClaw Infrastructure Patterns`?**
  _High betweenness centrality (0.090) - this node is a cross-community bridge._
- **Why does `GoClaw Platform` connect `GoClaw Infrastructure Patterns` to `claw-code Runtime Analysis`, `AutoAgent + OpenClaw Channel Patterns`, `Multi-Agent Communication (task/event/advisor)`, `Goose MCP + AutoAgent Meta-Patterns`, `Hermes Memory & Skill System`, `Anthropic API Gotchas & Security Rationale`?**
  _High betweenness centrality (0.059) - this node is a cross-community bridge._
- **Are the 2 inferred relationships involving `claw-code Platform` (e.g. with `bash_validation.rs` and `conversation.rs (turn loop)`) actually correct?**
  _`claw-code Platform` has 2 INFERRED edges - model-reasoned connections that need verification._
- **Are the 4 inferred relationships involving `Run()` (e.g. with `TestMessageJSONRoundtrip()` and `TestProviderFactory()`) actually correct?**
  _`Run()` has 4 INFERRED edges - model-reasoned connections that need verification._
- **What connects `Role`, `BlockType`, `ContentBlock` to the rest of the system?**
  _98 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Phase 2a Stubs & Tests` be split into smaller, more focused modules?**
  _Cohesion score 0.09 - nodes in this community are weakly interconnected._