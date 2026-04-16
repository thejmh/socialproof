package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	eth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"

	// Postgres 드라이버
	_ "github.com/lib/pq"
)

// EventRecord는 DB에 저장할 데이터 구조체입니다.
type EventRecord struct {
	BlockNumber     int64
	TxHash          string
	EventType       string
	ContractAddress string
	CallerAddress   string
	RawData         map[string]interface{}
}

// NewIndexer는 DB 연결을 초기화하는 생성자입니다.
type IndexerEngine struct {
	client      ethereum.Client
	logger      *slog.Logger
	db          *sql.DB // 핵심: 이 객체가 nil이면 안 됩니다.
	taskQueue   chan BlockTask
	workerCount int
	startBlock  int64
}

func NewIndexerEngine(client ethereum.Client, db *sql.DB, logger *slog.Logger, cfg *config.Config) *IndexerEngine {
	return &IndexerEngine{
		client:      client,
		db:          db, // 주입받은 객체를 그대로 할당
		logger:      logger,
		taskQueue:   make(chan BlockTask, cfg.BackfillQueueSize),
		workerCount: cfg.WorkerCount,
		startBlock:  cfg.StartBlock,
	}
}

// DB 연결을 초기화하는 함수입니다. 연결이 실패할 경우 nil을 반환합니다.
func NewDB(connStr string, logger *slog.Logger) *sql.DB {
	// DB 연결 시도 (재시도 로직 포함)
	var db *sql.DB
	var err error

	for i := 0; i < 5; i++ { // 최대 5번 재시도
		db, err = sql.Open("postgres", connStr)
		if err == nil {
			err = db.Ping() // 실제 연결 확인
			if err == nil {
				logger.Info("✅ PostgreSQL 연결 성공")
				return db
			}
		}
		//e.logger.Error("⚠️ DB 연결 실패 (재시도 %d/5): %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	return nil
}

// NewIndexer는 main.go에서 호출하며, 연결된 Indexer 객체를 반환합니다.
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

// SaveEvent는 온체인 이벤트를 onchain_events 테이블에 기록합니다.
func (e *IndexerEngine) SaveEvent(event EventRecord) error {
	// [안전장치] nil 포인터 패닉 방지
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

// Start는 생산자와 소비자를 모두 가동합니다.
func (e *IndexerEngine) Start(ctx context.Context) {
	e.logger.Info("이중 동기화 엔진 가동", "workers", e.workerCount, "start_from", e.startBlock)

	// 1. 소비자(Workers) 가동
	for i := 0; i < e.workerCount; i++ {
		go e.worker(ctx, i)
	}

	// 2. 실시간 감시자(Real-time Watcher) 시작
	go e.realtimeWatcher(ctx)

	// 3. 과거 데이터 백필(Historical Backfill) 시작
	//go e.historicalBackfill(ctx)
}

// Indexer 구조체는 DB 연결 관리를 위해 별도로 유지할 수 있습니다.
type Indexer struct {
	db *sql.DB
}

func (i *Indexer) GetDB() *sql.DB {
	return i.db
}

// historicalBackfill은 시작 지점부터 현재 블록까지 빠르게 채웁니다.
func (e *IndexerEngine) historicalBackfill(ctx context.Context) {
	e.logger.Info("과거 데이터 백필 시작")

	header, err := e.client.GetHTTP().HeaderByNumber(ctx, nil)
	if err != nil {
		e.logger.Error("백필: 최신 블록 조회 실패", "error", err)
		return
	}

	targetBlock := header.Number.Int64()
	cfg := config.New()
	e.logger.Info("백필 대상 블록 범위", "from", e.startBlock, "to", targetBlock)

	for i := e.startBlock; i <= targetBlock; i++ {
		select {
		case <-ctx.Done():
			return
		default:
			e.taskQueue <- BlockTask{BlockNumber: big.NewInt(i)}
			e.logger.Debug("백필 블록 투입", "number", i)

			if i > e.startBlock && i%cfg.IdxBatchSize == 0 {
				e.logger.Debug("백필: 배치 투입 완료, 잠시 대기...",
					"current", i,
					"target", targetBlock,
					"queue_size", len(e.taskQueue),
				)

				// 큐가 너무 꽉 찼다면 소비자가 처리할 시간을 더 줍니다.
				if len(e.taskQueue) > cfg.BackfillQueueSize { // 큐 전체 크기
					time.Sleep(cfg.SleepDuration * 2)
				} else {
					time.Sleep(cfg.SleepDuration)
				}
			}
		}
	}
	e.logger.Info("과거 데이터 백필 작업 큐 투입 완료")
}

// realtimeWatcher는 새로운 블록이 생성될 때마다 큐에 넣습니다.
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

			if lastBlock == nil || header.Number.Cmp(lastBlock) > 0 {
				if lastBlock != nil {
					// 누락된 블록이 있다면 사이를 메워줌 (Gap Filling)
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

// worker는 큐에서 블록 번호를 받아 처리하는 소비자입니다.
func (e *IndexerEngine) worker(ctx context.Context, id int) {
	e.logger.Info("Worker 가동", "id", id)
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-e.taskQueue:
			// 1. 블록 전체를 가져오는 대신 로그만 필터링 (가벼움!)
			query := eth.FilterQuery{
				FromBlock: task.BlockNumber,
				ToBlock:   task.BlockNumber,
				// Addresses: []common.Address{common.HexToAddress("0x...")},
			}

			logs, err := e.client.GetHTTP().FilterLogs(ctx, query)
			if err != nil {
				e.logger.Error("Worker: 로그 필터링 실패", "block", task.BlockNumber, "error", err)
				continue
			}

			// 1-1 관심 있는 이벤트가 없더라도 블록 전체를 처리해야 하는 경우
			// block, err := e.client.GetHTTP().BlockByNumber(ctx, task.BlockNumber)
			// if err != nil {
			// 	e.logger.Error("소비자: 블록 상세 조회 실패", "block", task.BlockNumber, "error", err)
			// 	// 실패 시 재시도 로직 등을 추가할 수 있음
			// 	continue
			// }

			for _, vLog := range logs {
				event := EventRecord{
					BlockNumber:     int64(vLog.BlockNumber),
					TxHash:          vLog.TxHash.Hex(),
					EventType:       "ONCHAIN_EVENT", // TODO: ABI 파싱 후 실제 Event Name 주입
					ContractAddress: vLog.Address.Hex(),
					// CallerAddress는 로그만으로는 알 수 없으므로 필요 시 Transaction 객체 조회 필요
					CallerAddress: "",
					RawData: map[string]interface{}{
						"address": vLog.Address.Hex(),
						"topics":  vLog.Topics,
						"data":    common.Bytes2Hex(vLog.Data),
						"index":   vLog.Index,
					},
				}

				if err := e.SaveEvent(event); err != nil {
					e.logger.Error("Worker: DB 저장 실패", "tx", event.TxHash, "error", err)
				} else {
					e.logger.Debug("Worker: 이벤트 저장 성공", "tx", event.TxHash)
				}
			}

			//e.logger.Info("소비자: 처리 완료", "block", block.Number().String(), "tx_count", len(block.Transactions()))
		}
	}
}
