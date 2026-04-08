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
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"
)

func main() {
	// 0. .env 파일 로드 (가장 먼저 실행!)
	if err := godotenv.Load(); err != nil {
		log.Println("알림: .env 파일을 찾을 수 없습니다. OS 환경 변수를 직접 참조합니다.(하이브리드 설정 로드)")
	}

	// 1. 설정 및 로거 초기화
	cfg := config.New()
	if cfg.EthRPCURL == "" {
		log.Fatal("치명적 에러: ETH_RPC_URL이 설정되지 않았습니다.")
	}
	var logLevel = slog.LevelInfo // 기본값

	// .env나 config에서 LOG_LEVEL=debug 라고 설정했다면
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}

	// HandlerOptions를 통해 최소 로그 레벨을 지정합니다.
	opts := &slog.HandlerOptions{
		Level: logLevel,
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, opts))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 2. EVM 클라이언트 생성
	client, err := ethereum.NewClient(ctx, cfg.EthRPCURL, cfg.EthWSURL, logger)
	if err != nil {
		logger.Error("시스템 초기화 실패", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	// 3. 엔진 초기화 , DB 연결을 포함하여 인덱서 엔진을 생성합니다.
	indexer := engine.NewIndexerEngine(client, cfg.DatabaseURL, logger, cfg)

	go indexer.Start(ctx)

	// 4. [추가] OS 신호를 기다리는 채널 생성
	sigChan := make(chan os.Signal, 1)
	// 인터럽트(Ctrl+C)와 터미네이트(Kill) 신호를 감시합니다.
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("시스템 가동 중... 정지하려면 Ctrl+C를 누르세요.")

	// 5. [핵심] 여기서 신호가 올 때까지 코드 실행이 "차단(Block)"됩니다.
	sig := <-sigChan

	logger.Info("셧다운 신호 수신", "signal", sig.String())

	// 6. 정리 작업 (Context 취소로 고루틴들에게 종료 알림)
	cancel()

	// 잠시 대기하여 고루틴들이 정리할 시간을 줍니다.
	time.Sleep(1 * time.Second)
	logger.Info("인덱서가 안전하게 종료되었습니다.")
}
