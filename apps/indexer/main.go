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
	"github.com/thejmh/socialproof/apps/indexer/internal/api" // 신규 의존성 추가
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/internal/engine"
	"github.com/thejmh/socialproof/apps/indexer/internal/storage"
	"github.com/thejmh/socialproof/apps/indexer/pkg/decoder"
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("알림: .env 파일을 찾을 수 없습니다. OS 환경 변 직접 참조합니다.")
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

	var wg sync.WaitGroup
	// [수정] 다중 엔진의 컨트롤(라우팅)을 위해 Map 형태로 관리합니다.
	activeEnginesMap := make(map[string]*engine.IndexerEngine)

	for _, contract := range cfg.Contracts {
		stateMgr, err := storage.NewStateManager(
			cfg.RedisAddr,
			cfg.RedisPassword,
			cfg.ChainID,
			contract.StartBlock,
			contract.Name,
			logger.With("contract", contract.Name),
		)
		if err != nil {
			logger.Error("Redis 초기화 실패", "contract", contract.Name, "error", err)
			continue
		}
		defer stateMgr.Close()

		var univDecoder *decoder.UniversalDecoder
		if contract.AbiString != "" {
			univDecoder, err = decoder.NewUniversalDecoder(contract.AbiString)
			if err != nil {
				logger.Error("디코더 초기화 실패", "contract", contract.Name, "error", err)
			}
		}

		idxEngine := engine.NewIndexerEngine(contract, client, sqlDB, stateMgr, univDecoder, asynqClient, logger, cfg)
		activeEnginesMap[contract.Name] = idxEngine
	}

	if len(activeEnginesMap) == 0 {
		logger.Error("실행할 컨트랙트 엔진이 없습니다. contracts.json을 확인하세요.")
		os.Exit(1)
	}

	asynqSrv := asynq.NewServer(asynqOpt, asynq.Config{
		Concurrency: cfg.WorkerCount * len(activeEnginesMap),
	})
	mux := asynq.NewServeMux()

	// Asynq 라우팅: Map에 등록된 첫번째 엔진(어느 엔진이든 DB 저장 메커니즘은 동일함) 할당
	var firstEngine *engine.IndexerEngine
	for _, e := range activeEnginesMap {
		firstEngine = e
		break
	}
	mux.HandleFunc("event:process", firstEngine.HandleEventProcessTask)

	go func() {
		if err := asynqSrv.Run(mux); err != nil {
			logger.Error("❌ Asynq 서버 실행 실패", "error", err)
		}
	}()
	defer asynqSrv.Stop()

	// 1. 모든 인덱서 루프 엔진 가동
	for name, eng := range activeEnginesMap {
		wg.Add(1)
		go func(e *engine.IndexerEngine, n string) {
			defer wg.Done()
			e.Start(ctx)
		}(eng, name)
	}

	// 2. API Bridge (Control Plane) 서버 가동 및 엔진 맵 주입
	apiPort := os.Getenv("API_PORT")
	if apiPort == "" {
		apiPort = "8080"
	}
	apiSrv := api.NewServer(apiPort, sqlDB, activeEnginesMap, asynqOpt, logger)
	go apiSrv.Start()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	logger.Info("🌐 SocialProof 이중 동기화 엔진 & API Bridge 가동 중...",
		"rpc", cfg.EthRPCURL,
		"active_engines", len(activeEnginesMap),
	)

	// Graceful Shutdown 파이프라인
	sig := <-sigChan
	logger.Info("셧다운 신호 수신, 우아한 종료(Graceful Shutdown)를 시작합니다.", "signal", sig.String())

	// API HTTP 서버 최우선 Drain
	if err := apiSrv.Shutdown(ctx); err != nil {
		logger.Error("API 서버 강제 종료 됨", "error", err)
	}

	cancel()  // 워커 루프에 컨텍스트 종료 시그널 전달
	wg.Wait() // 모든 고루틴 완료 대기

	logger.Info("시스템이 안전하게 종료되었습니다.")
}
