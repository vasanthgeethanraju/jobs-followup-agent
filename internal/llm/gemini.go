package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Gemini talks to Google's Gemini API over plain HTTP.
// An "API call" is just: a URL + a method (POST) + headers + a JSON body,
// and a JSON response back. SDKs only wrap this for you.
type Gemini struct {
	apiKey string
	model  string
	http   *http.Client
}

// NewGemini builds a client. Model example: "gemini-3.5-flash-lite".
func NewGemini(apiKey, model string) *Gemini {
	return &Gemini{
		apiKey: apiKey,
		model:  model,
		// ALWAYS set a timeout on outbound HTTP. Go's default is "wait forever",
		// which is exactly how a stuck request hangs your whole program.
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// Compile-time check: *Gemini fits the Client "port".
var _ Client = (*Gemini)(nil)

// --- The JSON shapes Gemini expects/returns. ---
// The `json:"..."` tags tell Go's encoder what each field is called in JSON.

type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Generate sends one prompt and returns the text answer.
func (g *Gemini) Generate(ctx context.Context, prompt string) (string, error) {
	// 1. Build the JSON body: {"contents":[{"parts":[{"text":"..."}]}]}
	body, err := json.Marshal(geminiRequest{
		Contents: []geminiContent{{Parts: []geminiPart{{Text: prompt}}}},
	})
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", g.model)

	// 2. Send it, retrying when Gemini is busy (429 = rate limit, 503 = overloaded).
	var resp geminiResponse
	wait := 2 * time.Second
	for attempt := 1; ; attempt++ {
		status, err := g.post(ctx, url, body, &resp)
		retryable := status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
		if err == nil || !retryable || attempt == 4 {
			if err != nil {
				return "", err
			}
			break
		}
		select {
		case <-time.After(wait):
			wait *= 2
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	// 3. Pull the text out of the response.
	if len(resp.Candidates) == 0 {
		return "", fmt.Errorf("gemini returned no answer (possibly blocked by safety filters)")
	}
	var sb strings.Builder // efficient way to join strings
	for _, p := range resp.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	return strings.TrimSpace(sb.String()), nil
}

// post sends one HTTP request and decodes the JSON reply into out.
// It returns the HTTP status code so the caller can decide whether to retry.
func (g *Gemini) post(ctx context.Context, url string, body []byte, out *geminiResponse) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	// Headers: what we're sending, and who we are. The key goes in a header,
	// not the URL, so it doesn't end up in logs.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)

	res, err := g.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("call gemini: %w", err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, err
	}
	*out = geminiResponse{} // reset in case this is a retry
	if err := json.Unmarshal(raw, out); err != nil {
		return res.StatusCode, fmt.Errorf("gemini: bad JSON (HTTP %d): %.200s", res.StatusCode, raw)
	}
	if res.StatusCode != http.StatusOK {
		msg := "unknown error"
		if out.Error != nil {
			msg = out.Error.Message
		}
		return res.StatusCode, fmt.Errorf("gemini HTTP %d: %s", res.StatusCode, msg)
	}
	return res.StatusCode, nil
}
