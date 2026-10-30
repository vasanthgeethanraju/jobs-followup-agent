package mailbox

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"regexp"
	"strings"

	"google.golang.org/api/gmail/v1"
)

// GetBody returns the plain-text body of one message (your original application),
// so the LLM can see what you wrote and to whom.
func GetBody(ctx context.Context, svc *gmail.Service, messageID string) (string, error) {
	m, err := withRetry(ctx, func() (*gmail.Message, error) {
		return svc.Users.Messages.Get("me", messageID).Format("full").Context(ctx).Do()
	})
	if err != nil {
		return "", fmt.Errorf("get body %s: %w", messageID, err)
	}

	// Emails are a TREE of parts (MIME): e.g. one plain-text version + one HTML
	// version + attachments. Prefer plain text; fall back to HTML with tags removed.
	if text := findPart(m.Payload, "text/plain"); text != "" {
		return text, nil
	}
	return htmlToText(findPart(m.Payload, "text/html")), nil
}

// findPart walks the tree (recursion: the function calls itself on child parts).
func findPart(p *gmail.MessagePart, mimeType string) string {
	if p == nil {
		return ""
	}
	if p.MimeType == mimeType && p.Body != nil && p.Body.Data != "" {
		return decodeBase64URL(p.Body.Data)
	}
	for _, child := range p.Parts {
		if text := findPart(child, mimeType); text != "" {
			return text
		}
	}
	return ""
}

// Gmail sends bodies as "base64url" (base64 that's safe inside URLs).
func decodeBase64URL(s string) string {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return ""
	}
	return string(b)
}

var (
	tagRE   = regexp.MustCompile(`(?s)<[^>]*>`)
	blankRE = regexp.MustCompile(`\n{3,}`)
)

// htmlToText is a rough HTML → text: good enough for an LLM to read.
func htmlToText(s string) string {
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "</div>", "\n", "</p>", "\n\n").Replace(s)
	s = html.UnescapeString(tagRE.ReplaceAllString(s, ""))
	return strings.TrimSpace(blankRE.ReplaceAllString(s, "\n\n"))
}
