package media

import "time"

// VideoDiagnostics retains a bounded execution history across retries and restarts.
type VideoDiagnostics struct {
	Attempt int          `json:"attempt"`
	Events  []VideoEvent `json:"events"`
}

type VideoEvent struct {
	Stage       string     `json:"stage"`
	StartedAt   time.Time  `json:"startedAt"`
	FinishedAt  *time.Time `json:"finishedAt"`
	Attempt     int        `json:"attempt"`
	AccountID   uint64     `json:"accountId"`
	AccountName string     `json:"accountName"`
	ItemIndex   int        `json:"itemIndex"`
	ItemTotal   int        `json:"itemTotal"`
	Error       string     `json:"error"`
	HTTPStatus  int        `json:"httpStatus"`
}

func (d *VideoDiagnostics) Current() *VideoEvent {
	if len(d.Events) == 0 {
		return nil
	}
	return &d.Events[len(d.Events)-1]
}

func (d *VideoDiagnostics) Advance(event VideoEvent) bool {
	if current := d.Current(); current != nil {
		if current.FinishedAt == nil && current.Stage == event.Stage && current.Attempt == event.Attempt && current.ItemIndex == event.ItemIndex && current.ItemTotal == event.ItemTotal {
			return false
		}
		if current.FinishedAt == nil {
			current.FinishedAt = &event.StartedAt
		}
	}
	d.Events = append(d.Events, event)
	if len(d.Events) > 128 {
		d.Events = append([]VideoEvent(nil), d.Events[len(d.Events)-128:]...)
	}
	return true
}

func (d *VideoDiagnostics) Fail(message string, status int, now time.Time) {
	if current := d.Current(); current != nil && current.Error == "" {
		current.Error, current.HTTPStatus, current.FinishedAt = message, status, &now
	}
}
