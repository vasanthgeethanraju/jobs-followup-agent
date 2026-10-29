// "package main" = this builds into a runnable program.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"text/tabwriter"
	"time"

	// Our own packages. Import path = module name (from go.mod) + folder.
	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/followup"
	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/mailbox"
	"github.com/vasanthgeethanraju/jobs-followup-agent/internal/store"
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
)

func main() {
	// Command-line flags, e.g.:  go run ./cmd/agent -days 5
	days := flag.Int("days", 7, "follow up on applications sent exactly this many days ago")
	flag.Parse()
	// flag.Int returns a *pointer* (*int); "*days" reads the value it points to.

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
