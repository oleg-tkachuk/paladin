package httpapi

import (
	"net/http"

	"paladin/internal/middleware"
	"paladin/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

type Server struct {
	engine *gin.Engine
}

func NewServer(mode string, log *zap.Logger, svc *service.ObjectsService, version, commit, buildTime string) *Server {
	gin.SetMode(mode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestID(log))
	r.Use(middleware.RequestLogger(log))

	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	r.GET("/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, VersionResponse{
			Service:   "paladin",
			Version:   version,
			Commit:    commit,
			BuildTime: buildTime,
		})
	})

	r.GET("/health/live", func(c *gin.Context) {
		log.Debug("Liveness check called")
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/health/ready", func(c *gin.Context) {
		log.Debug("Readiness check called")
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	v1 := r.Group("/v1")
	{
		v1.POST("/objects", createObjectHandler(svc))
		v1.GET("/objects/:id", getObjectHandler(svc))
		v1.POST("/objects/:id/complete", completeObjectHandler(svc))

		v1.POST("/multipart", initiateMultipartHandler(svc))
		v1.POST("/multipart/:upload_id/parts/:part_number/sign", signPartHandler(svc))
		v1.POST("/multipart/:upload_id/complete", completeMultipartHandler(svc))
		v1.POST("/multipart/:upload_id/abort", abortMultipartHandler(svc))
	}

	return &Server{engine: r}
}

func (s *Server) Handler() http.Handler { return s.engine }
