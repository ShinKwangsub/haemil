package runtime

import (
	"encoding/json"
	"testing"
)

// TestMessageJSONRoundtrip pins the JSON wire format for all three content
// block variants (text, tool_use, tool_result). Skeleton-stage regression
// test: exercised only the JSON struct tags, which are defined in message.go
// and do not depend on any panicking body.
func TestMessageJSONRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
	}{
		{
			name: "text_block",
			msg: Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: BlockTypeText, Text: "hello"},
				},
			},
		},
		{
			name: "assistant_text_plus_tool_use",
			msg: Message{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{Type: BlockTypeText, Text: "running ls for you"},
					{
						Type:  BlockTypeToolUse,
						ID:    "toolu_01abc",
						Name:  "bash",
						Input: json.RawMessage(`{"command":"ls"}`),
					},
				},
			},
		},
		{
			name: "user_tool_result_error",
			msg: Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{
						Type:      BlockTypeToolResult,
						ToolUseID: "toolu_01abc",
						Content:   "command not found",
						IsError:   true,
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var back Message
			if err := json.Unmarshal(raw, &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if back.Role != tc.msg.Role {
				t.Errorf("role: got %q, want %q", back.Role, tc.msg.Role)
			}
			if len(back.Content) != len(tc.msg.Content) {
				t.Fatalf("content len: got %d, want %d", len(back.Content), len(tc.msg.Content))
			}

			for i, gotBlock := range back.Content {
				wantBlock := tc.msg.Content[i]
				if gotBlock.Type != wantBlock.Type {
					t.Errorf("block[%d] type: got %q, want %q", i, gotBlock.Type, wantBlock.Type)
				}
				if gotBlock.Text != wantBlock.Text {
					t.Errorf("block[%d] text: got %q, want %q", i, gotBlock.Text, wantBlock.Text)
				}
				if gotBlock.ID != wantBlock.ID {
					t.Errorf("block[%d] id: got %q, want %q", i, gotBlock.ID, wantBlock.ID)
				}
				if gotBlock.Name != wantBlock.Name {
					t.Errorf("block[%d] name: got %q, want %q", i, gotBlock.Name, wantBlock.Name)
				}
				if string(gotBlock.Input) != string(wantBlock.Input) {
					t.Errorf("block[%d] input: got %q, want %q", i, gotBlock.Input, wantBlock.Input)
				}
				if gotBlock.ToolUseID != wantBlock.ToolUseID {
					t.Errorf("block[%d] tool_use_id: got %q, want %q", i, gotBlock.ToolUseID, wantBlock.ToolUseID)
				}
				if gotBlock.Content != wantBlock.Content {
					t.Errorf("block[%d] content: got %q, want %q", i, gotBlock.Content, wantBlock.Content)
				}
				if gotBlock.IsError != wantBlock.IsError {
					t.Errorf("block[%d] is_error: got %v, want %v", i, gotBlock.IsError, wantBlock.IsError)
				}
			}
		})
	}
}
