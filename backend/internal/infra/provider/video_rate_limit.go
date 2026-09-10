package provider

import (
	"context"
	"net/http"
	"time"
)

// Only an actual Web video submission HTTP 429 may create this error.
// It must never be interpreted as proof of exhausted account quota.
type VideoSubmissionRateLimitError struct {
	NodeID        uint64
	CooldownUntil time.Time
	CoolingError  error
	Err           error
}

func (e *VideoSubmissionRateLimitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "Web video submission rate limited"
}
func (e *VideoSubmissionRateLimitError) Unwrap() error       { return e.Err }
func (e *VideoSubmissionRateLimitError) HTTPStatusCode() int { return http.StatusTooManyRequests }

type VideoEgressRetrier interface {
	SelectVideoRetryNode(context.Context, []uint64) (uint64, error)
}
