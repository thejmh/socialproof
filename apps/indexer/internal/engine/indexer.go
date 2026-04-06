package engine

import (
	"context"
	"log/slog"
	"math/big"
	"time"

	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"
)

type IndexerEngine struct {
	client ethereum.Client
	logger *slog.Logger
	// taskQueue는 asyncio.Queue와 같은 역할을 하는 Go 채널입니다.
	taskQueue   chan BlockTask
	workerCount int
}

func NewIndexerEngine(client ethereum.Client, logger *slog.Logger, workerCount int) *IndexerEngine {
	return &IndexerEngine{
		client:      client,
		logger:      logger,
		taskQueue:   make(chan BlockTask, 100), // 버퍼 크기 100의 큐
		workerCount: workerCount,
	}
}

// Start는 생산자와 소비자를 모두 가동합니다.
func (e *IndexerEngine) Start(ctx context.Context) {
	e.logger.Info("파이프라인 가동 시작", "workers", e.workerCount)

	// 1. 소비자(Workers) 가동
	for i := 0; i < e.workerCount; i++ {
		go e.worker(ctx, i)
	}

	// 2. 생산자(Producer) 가동
	e.producer(ctx)
}

// producer는 최신 블록을 감지하여 큐에 넣습니다.
func (e *IndexerEngine) producer(ctx context.Context) {
	var lastBlock *big.Int

	for {
		select {
		case <-ctx.Done():
			return
		default:
			header, err := e.client.GetHTTP().HeaderByNumber(ctx, nil)
			if err != nil {
				e.logger.Error("생산자: 블록 헤더 조회 실패", "error", err)
				time.Sleep(5 * time.Second)
				continue
			}

			currBlock := header.Number
			if lastBlock == nil || currBlock.Cmp(lastBlock) > 0 {
				e.logger.Info("생산자: 새 블록 발견", "number", currBlock.String())

				// 큐에 작업 투입 (Non-blocking 방식을 권장하지만 여기선 간단히 처리)
				e.taskQueue <- BlockTask{BlockNumber: new(big.Int).Set(currBlock)}
				lastBlock = currBlock
			}

			time.Sleep(2 * time.Second)
		}
	}
}

// worker는 큐에서 작업을 꺼내 실제 데이터를 처리합니다.
func (e *IndexerEngine) worker(ctx context.Context, id int) {
	e.logger.Debug("소비자 워커 시작", "worker_id", id)

	for {
		select {
		case <-ctx.Done():
			return
		case task := <-e.taskQueue:
			e.logger.Info("소비자: 작업 처리 중", "worker_id", id, "block", task.BlockNumber.String())

			// 실제 온체인 데이터 상세 조회 로직
			block, err := e.client.GetHTTP().BlockByNumber(ctx, task.BlockNumber)
			if err != nil {
				e.logger.Error("소비자: 블록 상세 조회 실패", "block", task.BlockNumber, "error", err)
				// 실패 시 재시도 로직 등을 추가할 수 있음
				continue
			}

			// TODO: 여기서 PostgreSQL 및 Redis 저장 로직 호출
			e.logger.Info("소비자: 처리 완료", "block", block.Number().String(), "tx_count", len(block.Transactions()))
		}
	}
}
