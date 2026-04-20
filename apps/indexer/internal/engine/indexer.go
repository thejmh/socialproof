package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"sync/atomic" // Lock-Free 갱신을 위함
	"time"

	eth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/internal/storage"
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"

	_ "github.com/lib/pq"
)

type EventRecord struct {
	BlockNumber     int64
	TxHash          string
	EventType       string
	ContractAddress string
	CallerAddress   string
	RawData         map[string]interface{}
}

type IndexerEngine struct {
	client        ethereum.Client
	logger        *slog.Logger
	db            *sql.DB
	stateMgr      *storage.StateManager // Redis 매니저 주입
	taskQueue     chan BlockTask
	workerCount   int
	startBlock    int64
	currentLatest atomic.Int64 // 워커들이 동시 접근 가능한 최신 블록 (락 프리)
}

func NewIndexerEngine(client ethereum.Client, db *sql.DB, stateMgr *storage.StateManager, logger *slog.Logger, cfg *config.Config) *IndexerEngine {
	e := &IndexerEngine{
		client:      client,
		db:          db,
		stateMgr:    stateMgr,
		logger:      logger,
		taskQueue:   make(chan BlockTask, cfg.BackfillQueueSize),
		workerCount: cfg.WorkerCount,
		startBlock:  cfg.StartBlock,
	}
	e.currentLatest.Store(cfg.StartBlock) // 초기 분모값 설정
	return e
}

func NewDB(connStr string, logger *slog.Logger) *sql.DB {
	var db *sql.DB
	var err error

	for i := 0; i < 5; i++ {
		db, err = sql.Open("postgres", connStr)
		if err == nil {
			err = db.Ping()
			if err == nil {
				logger.Info("✅ PostgreSQL 연결 성공")
				return db
			}
		}
		time.Sleep(2 * time.Second)
	}
	return nil
}

type Indexer struct {
	db *sql.DB
}

func NewIndexer(connStr string) (*Indexer, error) {
	var db *sql.DB
	var err error

	for i := 0; i < 5; i++ {
		db, err = sql.Open("postgres", connStr)
		if err == nil {
			err = db.Ping()
			if err == nil {
				return &Indexer{db: db}, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("PostgreSQL 연결 최종 실패: %v", err)
}

func (i *Indexer) GetDB() *sql.DB {
	return i.db
}

func (e *IndexerEngine) SaveEvent(event EventRecord) error {
	if e.db == nil {
		return fmt.Errorf("데이터베이스 연결 객체가 초기화되지 않았습니다(nil)")
	}

	query := `
        INSERT INTO onchain_events (
            block_number, tx_hash, event_type, contract_address, caller_address, raw_data
        ) VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (tx_hash) DO NOTHING;
    `

	jsonData, err := json.Marshal(event.RawData)
	if err != nil {
		return err
	}

	_, err = e.db.Exec(query,
		event.BlockNumber,
		event.TxHash,
		event.EventType,
		event.ContractAddress,
		event.CallerAddress,
		jsonData,
	)
	return err
}

func (e *IndexerEngine) Start(ctx context.Context) {
	// Redis에서 기존 진행 커서를 가져와 시작점을 동적으로 갱신
	lastSavedBlock, err := e.stateMgr.GetLastBlock(ctx)
	if err == nil && lastSavedBlock > e.startBlock {
		e.startBlock = lastSavedBlock
	}

	e.logger.Info("이중 동기화 엔진 가동", "workers", e.workerCount, "actual_start_from", e.startBlock)

	for i := 0; i < e.workerCount; i++ {
		go e.worker(ctx, i)
	}

	go e.realtimeWatcher(ctx)
	// 백필이 필요할 경우 주석을 풀고 실행합니다.
	// go e.historicalBackfill(ctx)
}

func (e *IndexerEngine) historicalBackfill(ctx context.Context) {
	e.logger.Info("과거 데이터 백필 시작")

	header, err := e.client.GetHTTP().HeaderByNumber(ctx, nil)
	if err != nil {
		e.logger.Error("백필: 최신 블록 조회 실패", "error", err)
		return
	}

	targetBlock := header.Number.Int64()
	e.currentLatest.Store(targetBlock) // 워커들을 위해 원자적 최신값 갱신

	cfg := config.New()
	e.logger.Info("백필 대상 블록 범위", "from", e.startBlock, "to", targetBlock)

	for i := e.startBlock; i <= targetBlock; i++ {
		select {
		case <-ctx.Done():
			return
		default:
			e.taskQueue <- BlockTask{BlockNumber: big.NewInt(i)}

			if i > e.startBlock && i%cfg.IdxBatchSize == 0 {
				if len(e.taskQueue) > cfg.BackfillQueueSize {
					time.Sleep(cfg.SleepDuration * 2)
				} else {
					time.Sleep(cfg.SleepDuration)
				}
			}
		}
	}
	e.logger.Info("과거 데이터 백필 작업 큐 투입 완료")
}

func (e *IndexerEngine) realtimeWatcher(ctx context.Context) {
	e.logger.Info("실시간 블록 모니터링 시작")
	var lastBlock *big.Int

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			header, err := e.client.GetHTTP().HeaderByNumber(ctx, nil)
			if err != nil {
				e.logger.Error("Watcher: 헤더 조회 실패", "error", err)
				continue
			}

			e.currentLatest.Store(header.Number.Int64()) // 실시간 분모값 갱신

			if lastBlock == nil || header.Number.Cmp(lastBlock) > 0 {
				if lastBlock != nil {
					for i := new(big.Int).Add(lastBlock, big.NewInt(1)); i.Cmp(header.Number) <= 0; i.Add(i, big.NewInt(1)) {
						e.taskQueue <- BlockTask{BlockNumber: new(big.Int).Set(i)}
					}
				} else {
					e.taskQueue <- BlockTask{BlockNumber: header.Number}
				}
				lastBlock = header.Number
				e.logger.Debug("실시간 블록 감지", "number", lastBlock.String())
			}
		}
	}
}

func (e *IndexerEngine) worker(ctx context.Context, id int) {
	e.logger.Info("Worker 가동", "id", id)
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-e.taskQueue:
			query := eth.FilterQuery{
				FromBlock: task.BlockNumber,
				ToBlock:   task.BlockNumber,
			}

			logs, err := e.client.GetHTTP().FilterLogs(ctx, query)
			if err != nil {
				e.logger.Error("Worker: 로그 필터링 실패", "block", task.BlockNumber, "error", err)
				continue
			}

			for _, vLog := range logs {
				event := EventRecord{
					BlockNumber:     int64(vLog.BlockNumber),
					TxHash:          vLog.TxHash.Hex(),
					EventType:       "ONCHAIN_EVENT",
					ContractAddress: vLog.Address.Hex(),
					RawData: map[string]interface{}{
						"address": vLog.Address.Hex(),
						"topics":  vLog.Topics,
						"data":    common.Bytes2Hex(vLog.Data),
						"index":   vLog.Index,
					},
				}

				if err := e.SaveEvent(event); err != nil {
					e.logger.Error("Worker: DB 저장 실패", "tx", event.TxHash, "error", err)
				}
			}

			// 작업 완료 후, Redis에 진행 상태 업데이트 (Lock-free)
			latest := e.currentLatest.Load()
			if err := e.stateMgr.UpdateProgress(ctx, task.BlockNumber.Int64(), latest); err != nil {
				e.logger.Error("Worker: Redis 상태 업데이트 실패", "block", task.BlockNumber, "error", err)
			}
		}
	}
}
