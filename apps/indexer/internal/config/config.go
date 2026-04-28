package config

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/caarlos0/env/v10"
)

// [신규] 동적 컨트랙트 설정 구조체
type TargetContract struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	StartBlock int64  `json:"start_block"`
	AbiString  string `json:"abi_string"`
}

type Config struct {
	EthRPCURL         string        `env:"ETH_RPC_URL,required"`
	EthWSURL          string        `env:"ETH_WS_URL,required"`
	ChainID           int64         `env:"CHAIN_ID" envDefault:"1"`
	RedisAddr         string        `env:"REDIS_ADDR" envDefault:"localhost:6379"`
	RedisPassword     string        `env:"REDIS_PASSWORD" envDefault:""`
	LogLevel          string        `env:"LOG_LEVEL" envDefault:"debug"`
	StartBlock        int64         `env:"IDX_START_BLOCK" envDefault:"0"` // 전역 기본 시작 블록
	WorkerCount       int           `env:"IDX_WORKER_COUNT" envDefault:"10"`
	IdxBatchSize      int64         `env:"IDX_BATCH_SIZE" envDefault:"5"`
	SleepDuration     time.Duration `env:"IDX_SLEEP_DURATION" envDefault:"500ms"`
	BackfillQueueSize int           `env:"IDX_BACKFILL_QUEUE_SIZE" envDefault:"100"`
	TaskQueueSize     int           `env:"IDX_TASK_QUEUE_SIZE" envDefault:"500"`
	DatabaseURL       string        `env:"DATABASE_URL,required"`

	DBMaxOpenConns    int           `env:"DB_MAX_OPEN_CONNS" envDefault:"50"`
	DBMaxIdleConns    int           `env:"DB_MAX_IDLE_CONNS" envDefault:"20"`
	DBConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME" envDefault:"5m"`

	Contracts []TargetContract // [신규] 파싱된 다중 컨트랙트 배열
}

func New() *Config {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		log.Fatalf("환경 설정 로드 실패: %v", err)
	}

	contractsPath := os.Getenv("CONTRACTS_FILE_PATH")
	if contractsPath == "" {
		contractsPath = "contracts.json" // 기본값
	}

	file, err := os.Open(contractsPath)
	if err != nil {
		log.Printf("⚠️ contracts.json 파일을 찾을 수 없습니다. 다중 컨트랙트 라우팅이 비활성화됩니다. err: %v\n", err)
	} else {
		defer file.Close()
		if err := json.NewDecoder(file).Decode(&cfg.Contracts); err != nil {
			log.Fatalf("contracts.json 파싱 실패: %v", err)
		}
	}

	return cfg
}
