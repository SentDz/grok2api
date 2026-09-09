package provider

import "context"

type videoStepKey struct{}
type videoItemKey struct{}

func WithVideoItem(ctx context.Context, index, total int) context.Context {
	return context.WithValue(ctx, videoItemKey{}, [2]int{index, total})
}

// Reporters run synchronously on the video worker; they must not be called from
// background goroutines. Labels are fixed identifiers, never upstream content.
func WithVideoStepReporter(ctx context.Context, report func(string, int, int)) context.Context {
	return context.WithValue(ctx, videoStepKey{}, report)
}

func ReportVideoStep(ctx context.Context, stage string) {
	item, _ := ctx.Value(videoItemKey{}).([2]int)
	ReportVideoItemStep(ctx, stage, item[0], item[1])
}

func ReportVideoItemStep(ctx context.Context, stage string, index, total int) {
	if report, ok := ctx.Value(videoStepKey{}).(func(string, int, int)); ok {
		report(stage, index, total)
	}
}
