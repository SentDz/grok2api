package media

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	mediaapp "github.com/chenyme/grok2api/backend/internal/application/media"
	"github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/repository"
	"github.com/gin-gonic/gin"
)

type videoCancelerStub struct {
	id  string
	err error
}

func (s *videoCancelerStub) CancelVideoJob(_ context.Context, id string) error {
	s.id = id
	return s.err
}

func TestCancelVideoAdminEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"cancelled", nil, http.StatusOK},
		{"missing", repository.ErrNotFound, http.StatusNotFound},
		{"terminal", repository.ErrConflict, http.StatusConflict},
		{"storage error", errors.New("private database error"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := videoDetailRepository{job: media.Job{ID: "job", Status: media.StatusFailed, ErrorCode: media.VideoCancelledErrorCode}}
			handler := NewHandler(mediaapp.NewService(nil, repo, nil, nil, mediaapp.Config{}))
			canceler := &videoCancelerStub{err: tc.err}
			handler.SetVideoCanceler(canceler)
			router := gin.New()
			handler.RegisterAdmin(router.Group("/api/admin/v1"))
			handler.RegisterPublic(router)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/v1/media/videos/job/cancel", nil))
			if w.Code != tc.status || canceler.id != "job" {
				t.Fatalf("status=%d id=%q body=%s", w.Code, canceler.id, w.Body)
			}
			public := httptest.NewRecorder()
			router.ServeHTTP(public, httptest.NewRequest(http.MethodPost, "/v1/media/videos/job/cancel", nil))
			if public.Code != http.StatusNotFound {
				t.Fatalf("public cancellation exposed: %d", public.Code)
			}
		})
	}
}
