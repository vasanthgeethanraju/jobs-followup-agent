// Every Go file starts by naming its package.
// "package main" is special: it means "this builds into a runnable program".
package main

// Imports from Go's standard library (no installs needed).
import (
	"fmt"  // printing / formatting text
	"time" // dates and durations
)

// A struct groups related data — like a class without methods (yet).
// This is the shape of one job application we'll later read from Gmail.
type Application struct {
	Company   string
	Email     string
	AppliedAt time.Time
	Replied   bool
}

// How long to wait before following up. Constants can't change at runtime.
const followUpAfter = 7 * 24 * time.Hour

// needsFollowUp is a function: takes an Application and the current time,
// returns true/false. In Go, the type comes AFTER the name.
func needsFollowUp(app Application, now time.Time) bool {
	waited := now.Sub(app.AppliedAt) // time elapsed since applying
	return !app.Replied && waited >= followUpAfter
}

// main() is where the program starts.
func main() {
	now := time.Now() // ":=" declares a variable and infers its type

	// A slice ([]Application) is Go's growable list.
	// Fake data for now — step 2 replaces this with your real Gmail.
	apps := []Application{
		{Company: "Acme", Email: "hr@acme.com", AppliedAt: now.AddDate(0, 0, -10), Replied: false},
		{Company: "Globex", Email: "jobs@globex.com", AppliedAt: now.AddDate(0, 0, -3), Replied: false},
		{Company: "Initech", Email: "careers@initech.com", AppliedAt: now.AddDate(0, 0, -12), Replied: true},
	}

	fmt.Println("Checking", len(apps), "applications...")

	// "for _, app := range apps" loops over the slice.
	// The "_" throws away the index because we don't need it.
	for _, app := range apps {
		days := int(now.Sub(app.AppliedAt).Hours() / 24)
		if needsFollowUp(app, now) {
			fmt.Printf("  FOLLOW UP  %-8s (%s) — applied %d days ago, no reply\n", app.Company, app.Email, days)
		} else {
			fmt.Printf("  skip       %-8s — applied %d days ago, replied=%v\n", app.Company, days, app.Replied)
		}
	}
}
