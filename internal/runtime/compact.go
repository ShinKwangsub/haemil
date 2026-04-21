package runtime

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CompactionConfig controls when and how a session is compacted.
//
// Mirrors claw-code's CompactionConfig: preserve the last N messages
// verbatim, trigger compaction once estimated tokens on the compactable
// range hit MaxEstimatedTokens.
type CompactionConfig struct {
	// PreserveRecent: messages at the tail to keep verbatim. Default 4.
	PreserveRecent int
	// MaxEstimatedTokens: threshold on the compactable range (i.e. messages
	// that would be summarised if Compact were called now). Default 10000.
	MaxEstimatedTokens int
}

// DefaultCompactionConfig returns the claw-code defaults.
func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{
		PreserveRecent:     4,
		MaxEstimatedTokens: 10000,
	}
}

// CompactionResult bundles the rewritten message list, the generated summary
// text, and the count of removed messages — for diagnostics.
type CompactionResult struct {
	Messages     []Message
	Summary      string
	RemovedCount int
}

// EstimateMessageTokens approximates the token footprint of a single
// message with the claw-code heuristic (`len / 4 + 1` per block).
// Intentionally coarse — we only need a threshold, not an exact count.
func EstimateMessageTokens(m Message) int {
	n := 0
	for _, b := range m.Content {
		switch b.Type {
		case BlockTypeText:
			n += len(b.Text)/4 + 1
		case BlockTypeToolUse:
			n += (len(b.Name)+len(b.Input))/4 + 1
		case BlockTypeToolResult:
			n += (len(b.ToolUseID)+len(b.Content))/4 + 1
		}
	}
	return n
}

// EstimateSessionTokens sums EstimateMessageTokens across msgs.
func EstimateSessionTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateMessageTokens(m)
	}
	return total
}

// ShouldCompact reports whether msgs exceed cfg's budget on the compactable
// range. The "compactable" range excludes any leading system message that is
// itself a prior compaction summary (so repeated compaction doesn't pile up
// summaries forever).
func ShouldCompact(msgs []Message, cfg CompactionConfig) bool {
	if cfg.PreserveRecent <= 0 {
		cfg.PreserveRecent = 4
	}
	if cfg.MaxEstimatedTokens <= 0 {
		cfg.MaxEstimatedTokens = 10000
	}
	start := summaryPrefixLen(msgs)
	if len(msgs)-start <= cfg.PreserveRecent {
		return false
	}
	return EstimateSessionTokens(msgs[start:]) >= cfg.MaxEstimatedTokens
}

// Compact produces a shorter message list: [continuation-system-message,
// ...preserved-tail]. If msgs already starts with a prior compaction
// summary, the new summary merges previous highlights with the current one.
// If msgs is below threshold, the input is returned unchanged.
//
// Tool pair safety: if the would-be first preserved message starts with a
// tool_result block, we walk the boundary one step back so the paired
// assistant tool_use turn is preserved too. This matches claw-code's fix
// for OpenAI-compat adapters that otherwise reject orphaned tool messages.
func Compact(msgs []Message, cfg CompactionConfig) CompactionResult {
	if cfg.PreserveRecent <= 0 {
		cfg.PreserveRecent = 4
	}
	if cfg.MaxEstimatedTokens <= 0 {
		cfg.MaxEstimatedTokens = 10000
	}

	if !ShouldCompact(msgs, cfg) {
		return CompactionResult{Messages: append([]Message(nil), msgs...)}
	}

	prefixLen := summaryPrefixLen(msgs)
	rawKeepFrom := len(msgs) - cfg.PreserveRecent
	keepFrom := adjustKeepFromForToolPairs(msgs, rawKeepFrom, prefixLen)

	removed := msgs[prefixLen:keepFrom]
	preserved := append([]Message(nil), msgs[keepFrom:]...)

	newSummary := summarizeMessages(removed)
	if prefixLen > 0 {
		if existing, ok := extractExistingSummary(msgs[0]); ok {
			newSummary = mergeSummaries(existing, newSummary)
		}
	}

	continuation := buildContinuation(newSummary, len(preserved) > 0)
	out := make([]Message, 0, 1+len(preserved))
	out = append(out, Message{
		Role:    RoleSystem,
		Content: []ContentBlock{{Type: BlockTypeText, Text: continuation}},
	})
	out = append(out, preserved...)

	return CompactionResult{
		Messages:     out,
		Summary:      newSummary,
		RemovedCount: len(removed),
	}
}

