package mailbox

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"mime"
	"strings"
	"time"

	"google.golang.org/api/gmail/v1"
)

// Signature returns your default Gmail signature (HTML).
// Gmail only auto-adds signatures in its web UI — drafts made through the API
// don't get one, so we fetch it and add it ourselves.
func Signature(ctx context.Context, svc *gmail.Service) (string, error) {
	resp, err := withRetry(ctx, func() (*gmail.ListSendAsResponse, error) {
		return svc.Users.Settings.SendAs.List("me").Context(ctx).Do()
	})
	if err != nil {
		return "", fmt.Errorf("get signature: %w", err)
	}
	for _, s := range resp.SendAs {
		if s.IsDefault {
			return s.Signature, nil
		}
	}
	return "", nil
}

// Reply is everything needed to build a follow-up in the same thread.
type Reply struct {
	ThreadID      string
	To            string
	Subject       string // original subject; we add "Re: "
	Text          string // the follow-up itself (plain text, no signature)
	SignatureHTML string
	Original      Message // the email we're following up on (quoted below)
	OriginalBody  string
}

// CreateReplyDraft saves the follow-up as a Gmail draft. It does NOT send.
// Returns the draft ID.
func CreateReplyDraft(ctx context.Context, svc *gmail.Service, r Reply) (string, error) {
	raw := buildMIME(r)
	draft := &gmail.Draft{Message: &gmail.Message{
		// Raw = the whole email as text, base64url-encoded.
		Raw: base64.URLEncoding.EncodeToString([]byte(raw)),
		// ThreadId puts it in the same conversation in YOUR Gmail.
		ThreadId: r.ThreadID,
	}}
	d, err := withRetry(ctx, func() (*gmail.Draft, error) {
		return svc.Users.Drafts.Create("me", draft).Context(ctx).Do()
	})
	if err != nil {
		return "", fmt.Errorf("create draft: %w", err)
	}
	return d.Id, nil
}

// buildMIME writes a raw email (RFC 5322 + MIME). An email is just text:
// headers, a blank line, then the body. Ours has two versions of the body —
// plain text and HTML — and the reader's mail app picks one.
func buildMIME(r Reply) string {
	subject := r.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	quoteHeader := fmt.Sprintf("On %s %s wrote:",
		r.Original.Date.Local().Format("Mon, Jan 2, 2006 at 3:04 PM"), r.Original.From)

	// Plain-text version.
	var plain strings.Builder
	plain.WriteString(r.Text + "\n")
	if sig := htmlToText(r.SignatureHTML); sig != "" {
		plain.WriteString("\n" + sig + "\n")
	}
	plain.WriteString("\n" + quoteHeader + "\n")
	for _, line := range strings.Split(strings.TrimSpace(r.OriginalBody), "\n") {
		plain.WriteString("> " + line + "\n")
	}

	// HTML version (what Gmail shows). Escape our text so "<" etc. can't break it.
	var h strings.Builder
	for _, line := range strings.Split(r.Text, "\n") {
		if strings.TrimSpace(line) == "" {
			h.WriteString("<div><br></div>")
		} else {
			h.WriteString("<div>" + html.EscapeString(line) + "</div>")
		}
	}
	if r.SignatureHTML != "" {
		h.WriteString(`<br><div class="gmail_signature">` + r.SignatureHTML + "</div>")
	}
	h.WriteString(`<br><div class="gmail_quote"><div>` + html.EscapeString(quoteHeader) + `</div>` +
		`<blockquote class="gmail_quote" style="margin:0 0 0 .8ex;border-left:1px solid #ccc;padding-left:1ex">` +
		strings.ReplaceAll(html.EscapeString(strings.TrimSpace(r.OriginalBody)), "\n", "<br>") +
		`</blockquote></div>`)

	const boundary = "followup-agent-boundary"
	var b strings.Builder
	b.WriteString("To: " + r.To + "\r\n")
	// mime.QEncoding makes non-English characters safe inside a header.
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	if id := r.Original.MessageIDHeader; id != "" {
		// These two headers are what make it a REPLY on the recipient's side too.
		b.WriteString("In-Reply-To: " + id + "\r\n")
		b.WriteString("References: " + id + "\r\n")
	}
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString(`Content-Type: multipart/alternative; boundary="` + boundary + "\"\r\n\r\n")
	writePart(&b, boundary, "text/plain", plain.String())
	writePart(&b, boundary, "text/html", h.String())
	b.WriteString("--" + boundary + "--\r\n")
	return b.String()
}

func writePart(b *strings.Builder, boundary, contentType, body string) {
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: " + contentType + "; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	for len(enc) > 76 { // email lines must be short: wrap every 76 chars
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
}
