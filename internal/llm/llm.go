// Package llm lets the agent talk to any large language model.
package llm

import "context"

// Client is anything that can answer a prompt.
// Gemini, Claude, or a fake for tests: the agent doesn't care which.
type Client interface {
	// Generate sends a prompt and returns the model's text answer.
	// Inside an interface you only list method "signatures": name, inputs,
	// outputs — no body. Any type with this exact method fits automatically.
	Generate(ctx context.Context, prompt string) (string, error)
}
