package ethereum

import (
	"context"
	"fmt"
	"log/slog"

	eth "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Client 인터페이스는 테스트 시 Mocking을 용이하게 합니다.
type Client interface {
	GetHTTP() *ethclient.Client
	GetWS() *ethclient.Client
	Close()
}

type evmClient struct {
	httpConn *ethclient.Client
	wsConn   *ethclient.Client
	logger   *slog.Logger
}

func NewClient(ctx context.Context, httpURL, wsURL string, logger *slog.Logger) (Client, error) {
	// 1. HTTP 연결
	httpConn, err := ethclient.DialContext(ctx, httpURL)
	if err != nil {
		return nil, fmt.Errorf("HTTP RPC 연결 실패: %w", err)
	}

	// 2. WebSocket 연결 (실시간 이벤트 리스닝용)
	wsConn, err := ethclient.DialContext(ctx, wsURL)
	if err != nil {
		logger.Warn("WebSocket 연결 실패, 실시간 기능이 제한될 수 있습니다", "error", err)
	}

	return &evmClient{
		httpConn: httpConn,
		wsConn:   wsConn,
		logger:   logger,
	}, nil
}

func (c *evmClient) GetHTTP() *ethclient.Client { return c.httpConn }
func (c *evmClient) GetWS() *ethclient.Client   { return c.wsConn }

func (c *evmClient) Close() {
	if c.httpConn != nil {
		c.httpConn.Close()
	}
	if c.wsConn != nil {
		c.wsConn.Close()
	}
}

func (c *evmClient) FilterLogs(ctx context.Context, q eth.FilterQuery) ([]types.Log, error) {
	return c.httpConn.FilterLogs(ctx, q)
}
