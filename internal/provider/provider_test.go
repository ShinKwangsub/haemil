package provider

import (
	"strings"
	"testing"
)

// TestProviderFactory pins the factory's dispatch behaviour: "anthropic"
// returns a working runtime.Provider (non-nil, Name()=="anthropic"), every
// other name returns a descriptive error.
func TestProviderFactory(t *testing.T) {
	t.Run("anthropic_ok", func(t *testing.T) {
		p, err := New("anthropic", "sk-ant-fake-key-123456", "claude-sonnet-4-6")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p == nil {
			t.Fatal("nil provider")
		}
		if p.Name() != "anthropic" {
			t.Errorf("name: got %q, want %q", p.Name(), "anthropic")
		}
	})

	t.Run("openai_ok", func(t *testing.T) {
		p, err := New("openai", "sk-test", "gpt-4")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p == nil {
			t.Fatal("nil provider")
		}
		if p.Name() != "openai" {
			t.Errorf("name: got %q, want %q", p.Name(), "openai")
		}
	})

	t.Run("openai_no_key_allowed", func(t *testing.T) {
		// Local servers (oMLX, Ollama, etc.) don't require API keys.
		// Factory must accept empty keys for "openai" provider.
		p, err := New("openai", "", "gemma-4", Options{Endpoint: "http://127.0.0.1:8080"})
		if err != nil {
			t.Fatalf("unexpected error for empty key: %v", err)
		}
		if p == nil {
			t.Fatal("nil provider")
		}
	})

	t.Run("unknown_name", func(t *testing.T) {
		p, err := New("cohere", "key", "command")
		if err == nil {
			t.Fatal("expected error for unknown provider name")
		}
		if p != nil {
			t.Error("expected nil provider on error")
		}
		if !strings.Contains(err.Error(), "unknown") {
			t.Errorf("error message missing 'unknown': %q", err.Error())
		}
	})

	t.Run("empty_name", func(t *testing.T) {
		_, err := New("", "key", "model")
		if err == nil {
			t.Fatal("expected error for empty name")
		}
	})
}

// TestRedactAPIKey pins the redaction policy. This is a security guarantee
// we never want to regress — any code path that logs a key should go
// through RedactAPIKey, and RedactAPIKey must never emit the raw key.
func TestRedactAPIKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "<unset>"},
		{"tiny", "abc", "<redacted>"},
		{"short_7", "1234567", "<redacted>"},
		{"exact_8", "12345678", "1234...5678"},
		{"normal_anthropic", "sk-ant-api03-abcdefghijklmnop", "sk-a...mnop"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactAPIKey(tc.in)
			if got != tc.want {
				t.Errorf("RedactAPIKey(%q): got %q, want %q", tc.in, got, tc.want)
			}

			// Hard safety: never leak the raw key when the input is longer
			// than a handful of chars. Short inputs are already mapped to
			// "<redacted>" / "<unset>" above.
			if len(tc.in) >= 16 && strings.Contains(got, tc.in) {
				t.Errorf("RedactAPIKey leaked raw key in %q", got)
			}
		})
	}
}
