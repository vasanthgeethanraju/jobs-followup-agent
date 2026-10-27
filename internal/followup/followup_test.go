package followup

// Go's built-in testing: any file ending in _test.go, any func named TestXxx.
// Run with: go test ./...

import (
	"testing"
	"time"

	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/mailbox"
)

func TestDecide(t *testing.T) {
	now := time.Date(2026, 10, 27, 15, 0, 0, 0, time.Local)
	daysAgo := func(n int) time.Time { return now.AddDate(0, 0, -n) }

	sent := func(n int, extra ...string) mailbox.Message {
		return mailbox.Message{From: "me@gmail.com", To: "hr@acme.com", Date: daysAgo(n), LabelIDs: append([]string{"SENT"}, extra...)}
	}
	reply := func(from string, n int) mailbox.Message {
		return mailbox.Message{From: from, Date: daysAgo(n), LabelIDs: []string{"INBOX"}}
	}

	opts := Options{MinDays: 5, MaxDays: 30, StopLabelIDs: []string{"REJECTED"}}

	// A "table-driven test": a list of cases, then one loop that checks them all.
	// This is the most common testing style in Go.
	tests := []struct {
		name          string
		msgs          []mailbox.Message
		companyClosed bool
		wantFollowUp  bool
		wantReason    string
	}{
		{"5 days, no reply", []mailbox.Message{sent(5)}, false, true, "no reply yet"},
		{"too recent", []mailbox.Message{sent(4)}, false, false, "too recent"},
		{"too old", []mailbox.Message{sent(31)}, false, false, "too old"},
		{"they replied", []mailbox.Message{sent(6), reply("Jane <jane@acme.com>", 3)}, false, false, "they replied"},
		{"rejected label", []mailbox.Message{sent(6, "REJECTED")}, false, false, "labeled Rejected / Next Steps"},
		{"closed elsewhere", []mailbox.Message{sent(6)}, true, false, "company has Rejected / Next Steps in another email"},
		{"already followed up", []mailbox.Message{sent(10), sent(3)}, false, false, "already followed up"},
		{"bounced", []mailbox.Message{sent(6), reply("Mail Delivery Subsystem <mailer-daemon@googlemail.com>", 6)}, false, false, "bounced (address didn't work)"},
	}

	for _, tc := range tests {
		// t.Run gives each case its own name in the test output.
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(mailbox.Thread{Messages: tc.msgs}, opts, now, tc.companyClosed)
			if got.FollowUp != tc.wantFollowUp || got.Reason != tc.wantReason {
				t.Errorf("got (%v, %q), want (%v, %q)", got.FollowUp, got.Reason, tc.wantFollowUp, tc.wantReason)
			}
		})
	}
}

func TestCompanyKey(t *testing.T) {
	cases := map[string]string{
		"Jane Doe <jane@Acme.com>":  "acme.com",
		"founder@gmail.com":         "founder@gmail.com", // personal: full address
		"a@camfer.dev, b@other.com": "camfer.dev",        // first recipient wins
		"not an email":              "",
	}
	for in, want := range cases {
		if got := CompanyKey(in); got != want {
			t.Errorf("CompanyKey(%q) = %q, want %q", in, got, want)
		}
	}
}
