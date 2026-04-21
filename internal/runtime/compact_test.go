package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- helpers -----------------------------------------------------------

// mkText builds a text message for the given role.
func mkText(role Role, text string) Message {
	return Message{Role: role, Content: []ContentBlock{{Type: BlockTypeText, Text: text}}}
}

// mkToolUse builds an assistant message that invokes a tool.
func mkToolUse(name, id, input string) Message {
	return Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: BlockTypeToolUse, ID: id, Name: name, Input: json.RawMessage(input)},
		},
	}
}

// mkToolResult builds the paired user message carrying a tool_result.
func mkToolResult(id, out string, isErr bool) Message {
	return Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: BlockTypeToolResult, ToolUseID: id, Content: out, IsError: isErr},
		},
	}
}

// --- EstimateTokens ----------------------------------------------------

func TestEstimateMessageTokens(t *testing.T) {
	m := mkText(RoleUser, strings.Repeat("a", 100))
	got := EstimateMessageTokens(m)
	want := 100/4 + 1 // == 26
	if got != want {
		t.Errorf("text: got %d, want %d", got, want)
	}

	// Multi-block message.
	m = Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: BlockTypeText, Text: "abcd"},                                        // 4/4+1 = 2
			{Type: BlockTypeToolUse, Name: "bash", Input: json.RawMessage(`{"x":1}`)}, // (4+7)/4+1 = 3
		},
	}
	got = EstimateMessageTokens(m)
	want = 2 + 3
	if got != want {
		t.Errorf("multi-block: got %d, want %d", got, want)
	}
}

// --- ShouldCompact -----------------------------------------------------

func TestShouldCompactBelowThreshold(t *testing.T) {
	msgs := []Message{
		mkText(RoleUser, "hi"),
		mkText(RoleAssistant, "hello"),
	}
	cfg := CompactionConfig{PreserveRecent: 4, MaxEstimatedTokens: 10}
	if ShouldCompact(msgs, cfg) {
		t.Error("expected false for 2-message session")
	}
}

func TestShouldCompactAboveThreshold(t *testing.T) {
	blob := strings.Repeat("x", 2000) // ≈ 500 tokens
	msgs := []Message{
		mkText(RoleUser, blob),
		mkText(RoleAssistant, blob),
		mkText(RoleUser, blob),
		mkText(RoleAssistant, blob),
		mkText(RoleUser, blob),
		mkText(RoleAssistant, blob),
	}
	cfg := CompactionConfig{PreserveRecent: 4, MaxEstimatedTokens: 100}
	if !ShouldCompact(msgs, cfg) {
		t.Errorf("expected true: %d messages, ~%d tokens", len(msgs), EstimateSessionTokens(msgs))
	}
}

// --- Compact basic -----------------------------------------------------

func TestCompactPreservesRecent(t *testing.T) {
	blob := strings.Repeat("y", 1500)
	msgs := []Message{
		mkText(RoleUser, "oldest "+blob),
		mkText(RoleAssistant, "reply1 "+blob),
		mkText(RoleUser, "mid "+blob),
		mkText(RoleAssistant, "reply2 "+blob),
		mkText(RoleUser, "recent-1 "+blob),
		mkText(RoleAssistant, "recent-2 "+blob),
		mkText(RoleUser, "recent-3 "+blob),
		mkText(RoleAssistant, "recent-4 "+blob),
	}
	cfg := CompactionConfig{PreserveRecent: 4, MaxEstimatedTokens: 100}
	result := Compact(msgs, cfg)

	if result.RemovedCount != 4 {
		t.Errorf("removed: got %d, want 4", result.RemovedCount)
	}
	// Output: [continuation-system, recent-1, recent-2, recent-3, recent-4]
	if len(result.Messages) != 5 {
		t.Fatalf("messages: got %d, want 5", len(result.Messages))
	}
	if result.Messages[0].Role != RoleSystem {
		t.Errorf("msg[0] role: got %s, want system", result.Messages[0].Role)
	}
	if !strings.Contains(result.Messages[0].Content[0].Text, "This session is being continued") {
		t.Errorf("continuation missing preamble: %q", result.Messages[0].Content[0].Text)
	}
	if !strings.Contains(result.Summary, "<summary>") || !strings.Contains(result.Summary, "</summary>") {
		t.Errorf("summary missing tags: %q", result.Summary)
	}
	// Preserved tail should be the last 4 messages verbatim.
	for i, want := range msgs[4:] {
		got := result.Messages[i+1]
		if got.Role != want.Role || got.Content[0].Text != want.Content[0].Text {
			t.Errorf("preserved[%d] mismatch: got %+v, want %+v", i, got, want)
		}
	}
}

