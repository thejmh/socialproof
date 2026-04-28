package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	eth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/hibiken/asynq"
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/internal/storage"
	"github.com/thejmh/socialproof/apps/indexer/pkg/decoder"
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"

	_ "github.com/lib/pq"
)

const (
	SafeConfirmations = int64(30)
)

type EventRecord struct {
	BlockNumber     int64
	TxHash          string
	BlockHash       string //  Reorg 검증을 위한 블록 해시
	LogIndex        int    //  멱등성 보장을 위한 필수 식별자
	EventType       string
	ContractAddress string
	CallerAddress   string
	RawData         map[string]interface{}
}

// 1. IndexerEngine 구조체에 쓰로틀링(Throttling)용 변수 추가
type IndexerEngine struct {
	target        config.TargetContract // 해당 엔진이 전담할 컨트랙트 정보
	client        ethereum.Client
	logger        *slog.Logger
	db            *sql.DB
	stateMgr      *storage.StateManager
	decoder       *decoder.UniversalDecoder
	asynqClient   *asynq.Client // 큐 적재기
	taskQueue     chan BlockTask
	workerCount   int
	startBlock    int64
	currentLatest atomic.Int64
	//  RPC 폭격 방지용 변수
	lastReorgCheck time.Time
	reorgMu        sync.Mutex
}

// 생성자에서 config.TargetContract를 받아 독립적으로 초기화합니다.
func NewIndexerEngine(
	target config.TargetContract,
	client ethereum.Client,
	db *sql.DB,
	stateMgr *storage.StateManager,
	univDecoder *decoder.UniversalDecoder,
	asynqClient *asynq.Client,
	logger *slog.Logger,
	cfg *config.Config,
) *IndexerEngine {
	// 컨트랙트별 시작 블록 우선순위 적용 (JSON 설정 > ENV 전역 설정)
	start := target.StartBlock
	if start == 0 {
		start = cfg.StartBlock
	}

	e := &IndexerEngine{
		target:      target,
		client:      client,
		db:          db,
		stateMgr:    stateMgr,
		decoder:     univDecoder,
		asynqClient: asynqClient,
		// 로그에 어떤 컨트랙트를 처리 중인지 태그 부착
		logger:      logger.With("contract", target.Name),
		taskQueue:   make(chan BlockTask, cfg.BackfillQueueSize),
		workerCount: cfg.WorkerCount,
		startBlock:  start,
	}
	e.currentLatest.Store(start)
	return e
}

func NewDB(connStr string, logger *slog.Logger, cfg *config.Config) *sql.DB {
	var db *sql.DB
	var err error

	for i := 0; i < 5; i++ {
		db, err = sql.Open("postgres", connStr)
		if err == nil && db.Ping() == nil {
			db.SetMaxOpenConns(cfg.DBMaxOpenConns)
			db.SetMaxIdleConns(cfg.DBMaxIdleConns)
			db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
			return db
		}
		time.Sleep(2 * time.Second)
	}
	return nil
}

