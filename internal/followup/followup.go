// Package followup holds the "should we follow up?" rules.
//
// It deliberately knows nothing about the Gmail API — it only looks at
// plain data. That keeps the rules easy to read and easy to unit-test.
package followup

import (
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/mailbox"
)

// Options are the knobs you can tune from the command line.
type Options struct {
	MinDays      int      // follow up once the application is at least this old
	MaxDays      int      // ...but ignore applications older than this
	StopLabelIDs []string // e.g. !Jobs/Rejected, !Jobs/Next Steps
}

// Decision is the verdict for one conversation.
type Decision struct {
	ThreadID  string // Gmail conversation ID (our database key)
	FollowUp  bool
	Reason    string    // why we did / didn't pick it
	To        string    // who we'd follow up with
	Subject   string    // subject of your original email
	AppliedAt time.Time // when you sent it
	DaysAgo   int
}

// Decide applies the rules to one thread. companyClosed = "this company has a
// Rejected / Next Steps email in some OTHER conversation" (looked up by the caller).
func Decide(t mailbox.Thread, opts Options, now time.Time, companyClosed bool) Decision {
	var mine []mailbox.Message
	for _, m := range t.Messages {
		if m.SentByMe() {
			mine = append(mine, m)
		}
	}
	if len(mine) == 0 {
		return Decision{Reason: "no email sent by you"}
	}

	first := mine[0]
	d := Decision{
		ThreadID:  t.ID,
		To:        first.To,
		Subject:   first.Subject,
		AppliedAt: first.Date,
		DaysAgo:   daysBetween(first.Date, now),
	}

	// The rules, in order. The first one that matches decides.
	switch {
	case hasAnyLabel(t, opts.StopLabelIDs):
		d.Reason = "labeled Rejected / Next Steps"
	case companyClosed:
		d.Reason = "company has Rejected / Next Steps in another email"
	case bounced(t):
		d.Reason = "bounced (address didn't work)"
	case len(mine) < len(t.Messages):
		d.Reason = "they replied"
	case len(mine) > 1:
		d.Reason = "already followed up"
	case d.DaysAgo < opts.MinDays:
		d.Reason = "too recent"
	case d.DaysAgo > opts.MaxDays:
		d.Reason = "too old"
	default:
		d.FollowUp = true
		d.Reason = "no reply yet"
	}
	return d
}

// daysBetween counts calendar days, so "sent on the 22nd" is 5 days ago on
// the 27th no matter what time of day you run it.
func daysBetween(from, to time.Time) int {
	y1, m1, d1 := from.Local().Date()
	y2, m2, d2 := to.Local().Date()
	a := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}

func hasAnyLabel(t mailbox.Thread, labelIDs []string) bool {
	for _, m := range t.Messages {
		for _, id := range labelIDs {
			if slices.Contains(m.LabelIDs, id) {
				return true
			}
		}
	}
	return false
}

func bounced(t mailbox.Thread) bool {
	for _, m := range t.Messages {
		from := strings.ToLower(m.From)
		if strings.Contains(from, "mailer-daemon") || strings.Contains(from, "postmaster") {
			return true
		}
	}
	return false
}

// Personal email providers: everyone shares the domain, so we can't treat
// "gmail.com" as one company — we match the full address instead.
var personalDomains = []string{
	"gmail.com", "googlemail.com", "yahoo.com", "outlook.com", "hotmail.com",
	"live.com", "icloud.com", "me.com", "aol.com", "proton.me", "protonmail.com",
}

// CompanyKey turns a "To" header like `Jane <jane@acme.com>` into what
// identifies the company in Gmail search: "acme.com" (or the full address
// for personal providers). Returns "" if it can't parse an address.
func CompanyKey(to string) string {
	addrs, err := mail.ParseAddressList(to)
	if err != nil || len(addrs) == 0 {
		return ""
	}
	addr := strings.ToLower(addrs[0].Address)
	_, domain, ok := strings.Cut(addr, "@")
	if !ok {
		return ""
	}
	if slices.Contains(personalDomains, domain) {
		return addr
	}
	return domain
}
