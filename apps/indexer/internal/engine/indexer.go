package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"sync/atomic"
	"time"

	eth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/internal/storage"
	"github.com/thejmh/socialproof/apps/indexer/pkg/decoder" // 디코더 임포트
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
	stateMgr      *storage.StateManager
	decoder       *decoder.UniversalDecoder // [추가] 디코더 의존성
	taskQueue     chan BlockTask
	workerCount   int
	startBlock    int64
	currentLatest atomic.Int64
}

// [변경] 생성자에 UniversalDecoder 파라미터 추가
func NewIndexerEngine(client ethereum.Client, db *sql.DB, stateMgr *storage.StateManager, univDecoder *decoder.UniversalDecoder, logger *slog.Logger, cfg *config.Config) *IndexerEngine {
	e := &IndexerEngine{
		client:      client,
		db:          db,
		stateMgr:    stateMgr,
		decoder:     univDecoder,
		logger:      logger,
		taskQueue:   make(chan BlockTask, cfg.BackfillQueueSize),
		workerCount: cfg.WorkerCount,
		startBlock:  cfg.StartBlock,
	}
	e.currentLatest.Store(cfg.StartBlock)
	return e
}

// [변경] NewDB 팩토리 함수에 Config 객체 주입을 통한 Connection Pool 동적 튜닝
func NewDB(connStr string, logger *slog.Logger, cfg *config.Config) *sql.DB {
	var db *sql.DB
	var err error

	for i := 0; i < 5; i++ {
		db, err = sql.Open("postgres", connStr)
		if err == nil && db.Ping() == nil {
			// 시스템 병목 해소 공식(SBR) 반영: 커넥션 풀 명시적 세팅
			db.SetMaxOpenConns(cfg.DBMaxOpenConns)
			db.SetMaxIdleConns(cfg.DBMaxIdleConns)
			db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

			logger.Info("✅ PostgreSQL 연결 및 Connection Pool 최적화 성공",
				"MaxOpen", cfg.DBMaxOpenConns,
				"MaxIdle", cfg.DBMaxIdleConns)
			return db
		}
		logger.Warn("PostgreSQL 연결 재시도 중...", "attempt", i+1, "error", err)
		time.Sleep(2 * time.Second)
	}

	logger.Error("PostgreSQL 연결 최종 실패", "error", err)
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

// SaveEvent 로직 유지
func (e *IndexerEngine) SaveEvent(event EventRecord) error {
	if e.db == nil {
		return fmt.Errorf("데이터베이스 연결 객체가 초기화되지 않았습니다")
	}

	query := `
        INSERT INTO onchain_events (
            block_number, tx_hash, event_type, contract_address, caller_address, raw_data
        ) VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (tx_hash) DO UPDATE SET
            block_number = EXCLUDED.block_number,
            event_type = EXCLUDED.event_type,
            raw_data = onchain_events.raw_data || EXCLUDED.raw_data
        WHERE 
            onchain_events.block_number IS DISTINCT FROM EXCLUDED.block_number OR
            onchain_events.event_type IS DISTINCT FROM EXCLUDED.event_type OR
            onchain_events.raw_data IS DISTINCT FROM EXCLUDED.raw_data;
    `

	jsonData, err := json.Marshal(event.RawData)
	if err != nil {
		return err
	}

	_, err = e.db.Exec(query, event.BlockNumber, event.TxHash, event.EventType, event.ContractAddress, event.CallerAddress, jsonData)
	return err
}

func (e *IndexerEngine) Start(ctx context.Context) {
	if e.stateMgr != nil {
		lastSavedBlock, err := e.stateMgr.GetLastBlock(ctx)
		if err == nil && lastSavedBlock > e.startBlock {
			e.startBlock = lastSavedBlock
		}
	}
	e.logger.Info("🚀 이중 동기화 엔진 가동 시작",
		"workers", e.workerCount,
		"start_from", e.startBlock)

	// 2. 소비자(Worker) 가동: 설정된 워커 개수만큼 병렬 실행
	for i := 0; i < e.workerCount; i++ {
		go e.worker(ctx, i)
	}

	// 3. 백필 생산자 가동
	go e.historicalBackfill(ctx)

	// 4. 실시간 생산자 가동
	go e.realtimeWatcher(ctx)
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
	batchSize := cfg.IdxBatchSize
	if batchSize == 0 {
		batchSize = 2000 // 기본 안전 마진 (예: RPC 노드 최대 허용 범위)
	}

	e.logger.Info("백필 대상 블록 범위", "from", e.startBlock, "to", targetBlock, "batch_size", batchSize)

	// [핵심 변경] 블록 1개씩 큐에 넣는 대신, batchSize 단위로 묶어서(Chunk) 큐에 투입
	for from := e.startBlock; from <= targetBlock; from += batchSize {
		select {
		case <-ctx.Done():
			return
		default:
			to := from + batchSize - 1
			if to > targetBlock {
				to = targetBlock
			}

			// 구간 단위(From~To)로 워커에게 작업 하달
			e.taskQueue <- BlockTask{
				FromBlock: big.NewInt(from),
				ToBlock:   big.NewInt(to),
			}

			// 큐에 작업이 많이 쌓여있으면 백프레셔(대기) 작동 - 원본 로직 유지
			if len(e.taskQueue) > cfg.BackfillQueueSize {
				time.Sleep(cfg.SleepDuration * 2)
			} else {
				time.Sleep(cfg.SleepDuration)
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
						e.taskQueue <- BlockTask{FromBlock: new(big.Int).Set(i), ToBlock: new(big.Int).Set(i)}
					}
				} else {
					e.taskQueue <- BlockTask{FromBlock: header.Number, ToBlock: header.Number}
				}
				lastBlock = header.Number
				e.logger.Debug("실시간 블록 감지", "number", lastBlock.String())
			}
		}
	}
}

