## 18:15 - Claw-code Harness Pattern Discovery
- Analyzed `bash_validation.rs`, `conversation.rs`, and `policy_engine.rs` from `claw-code/rust/crates/runtime/src/`
- Identified new command validation, loop control, and policy patterns

### 🛡️ Command Validation (from `bash_validation.rs`)
- **Semantic Classification:** Classify commands into `ReadOnly`, `Write`, `Destructive`, `Network`, `ProcessManagement`, `PackageManagement`, `SystemAdmin`, and `Unknown`.
- **Write-Prevention in Read-Only Mode:** Block `WRITE_COMMANDS` (cp, mv, rm, etc.) and `STATE_MODIFYING_COMMANDS` (apt, brew, docker, etc.) when in `ReadOnly` mode.
- **Destructive Pattern Warning:** Flag highly dangerous patterns (e.g., `rm -rf /`, `mkfs`, `dd if=`, fork bombs `:(){ :|:& };:`) with a `Warn` instead of a hard `Block`.
- **Path Safety:** Warn on directory traversal (`../`) and home directory references (`~/`, `$HOME`) that could escape the workspace.
- **Sed Safety:** Block in-place editing (`sed -i`) when in `ReadOnly` mode.

### 🔄 Conversation & Loop Control (from `conversation.rs`)
- **Max Iterations Limit:** Enforce a `max_iterations` cap on the model-tool loop to prevent infinite loops/token exhaustion.
- **Automatic Session Compaction:** Trigger compaction when cumulative input tokens exceed a specific threshold (e.g., 100k tokens) to maintain context efficiency.
- **Turn Summarization:** Track and record `TurnSummary` (iterations, tool usage, compaction events) for every turn.

### ⚖️ Policy Engine Rules (from `policy_engine.rs`)
- **Priority-Based Execution:** Rules are evaluated and executed in order of their `priority` field.
- **Chainable Actions:** Support complex automation by chaining multiple actions (`PolicyAction::Chain`).
- **Conditional Logic:** Support complex decision-making using `And` and `Or` combinators for multiple conditions.
- **Reconciliation Logic:** Implement specific patterns for "reconciling" completed work (e.g., `AlreadyMerged`, `Superseded`, `EmptyDiff`).

**Source:** `~/claw-code/rust/crates/runtime/src/`
