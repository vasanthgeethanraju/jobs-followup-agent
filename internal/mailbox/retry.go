package mailbox

import (
	"context"
	"errors"
	"net/http"
	"time"

	"google.golang.org/api/googleapi"
)

// withRetry calls fn, and if Gmail says "slow down" (rate limit),
// waits and tries again: 1s, 2s, 4s, 8s, 16s. This is called
// "exponential backoff" — a standard pattern for any external API.
//
// [T any] makes this a *generic* function: it works for any return type T
// (a *gmail.Thread, a label list, ...) without copy-pasting it per type.
func withRetry[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	wait := time.Second
	for attempt := 1; ; attempt++ {
		result, err := fn()
		if err == nil || !isRateLimit(err) || attempt == 5 {
			return result, err
		}
		select {
		case <-time.After(wait):
			wait *= 2
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}
}

func isRateLimit(err error) bool {
	// errors.As checks whether err is (or wraps) a *googleapi.Error,
	// and if so fills in gerr so we can inspect it.
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	if gerr.Code == http.StatusTooManyRequests {
		return true
	}
	for _, e := range gerr.Errors {
		if e.Reason == "rateLimitExceeded" || e.Reason == "userRateLimitExceeded" {
			return true
		}
	}
	return false
}
