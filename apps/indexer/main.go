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
	"github.com/thejmh/socialproof/apps/indexer/pkg/decoder"
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"
)

// [테스트용 대리망] EAS (Ethereum Attestation Service)의 Attested 이벤트 ABI
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

	// 1. PostgreSQL 연결 초기화
	dbInstance, err := engine.NewIndexer(cfg.DatabaseURL)
	if err != nil {
		logger.Error("데이터베이스 초기화 실패", "error", err)
		os.Exit(1)
	}
	sqlDB := dbInstance.GetDB()
	defer sqlDB.Close()

	// 2. Redis StateManager 초기화
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

	// 4. [혁신 적용] 유니버설 디코더 초기화 (EAS ABI 주입)
	univDecoder, err := decoder.NewUniversalDecoder(easAttestedABI)
	if err != nil {
		logger.Error("디코더 초기화 실패", "error", err)
		os.Exit(1)
	}
	logger.Info("✅ 범용 동적 디코더 엔진 초기화 성공")

	// 5. 의존성 주입을 통한 인덱서 엔진 생성 (디코더 포함)
	indexer := engine.NewIndexerEngine(client, sqlDB, stateMgr, univDecoder, logger, cfg)

	// 6. 엔진 실행
	go indexer.Start(ctx)

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
	cancel()
	time.Sleep(1 * time.Second)
	logger.Info("인덱서가 안전하게 종료되었습니다.")
}
