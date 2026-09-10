package provider

import (
	"context"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/media"
)

type videoStepKey struct{}
type videoItemKey struct{}

func WithVideoItem(ctx context.Context, index, total int) context.Context {
	return context.WithValue(ctx, videoItemKey{}, [2]int{index, total})
}

// Reporters run synchronously on the video worker; they must not be called from
// background goroutines. Labels are fixed identifiers, never upstream content.
func WithVideoStepReporter(ctx context.Context, report func(string, int, int)) context.Context {
	return WithVideoEventReporter(ctx, func(event media.VideoEvent) {
		report(event.Stage, event.ItemIndex, event.ItemTotal)
	})
}

func WithVideoEventReporter(ctx context.Context, report func(media.VideoEvent)) context.Context {
	return context.WithValue(ctx, videoStepKey{}, report)
}

func HasVideoEventReporter(ctx context.Context) bool {
	report, ok := ctx.Value(videoStepKey{}).(func(media.VideoEvent))
	return ok && report != nil
}

func ReportVideoEvent(ctx context.Context, event media.VideoEvent) {
	if report, ok := ctx.Value(videoStepKey{}).(func(media.VideoEvent)); ok && report != nil {
		if event.StartedAt.IsZero() {
			event.StartedAt = time.Now().UTC()
		}
		if event.ItemTotal == 0 {
			item, _ := ctx.Value(videoItemKey{}).([2]int)
			event.ItemIndex, event.ItemTotal = item[0], item[1]
		}
		report(event)
	}
}

func ReportVideoStep(ctx context.Context, stage string) {
	item, _ := ctx.Value(videoItemKey{}).([2]int)
	ReportVideoItemStep(ctx, stage, item[0], item[1])
}

func ReportVideoItemStep(ctx context.Context, stage string, index, total int) {
	ReportVideoEvent(ctx, media.VideoEvent{Stage: stage, ItemIndex: index, ItemTotal: total})
}