// ---------------------------------------------------------------------------
// Helpers: prefix detection, boundary adjustment, summary build, merge.
// ---------------------------------------------------------------------------

const (
	compactContinuationPreamble = "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation.\n\n"
	compactRecentNote           = "Recent messages are preserved verbatim."
	compactDirectResume         = "Continue the conversation from where it left off without asking the user any further questions. Resume directly — do not acknowledge the summary, do not recap what was happening, and do not preface with continuation text."
	compactTagOpen              = "<summary>"
	compactTagClose             = "</summary>"
)

// summaryPrefixLen returns 1 if msgs[0] is a prior compaction summary (a
// System message whose Text starts with the preamble), 0 otherwise.
func summaryPrefixLen(msgs []Message) int {
	if _, ok := existingSummaryText(msgs); ok {
		return 1
	}
	return 0
}

func existingSummaryText(msgs []Message) (string, bool) {
	if len(msgs) == 0 {
		return "", false
	}
	m := msgs[0]
	if m.Role != RoleSystem || len(m.Content) == 0 {
		return "", false
	}
	if m.Content[0].Type != BlockTypeText {
		return "", false
	}
	txt := m.Content[0].Text
	if !strings.HasPrefix(txt, compactContinuationPreamble) {
		return "", false
	}
	return txt, true
}

func extractExistingSummary(m Message) (string, bool) {
	if m.Role != RoleSystem || len(m.Content) == 0 {
		return "", false
	}
	if m.Content[0].Type != BlockTypeText {
		return "", false
	}
	txt := m.Content[0].Text
	openIdx := strings.Index(txt, compactTagOpen)
	closeIdx := strings.Index(txt, compactTagClose)
	if openIdx < 0 || closeIdx < 0 || closeIdx <= openIdx {
		return "", false
	}
	return txt[openIdx : closeIdx+len(compactTagClose)], true
}

// adjustKeepFromForToolPairs walks the boundary one step back if the first
// preserved message starts with a tool_result whose matching tool_use lives
// in the message immediately before. See claw-code's commentary in
// compact.rs around line 115 — required for OpenAI-compat correctness.
func adjustKeepFromForToolPairs(msgs []Message, raw, prefixLen int) int {
	k := raw
	for {
		if k <= prefixLen || k <= 0 {
			return k
		}
		first := msgs[k]
		if !startsWithToolResult(first) {
			return k
		}
		preceding := msgs[k-1]
		if hasToolUse(preceding) {
			// Pair is intact — include the assistant turn on the preserved side.
			return k - 1
		}
		// Pair already orphaned; walk back and retry.
		k--
	}
}

func startsWithToolResult(m Message) bool {
	return len(m.Content) > 0 && m.Content[0].Type == BlockTypeToolResult
}

func hasToolUse(m Message) bool {
	for _, b := range m.Content {
		if b.Type == BlockTypeToolUse {
			return true
		}
	}
	return false
}

// summarizeMessages builds the <summary>...</summary> block from removed
// messages. Template-based — no LLM call (matches claw-code).
func summarizeMessages(msgs []Message) string {
	user, assistant, tool := 0, 0, 0
	for _, m := range msgs {
		switch m.Role {
		case RoleUser:
			if startsWithToolResult(m) {
				tool++
			} else {
				user++
			}
		case RoleAssistant:
			assistant++
		}
	}

	toolNames := uniqueToolNames(msgs)
	recentUser := collectRecentTextByRole(msgs, RoleUser, 3, true /*excludeToolResultOnly*/)
	pending := inferPending(msgs)
	keyFiles := collectKeyFiles(msgs)
	current := inferCurrent(msgs)

	var b strings.Builder
	b.WriteString(compactTagOpen)
	b.WriteByte('\n')
	b.WriteString("Conversation summary:\n")
	b.WriteString("- Scope: ")
	b.WriteString(strconv.Itoa(len(msgs)))
	b.WriteString(" earlier messages compacted (user=")
	b.WriteString(strconv.Itoa(user))
	b.WriteString(", assistant=")
	b.WriteString(strconv.Itoa(assistant))
	b.WriteString(", tool=")
	b.WriteString(strconv.Itoa(tool))
	b.WriteString(").\n")

	if len(toolNames) > 0 {
		b.WriteString("- Tools mentioned: ")
		b.WriteString(strings.Join(toolNames, ", "))
		b.WriteString(".\n")
	}

	if len(recentUser) > 0 {
		b.WriteString("- Recent user requests:\n")
		for _, r := range recentUser {
			b.WriteString("  - ")
			b.WriteString(r)
			b.WriteByte('\n')
		}
	}

	if len(pending) > 0 {
		b.WriteString("- Pending work:\n")
		for _, p := range pending {
			b.WriteString("  - ")
			b.WriteString(p)
			b.WriteByte('\n')
		}
	}

	if len(keyFiles) > 0 {
		b.WriteString("- Key files referenced: ")
		b.WriteString(strings.Join(keyFiles, ", "))
		b.WriteString(".\n")
	}

	if current != "" {
		b.WriteString("- Current work: ")
		b.WriteString(current)
		b.WriteByte('\n')
	}

	b.WriteString("- Key timeline:\n")
	for _, m := range msgs {
		b.WriteString("  - ")
		b.WriteString(roleLabel(m.Role))
		b.WriteString(": ")
		b.WriteString(summarizeBlocks(m.Content))
		b.WriteByte('\n')
	}
	b.WriteString(compactTagClose)
	return b.String()
}

