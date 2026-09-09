package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/chenyme/grok2api/backend/internal/infra/provider"
)

func TestVideoDiagnosticsIdentifyReferenceFailureStage(t *testing.T) {
	for _, model := range []string{"grok-imagine-video", "grok-imagine-video-1.5"} {
		for _, failure := range []struct{ path, stage string }{
			{"/http/upload-file-v2/direct", "upload_image"},
			{"/rest/media/post/create", "create_post"},
			{"/rest/app-chat/conversations/new", "submit_video"},
			{"", "wait_generation"},
		} {
			t.Run(model+"/"+failure.stage, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == failure.path {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, `{"error":{"message":"reference rejected"}}`)
						return
					}
					switch r.URL.Path {
					case "/http/upload-file-v2/direct":
						_, _ = io.WriteString(w, `{"fileMetadata":{"fileMetadataId":"image-id","fileUri":"users/test/image.png"}}`)
					case "/rest/media/post/create":
						_, _ = io.WriteString(w, `{"post":{"id":"post-id"}}`)
					case "/rest/app-chat/conversations/new":
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"result\":{\"response\":{\"streamingVideoGenerationResponse\":{\"progress\":100,\"videoUrl\":\"/videos/final.mp4\"}}}}\n\n")
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				adapter, credential := testMediaAdapter(t, server.URL)
				var stages []string
				var uploadIndex, uploadTotal int
				ctx := provider.WithVideoStepReporter(context.Background(), func(stage string, index, total int) {
					stages = append(stages, stage)
					if stage == "upload_image" {
						uploadIndex, uploadTotal = index, total
					}
				})
				_, err := adapter.GenerateVideo(ctx, provider.VideoRequest{
					Credential: credential, Model: model, Prompt: "test", Duration: 6, Resolution: "720p",
					ReferenceURLs: []string{"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="},
				})
				if (err != nil) != (failure.path != "") || len(stages) == 0 || stages[len(stages)-1] != failure.stage {
					t.Fatalf("stages=%v err=%v", stages, err)
				}
				if !slices.Contains(stages, "load_image") || uploadIndex != 1 || uploadTotal != 1 {
					t.Fatalf("reference stages=%v item=%d/%d", stages, uploadIndex, uploadTotal)
				}
			})
		}
	}
}
