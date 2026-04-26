-- 1. log_index 컬럼 추가 (복합키 멱등성 보장용)
ALTER TABLE onchain_events ADD COLUMN log_index INT NOT NULL DEFAULT 0;

-- 2. 기존 단일 트랜잭션 해시 UNIQUE 제약 및 인덱스 제거
ALTER TABLE onchain_events DROP CONSTRAINT IF EXISTS unique_tx_hash;
DROP INDEX IF EXISTS idx_unique_tx_hash;

-- 3. SoT 1항 충족: tx_hash + log_index 복합키 UNIQUE 제약 추가
ALTER TABLE onchain_events ADD CONSTRAINT unique_tx_log UNIQUE (tx_hash, log_index);
CREATE UNIQUE INDEX idx_unique_tx_log ON onchain_events(tx_hash, log_index);

-- 4. 기존 B-Tree 블록 인덱스 제거 및 BRIN 인덱스 도입 (쓰기 성능 극대화)
DROP INDEX IF EXISTS idx_events_block;
CREATE INDEX idx_events_block_brin ON onchain_events USING BRIN (block_number);