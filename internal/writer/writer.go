// Package writer drafts the follow-up email text.
//
// Design idea: give the LLM ONLY the part that needs creativity (the middle
// 2-3 sentences + reading names out of your original email). Everything with a
// fixed rule — greeting, opener, sign-off — is plain Go code, so it's always
// exactly how you want it. Less freedom for the LLM = less "AI-sounding" text.
package writer

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/llm"
)

// Input is what the writer knows about one application.
type Input struct {
	To           string // e.g. `"Dylan" <dylan@joinpond.ai>`
	Subject      string
	OriginalBody string // the email you sent
	Now          time.Time
	ProjectURL   string // the GitHub link to show off
	ProjectDesc  string // one line about that project
	SignOff      string // optional; empty when your Gmail signature already has one
}

// Email is the finished follow-up.
type Email struct {
	Name    string // first name, or "" if we only know the company
	Company string
	Role    string
	Text    string // full text: greeting → sign-off (Gmail signature added later)
}

// The JSON fields we ask the LLM for (structured output).
type llmReply struct {
	FirstName string `json:"first_name"`
	Company   string `json:"company"`
	Role      string `json:"role"`
	Middle    string `json:"middle"`
}

var schema = llm.Schema{
	"first_name": "Recipient's first name if clearly a real person, else empty string",
	"company":    "Company name as people say it, e.g. Pond (not joinpond.ai)",
	"role":       "Job title applied for, short",
	"middle":     "Exactly 2 sentences: the follow-up line and the project line, following all rules",
}

const maxAttempts = 3

// Write asks the LLM for the email and checks it against the style rules.
// If the LLM breaks a rule, we ask again (up to 3 times) — a "guardrail".
func Write(ctx context.Context, client llm.Client, in Input) (Email, error) {
	prompt := buildPrompt(in)
	var problems []string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var r llmReply
		if err := client.GenerateJSON(ctx, prompt, schema, &r); err != nil {
			return Email{}, err
		}
		middle := cleanUp(r.Middle)

		problems = StyleProblems(middle)
		if len(problems) == 0 {
			return Email{
				Name:    strings.TrimSpace(r.FirstName),
				Company: strings.TrimSpace(r.Company),
				Role:    strings.TrimSpace(r.Role),
				Text:    assemble(r.FirstName, r.Company, middle, in),
			}, nil
		}
		// Tell the model what it did wrong and try again.
		prompt = buildPrompt(in) + "\n\nYour last answer broke these rules, fix them: " + strings.Join(problems, "; ")
	}
	return Email{}, fmt.Errorf("LLM kept breaking style rules: %s", strings.Join(problems, "; "))
}

// assemble glues the fixed parts around the LLM's middle:
//
//	Hello Dylan,             ← Greeting (code)
//	Hope you're doing well!  ← Opener (code, depends on the weekday)
//	<2 sentences>  <ask>     ← middle (LLM) + Ask (code)
//	[Gmail signature]        ← added when the draft is built
func assemble(firstName, company, middle string, in Input) string {
	text := Greeting(firstName, company) + "\n\n" +
		Opener(in.Now) + "\n\n" +
		middle + " " + Ask(in.Now)
	if in.SignOff != "" { // empty by default: your Gmail signature already says "Regards"
		text += "\n\n" + in.SignOff
	}
	return text
}

// Ask is the closing question. Late in the week, "this week" is unrealistic.
func Ask(now time.Time) string {
	switch now.Weekday() {
	case time.Friday, time.Saturday, time.Sunday:
		return "Would you have 15 minutes for a quick chat next week?"
	default:
		return "Would you have 15 minutes for a quick chat this week?"
	}
}

// Greeting: "Hello Dylan," or, if no real name, "Hello Pond team,".
func Greeting(firstName, company string) string {
	firstName = strings.TrimSpace(firstName)
	if firstName != "" {
		return "Hello " + firstName + ","
	}
	company = strings.TrimSpace(company)
	if company == "" {
		return "Hello there,"
	}
	return "Hello " + company + " team,"
}

// Opener adds a human touch based on the day of the week.
func Opener(now time.Time) string {
	switch now.Weekday() {
	case time.Monday:
		return "Happy Monday! Hope you had a great weekend."
	case time.Friday:
		return "Happy Friday! Hope you're doing well."
	case time.Saturday, time.Sunday:
		return "Hope you're having a nice weekend!"
	default:
		return "Hope you're doing well!"
	}
}

