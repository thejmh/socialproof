package engine

import (
	"context"
	"log/slog"
	"math/big"
	"time"

	//TODO :필터가동 시 해제
	// eth "github.com/ethereum/go-ethereum"
	// "github.com/ethereum/go-ethereum/common"
	"github.com/thejmh/socialproof/apps/indexer/internal/config"
	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"
)

type IndexerEngine struct {
	client ethereum.Client
	logger *slog.Logger
	// taskQueue는 asyncio.Queue와 같은 역할을 하는 Go 채널입니다.
	taskQueue   chan BlockTask
	workerCount int
	startBlock  int64
}

func NewIndexerEngine(client ethereum.Client, logger *slog.Logger, workerCount int, startBlock int64) *IndexerEngine {
	return &IndexerEngine{
		client:      client,
		logger:      logger,
		taskQueue:   make(chan BlockTask, config.New().TaskQueueSize), // 대량 백필을 위해 큐 크기 확장
		workerCount: workerCount,
		startBlock:  startBlock,
	}
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
	go e.historicalBackfill(ctx)
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
func (e *IndexerEngine) worker(ctx context.Context, id int) {
	e.logger.Info("Worker 가동", "id", id)
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-e.taskQueue:

			// 1. 블록 전체를 가져오는 대신 로그만 필터링 (가벼움!)

			//TODO : 실제로 관심 있는 이벤트가 있다면, 여기서 필터링 조건을 넣어야 합니다.
			// query := eth.FilterQuery{
			// 	FromBlock: task.BlockNumber,
			// 	ToBlock:   task.BlockNumber,
			// 	Addresses: []common.Address{common.HexToAddress("0x...")},
			// }

			// logs, err := e.client.GetHTTP().FilterLogs(ctx, query)
			// if err != nil {
			// 	e.logger.Error("Worker: 로그 필터링 실패", "block", task.BlockNumber, "error", err)
			// 	continue
			// }

			// 1-1 관심 있는 이벤트가 없더라도 블록 전체를 처리해야 하는 경우
			block, err := e.client.GetHTTP().BlockByNumber(ctx, task.BlockNumber)
			if err != nil {
				e.logger.Error("소비자: 블록 상세 조회 실패", "block", task.BlockNumber, "error", err)
				// 실패 시 재시도 로직 등을 추가할 수 있음
				continue
			}
			//TODO : DB 저장은 여기서 수행해야 합니다. 로그가 없더라도 블록 자체의 정보가 중요할 수 있기 때문입니다.

			e.logger.Info("소비자: 처리 완료", "block", block.Number().String(), "tx_count", len(block.Transactions()))

			// 2. 관련 로그가 있을 때만 처리
			// for _, vLog := range logs {
			// 	e.logger.Info("중요 이벤트 발견!", "tx", vLog.TxHash.Hex(), "block", vLog.BlockNumber)
			// 	// 여기서만 DB 저장 수행!
			// }
		}
	}
}
