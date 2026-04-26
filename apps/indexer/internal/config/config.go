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
	RedisPassword     string        `env:"REDIS_PASSWORD" envDefault:""`
	LogLevel          string        `env:"LOG_LEVEL" envDefault:"debug"`
	StartBlock        int64         `env:"IDX_START_BLOCK" envDefault:"0"`
	WorkerCount       int           `env:"IDX_WORKER_COUNT" envDefault:"10"` // 튜닝됨
	IdxBatchSize      int64         `env:"IDX_BATCH_SIZE" envDefault:"5"`
	SleepDuration     time.Duration `env:"IDX_SLEEP_DURATION" envDefault:"500ms"`
	BackfillQueueSize int           `env:"IDX_BACKFILL_QUEUE_SIZE" envDefault:"100"`
	TaskQueueSize     int           `env:"IDX_TASK_QUEUE_SIZE" envDefault:"500"`
	DatabaseURL       string        `env:"DATABASE_URL,required"`

	// [추가] DB 커넥션 풀 최적화
	DBMaxOpenConns    int           `env:"DB_MAX_OPEN_CONNS" envDefault:"50"`
	DBMaxIdleConns    int           `env:"DB_MAX_IDLE_CONNS" envDefault:"20"`
	DBConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME" envDefault:"5m"`
}

func New() *Config {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		log.Fatalf("환경 설정 로드 실패: %v", err)
	}
	return cfg
}
