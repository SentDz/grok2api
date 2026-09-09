package media

import (
	"testing"
	"time"
)

func TestVideoDiagnosticsKeepsFailuresAcrossRetriesAndBoundsHistory(t *testing.T) {
	var d VideoDiagnostics
	now := time.Now().UTC()
	first := VideoEvent{Stage: "upload_image", Attempt: 1, AccountID: 7, ItemIndex: 1, ItemTotal: 2, StartedAt: now}
	d.Advance(first)
	first.StartedAt = now.Add(time.Second)
	if d.Advance(first) || !d.Current().StartedAt.Equal(now) {
		t.Fatal("repeated stage reset its start time")
	}
	d.Fail("upload rejected", 403, now.Add(2*time.Second))
	d.Fail("generic public error", 0, now.Add(3*time.Second))
	d.Advance(VideoEvent{Stage: "upload_image", Attempt: 2, AccountID: 8, StartedAt: now.Add(4 * time.Second)})
	if len(d.Events) != 2 || d.Events[0].Error != "upload rejected" || d.Events[0].HTTPStatus != 403 || d.Events[0].FinishedAt == nil || d.Current().Error != "" {
		t.Fatalf("retry history = %#v", d)
	}
	for i := 0; i < 140; i++ {
		d.Advance(VideoEvent{Stage: "upload_image", Attempt: i + 3, StartedAt: now.Add(time.Duration(i+5) * time.Second)})
	}
	if len(d.Events) != 128 || d.Current().Attempt != 142 || d.Current().FinishedAt != nil {
		t.Fatalf("bounded history = %#v", d)
	}
}
