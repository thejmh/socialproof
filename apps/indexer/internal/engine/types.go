package engine

import "math/big"

// BlockTask는 소비자가 처리해야 할 작업 단위입니다.
type BlockTask struct {
	BlockNumber *big.Int
	RetryCount  int
}