func buildPrompt(in Input) string {
	body := in.OriginalBody
	if len(body) > 4000 { // keep prompts small: faster + cheaper
		body = body[:4000]
	}
	return fmt.Sprintf(`You help a software engineer follow up on a job application he emailed last week.
He got no reply. Write ONLY the middle part of a short follow-up email.
The greeting ("Hello ...,"), a "hope you're doing well" line, the closing question asking for a chat,
and the sign-off are all added separately. Do not write them.

What the middle must do, in exactly 2 short sentences:
1. Say he is following up on his application for the role (name the role).
2. Share his current project in ONE sentence, introduced like "I'd like to share what I'm building right now",
   and include the link exactly once: %s
   About the project: %s
   - It is still in progress. Never say "I built". Don't start with "Lately".
   - Describe it ONLY with the facts above. Never add features it doesn't have.
   - Never say it writes, drafts, or sends emails or follow-ups.
   - Only connect it to the company if his original email clearly says what the company does, in a few words.
     Never invent facts about the company. If there is no clear link, just mention the project.
Do NOT ask for a chat or a call. That question is added after your text.

Style rules (very important):
- Write like a real person typing a quick email. Simple, everyday English. Short sentences.
- Use contractions like a person would: "I'm", "I've", "you're" (not "I am", "I have").
- No em dashes or en dashes. No semicolons. No bullet points. At most one exclamation mark.
- Never use these words or phrases: thrilled, excited to, passionate, delve, leverage, synergy, reiterate,
  keen, eager, align, cutting-edge, innovative, robust, seamless, dynamic, landscape, journey,
  "I hope this email finds you well", "touch base", "circle back", "furthermore", "moreover", "additionally".

Example of the right tone for the middle (do not copy it word for word):
"I'm following up on my application for the Backend Engineer role I sent last week. I'd like to share what I'm building right now, an AI agent in Go that works with the Gmail API, Gemini, and Postgres: <link>."

Also fill in: the recipient's first name (only if clearly a real person, from the "To" line, his greeting, or the address like dylan@...; generic inboxes like careers@, hiring@, founders@, join@ mean empty), the company name, and the role.

To: %s
Subject: %s
His original email:
"""
%s
"""`, in.ProjectURL, in.ProjectDesc, in.To, in.Subject, body)
}

// cleanUp fixes small things we never want, even if the model slips.
func cleanUp(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " — ", ", ")
	s = strings.ReplaceAll(s, "—", ", ")
	s = strings.ReplaceAll(s, " – ", ", ")
	s = strings.ReplaceAll(s, "–", "-")
	return s
}

var bannedPhrases = []string{
	"thrilled", "excited to", "passionate", "delve", "leverag", "synergy", "reiterate",
	"keen", "eager", "align", "cutting-edge", "innovative", "robust", "seamless",
	"dynamic", "landscape", "journey", "finds you well", "touch base", "circle back",
	"furthermore", "moreover", "additionally", "esteemed", "testament", "lately",
}

// StyleProblems returns every rule the text breaks (empty = all good).
func StyleProblems(s string) []string {
	var problems []string
	lower := strings.ToLower(s)
	for _, p := range bannedPhrases {
		if strings.Contains(lower, p) {
			problems = append(problems, fmt.Sprintf("don't use %q", p))
		}
	}
	if strings.ContainsAny(s, "—–;") {
		problems = append(problems, "no em dashes, en dashes, or semicolons")
	}
	if strings.Count(s, "!") > 1 {
		problems = append(problems, "at most one exclamation mark")
	}
	if n := countSentences(s); n > 3 {
		problems = append(problems, fmt.Sprintf("too long (%d sentences, max 2)", n))
	}
	if strings.Contains(lower, "quick chat") || strings.Contains(lower, "15 minutes") {
		problems = append(problems, "don't ask for a chat, that line is added separately")
	}
	if strings.HasPrefix(strings.ToLower(s), "hello") || strings.HasPrefix(strings.ToLower(s), "hi ") {
		problems = append(problems, "don't include the greeting")
	}
	return problems
}

var (
	urlRE         = regexp.MustCompile(`https?://\S+`)
	sentenceEndRE = regexp.MustCompile(`[.!?]+(\s|$)`) // ". " or "?" at the end
)

// countSentences: remove links first (their dots aren't sentence ends), then
// count punctuation followed by a space or the end. Rough, so the limit is 4.
func countSentences(s string) int {
	return len(sentenceEndRE.FindAllString(urlRE.ReplaceAllString(s, "LINK"), -1))
}
