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

// Options carries optional configuration for provider construction.
// Zero-value defaults are sensible; callers only set fields they care about.
type Options struct {
	// Endpoint overrides the default API URL. For Anthropic this replaces
	// https://api.anthropic.com/v1/messages. For OpenAI-compatible providers
	// it replaces https://api.openai.com/v1 (the /chat/completions suffix is
	// always appended). Use this to point at a local gateway (e.g. oMLX).
	Endpoint string

	// HTTPTimeout caps a single request. Default 120s.
	HTTPTimeout time.Duration
}

// New is the single entry point callers use to construct a provider by name.
//
// Supported names:
//   - "anthropic" — Anthropic Messages API (x-api-key auth)
//   - "openai"    — OpenAI-compatible chat/completions API (Bearer auth;
//                   apiKey may be empty for local servers that don't require one)
func New(name, apiKey, model string, opts ...Options) (runtime.Provider, error) {
	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.HTTPTimeout == 0 {
		opt.HTTPTimeout = 120 * time.Second
	}
	client := &http.Client{Timeout: opt.HTTPTimeout}

	switch name {
	case "anthropic":
		return &anthropicProvider{
			apiKey:      apiKey,
			model:       model,
			http:        client,
			endpointURL: opt.Endpoint,
		}, nil
	case "openai":
		return &openaiProvider{
			apiKey:      apiKey,
			model:       model,
			http:        client,
			endpointURL: opt.Endpoint,
		}, nil
	default:
		return nil, fmt.Errorf("provider: unknown %q (supported: anthropic, openai)", name)
	}
}

// RedactAPIKey returns a safe-to-log representation of an API key. Use this
// anywhere a key might end up in logs, error messages, or stderr.
//
// Policy:
//   - Empty string  → "<unset>"
//   - Short  (< 8)  → "<redacted>"
//   - Normal        → first 4 chars + "..." + last 4 chars
func RedactAPIKey(key string) string {
	if key == "" {
		return "<unset>"
	}
	if len(key) < 8 {
		return "<redacted>"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
