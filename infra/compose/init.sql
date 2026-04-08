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
ALTER TABLE onchain_events ADD CONSTRAINT unique_tx_hash UNIQUE (tx_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_unique_tx_hash ON onchain_events(tx_hash);