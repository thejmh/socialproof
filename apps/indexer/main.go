package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/hibiken/asynq"
	"github.com/joho/godotenv"
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/internal/engine"
	"github.com/thejmh/socialproof/apps/indexer/internal/storage"
	"github.com/thejmh/socialproof/apps/indexer/pkg/decoder"
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

	dbInstance, err := engine.NewIndexer(cfg.DatabaseURL)
	if err != nil {
		logger.Error("데이터베이스 초기화 실패", "error", err)
		os.Exit(1)
	}
	sqlDB := dbInstance.GetDB()
	defer sqlDB.Close()

	client, err := ethereum.NewClient(ctx, cfg.EthRPCURL, cfg.EthWSURL, logger)
	if err != nil {
		logger.Error("EVM 클라이언트 초기화 실패", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	asynqOpt := asynq.RedisClientOpt{Addr: cfg.RedisAddr, Password: cfg.RedisPassword}
	asynqClient := asynq.NewClient(asynqOpt)
	defer asynqClient.Close()

	// 단일 엔진 생성이 아닌, 다중 엔진 스포닝
	var wg sync.WaitGroup
	var activeEngines []*engine.IndexerEngine

	for _, contract := range cfg.Contracts {
		// 1. 컨트랙트별 독립적인 StateManager (커서 키 분리)
		// 참고: storage.NewStateManager 내부에서 contract.Name 을 접두사로 사용하도록 수정 권장
		stateMgr, err := storage.NewStateManager(
			cfg.RedisAddr,
			cfg.RedisPassword,
			cfg.ChainID,
			contract.StartBlock,
			contract.Name, // <--- 이 부분이 추가되었습니다!
			logger.With("contract", contract.Name),
		)
		if err != nil {
			logger.Error("Redis 초기화 실패", "contract", contract.Name, "error", err)
			continue
		}
		defer stateMgr.Close()

		// 2. 컨트랙트별 독립적인 Decoder 생성
		var univDecoder *decoder.UniversalDecoder
		if contract.AbiString != "" {
			univDecoder, err = decoder.NewUniversalDecoder(contract.AbiString)
			if err != nil {
				logger.Error("디코더 초기화 실패", "contract", contract.Name, "error", err)
				// 디코더가 없어도 생데이터 수집을 위해 계속 진행할 수 있음
			}
		}

		// 3. 엔진 생성 및 보관
		idxEngine := engine.NewIndexerEngine(contract, client, sqlDB, stateMgr, univDecoder, asynqClient, logger, cfg)
		activeEngines = append(activeEngines, idxEngine)
	}

	if len(activeEngines) == 0 {
		logger.Error("실행할 컨트랙트 엔진이 없습니다. contracts.json을 확인하세요.")
		os.Exit(1)
	}

	// [공용] Asynq 소비자 서버 가동 (여러 엔진이 넣은 작업을 처리)
	asynqSrv := asynq.NewServer(asynqOpt, asynq.Config{
		Concurrency: cfg.WorkerCount * len(activeEngines), // 엔진 수에 비례하여 워커 증가
	})
	mux := asynq.NewServeMux()

	// 편의상 첫 번째 엔진의 Handler를 사용 (어느 엔진이든 DB 저장 로직은 동일함)
	mux.HandleFunc("event:process", activeEngines[0].HandleEventProcessTask)

	go func() {
		if err := asynqSrv.Run(mux); err != nil {
			logger.Error("❌ Asynq 서버 실행 실패", "error", err)
		}
	}()
	defer asynqSrv.Stop()

	// [실행] 구성된 모든 엔진을 동시에 가동
	for _, eng := range activeEngines {
		wg.Add(1)
		go func(e *engine.IndexerEngine) {
			defer wg.Done()
			e.Start(ctx)
		}(eng)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("🌐 SocialProof 다중 컨트랙트 인덱서 시스템 가동 중...",
		"rpc", cfg.EthRPCURL,
		"active_engines", len(activeEngines),
	)

	sig := <-sigChan
	logger.Info("셧다운 신호 수신", "signal", sig.String())
	cancel()
	wg.Wait() // 모든 엔진이 종료될 때까지 대기
	logger.Info("인덱서가 안전하게 종료되었습니다.")
}
