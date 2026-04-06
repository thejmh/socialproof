package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/internal/engine"
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"
)

func main() {
	// 0. .env 파일 로드 (가장 먼저 실행!)
	err := godotenv.Load()
	// 이 함수가 # 주석을 다 걸러내고 환경 변수로 등록해줍니다.
	if err != nil {
		log.Println(".env 파일을 찾을 수 없습니다. 시스템 환경 변수를 사용합니다.")
	}

	// 1. 설정 및 로거 초기화
	cfg := config.New()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger.Info(cfg.EthWSURL)
	// 2. EVM 클라이언트 생성
	client, err := ethereum.NewClient(ctx, cfg.EthRPCURL, cfg.EthWSURL, logger)
	if err != nil {
		logger.Error("시스템 초기화 실패", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	// 3. 엔진 초기화 및 실행
	indexer := engine.NewIndexerEngine(client, logger)

	// Graceful Shutdown 처리
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		logger.Info("셧다운 신호 수신, 정리 시작...")
		cancel()
	}()

	if err := indexer.StartSync(ctx); err != nil {
		logger.Error("엔진 실행 중단", "error", err)
	}
}
