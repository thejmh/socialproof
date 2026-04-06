package config

import (
	"log"

	"github.com/caarlos0/env/v10"
)

type Config struct {
	EthRPCURL string `env:"ETH_RPC_URL,required"`
	EthWSURL  string `env:"ETH_WS_URL,required"`
	ChainID   int64  `env:"CHAIN_ID" envDefault:"1"`
	RedisAddr string `env:"REDIS_ADDR" envDefault:"localhost:6379"`
	LogLevel  string `env:"LOG_LEVEL" envDefault:"info"`
}

func New() *Config {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		log.Fatalf("환경 설정 로드 실패: %v", err)
	}
	return cfg
}
