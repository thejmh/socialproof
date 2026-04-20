package config

import (
	"log"
	"time"

	"github.com/caarlos0/env/v10"
)

type Config struct {
	EthRPCURL         string        `env:"ETH_RPC_URL,required"`
	EthWSURL          string        `env:"ETH_WS_URL,required"`
	ChainID           int64         `env:"CHAIN_ID" envDefault:"1"`
	RedisAddr         string        `env:"REDIS_ADDR" envDefault:"localhost:6379"`
	RedisPassword     string        `env:"REDIS_PASSWORD" envDefault:""` // 추가: Redis 비밀번호
	LogLevel          string        `env:"LOG_LEVEL" envDefault:"debug"`
	StartBlock        int64         `env:"IDX_START_BLOCK" envDefault:"0"` // 인덱싱 시작점
	WorkerCount       int           `env:"IDX_WORKER_COUNT" envDefault:"2"`
	IdxBatchSize      int64         `env:"IDX_BATCH_SIZE" envDefault:"5"`           // 한 번에 처리할 블록 수
	SleepDuration     time.Duration `env:"IDX_SLEEP_DURATION" envDefault:"500ms"`   // 백필 시 대기 시간 (ms)
	BackfillQueueSize int           `env:"IDX_BACKFILL_QUEUE_SIZE" envDefault:"10"` // 인덱싱 작업 큐 크기
	TaskQueueSize     int           `env:"IDX_TASK_QUEUE_SIZE" envDefault:"100"`    // 실제 작업을 담는 큐 크기
	DatabaseURL       string        `env:"DATABASE_URL,required"`                   // PostgreSQL 연결 문자열
}

func New() *Config {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		log.Fatalf("환경 설정 로드 실패: %v", err)
	}
	return cfg
}
