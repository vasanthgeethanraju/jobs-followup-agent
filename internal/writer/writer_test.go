package writer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/llm"
)

// This is where the Fake LLM pays off: we test the whole writer —
// prompt → JSON → style check → assembly — with no internet and no cost.

var thursday = time.Date(2026, 10, 29, 10, 0, 0, 0, time.Local)

func TestWriteAssemblesEmail(t *testing.T) {
	fake := llm.Fake{Reply: `{"first_name":"Dylan","company":"Pond","role":"Software Engineer",
		"middle":"I'm following up on my Software Engineer application. I'd like to share what I'm building right now: https://github.com/x/y."}`}

	// No SignOff: the Gmail signature provides "Regards, ..." later.
	got, err := Write(context.Background(), fake, Input{Now: thursday})
	if err != nil {
		t.Fatal(err)
	}
	want := "Hello Dylan,\n\nHope you're doing well!\n\nI'm following up on my Software Engineer application. " +
		"I'd like to share what I'm building right now: https://github.com/x/y. " +
		"Would you have 15 minutes for a quick chat this week?"
	if got.Text != want {
		t.Errorf("got:\n%s\n\nwant:\n%s", got.Text, want)
	}
}

func TestWriteGivesUpOnBadStyle(t *testing.T) {
	// The fake ALWAYS breaks the rules, so after 3 tries Write must return an error.
	fake := llm.Fake{Reply: `{"first_name":"","company":"Acme","role":"SWE","middle":"I'm thrilled to delve into this!"}`}
	if _, err := Write(context.Background(), fake, Input{Now: thursday}); err == nil {
		t.Fatal("expected an error for AI-sounding text, got nil")
	}
}

func TestGreeting(t *testing.T) {
	cases := []struct{ name, company, want string }{
		{"Dylan", "Pond", "Hello Dylan,"},
		{"", "Pond", "Hello Pond team,"},
		{"", "", "Hello there,"},
	}
	for _, c := range cases {
		if got := Greeting(c.name, c.company); got != c.want {
			t.Errorf("Greeting(%q, %q) = %q, want %q", c.name, c.company, got, c.want)
		}
	}
}

func TestOpener(t *testing.T) {
	monday := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local)
	friday := time.Date(2026, 10, 30, 9, 0, 0, 0, time.Local)
	if got := Opener(monday); !strings.Contains(got, "weekend") {
		t.Errorf("Monday opener should mention the weekend, got %q", got)
	}
	if got := Opener(friday); !strings.HasPrefix(got, "Happy Friday") {
		t.Errorf("Friday opener = %q", got)
	}
}

func TestAsk(t *testing.T) {
	friday := time.Date(2026, 10, 30, 9, 0, 0, 0, time.Local)
	if got := Ask(thursday); !strings.HasSuffix(got, "this week?") {
		t.Errorf("Thursday ask = %q, want 'this week'", got)
	}
	if got := Ask(friday); !strings.HasSuffix(got, "next week?") {
		t.Errorf("Friday ask = %q, want 'next week'", got)
	}
}

func TestStyleProblems(t *testing.T) {
	good := "I'm following up on my application from last week. See https://github.com/a/b.c for my project. Any time for a chat?"
	if p := StyleProblems(good); len(p) != 0 {
		t.Errorf("good text flagged: %v", p)
	}
	bad := "I am thrilled; truly — excited to leverage synergy!!"
	if p := StyleProblems(bad); len(p) < 4 {
		t.Errorf("bad text should break many rules, got %v", p)
	}
}