func NewIndexer(connStr string) (*Indexer, error) {
	var db *sql.DB
	var err error
	for i := 0; i < 5; i++ {
		db, err = sql.Open("postgres", connStr)
		if err == nil && db.Ping() == nil {
			return &Indexer{db: db}, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("PostgreSQL 연결 최종 실패: %v", err)
}

type Indexer struct {
	db *sql.DB
}

func (i *Indexer) GetDB() *sql.DB {
	return i.db
}

// DB의 실제 최댓값을 기준으로 Reorg를 검증하도록 로직 변경
func (e *IndexerEngine) checkAndHandleReorg(ctx context.Context) (bool, error) {
	// 다중 워커의 RPC 융단폭격(DDoS) 방지
	// 3초 이내에 이미 다른 워커가 리오그 검사를 마쳤다면, RPC를 찌르지 않고 안전하다고 판단(Pass)합니다.
	e.reorgMu.Lock()
	if time.Since(e.lastReorgCheck) < 3*time.Second {
		e.reorgMu.Unlock()
		return false, nil
	}
	// 검사 시간 갱신
	e.lastReorgCheck = time.Now()
	e.reorgMu.Unlock()

	var dbBlockNum int64
	var dbHash string

	query := "SELECT block_number, block_hash FROM onchain_events WHERE contract_address = $1 ORDER BY block_number DESC LIMIT 1"
	// [주의] 다중 컨트랙트 환경이므로 WHERE 조건에 e.target.Address를 추가하는 것이 안전합니다!
	err := e.db.QueryRowContext(ctx, query, e.target.Address).Scan(&dbBlockNum, &dbHash)

	if err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("마지막 이벤트 블록 조회 실패: %w", err)
	}

	if dbHash == "" {
		return false, nil
	}

	rpcBlock, err := e.client.GetHTTP().BlockByNumber(ctx, big.NewInt(dbBlockNum))
	if err != nil {
		return false, fmt.Errorf("RPC 블록 조회 실패: %w", err)
	}
	rpcHash := rpcBlock.Hash().Hex()

	if dbHash != rpcHash {
		e.logger.Warn("⚠️ 체인 리오그(Reorg) 감지!", "block", dbBlockNum, "db_hash", dbHash, "rpc_hash", rpcHash)

		_, err = e.db.ExecContext(ctx, "DELETE FROM onchain_events WHERE block_number >= $1 AND contract_address = $2", dbBlockNum, e.target.Address)
		if err != nil {
			return false, fmt.Errorf("DB 롤백 실패: %w", err)
		}

		if e.stateMgr != nil {
			if err := e.stateMgr.RollbackProgress(ctx, dbBlockNum-1); err != nil {
				return false, fmt.Errorf("Redis 롤백 실패: %w", err)
			}
		}
		return true, nil
	}

	return false, nil
}

// tx_hash + log_index 멱등성 및 ON CONFLICT DO NOTHING 강제화 적용
func (e *IndexerEngine) SaveEvent(event EventRecord) error {
	if e.db == nil {
		return fmt.Errorf("데이터베이스 연결 객체가 초기화되지 않았습니다")
	}

	query := `
        INSERT INTO onchain_events (
            block_number, block_hash, tx_hash, log_index, event_type, contract_address, caller_address, raw_data
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        ON CONFLICT (tx_hash, log_index) DO NOTHING;
    `
	jsonData, _ := json.Marshal(event.RawData)
	_, err := e.db.Exec(query, event.BlockNumber, event.BlockHash, event.TxHash, event.LogIndex, event.EventType, event.ContractAddress, event.CallerAddress, jsonData)
	return err
}

// [신규] Asynq 태스크 핸들러 (에러 격리 & 재시도 로직)
func (e *IndexerEngine) HandleEventProcessTask(ctx context.Context, t *asynq.Task) error {
	var event EventRecord
	if err := json.Unmarshal(t.Payload(), &event); err != nil {
		e.saveToDLQ(event, "Payload Parse Error: "+err.Error())
		return nil // 치명적 에러 격리 완료, 재시도 중단
	}

	if err := e.SaveEvent(event); err != nil {
		retryCount, _ := asynq.GetRetryCount(ctx)
		maxRetry, _ := asynq.GetMaxRetry(ctx)

		if retryCount >= maxRetry {
			// 최대 재시도(5회) 초과 시 Dead Letter Queue 테이블로 추방
			e.saveToDLQ(event, fmt.Sprintf("Max Retries Reached DB Error: %v", err))
			return nil // 재시도 중단 후 안전하게 큐에서 제거
		}
		// 일반 에러 반환 시 Asynq가 자동으로 지수 백오프(Exponential Backoff) 재시도 수행
		return err
	}
	return nil
}

// [신규] Dead Letter Queue 적재 로직
func (e *IndexerEngine) saveToDLQ(event EventRecord, reason string) {
	if e.db == nil {
		return
	}

	query := `
        INSERT INTO failed_events (
            tx_hash, log_index, block_number, raw_log_json, error_reason, status
        ) VALUES ($1, $2, $3, $4, $5, 'PENDING')
        ON CONFLICT (tx_hash, log_index) DO NOTHING;
    `
	rawJSON, _ := json.Marshal(event.RawData)
	_, err := e.db.Exec(query, event.TxHash, event.LogIndex, event.BlockNumber, rawJSON, reason)
	if err != nil {
		e.logger.Error("DLQ 적재 최종 실패 (치명적 로직 에러)", "tx", event.TxHash, "error", err)
	} else {
		e.logger.Warn("⚠️ 이벤트를 안전하게 DLQ로 격리했습니다", "tx", event.TxHash, "log_index", event.LogIndex, "reason", reason)
	}
}

func (e *IndexerEngine) Start(ctx context.Context) {
	if e.stateMgr != nil {
		// Redis 상태 관리자도 컨트랙트별로 분리된 커서를 가져와야 함 (메서드 내에서 처리된다고 가정)
		lastSavedBlock, err := e.stateMgr.GetLastBlock(ctx)
		if err == nil && lastSavedBlock > e.startBlock {
			e.startBlock = lastSavedBlock
		}
	}
	e.logger.Info("🚀 이중 동기화 엔진 가동 시작",
		"address", e.target.Address, // 대상 주소 로깅
		"workers", e.workerCount,
		"start_from", e.startBlock,
		"safe_margin", SafeConfirmations)

	for i := 0; i < e.workerCount; i++ {
		go e.worker(ctx, i)
	}

	go e.historicalBackfill(ctx)
	go e.realtimeWatcher(ctx)
}

func (e *IndexerEngine) historicalBackfill(ctx context.Context) {
	e.logger.Info("과거 데이터 백필 시작")

	header, err := e.client.GetHTTP().HeaderByNumber(ctx, nil)
	if err != nil {
		e.logger.Error("백필: 최신 블록 조회 실패", "error", err)
		return
	}

	targetBlock := header.Number.Int64() - SafeConfirmations
	if targetBlock < 0 {
		targetBlock = 0
	}

	e.currentLatest.Store(targetBlock)

	cfg := config.New()
	batchSize := cfg.IdxBatchSize
	if batchSize == 0 {
		batchSize = 2000
	}

	for from := e.startBlock; from <= targetBlock; from += batchSize {
		select {
		case <-ctx.Done():
			return
		default:
			to := from + batchSize - 1
			if to > targetBlock {
				to = targetBlock
			}

			e.taskQueue <- BlockTask{
				FromBlock: big.NewInt(from),
				ToBlock:   big.NewInt(to),
			}

			if len(e.taskQueue) > cfg.BackfillQueueSize {
				time.Sleep(cfg.SleepDuration * 2)
			} else {
				time.Sleep(cfg.SleepDuration)
			}
		}
	}
}

func (e *IndexerEngine) realtimeWatcher(ctx context.Context) {
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
				continue
			}

			safeBlockNum := header.Number.Int64() - SafeConfirmations
			if safeBlockNum < 0 {
				safeBlockNum = 0
			}
			safeHeader := big.NewInt(safeBlockNum)

			e.currentLatest.Store(safeHeader.Int64())

			if lastBlock == nil || safeHeader.Cmp(lastBlock) > 0 {
				if lastBlock != nil {
					for i := new(big.Int).Add(lastBlock, big.NewInt(1)); i.Cmp(safeHeader) <= 0; i.Add(i, big.NewInt(1)) {
						e.taskQueue <- BlockTask{FromBlock: new(big.Int).Set(i), ToBlock: new(big.Int).Set(i)}
					}
				} else {
					e.taskQueue <- BlockTask{FromBlock: safeHeader, ToBlock: safeHeader}
				}
				lastBlock = safeHeader
			}
		}
	}
}

