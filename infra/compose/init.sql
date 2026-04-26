CREATE TABLE onchain_events (
    id BIGSERIAL PRIMARY KEY,
    block_number BIGINT NOT NULL,
    tx_hash CHAR(66) NOT NULL,
    event_type VARCHAR(50) NOT NULL, -- 'ContractCreated', 'Approved', 'Settled'
    contract_address CHAR(42) NOT NULL,
    caller_address CHAR(42) NOT NULL,
    raw_data JSONB, -- 나중에 디코딩을 위해 원본 저장
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
-- 1. 온체인 이벤트 로그 (사실 기록용)
CREATE TABLE onchain_events (
    id BIGSERIAL PRIMARY KEY,
    block_number BIGINT NOT NULL,
    tx_hash CHAR(66) NOT NULL,
    event_type VARCHAR(50) NOT NULL, -- 'ContractCreated', 'Approved', 'Settled'
    contract_address CHAR(42) NOT NULL,
    caller_address CHAR(42) NOT NULL,
    raw_data JSONB, -- 나중에 디코딩을 위해 원본 저장
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 2. 정산 및 승인 결과 (정제된 상태)
CREATE TABLE settlements (
    id SERIAL PRIMARY KEY,
    tx_hash CHAR(66) UNIQUE NOT NULL,
    recipient CHAR(42) NOT NULL,
    amount NUMERIC(78, 0), -- 256-bit uint 대응용
    status VARCHAR(20),    -- 'PENDING', 'SUCCESS', 'FAILED'
    settled_at TIMESTAMP WITH TIME ZONE
);

-- 3. Attestation Reference (신뢰의 근거)
CREATE TABLE attestations (
    uid CHAR(66) PRIMARY KEY, -- EAS(Ethereum Attestation Service) 등의 UID
    attester CHAR(42) NOT NULL,
    subject CHAR(42) NOT NULL,
    ref_schema_id CHAR(66),
    data_payload JSONB,
    indexed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 4. Trust Engine 계산 결과 (오프체인 계산값)
CREATE TABLE trust_scores (
    subject_address CHAR(42) PRIMARY KEY,
    total_score DECIMAL(18, 8) DEFAULT 0,
    level INTEGER DEFAULT 1,
    calculation_log JSONB, -- 어떤 근거로 이 점수가 나왔는지 요약
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- 인덱스 설정 (조회 성능 최적화)
CREATE INDEX idx_events_type ON onchain_events(event_type);
CREATE INDEX idx_trust_score_rank ON trust_scores(total_score DESC);

ALTER TABLE onchain_events ADD CONSTRAINT unique_tx_hash UNIQUE (tx_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_tx_hash ON onchain_events(tx_hash);

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