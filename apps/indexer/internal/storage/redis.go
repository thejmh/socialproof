package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// StateManager는 인덱서의 진행 상태를 캐싱하고 관리합니다.
type StateManager struct {
	client     *redis.Client
	logger     *slog.Logger
	chainID    int64
	startBlock int64
	stateKey   string
}

// NewStateManager는 확장성을 고려하여 ChainID를 포함한 네임스페이스를 구축합니다.
func NewStateManager(addr, password string, chainID, startBlock int64, logger *slog.Logger) (*StateManager, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("Redis 연결 실패: %w", err)
	}

	logger.Info("✅ Redis State Manager 연결 성공", "addr", addr)

	return &StateManager{
		client:     client,
		logger:     logger,
		chainID:    chainID,
		startBlock: startBlock,
		stateKey:   fmt.Sprintf("sp:indexer:v1:%d:state", chainID), // 다중 체인 대응 네임스페이스
	}, nil
}

// UpdateProgress는 Lua 스크립트를 사용하여 원자성(Atomicity)을 보장하며 상태를 갱신합니다.
// 워커들의 처리 순서가 섞여도 가장 높은 블록 번호만 커서로 저장되도록 보장합니다.
func (sm *StateManager) UpdateProgress(ctx context.Context, processedBlock, latestBlock int64) error {
	script := `
		local key = KEYS[1]
		local new_block = tonumber(ARGV[1])
		local latest_block = tonumber(ARGV[2])
		local start_block = tonumber(ARGV[3])
		local timestamp = ARGV[4]

		local current_last = tonumber(redis.call('HGET', key, 'last_block') or '0')
		local max_block = current_last

		-- 기존 커서보다 큰 블록이 들어왔을 때만 최댓값 갱신
		if new_block > current_last then
			redis.call('HSET', key, 'last_block', new_block)
			max_block = new_block
		end

		local progress = 0
		if latest_block > start_block then
			progress = ((max_block - start_block) / (latest_block - start_block)) * 100
			if progress > 100 then progress = 100 end
		end

		redis.call('HSET', key, 'latest_block', latest_block)
		redis.call('HSET', key, 'progress_percent', string.format("%.2f", progress))
		redis.call('HSET', key, 'updated_at', timestamp)

		return max_block
	`

	_, err := sm.client.Eval(ctx, script, []string{sm.stateKey},
		processedBlock,
		latestBlock,
		sm.startBlock,
		time.Now().Format(time.RFC3339),
	).Result()

	return err
}

// GetLastBlock은 구동 시점에 Redis에서 마지막 작업 블록을 가져옵니다.
func (sm *StateManager) GetLastBlock(ctx context.Context) (int64, error) {
	val, err := sm.client.HGet(ctx, sm.stateKey, "last_block").Int64()
	if err == redis.Nil {
		return sm.startBlock, nil // 기록이 없으면 StartBlock 반환
	}
	return val, err
}

func (sm *StateManager) Close() error {
	return sm.client.Close()
}

// RollbackProgress는 Reorg 감지 시 커서를 강제로 이전 블록으로 되돌립니다.
// 현재 저장된 값보다 작을 때만 업데이트하여 데이터 정합성을 보장합니다.
func (sm *StateManager) RollbackProgress(ctx context.Context, rollbackBlock int64) error {
	script := `
		local key = KEYS[1]
		local rollback_to = tonumber(ARGV[1])
		local current = tonumber(redis.call('HGET', key, 'last_block') or '0')

		-- 현재 커서보다 낮은 번호로만 롤백 허용 (강제 후진)
		if rollback_to < current then
			redis.call('HSET', key, 'last_block', rollback_to)
			redis.call('HSET', key, 'updated_at', ARGV[2])
			return 1
		end
		return 0
	`
	_, err := sm.client.Eval(ctx, script, []string{sm.stateKey},
		rollbackBlock,
		time.Now().Format(time.RFC3339),
	).Result()

	if err == nil {
		sm.logger.Warn("🔄 Redis 커서 롤백 완료", "to_block", rollbackBlock)
	}
	return err
}
