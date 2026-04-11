// Package provider contains concrete LLM backend implementations.
//
// Each provider implements runtime.Provider (declared in internal/runtime,
// following the "consumer defines the interface" Go idiom). Providers may
// import runtime but MUST NOT import the tools package.
package provider

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ShinKwangsub/haemil/internal/runtime"
)

// New is the single entry point callers use to construct a provider by name.
// cli.Run calls New during wiring. It is safe to call even without network
// access because the constructor performs no I/O.
//
// Currently supported names: "anthropic".
func New(name, apiKey, model string) (runtime.Provider, error) {
	switch name {
	case "anthropic":
		return &anthropicProvider{
			apiKey: apiKey,
			model:  model,
			http: &http.Client{
				Timeout: 60 * time.Second,
			},
		}, nil
	default:
		return nil, fmt.Errorf("provider: unknown %q (supported: anthropic)", name)
	}
}

// RedactAPIKey returns a safe-to-log representation of an API key. Use this
// anywhere a key might end up in logs, error messages, or stderr.
//
// Policy (see skeleton.md "알아둘 함정" for rationale):
//   - Empty string  → "<unset>"
//   - Short  (< 8)  → "<redacted>"
//   - Normal        → first 4 chars + "..." + last 4 chars
//
// Tests pin this behaviour in provider_test.go.
func RedactAPIKey(key string) string {
	if key == "" {
		return "<unset>"
	}
	if len(key) < 8 {
		return "<redacted>"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
