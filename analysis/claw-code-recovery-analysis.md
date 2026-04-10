# Claw-Code Recovery Analysis

## Overview
Analyzed `claw-code/rust/crates/runtime/src/recovery_recipes.rs` to understand the structured recovery mechanism used in the Claude Code reimplementation.

## Core Principles
- **One-Attempt Policy**: Automatic recovery is limited to a maximum of 1 attempt per scenario to prevent infinite loops.
- **Escalation**: If automatic recovery fails, the system must escalate (Alert Human, Log & Continue, or Abort) based on the predefined policy.
- **Structured Events**: Every recovery attempt emits a `RecoveryEvent` (Attempted, Succeeded, Failed, or Escalated) for observability.

## Defined Failure Scenarios & Recipes

| Failure Scenario | Recovery Steps | Max Attempts | Escalation Policy |
| :--- | :--- | :---: | :--- |
| **TrustPromptUnresolved** | `AcceptTrustPrompt` | 1 | `AlertHuman` |
| **PromptMisdelivery** | `RedirectPromptToAgent` | 1 | `AlertHuman` |
| **StaleBranch** | `RebaseBranch` $\rightarrow$ `CleanBuild` | 1 | `AlertHuman` |
| **CompileRedCrossCrate** | `CleanBuild` | 1 | `AlertHuman` |
| **McpHandshakeFailure** | `RetryMcpHandshake` (timeout: 5s) | 1 | `Abort` |
| **PartialPluginStartup** | `RestartPlugin` $\rightarrow$ `RetryMcpHandshake` | 1 | `LogAndContinue` |
| **ProviderFailure** | `RestartWorker` | 1 | `AlertHuman` |

## Implementation Details
- **RecoveryStep**: Granular actions like `RebaseBranch`, `CleanBuild`, `RestartWorker`, etc.
- **RecoveryResult**: 
    - `Recovered`: All steps succeeded.
    - `PartialRecovery`: Some steps succeeded, but some remain.
    - `EscalationRequired`: Max attempts reached or critical failure.
- **EscalationPolicy**:
    - `AlertHuman`: Notify the user immediately.
    - `LogAndContinue`: Log the error but keep the worker running.
    - `Abort`: Stop the operation entirely.

## Lessons for OpenClaw
- Implement a similar "Scenario-based" recovery in `AGENTS.md`.
- Enforce the "1-attempt before escalation" rule strictly.
- Use structured status reporting for recovery outcomes.