// --- Compact boundary safety ------------------------------------------

func TestCompactWalksBackOnToolResultBoundary(t *testing.T) {
	blob := strings.Repeat("z", 1500)
	msgs := []Message{
		mkText(RoleUser, "first "+blob),
		mkText(RoleAssistant, "second "+blob),
		mkText(RoleUser, "third "+blob),
		// raw_keep_from = len-4 = 4. msgs[4] will be the toolUse.
		// We expect walk-back to include the tool pair's both halves.
		mkText(RoleUser, "fourth "+blob),
		mkToolUse("bash", "t1", `{"command":"ls"}`),
		mkToolResult("t1", "a\nb\n", false),
		mkText(RoleAssistant, "done"),
		mkText(RoleUser, "thanks"),
	}
	cfg := CompactionConfig{PreserveRecent: 4, MaxEstimatedTokens: 100}
	result := Compact(msgs, cfg)

	// Preserved tail must contain the tool_use at its head, NOT start with tool_result.
	// So first preserved message after the continuation system is the assistant tool_use.
	if len(result.Messages) < 2 {
		t.Fatalf("compacted too aggressively: %d msgs", len(result.Messages))
	}
	first := result.Messages[1]
	if first.Role != RoleAssistant {
		t.Errorf("first preserved: got role %s, want assistant", first.Role)
	}
	hasToolUseBlock := false
	for _, b := range first.Content {
		if b.Type == BlockTypeToolUse {
			hasToolUseBlock = true
			break
		}
	}
	if !hasToolUseBlock {
		t.Error("first preserved should carry the tool_use block to keep the pair intact")
	}
	// Second preserved must be the tool_result.
	second := result.Messages[2]
	if second.Content[0].Type != BlockTypeToolResult {
		t.Errorf("second preserved block type: got %s, want tool_result", second.Content[0].Type)
	}
}

// --- Compact merges existing summary ----------------------------------

func TestCompactMergesExistingSummary(t *testing.T) {
	blob := strings.Repeat("q", 2000)
	// First compaction produces a session of shape [System, msg, msg, msg, msg, ...].
	prior := Message{
		Role: RoleSystem,
		Content: []ContentBlock{{
			Type: BlockTypeText,
			Text: compactContinuationPreamble +
				"Summary:\n<summary>\nConversation summary:\n- Key timeline:\n  - user: very old stuff\n  - assistant: earlier reply\n</summary>\n" +
				compactRecentNote + "\n" + compactDirectResume,
		}},
	}
	msgs := []Message{prior}
	// Add enough new messages to trigger re-compaction.
	for i := 0; i < 6; i++ {
		msgs = append(msgs, mkText(RoleUser, "u "+blob))
		msgs = append(msgs, mkText(RoleAssistant, "a "+blob))
	}
	cfg := CompactionConfig{PreserveRecent: 4, MaxEstimatedTokens: 100}
	result := Compact(msgs, cfg)

	if !strings.Contains(result.Summary, "Previously compacted context") {
		t.Errorf("merged summary should name the prior context; got:\n%s", result.Summary)
	}
	if !strings.Contains(result.Summary, "Newly compacted context") {
		t.Errorf("merged summary should name the new context; got:\n%s", result.Summary)
	}
}

// --- Summary shape -----------------------------------------------------

func TestSummarizeMessagesIncludesTimelineAndTools(t *testing.T) {
	msgs := []Message{
		mkText(RoleUser, "please list files"),
		mkToolUse("bash", "t1", `{"command":"ls"}`),
		mkToolResult("t1", "file1\nfile2\n", false),
		mkText(RoleAssistant, "I listed them"),
	}
	s := summarizeMessages(msgs)
	if !strings.Contains(s, "<summary>") || !strings.Contains(s, "</summary>") {
		t.Error("summary missing tags")
	}
	if !strings.Contains(s, "Tools mentioned: bash") {
		t.Errorf("summary missing tool names: %q", s)
	}
	if !strings.Contains(s, "Key timeline:") {
		t.Error("summary missing timeline header")
	}
	if !strings.Contains(s, "Recent user requests:") {
		t.Error("summary missing recent user requests")
	}
}

