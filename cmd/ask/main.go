// ask is a tiny tool to test the LLM connection:
//
//	go run ./cmd/ask "Say hi in 5 words"
//
// A Go project can have several programs: one folder per program under cmd/.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/llm"
)

func main() {
	// Load .env into environment variables (Go doesn't do this by itself).
	// Ignore the error: in the cloud there's no .env, variables are set directly.
	_ = godotenv.Load()

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY is not set (add it to .env)")
	}
	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		model = "gemini-3.5-flash-lite"
	}

	prompt := strings.Join(os.Args[1:], " ") // everything typed after the command
	if prompt == "" {
		prompt = "Say hi in 5 words."
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Note the type: we hold it as the INTERFACE llm.Client, not *llm.Gemini.
	// Swapping providers later = changing only this one line.
	var client llm.Client = llm.NewGemini(apiKey, model)

	start := time.Now()
	answer, err := client.Generate(ctx, prompt)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(answer)
	fmt.Fprintf(os.Stderr, "\n(%s, %s)\n", model, time.Since(start).Round(time.Millisecond))
}
