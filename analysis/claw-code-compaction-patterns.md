# Claw Code Session Auto-Compaction Patterns

## Overview
This document outlines the patterns and logic used for session compaction in the `claw-code` runtime, based on the analysis of `compact.rs`.

## Core Compaction Patterns

### 1. Threshold-Based Triggering (`should_compact`)
Compaction is not constant; it is triggered when specific budget constraints are met:
- **Message Count Constraint:** The number of compactable messages must exceed `config.preserve_recent_messages`.
- **Token Footprint Constraint:** The estimated token count of the compactable messages must be greater than or equal to `config.max_estimated_tokens`.
- **Logic:** Only the "middle" portion of the history is compacted, leaving a "tail" of recent messages untouched.

### 2. Summary-Centric History Reduction (`summarize_messages`)
Instead of simple truncation, the system replaces older messages with a structured `<summary>` block:
- **Metadata Extraction:** Captures counts of User, Assistant, and Tool messages.
- **Tool Awareness:** Identifies and lists unique tool names used in the compacted segment.
- **Context Preservation:**
    - **Recent Requests:** Summarizes the last few user requests.
    - **Pending Work:** Inferences "todo" or "next" items from text.
    - **Key Files:** Tracks which files were most frequently referenced.
    - **Current Work:** Attempts to identify the active task.
- **Timeline Reconstruction:** A condensed timeline of roles and truncated content is maintained to provide a "gist" of the history.

### 3. Synthetic System Message Generation (`get_compact_continuation_message`)
To ensure seamless continuity after compaction, the system injects a specialized System message:
- **Preamble:** Explicitly informs the LLM that it is continuing a previous conversation.
- **Instructional Guardrails:**
    - **Direct Resume:** Instructs the agent to "Resume directly — do not acknowledge the summary, do not recap...".
    - **Question Suppression:** Can be configured to prevent the agent from asking follow-up questions about the summary itself.
- **State Injection:** Combines the formatted summary with the preserved tail of recent messages and relevant metadata.

### 4. Recursive Compaction Support (`merge_compact_summaries`)
The system supports multiple rounds of compaction:
- When a new compaction occurs, it doesn't just overwrite the old summary.
- It extracts "highlights" and "timelines" from the *existing* summary and merges them with the *new* summary.
- This prevents "lossy" compression where too much context is discarded during repeated cycles.

## Conclusion
The compaction pattern is designed to maximize context window utility by replacing high-token history with a low-token, high-density semantic summary, while using explicit system instructions to prevent the LLM from "breaking character" or becoming confused by the transition.