// --- Key file extraction ----------------------------------------------

func TestCollectKeyFiles(t *testing.T) {
	msgs := []Message{
		mkText(RoleUser, "edit internal/runtime/compact.go and cmd/haemil/main.go"),
		mkText(RoleAssistant, "opened the files"),
	}
	got := collectKeyFiles(msgs)
	found := map[string]bool{}
	for _, f := range got {
		found[f] = true
	}
	if !found["internal/runtime/compact.go"] {
		t.Errorf("expected compact.go in files, got %v", got)
	}
	if !found["cmd/haemil/main.go"] {
		t.Errorf("expected main.go in files, got %v", got)
	}
}

// --- Session compaction roundtrip -------------------------------------

func TestSessionApplyCompactionRoundtrip(t *testing.T) {
	dir := t.TempDir()
	sess, err := NewSession(dir)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	blob := strings.Repeat("w", 1500)
	original := []Message{
		mkText(RoleUser, "1 "+blob),
		mkText(RoleAssistant, "2 "+blob),
		mkText(RoleUser, "3 "+blob),
		mkText(RoleAssistant, "4 "+blob),
		mkText(RoleUser, "5 "+blob),
		mkText(RoleAssistant, "6 "+blob),
	}
	for _, m := range original {
		if m.Role == RoleUser {
			if err := sess.AppendUser(m); err != nil {
				t.Fatalf("append user: %v", err)
			}
		} else {
			if err := sess.AppendAssistant(m); err != nil {
				t.Fatalf("append assistant: %v", err)
			}
		}
	}

	cfg := CompactionConfig{PreserveRecent: 4, MaxEstimatedTokens: 100}
	result := Compact(sess.Messages(), cfg)
	if result.RemovedCount == 0 {
		t.Fatal("expected compaction to fire")
	}
	if err := sess.ApplyCompaction(result); err != nil {
		t.Fatalf("ApplyCompaction: %v", err)
	}
	afterApply := sess.Messages()
	if len(afterApply) != len(result.Messages) {
		t.Errorf("post-apply msgs: got %d, want %d", len(afterApply), len(result.Messages))
	}

	// Append a new user message, then reopen — the replay must see the
	// compacted state + the new message (NOT the original 6).
	id := sess.ID()
	if err := sess.AppendUser(mkText(RoleUser, "after compact")); err != nil {
		t.Fatalf("post-compact append: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := OpenSession(dir, id)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer reopened.Close()

	replayMsgs := reopened.Messages()
	wantLen := len(result.Messages) + 1 // continuation + preserved + new
	if len(replayMsgs) != wantLen {
		t.Errorf("replay msgs: got %d, want %d", len(replayMsgs), wantLen)
	}
	// Last message must be the post-compact user append.
	last := replayMsgs[len(replayMsgs)-1]
	if last.Role != RoleUser || last.Content[0].Text != "after compact" {
		t.Errorf("last msg: got %+v, want user 'after compact'", last)
	}
	// First must be the continuation system message.
	first := replayMsgs[0]
	if first.Role != RoleSystem {
		t.Errorf("first msg role: got %s, want system", first.Role)
	}
}

// --- Slash command: /compact through the REPL path --------------------
// Tested in internal/cli/repl_test.go instead since it needs bufio wiring.

// --- formatSummary cleanup --------------------------------------------

func TestFormatSummaryStripsAnalysisBlock(t *testing.T) {
	in := "<analysis>private thinking</analysis><summary>\npublic summary\n</summary>"
	got := formatSummary(in)
	if strings.Contains(got, "analysis") || strings.Contains(got, "private thinking") {
		t.Errorf("analysis block should be stripped, got: %q", got)
	}
	if !strings.Contains(got, "Summary:") {
		t.Errorf("summary header should be rewritten, got: %q", got)
	}
}
