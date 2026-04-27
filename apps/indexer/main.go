package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq" // Asynq 추가
	"github.com/joho/godotenv"
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/internal/engine"
	"github.com/thejmh/socialproof/apps/indexer/internal/storage"
	"github.com/thejmh/socialproof/apps/indexer/pkg/decoder"
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"
)

const easAttestedABI = `[{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"recipient","type":"address"},{"indexed":true,"internalType":"address","name":"attester","type":"address"},{"indexed":false,"internalType":"bytes32","name":"uid","type":"bytes32"},{"indexed":true,"internalType":"bytes32","name":"schema","type":"bytes32"}],"name":"Attested","type":"event"}]`

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

	dbInstance, err := engine.NewIndexer(cfg.DatabaseURL)
	if err != nil {
		logger.Error("데이터베이스 초기화 실패", "error", err)
		os.Exit(1)
	}
	sqlDB := dbInstance.GetDB()
	defer sqlDB.Close()

	stateMgr, err := storage.NewStateManager(cfg.RedisAddr, cfg.RedisPassword, cfg.ChainID, cfg.StartBlock, logger)
	if err != nil {
		logger.Error("Redis 초기화 실패", "error", err)
		os.Exit(1)
	}
	defer stateMgr.Close()

	client, err := ethereum.NewClient(ctx, cfg.EthRPCURL, cfg.EthWSURL, logger)
	if err != nil {
		logger.Error("EVM 클라이언트 초기화 실패", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	univDecoder, err := decoder.NewUniversalDecoder(easAttestedABI)
	if err != nil {
		logger.Error("디코더 초기화 실패", "error", err)
		os.Exit(1)
	}
	logger.Info("✅ 범용 동적 디코더 엔진 초기화 성공")

	// [신규] Asynq 클라이언트 및 서버 초기화
	asynqOpt := asynq.RedisClientOpt{Addr: cfg.RedisAddr, Password: cfg.RedisPassword}
	asynqClient := asynq.NewClient(asynqOpt)
	defer asynqClient.Close()

	// 파라미터에 asynqClient 추가 주입
	indexer := engine.NewIndexerEngine(client, sqlDB, stateMgr, univDecoder, asynqClient, logger, cfg)

	// [신규] Asynq 백그라운드 서버 가동 (소비자 역할)
	asynqSrv := asynq.NewServer(asynqOpt, asynq.Config{
		Concurrency: cfg.WorkerCount, // 큐 처리 워커 수 설정
	})
	mux := asynq.NewServeMux()
	mux.HandleFunc("event:process", indexer.HandleEventProcessTask) // 큐 라우팅

	go func() {
		if err := asynqSrv.Run(mux); err != nil {
			logger.Error("❌ Asynq 서버 실행 실패", "error", err)
		}
	}()
	defer asynqSrv.Stop()

	go indexer.Start(ctx)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("SocialProof 인덱서 시스템 가동 중...",
		"rpc", cfg.EthRPCURL,
		"db", "connected",
		"redis_asynq", "connected",
		"log_level", logLevel.String(),
	)

	sig := <-sigChan
	logger.Info("셧다운 신호 수신", "signal", sig.String())
	cancel()
	time.Sleep(1 * time.Second)
	logger.Info("인덱서가 안전하게 종료되었습니다.")
}
