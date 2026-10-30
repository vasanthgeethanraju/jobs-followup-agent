// Package llm lets the agent talk to any large language model.
package llm

import "context"

// Client is anything that can answer a prompt.
// Gemini, Claude, or a fake for tests: the agent doesn't care which.
type Client interface {
	// Generate sends a prompt and returns the model's text answer.
	// Inside an interface you only list method "signatures": name, inputs,
	// outputs — no body. Any type with these exact methods fits automatically.
	Generate(ctx context.Context, prompt string) (string, error)

	// GenerateJSON asks for an answer shaped like `schema` (structured output)
	// and decodes it into `out` (a pointer to a struct).
	// Free text is hard for code to use; JSON with known fields is easy.
	GenerateJSON(ctx context.Context, prompt string, schema Schema, out any) error
}

// Schema describes the JSON shape we want back: field name -> description.
// All fields are strings and required — enough for this project, and simple.
type Schema map[string]string
