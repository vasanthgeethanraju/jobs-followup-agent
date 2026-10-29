package llm

import "context"

// Fake is a pretend LLM for tests. It always replies with Reply.
type Fake struct {
	Reply string
}

// Generate is a METHOD on Fake: "(f Fake)" before the name is the receiver,
// like "this"/"self" in other languages. Same signature as the interface,
// so Fake fits llm.Client. The "_" names mean "I accept this but don't use it".
func (f Fake) Generate(_ context.Context, _ string) (string, error) {
	return f.Reply, nil // nil = "no error"
}

// This line is a compile-time check: "Fake must fit the Client port".
// If your method is wrong, `go build` fails here with a helpful message.
var _ Client = Fake{}
