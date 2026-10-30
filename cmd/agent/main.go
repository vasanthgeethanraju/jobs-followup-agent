// "package main" = this builds into a runnable program.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	// Our own packages. Import path = module name (from go.mod) + folder.
	"github.com/joho/godotenv"

	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/followup"
	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/llm"
	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/mailbox"
	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/store"
	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/writer"
	"google.golang.org/api/gmail/v1"
)

const (
	jobsLabel      = "!Jobs"
	rejectedLabel  = "!Jobs/Rejected"
	nextStepsLabel = "!Jobs/Next Steps"

	// If the agent hasn't run for ages, don't try to catch up further than this.
	maxCatchUpDays = 14

	// Default database for local dev (matches compose.yaml).
	// Override with the DATABASE_URL environment variable.
	defaultDatabaseURL = "postgres://agent:agent@localhost:5433/jobs?sslmode=disable"

	// The project mentioned in follow-ups. Override in .env.
	defaultProjectURL  = "https://github.com/vasanthgeethanraju/jobs-followup-agent"
	defaultProjectDesc = "an AI agent written in Go. It connects to Gmail through the Gmail API, uses an LLM (Google Gemini) to understand emails, and stores data in Postgres. Next I am adding RAG and an MCP server."
)

func main() {
	// Command-line flags, e.g.:  go run ./cmd/agent -days 5
	days := flag.Int("days", 7, "follow up on applications sent exactly this many days ago")
	createDrafts := flag.Bool("draft", false, "create Gmail drafts (without it: just preview the emails here)")
	flag.Parse()
	// flag.Int returns a *pointer* (*int); "*days" reads the value it points to.

	// Load settings from .env (API keys etc.). In the cloud there is no .env file;
	// the variables are set directly, so a missing file is fine.
	_ = godotenv.Load()

	// A context carries deadlines/cancellation through every API call.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	startedAt := time.Now()

	// 1. Connect to Postgres (our memory).
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 2. Log in to Gmail (opens the browser the first time, then uses token.json).
	svc, err := mailbox.NewService(ctx, "credentials.json", "token.json")
	if err != nil {
		log.Fatalf("gmail login: %v", err)
	}

	// 3. Look up our labels' internal IDs.
	labels, err := mailbox.FindLabelIDs(ctx, svc, jobsLabel, rejectedLabel, nextStepsLabel)
	if err != nil {
		log.Fatal(err)
	}
	stopLabels := []string{labels[rejectedLabel], labels[nextStepsLabel]}

	// 4. Work out WHICH days to check, using the last run from the database.
	//    Ran yesterday  → check the one day that became due today.
	//    Missed 2 days  → check 3 days (catch up).
	//    Already ran today → re-check today's day (safe: saving is an upsert).
	from, to, err := searchWindow(ctx, db, startedAt, *days)
	if err != nil {
		log.Fatal(err)
	}
	query := fmt.Sprintf("in:sent after:%d before:%d", from.Unix(), to.Unix())
	threadIDs, err := mailbox.ListThreadIDs(ctx, svc, labels[jobsLabel], query)
	if err != nil {
		log.Fatal(err)
	}
	// Go formats dates "by example": you write how Jan 2, 2006 should look.
	fmt.Printf("Checking %d applications sent %s → %s...\n\n",
		len(threadIDs), from.Format("Mon Jan 2"), to.AddDate(0, 0, -1).Format("Mon Jan 2"))

	catchUp := int(to.Sub(from).Hours()/24) - 1 // extra days beyond the normal one
	opts := followup.Options{MinDays: *days, MaxDays: *days + catchUp, StopLabelIDs: stopLabels}

	// 5. Decide for each conversation, and SAVE the result.
	var toFollowUp, skipped []followup.Decision
	for _, id := range threadIDs {
		thread, err := mailbox.GetThread(ctx, svc, id)
		if err != nil {
			log.Fatal(err)
		}

		closed, err := companyClosed(ctx, svc, thread, stopLabels)
		if err != nil {
			log.Fatal(err)
		}

		d := followup.Decide(thread, opts, startedAt, closed)
		if d.To == "" {
			continue // not an email you sent; nothing to track
		}

		status := "skipped"
		if d.Reason == "draft waiting in Gmail" {
			status = "drafted"
		}
		if d.FollowUp {
			status = "follow_up_due"
			toFollowUp = append(toFollowUp, d)
		} else {
			skipped = append(skipped, d)
		}

		err = db.SaveApplication(ctx, store.Application{
			ThreadID:   d.ThreadID,
			CompanyKey: followup.CompanyKey(d.To),
			To:         d.To,
			Subject:    d.Subject,
			AppliedAt:  d.AppliedAt,
			Status:     status,
			Reason:     d.Reason,
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	// 6. Remember this run, so tomorrow knows where to pick up.
	if err := db.RecordRun(ctx, startedAt, len(threadIDs), len(toFollowUp)); err != nil {
		log.Fatal(err)
	}

	printTable(fmt.Sprintf("FOLLOW UP (%d)", len(toFollowUp)), toFollowUp)
	printTable(fmt.Sprintf("SKIPPED (%d)", len(skipped)), skipped)

	// 7. Write the follow-ups (and, with -draft, save them as Gmail drafts).
	if len(toFollowUp) > 0 {
		if err := writeFollowUps(ctx, svc, db, toFollowUp, *createDrafts); err != nil {
			log.Fatal(err)
		}
	}
}

// writeFollowUps has the LLM write each email, prints it, and optionally
// saves it as a Gmail draft. Drafts are never SENT — you review and send.
func writeFollowUps(ctx context.Context, svc *gmail.Service, db *store.Store, due []followup.Decision, createDrafts bool) error {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("GEMINI_API_KEY is not set (add it to .env)")
	}
	// Held as the INTERFACE type: swapping to Claude later only changes this line.
	var client llm.Client = llm.NewGemini(apiKey, envOr("GEMINI_MODEL", "gemini-3.5-flash-lite"))

	signature, err := mailbox.Signature(ctx, svc)
	if err != nil {
		return err
	}

	mode := "PREVIEW (nothing saved to Gmail; add -draft to create drafts)"
	if createDrafts {
		mode = "CREATING GMAIL DRAFTS (not sent: review them in Gmail → Drafts)"
	}
	fmt.Println("==========", mode, "==========")

	for i, d := range due {
		body, err := mailbox.GetBody(ctx, svc, d.Original.ID)
		if err != nil {
			return err
		}

		email, err := writer.Write(ctx, client, writer.Input{
			To:           d.To,
			Subject:      d.Subject,
			OriginalBody: body,
			Now:          time.Now(),
			ProjectURL:   envOr("FOLLOWUP_PROJECT_URL", defaultProjectURL),
			ProjectDesc:  envOr("FOLLOWUP_PROJECT_DESC", defaultProjectDesc),
			// In .env, write a line break as \n — we turn it into a real one here.
			SignOff: strings.ReplaceAll(envOr("FOLLOWUP_SIGNOFF", ""), `\n`, "\n"), // empty: Gmail signature has it
		})
		if err != nil {
			// One bad email shouldn't stop the rest: report it and move on.
			fmt.Printf("\n[%d/%d] %s: could not write email: %v\n", i+1, len(due), d.To, err)
			continue
		}

		fmt.Printf("\n[%d/%d] To: %s | %s at %s\nSubject: Re: %s\n\n%s\n",
			i+1, len(due), d.To, email.Role, email.Company, d.Subject, email.Text)

		if !createDrafts {
			continue
		}
		draftID, err := mailbox.CreateReplyDraft(ctx, svc, mailbox.Reply{
			ThreadID:      d.ThreadID,
			To:            d.To,
			Subject:       d.Subject,
			Text:          email.Text,
			SignatureHTML: signature,
			Original:      d.Original,
			OriginalBody:  body,
		})
		if err != nil {
			return err
		}
		if err := db.MarkDrafted(ctx, d.ThreadID, draftID, email.Company, email.Role); err != nil {
			return err
		}
		fmt.Println("  → draft saved ✓")
	}
	return nil
}

// envOr reads an environment variable, with a fallback if it's not set.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// searchWindow returns [from, to): local midnights bounding the days whose
// applications became due since the last run.
func searchWindow(ctx context.Context, db *store.Store, now time.Time, days int) (from, to time.Time, err error) {
	today := midnight(now)

	lastRunDay := today.AddDate(0, 0, -1) // first run ever: act as if we ran yesterday
	last, ok, err := db.LastRun(ctx)
	if err != nil {
		return from, to, err
	}
	if ok {
		lastRunDay = midnight(last)
	}
	if oldest := today.AddDate(0, 0, -maxCatchUpDays); lastRunDay.Before(oldest) {
		lastRunDay = oldest
	}

	// A run on day R handles applications sent on day R - days.
	// So runs (lastRunDay, today] cover sends (lastRunDay+1-days, today-days].
	from = lastRunDay.AddDate(0, 0, 1-days)
	to = today.AddDate(0, 0, 1-days)
	if !from.Before(to) { // already ran today → just re-check today's day
		from = today.AddDate(0, 0, -days)
	}
	return from, to, nil
}

// midnight returns 00:00 local time on the same calendar day.
func midnight(t time.Time) time.Time {
	y, m, d := t.Local().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// companyClosed asks Gmail: does this company have a Rejected or Next Steps
// email in ANY conversation? Uses Gmail search like "{from:acme.com to:acme.com}"
// (curly braces mean OR in Gmail search).
func companyClosed(ctx context.Context, svc *gmail.Service, t mailbox.Thread, stopLabels []string) (bool, error) {
	var to string
	for _, m := range t.Messages {
		if m.SentByMe() {
			to = m.To
			break
		}
	}
	key := followup.CompanyKey(to)
	if key == "" {
		return false, nil
	}

	query := fmt.Sprintf("{from:%s to:%s}", key, key)
	for _, label := range stopLabels {
		found, err := mailbox.AnyMatch(ctx, svc, label, query)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// printTable lines up columns nicely using the standard library's tabwriter.
func printTable(title string, ds []followup.Decision) {
	fmt.Println(title)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  SENT\tDAYS\tTO\tSUBJECT\tREASON")
	for _, d := range ds {
		fmt.Fprintf(w, "  %s\t%d\t%s\t%.45s\t%s\n",
			d.AppliedAt.Local().Format("Jan 02"), d.DaysAgo, d.To, d.Subject, d.Reason)
	}
	w.Flush()
	fmt.Println()
}
