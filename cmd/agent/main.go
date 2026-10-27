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
	"google.golang.org/api/gmail/v1"
)

const (
	jobsLabel      = "!Jobs"
	rejectedLabel  = "!Jobs/Rejected"
	nextStepsLabel = "!Jobs/Next Steps"
)

func main() {
	// Command-line flags, e.g.:  go run ./cmd/agent -min-days 7
	minDays := flag.Int("min-days", 5, "follow up once an application is at least this many days old")
	maxDays := flag.Int("max-days", 30, "ignore applications older than this many days")
	flag.Parse()
	// flag.Int returns a *pointer* (*int); "*minDays" reads the value it points to.

	// A context carries deadlines/cancellation through every API call.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 1. Log in to Gmail (opens the browser the first time, then uses token.json).
	svc, err := mailbox.NewService(ctx, "credentials.json", "token.json")
	if err != nil {
		log.Fatalf("gmail login: %v", err)
	}

	// 2. Look up our labels' internal IDs.
	labels, err := mailbox.FindLabelIDs(ctx, svc, jobsLabel, rejectedLabel, nextStepsLabel)
	if err != nil {
		log.Fatal(err)
	}
	stopLabels := []string{labels[rejectedLabel], labels[nextStepsLabel]}
	opts := followup.Options{MinDays: *minDays, MaxDays: *maxDays, StopLabelIDs: stopLabels}

	// 3. Only conversations where YOU sent a !Jobs email in the time window.
	//    "after:" keeps Gmail from returning old ones at all (fewer API calls).
	now := time.Now()
	since := now.AddDate(0, 0, -(*maxDays + 1)).Format("2006/01/02") // Go's date format is "by example"
	threadIDs, err := mailbox.ListThreadIDs(ctx, svc, labels[jobsLabel], "in:sent after:"+since)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Checking %d applications you sent in the last %d days...\n\n", len(threadIDs), *maxDays)

	// 4. Decide for each conversation.
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

		d := followup.Decide(thread, opts, now, closed)
		if d.FollowUp {
			toFollowUp = append(toFollowUp, d)
		} else {
			skipped = append(skipped, d)
		}
	}

	printTable(fmt.Sprintf("FOLLOW UP (%d)", len(toFollowUp)), toFollowUp)
	printTable(fmt.Sprintf("SKIPPED (%d)", len(skipped)), skipped)
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
