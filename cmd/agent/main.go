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
	// Command-line flags, e.g.:  go run ./cmd/agent -days 5
	days := flag.Int("days", 7, "follow up on applications sent exactly this many days ago")
	flag.Parse()
	// flag.Int returns a *pointer* (*int); "*days" reads the value it points to.

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
	// Min = Max = days: the rules double-check the same window the search uses.
	opts := followup.Options{MinDays: *days, MaxDays: *days, StopLabelIDs: stopLabels}

	// 3. Only conversations where YOU sent a !Jobs email on that exact day.
	//    We give Gmail precise Unix timestamps (seconds since 1970) for
	//    YOUR local midnight → next midnight. Plain dates like "after:2026/10/20"
	//    would be read in Pacific time, which is wrong if you're elsewhere.
	now := time.Now()
	y, m, d := now.AddDate(0, 0, -*days).Date()
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	dayEnd := dayStart.AddDate(0, 0, 1)
	query := fmt.Sprintf("in:sent after:%d before:%d", dayStart.Unix(), dayEnd.Unix())

	threadIDs, err := mailbox.ListThreadIDs(ctx, svc, labels[jobsLabel], query)
	if err != nil {
		log.Fatal(err)
	}
	// Go formats dates "by example": you write how Jan 2, 2006 should look.
	fmt.Printf("Checking %d applications you sent on %s (%d days ago)...\n\n",
		len(threadIDs), dayStart.Format("Mon Jan 2"), *days)

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
