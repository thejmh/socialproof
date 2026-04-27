-- 1. UUID 확장이 없을 경우 생성 (failed_events PK 용도)
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- 2. 장애 발생 시 에러를 격리할 Dead Letter Queue 테이블 생성
CREATE TABLE IF NOT EXISTS failed_events (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tx_hash CHAR(66) NOT NULL,
    log_index INT NOT NULL,
    block_number BIGINT NOT NULL,
    raw_log_json JSONB,
    error_reason TEXT,
    status VARCHAR(20) DEFAULT 'PENDING',
    CONSTRAINT unique_failed_tx_log UNIQUE (tx_hash, log_index)
);

CREATE INDEX IF NOT EXISTS idx_failed_events_status ON failed_events(status);

-- 3. (옵션) 수동 재처리 등을 위해 상태를 저장하는 테이블
CREATE TABLE IF NOT EXISTS indexer_state (
    contract_name VARCHAR(255) PRIMARY KEY,
    last_indexed_block BIGINT NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);