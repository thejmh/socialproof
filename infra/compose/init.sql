-- 기존 테이블이 있다면 날리고 완벽한 구조로 재창조합니다.
DROP TABLE IF EXISTS onchain_events CASCADE;

CREATE TABLE onchain_events (
    id SERIAL PRIMARY KEY,
    block_number BIGINT NOT NULL,
    tx_hash VARCHAR(66) NOT NULL,
    log_index INT NOT NULL DEFAULT 0,          -- [추가] 이벤트의 고유 순서
    event_type VARCHAR(255) NOT NULL,
    contract_address VARCHAR(42) NOT NULL,
    caller_address VARCHAR(42),
    raw_data JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    
    -- [핵심] SoT 1항 무결성 보장을 위한 복합 고유키 제약조건
    CONSTRAINT unique_tx_hash_log_index UNIQUE (tx_hash, log_index)
);

-- 1. 장애 발생 시 에러를 격리할 Dead Letter Queue 테이블 생성
CREATE TABLE IF NOT EXISTS failed_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),  -- 내장 함수로 교체!
    tx_hash CHAR(66) NOT NULL,
    log_index INT NOT NULL,
    block_number BIGINT NOT NULL,
    raw_log_json JSONB,
    error_reason TEXT,
    status VARCHAR(20) DEFAULT 'PENDING',
    CONSTRAINT unique_failed_tx_log UNIQUE (tx_hash, log_index)
);

CREATE INDEX IF NOT EXISTS idx_failed_events_status ON failed_events(status);

-- 2. 인덱서 상태 테이블 (그대로 유지)
CREATE TABLE IF NOT EXISTS indexer_state (
    contract_name VARCHAR(255) PRIMARY KEY,
    last_indexed_block BIGINT NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 인덱스 최적화
CREATE INDEX idx_onchain_events_block_number ON onchain_events(block_number);
CREATE INDEX idx_onchain_events_event_type ON onchain_events(event_type);

-- 1. 기존 테이블에 블록 해시 컬럼 추가
ALTER TABLE onchain_events ADD COLUMN block_hash VARCHAR(66);

-- 2. 리오그 감지 시 대량 삭제 성능을 위한 인덱스 추가
CREATE INDEX IF NOT EXISTS idx_events_block_num_lookup ON onchain_events(block_number);

-- 3. (선택) 인덱서 상태 테이블에 해시 기록용 컬럼 추가 (복구용)
ALTER TABLE indexer_state ADD COLUMN last_block_hash VARCHAR(66);