package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/hibiken/asynqmon"
	"github.com/thejmh/socialproof/apps/indexer/internal/api/handlers"
	"github.com/thejmh/socialproof/apps/indexer/internal/engine"
)

type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

func NewServer(port string, db *sql.DB, engines map[string]*engine.IndexerEngine, asynqOpt asynq.RedisClientOpt, logger *slog.Logger) *Server {
	if port == "" {
		port = "8080" // Default port
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	// Handlers 주입
	adminHandler := handlers.NewAdminHandler(db, engines, logger)

	// =====================================================================
	// 1. 관측 평면: Asynqmon Web UI (HTML 반환 영역)
	// API 트리와 완벽히 격리된 독립된 '/monitoring' 경로 사용
	// =====================================================================
	monRootPath := "/monitoring"
	mon := asynqmon.New(asynqmon.Options{
		RootPath:     monRootPath,
		RedisConnOpt: asynqOpt,
	})
	// 정적 리소스 및 SPA 라우팅을 위해 Any와 와일드카드 모두 등록
	router.Any(monRootPath, gin.WrapH(mon))
	router.Any(monRootPath+"/*any", gin.WrapH(mon))

	// =====================================================================
	// 2. 제어 평면: Admin REST API (순수 JSON 반환 영역)
	// =====================================================================
	v1 := router.Group("/api/v1/admin")
	{
		// 와일드카드 간섭 없이 순수하게 API로만 동작합니다.
		v1.GET("/sync-failures", adminHandler.GetSyncFailures)
		v1.POST("/reprocess/event/:id", adminHandler.ReprocessEvent)
		v1.POST("/reprocess/block-range", adminHandler.ReprocessBlockRange)
	}

	return &Server{
		httpServer: &http.Server{
			Addr:    ":" + port,
			Handler: router,
		},
		logger: logger.With("component", "api_bridge"),
	}
}

func (s *Server) Start() {
	s.logger.Info("🌐 API Bridge Server 가동 시작", "port", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		s.logger.Error("API Server 실행 중 오류 발생", "error", err)
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("🛑 API Bridge Server 종료 진행 중...")
	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctxTimeout)
}
