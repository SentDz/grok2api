package web

import "github.com/chenyme/grok2api/backend/internal/domain/statsig"

type builtinStatsigUnavailable struct{ cause error }

func (e builtinStatsigUnavailable) Error() string              { return "builtin Statsig is not ready" }
func (e builtinStatsigUnavailable) Unwrap() error              { return e.cause }
func (e builtinStatsigUnavailable) HTTPStatusCode() int        { return 503 }
func (e builtinStatsigUnavailable) RequestScopedFailure() bool { return true }

// SetBuiltinStatsig is called once before the HTTP server starts.
func (a *Adapter) SetBuiltinStatsig(signer statsig.Signer) { a.builtinStatsig = signer }
