package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/internal/engine"
	"github.com/thejmh/socialproof/apps/indexer/internal/storage"
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("알림: .env 파일을 찾을 수 없습니다. OS 환경 변수를 직접 참조합니다.")
	}

	cfg := config.New()
	if cfg.EthRPCURL == "" {
		log.Fatal("치명적 에러: ETH_RPC_URL이 설정되지 않았습니다.")
	}

	var logLevel = slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{Level: logLevel}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, opts))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. PostgreSQL 연결 초기화
	dbInstance, err := engine.NewIndexer(cfg.DatabaseURL)
	if err != nil {
		logger.Error("데이터베이스 초기화 실패", "error", err)
		os.Exit(1)
	}
	sqlDB := dbInstance.GetDB()
	defer sqlDB.Close()

	// 2. Redis StateManager 초기화 및 주입 준비
	stateMgr, err := storage.NewStateManager(cfg.RedisAddr, cfg.RedisPassword, cfg.ChainID, cfg.StartBlock, logger)
	if err != nil {
		logger.Error("Redis 초기화 실패", "error", err)
		os.Exit(1)
	}
	defer stateMgr.Close()

	// 3. EVM 클라이언트 생성
	client, err := ethereum.NewClient(ctx, cfg.EthRPCURL, cfg.EthWSURL, logger)
	if err != nil {
		logger.Error("EVM 클라이언트 초기화 실패", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	// 4. 의존성 주입을 통한 인덱서 엔진 생성 (DB, Redis, Client 주입)
	indexer := engine.NewIndexerEngine(client, sqlDB, stateMgr, logger, cfg)

	// 5. 엔진 실행
	go indexer.Start(ctx)

	// 6. OS 신호 감시를 통한 그레이스풀 셧다운
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("SocialProof 인덱서 시스템 가동 중...",
		"rpc", cfg.EthRPCURL,
		"db", "connected",
		"redis", "connected",
		"log_level", logLevel.String(),
	)

	sig := <-sigChan
	logger.Info("셧다운 신호 수신", "signal", sig.String())

	cancel() // 진행 중인 작업 취소 및 고루틴 종료 알림

	time.Sleep(1 * time.Second) // 워커 정리 대기 시간
	logger.Info("인덱서가 안전하게 종료되었습니다.")
}