// mergeSummaries: if a prior compaction has already happened, preserve
// previous highlights (lines nested under the previous summary's "Key
// timeline:" / "Newly compacted context:") and append new highlights.
func mergeSummaries(existing, current string) string {
	prev := extractSummaryLines(existing, "Key timeline:")
	if len(prev) == 0 {
		prev = extractSummaryLines(existing, "Newly compacted context:")
	}
	newLines := extractSummaryLines(current, "Key timeline:")

	var b strings.Builder
	b.WriteString(compactTagOpen)
	b.WriteByte('\n')
	b.WriteString("Conversation summary:\n")

	if len(prev) > 0 {
		b.WriteString("- Previously compacted context:\n")
		for _, l := range prev {
			b.WriteString("  ")
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}

	if len(newLines) > 0 {
		b.WriteString("- Newly compacted context:\n")
		for _, l := range newLines {
			b.WriteString("  ")
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}

	b.WriteString(compactTagClose)
	return b.String()
}

// buildContinuation wraps a summary as the replacement system message.
func buildContinuation(summary string, preservedTail bool) string {
	var b strings.Builder
	b.WriteString(compactContinuationPreamble)
	b.WriteString(formatSummary(summary))
	if preservedTail {
		b.WriteString("\n\n")
		b.WriteString(compactRecentNote)
	}
	b.WriteByte('\n')
	b.WriteString(compactDirectResume)
	return b.String()
}

// formatSummary converts the raw <summary>...</summary> block into a
// user-facing "Summary:\n..." form. Strips any <analysis> block that a
// future LLM path might emit.
func formatSummary(summary string) string {
	stripped := stripTagBlock(summary, "analysis")
	if inner, ok := extractTagBlock(stripped, "summary"); ok {
		wrapped := compactTagOpen + inner + compactTagClose
		stripped = strings.Replace(stripped, wrapped, "Summary:\n"+strings.TrimSpace(inner), 1)
	}
	return strings.TrimSpace(collapseBlankLines(stripped))
}

// ---------------------------------------------------------------------------
// Heuristic helpers (mirrors of claw-code's inference functions).
// ---------------------------------------------------------------------------

func uniqueToolNames(msgs []Message) []string {
	seen := map[string]struct{}{}
	for _, m := range msgs {
		for _, b := range m.Content {
			var name string
			switch b.Type {
			case BlockTypeToolUse:
				name = b.Name
			case BlockTypeToolResult:
				// Block.Name isn't populated on tool_result. Skip.
				continue
			}
			if name != "" {
				seen[name] = struct{}{}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func collectRecentTextByRole(msgs []Message, role Role, limit int, excludeToolResultOnly bool) []string {
	var out []string
	for i := len(msgs) - 1; i >= 0 && len(out) < limit; i-- {
		m := msgs[i]
		if m.Role != role {
			continue
		}
		if excludeToolResultOnly && startsWithToolResult(m) {
			continue
		}
		if txt, ok := firstTextBlock(m); ok {
			out = append(out, truncateSummary(txt, 160))
		}
	}
	// Reverse to chronological.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func inferPending(msgs []Message) []string {
	var out []string
	for i := len(msgs) - 1; i >= 0 && len(out) < 3; i-- {
		txt, ok := firstTextBlock(msgs[i])
		if !ok {
			continue
		}
		low := strings.ToLower(txt)
		if strings.Contains(low, "todo") ||
			strings.Contains(low, "next") ||
			strings.Contains(low, "pending") ||
			strings.Contains(low, "follow up") ||
			strings.Contains(low, "remaining") {
			out = append(out, truncateSummary(txt, 160))
		}
	}
	// Reverse to chronological.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func collectKeyFiles(msgs []Message) []string {
	seen := map[string]struct{}{}
	for _, m := range msgs {
		for _, b := range m.Content {
			var s string
			switch b.Type {
			case BlockTypeText:
				s = b.Text
			case BlockTypeToolUse:
				s = string(b.Input)
			case BlockTypeToolResult:
				s = b.Content
			}
			for _, cand := range extractFileCandidates(s) {
				seen[cand] = struct{}{}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func inferCurrent(msgs []Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if txt, ok := firstTextBlock(msgs[i]); ok {
			return truncateSummary(txt, 200)
		}
	}
	return ""
}

func firstTextBlock(m Message) (string, bool) {
	for _, b := range m.Content {
		if b.Type == BlockTypeText && strings.TrimSpace(b.Text) != "" {
			return b.Text, true
		}
	}
	return "", false
}

func summarizeBlocks(blocks []ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		var raw string
		switch b.Type {
		case BlockTypeText:
			raw = b.Text
		case BlockTypeToolUse:
			raw = "tool_use " + b.Name + "(" + string(b.Input) + ")"
		case BlockTypeToolResult:
			prefix := "tool_result: "
			if b.IsError {
				prefix = "tool_result error: "
			}
			raw = prefix + b.Content
		}
		parts = append(parts, truncateSummary(raw, 160))
	}
	return strings.Join(parts, " | ")
}

func extractFileCandidates(s string) []string {
	var out []string
	for _, tok := range strings.Fields(s) {
		cand := strings.Trim(tok, ",.:;()\"'`")
		if !strings.Contains(cand, "/") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(cand))
		if ext == "" {
			continue
		}
		switch ext {
		case ".rs", ".ts", ".tsx", ".js", ".json", ".md", ".go", ".py", ".yaml", ".yml", ".toml":
			out = append(out, cand)
		}
	}
	return out
}

func truncateSummary(s string, maxChars int) string {
	r := []rune(s)
	if len(r) <= maxChars {
		return s
	}
	return string(r[:maxChars]) + "…"
}

func roleLabel(r Role) string {
	switch r {
	case RoleUser:
		return "user"
	case RoleAssistant:
		return "assistant"
	case RoleSystem:
		return "system"
	default:
		return string(r)
	}
}

// extractTagBlock returns the inner text between <tag>...</tag> if present.
func extractTagBlock(content, tag string) (string, bool) {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	s := strings.Index(content, openTag)
	if s < 0 {
		return "", false
	}
	s += len(openTag)
	e := strings.Index(content[s:], closeTag)
	if e < 0 {
		return "", false
	}
	return content[s : s+e], true
}

// stripTagBlock removes <tag>...</tag> (and its contents) from content.
func stripTagBlock(content, tag string) string {
	openTag := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	s := strings.Index(content, openTag)
	e := strings.Index(content, closeTag)
	if s < 0 || e < 0 {
		return content
	}
	return content[:s] + content[e+len(closeTag):]
}

// collapseBlankLines drops consecutive blank lines.
func collapseBlankLines(s string) string {
	var b strings.Builder
	lastBlank := false
	for _, line := range strings.Split(s, "\n") {
		isBlank := strings.TrimSpace(line) == ""
		if isBlank && lastBlank {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		lastBlank = isBlank
	}
	return b.String()
}

// extractSummaryLines pulls bullet lines under `section:` inside a summary.
// Returns the child bullet lines (`- ...` and `  - ...`), not the header.
func extractSummaryLines(summary, section string) []string {
	lines := strings.Split(summary, "\n")
	var out []string
	inSection := false
	for _, l := range lines {
		trim := strings.TrimRight(l, " \t")
		if strings.Contains(trim, "- "+section) {
			inSection = true
			continue
		}
		if inSection {
			// Stop at next top-level bullet or the closing tag.
			if strings.HasPrefix(trim, "- ") && !strings.HasPrefix(trim, "  ") {
				inSection = false
				continue
			}
			if strings.Contains(trim, compactTagClose) {
				break
			}
			ts := strings.TrimSpace(trim)
			if ts != "" {
				out = append(out, ts)
			}
		}
	}
	return out
}

