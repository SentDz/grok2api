package media

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mediaapp "github.com/chenyme/grok2api/backend/internal/application/media"
	mediadomain "github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/repository"
	"github.com/gin-gonic/gin"
)

type videoDetailRepository struct {
	repository.MediaJobRepository
	job mediadomain.Job
	err error
}

func (r videoDetailRepository) GetMediaJobsByIDs(_ context.Context, ids []string) ([]mediadomain.Job, error) {
	if r.err != nil {
		return nil, r.err
	}
	if len(ids) == 1 && ids[0] == r.job.ID {
		return []mediadomain.Job{r.job}, nil
	}
	return nil, nil
}

func TestVideoDetailReturnsDiagnosticsWithoutPrivateInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	job := mediadomain.Job{ID: "video-detail", RequestID: "request-1", Status: mediadomain.StatusInProgress, Progress: 1,
		CreatedAt: now, UpdatedAt: now, InputJSON: "private-input", UpstreamURL: "private-upstream", ClaimToken: "private-claim"}
	job.Diagnostics.Advance(mediadomain.VideoEvent{Stage: "upload_image", StartedAt: now, ItemIndex: 2, ItemTotal: 3})
	deadline := now.Add(15 * time.Second)
	job.Diagnostics.Advance(mediadomain.VideoEvent{Stage: "http_headers", Request: "statsig_meta_index", StartedAt: now,
		HTTPStatus: 404, EgressNodeID: 7, EgressNodeName: "submission-proxy", EgressScope: "grok_web_submit", EgressMode: "proxy", DeadlineAt: &deadline})
	for _, test := range []struct {
		name, id string
		repo     videoDetailRepository
		status   int
	}{
		{"active", job.ID, videoDetailRepository{job: job}, 200},
		{"missing", "missing", videoDetailRepository{job: job}, 404},
		{"storage failure", job.ID, videoDetailRepository{err: errors.New("private-database-error")}, 500},
		{"legacy", "legacy", videoDetailRepository{job: mediadomain.Job{ID: "legacy", Status: mediadomain.StatusFailed}}, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			handler := NewHandler(mediaapp.NewService(nil, test.repo, nil, nil, mediaapp.Config{}))
			handler.RegisterAdmin(router.Group("/api/admin/v1"))
			handler.RegisterPublic(router)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/v1/media/videos/"+test.id, nil))
			if w.Code != test.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			for _, secret := range []string{"private-input", "private-upstream", "private-claim", "private-database-error", "inputJSON", "claimToken", "upstreamURL"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatalf("response leaked %q", secret)
				}
			}
			if test.name == "active" {
				var payload struct {
					Data videoJobDetailDTO `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Data.Diagnostics.Current() == nil || payload.Data.Diagnostics.Current().Stage != "http_headers" || payload.Data.Progress != 1 || payload.Data.RequestID != "request-1" || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("detail = %#v", payload.Data)
				}
				event := payload.Data.Diagnostics.Current()
				if event.HTTPStatus != 404 || event.Request != "statsig_meta_index" || event.EgressNodeID != 7 || event.DeadlineAt == nil {
					t.Fatalf("network details = %#v", event)
				}
			}
			public := httptest.NewRecorder()
			router.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/v1/media/videos/"+test.id+"/diagnostics", nil))
			if public.Code != http.StatusNotFound {
				t.Fatalf("public diagnostics status=%d", public.Code)
			}
		})
	}
}
