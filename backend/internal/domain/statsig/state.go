package statsig

import (
	"context"
	"errors"
	"time"
)

var ErrRejected = errors.New("Grok rejected the signature material")

type Sample struct {
	Seed  string   `json:"seed"`
	HEX   string   `json:"hex"`
	Paths []string `json:"paths"`
}

type Capture struct {
	Samples     []Sample `json:"samples"`
	Scripts     string   `json:"scripts"`
	Fingerprint string   `json:"fingerprint"`
}

type Version struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	Source      string    `json:"source"`
	Code        string    `json:"code"`
	Sample      Sample    `json:"sample"`
	Fingerprint string    `json:"fingerprint"`
}

type Event struct {
	At      time.Time `json:"at"`
	Action  string    `json:"action"`
	Message string    `json:"message"`
}

type State struct {
	Active      *Version   `json:"active,omitempty"`
	Previous    *Version   `json:"previous,omitempty"`
	Status      string     `json:"status"`
	LastChecked *time.Time `json:"lastChecked,omitempty"`
	NextCheck   *time.Time `json:"nextCheck,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	Failures    int        `json:"failures"`
	Events      []Event    `json:"events"`
}

type Repository interface {
	Load(context.Context) (State, uint64, error)
	Save(context.Context, State, uint64) (uint64, error)
}

type Signer interface {
	Sign(context.Context, string, string) (string, error)
	Invalidate()
}
