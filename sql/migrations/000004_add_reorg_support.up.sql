-- 1. 기존 테이블에 블록 해시 컬럼 추가
ALTER TABLE onchain_events ADD COLUMN block_hash VARCHAR(66);

-- 2. 리오그 감지 시 대량 삭제 성능을 위한 인덱스 추가
CREATE INDEX IF NOT EXISTS idx_events_block_num_lookup ON onchain_events(block_number);

-- 3. (선택) 인덱서 상태 테이블에 해시 기록용 컬럼 추가 (복구용)
ALTER TABLE indexer_state ADD COLUMN last_block_hash VARCHAR(66);