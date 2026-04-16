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
	// 0. .env 파일 로드
	if err := godotenv.Load(); err != nil {
		log.Println("알림: .env 파일을 찾을 수 없습니다. OS 환경 변수를 직접 참조합니다.")
	}

	// 1. 설정 및 로거 초기화
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

	// ---------------------------------------------------------
	// [추가/수정] 2. PostgreSQL 연결 (엔진에 넘겨주기 전 미리 연결)
	// ---------------------------------------------------------
	// NewIndexer는 내부적으로 5번 재시도하며 연결을 시도합니다.
	dbInstance, err := engine.NewIndexer(cfg.DatabaseURL)
	if err != nil {
		logger.Error("데이터베이스 초기화 실패", "error", err)
		os.Exit(1)
	}
	// Indexer 구조체 내부의 실제 *sql.DB 추출 (GetDB 메서드가 있다고 가정)
	// 만약 메서드가 없다면 dbInstance.DB 등으로 직접 접근 가능하게 필드를 확인하세요.
	sqlDB := dbInstance.GetDB()
	defer sqlDB.Close() // 프로그램 종료 시 안전하게 DB 연결 해제
	// ---------------------------------------------------------

	// 3. EVM 클라이언트 생성
	client, err := ethereum.NewClient(ctx, cfg.EthRPCURL, cfg.EthWSURL, logger)
	if err != nil {
		logger.Error("EVM 클라이언트 초기화 실패", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	// 2. 이제 객체를 인자로 전달 (타입 일치!)
	indexer := engine.NewIndexerEngine(client, sqlDB, logger, cfg)

	// 5. 엔진 실행
	go indexer.Start(ctx)

	// 6. OS 신호 감시
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("SocialProof 인덱서 시스템 가동 중...",
		"rpc", cfg.EthRPCURL,
		"db", "connected",
		"log_level", logLevel.String(),
	)

	sig := <-sigChan
	logger.Info("셧다운 신호 수신", "signal", sig.String())

	cancel() // 고루틴 종료 알림

	time.Sleep(1 * time.Second)
	logger.Info("인덱서가 안전하게 종료되었습니다.")
}
