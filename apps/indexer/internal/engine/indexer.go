package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/thejmh/socialproof/apps/indexer/pkg/ethereum"
)

type IndexerEngine struct {
	client ethereum.Client
	logger *slog.Logger
}

func NewIndexerEngine(client ethereum.Client, logger *slog.Logger) *IndexerEngine {
	return &IndexerEngine{
		client: client,
		logger: logger,
	}
}

// StartSync는 블록 체인을 추적하며 데이터를 처리합니다.
func (e *IndexerEngine) StartSync(ctx context.Context) error {
	e.logger.Info("온체인 인덱싱 엔진 시작...")

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			// 최신 블록 넘버 확인
			header, err := e.client.GetHTTP().HeaderByNumber(ctx, nil)
			if err != nil {
				e.logger.Error("최신 블록 조회 실패", "error", err)
				time.Sleep(5 * time.Second)
				continue
			}

			e.logger.Info("새로운 블록 감지", "number", header.Number.String())

			// TODO: 여기에 Asynq Task를 발행하거나 DB에 저장하는 로직이 들어갑니다.

			time.Sleep(12 * time.Second) // 이더리움 평균 블록 타임 대기
		}
	}
}
