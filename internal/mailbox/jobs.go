package mailbox

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/api/gmail/v1"
)

// Message is our own simplified view of one Gmail message —
// we only keep the fields the agent cares about.
type Message struct {
	ID       string
	From     string
	To       string
	Subject  string
	Date     time.Time
	LabelIDs []string
	// The email's worldwide-unique ID header, e.g. <CAB...@mail.gmail.com>.
	// Replies point at it (In-Reply-To) so mail apps show them as one thread.
	MessageIDHeader string
}

// IsDraft: unsent drafts also live inside threads, with the "DRAFT" label.
func (m Message) IsDraft() bool {
	return slices.Contains(m.LabelIDs, "DRAFT")
}

// SentByMe: Gmail puts the built-in "SENT" label on everything you send.
// (A function attached to a type like this is called a *method*.)
func (m Message) SentByMe() bool {
	return slices.Contains(m.LabelIDs, "SENT")
}

// Thread is a whole conversation: your application plus any replies.
type Thread struct {
	ID       string
	Messages []Message // oldest first
}

// FindLabelIDs turns label names like "!Jobs" into Gmail's internal label IDs,
// because the API filters by ID, not by name. Returns a map: name -> ID.
func FindLabelIDs(ctx context.Context, svc *gmail.Service, names ...string) (map[string]string, error) {
	resp, err := withRetry(ctx, func() (*gmail.ListLabelsResponse, error) {
		return svc.Users.Labels.List("me").Context(ctx).Do()
	})
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}

	ids := make(map[string]string) // a map is Go's dictionary / hash map
	for _, l := range resp.Labels {
		if slices.Contains(names, l.Name) {
			ids[l.Name] = l.Id
		}
	}
	for _, n := range names {
		if _, ok := ids[n]; !ok { // "comma ok": ok is false if the key is missing
			return nil, fmt.Errorf("label %q not found in Gmail", n)
		}
	}
	return ids, nil
}

// ListThreadIDs returns the IDs of conversations that have labelID and match
// a Gmail search query (same syntax as the Gmail search box, e.g. "in:sent").
// Listing only returns IDs, so it's cheap: one request per 500 results.
func ListThreadIDs(ctx context.Context, svc *gmail.Service, labelID, query string) ([]string, error) {
	var ids []string
	err := svc.Users.Messages.List("me").LabelIds(labelID).Q(query).MaxResults(500).
		Pages(ctx, func(page *gmail.ListMessagesResponse) error {
			for _, m := range page.Messages {
				if !slices.Contains(ids, m.ThreadId) { // dedupe: many messages, one thread
					ids = append(ids, m.ThreadId)
				}
			}
			return nil
		})
	return ids, err
}

// AnyMatch reports whether at least one email has labelID and matches query.
// We use it to ask "does this company have a Rejected email anywhere?"
func AnyMatch(ctx context.Context, svc *gmail.Service, labelID, query string) (bool, error) {
	resp, err := withRetry(ctx, func() (*gmail.ListMessagesResponse, error) {
		return svc.Users.Messages.List("me").LabelIds(labelID).Q(query).MaxResults(1).Context(ctx).Do()
	})
	if err != nil {
		return false, err
	}
	return len(resp.Messages) > 0, nil
}

// GetThread fetches one whole conversation (headers only, no bodies).
func GetThread(ctx context.Context, svc *gmail.Service, id string) (Thread, error) {
	t, err := withRetry(ctx, func() (*gmail.Thread, error) {
		return svc.Users.Threads.Get("me", id).
			Format("metadata").
			MetadataHeaders("From", "To", "Subject", "Message-ID").
			Context(ctx).Do()
	})
	if err != nil {
		return Thread{}, fmt.Errorf("get thread %s: %w", id, err)
	}

	thread := Thread{ID: t.Id}
	for _, m := range t.Messages {
		thread.Messages = append(thread.Messages, toMessage(m))
	}
	return thread, nil
}

func toMessage(m *gmail.Message) Message {
	msg := Message{
		ID:       m.Id,
		LabelIDs: m.LabelIds,
		// InternalDate = when Gmail received/sent it, in milliseconds.
		// More reliable than the "Date" header, which the sender controls.
		Date: time.UnixMilli(m.InternalDate),
	}
	for _, h := range m.Payload.Headers {
		// Header names are case-insensitive ("Message-ID" vs "Message-Id").
		if strings.EqualFold(h.Name, "Message-ID") {
			msg.MessageIDHeader = h.Value
			continue
		}
		switch h.Name {
		case "From":
			msg.From = h.Value
		case "To":
			msg.To = h.Value
		case "Subject":
			msg.Subject = h.Value
		}
	}
	return msg
}
