package settings

import (
	statsigapp "github.com/chenyme/grok2api/backend/internal/application/statsig"
	"github.com/chenyme/grok2api/backend/internal/shared/response"
	"github.com/gin-gonic/gin"
	"net/http"
)

func RegisterStatsig(router *gin.RouterGroup, service *statsigapp.Service) {
	if service == nil {
		return
	}
	router.GET("/settings/statsig", func(c *gin.Context) { response.Success(c, http.StatusOK, service.Status()) })
	router.POST("/settings/statsig/:action", func(c *gin.Context) {
		if err := service.Trigger(c.Param("action")); err != nil {
			response.Error(c, http.StatusConflict, "statsigActionFailed", err.Error())
			return
		}
		response.Success(c, http.StatusAccepted, gin.H{"accepted": true})
	})
}