func (e *IndexerEngine) worker(ctx context.Context, id int) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-e.taskQueue:
			reorged, err := e.checkAndHandleReorg(ctx)
			if err != nil {
				e.logger.Error("Reorg 검사 중 오류", "err", err)
				time.Sleep(time.Second)
				continue
			}
			if reorged {
				continue
			}

			// 하드코딩된 주소 제거 -> target.Address 사용
			query := eth.FilterQuery{
				FromBlock: task.FromBlock,
				ToBlock:   task.ToBlock,
				Addresses: []common.Address{
					common.HexToAddress(e.target.Address),
				},
			}

			logs, err := e.client.GetHTTP().FilterLogs(ctx, query)
			if err != nil {
				continue
			}

			for _, vLog := range logs {
				eventName := "UNKNOWN_EVENT"
				rawDataMap := map[string]interface{}{
					"address": vLog.Address.Hex(),
					"topics":  vLog.Topics,
					"data":    common.Bytes2Hex(vLog.Data),
					"index":   vLog.Index,
				}

				if e.decoder != nil {
					parsedEventName, decodedArgs, err := e.decoder.DecodeEvent(vLog)
					if err == nil {
						eventName = parsedEventName
						rawDataMap["decoded_args"] = decodedArgs
					}
				}

				event := EventRecord{
					BlockNumber:     int64(vLog.BlockNumber),
					BlockHash:       vLog.BlockHash.Hex(),
					TxHash:          vLog.TxHash.Hex(),
					LogIndex:        int(vLog.Index),
					EventType:       eventName,
					ContractAddress: vLog.Address.Hex(),
					CallerAddress:   "",
					RawData:         rawDataMap,
				}

				payload, err := json.Marshal(event)
				if err == nil {
					asynqTask := asynq.NewTask("event:process", payload, asynq.MaxRetry(5))
					if _, err := e.asynqClient.Enqueue(asynqTask); err != nil {
						e.logger.Error("Worker: Asynq 큐 적재 실패", "tx", event.TxHash, "error", err)
					}
				}
			}

			if e.stateMgr != nil {
				latest := e.currentLatest.Load()
				e.stateMgr.UpdateProgress(ctx, task.ToBlock.Int64(), latest)
			}
		}
	}
}
