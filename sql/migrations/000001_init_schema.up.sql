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
CREATE INDEX idx_events_block ON onchain_events(block_number);
CREATE INDEX idx_events_type ON onchain_events(event_type);
CREATE INDEX idx_trust_score_rank ON trust_scores(total_score DESC);