// [변경] 워커 내부에서 디코딩 로직 수행 및 병합
func (e *IndexerEngine) worker(ctx context.Context, id int) {
	e.logger.Info("Worker 가동", "id", id)
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-e.taskQueue:
			query := eth.FilterQuery{
				FromBlock: task.FromBlock,
				ToBlock:   task.ToBlock, Addresses: []common.Address{
					// Sepolia EAS 공식 컨트랙트 주소 (메인넷의 경우 메인넷 EAS 주소로 변경)
					common.HexToAddress("0xC2679fBD37d54388Ce493F1DB75320D236e1815e"),
				},
			}

			// 주의: 클라이언트는 타입 호환성 문제가 없도록 go-ethereum의 패키지 사용
			logs, err := e.client.GetHTTP().FilterLogs(ctx, query)
			if err != nil {
				e.logger.Error("Worker: 로그 필터링 실패", "from_block", task.FromBlock, "to_block", task.ToBlock, "error", err)
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

				// [혁신] 디코더가 존재할 경우 로그를 해석 시도
				if e.decoder != nil {
					parsedEventName, decodedArgs, err := e.decoder.DecodeEvent(vLog)
					if err == nil {
						eventName = parsedEventName
						rawDataMap["decoded_args"] = decodedArgs // 성공 시 해시맵에 병합
						e.logger.Debug("이벤트 디코딩 성공", "event", eventName)
					} else {
						// 우아한 실패: 에러가 나더라도 죽지 않고 RawData만 저장
						e.logger.Warn("이벤트 디코딩 실패 (Raw 저장 진행)", "tx", vLog.TxHash.Hex(), "error", err)
					}
				}

				event := EventRecord{
					BlockNumber:     int64(vLog.BlockNumber),
					TxHash:          vLog.TxHash.Hex(),
					EventType:       eventName, // 동적으로 파싱된 이름 적용
					ContractAddress: vLog.Address.Hex(),
					CallerAddress:   "",
					RawData:         rawDataMap,
				}

				if err := e.SaveEvent(event); err != nil {
					e.logger.Error("Worker: DB 저장 실패", "tx", event.TxHash, "error", err)
				}
			}

			if e.stateMgr != nil {
				latest := e.currentLatest.Load()
				if err := e.stateMgr.UpdateProgress(ctx, task.FromBlock.Int64(), latest); err != nil {
					e.logger.Error("Worker: Redis 상태 업데이트 실패", "from_block", task.FromBlock, "to_block", task.ToBlock, "error", err)
				}
			}
		}
	}
}
