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
	// 2. EVM 클라이언트 생성
	client, err := ethereum.NewClient(ctx, cfg.EthRPCURL, cfg.EthWSURL, logger)
	if err != nil {
		logger.Error("시스템 초기화 실패", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	// 3. 엔진 초기화 및 실행
	indexer := engine.NewIndexerEngine(client, logger, cfg.WorkerCount, cfg.StartBlock)

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
