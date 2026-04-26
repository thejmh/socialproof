[역할]
너는  프로젝트 신규설계AI가 아니라 기존 구조를 보존하는 유지보수 AI다.
후술할 SoT문서는 프로젝트 진행 전 과정에서 엄중하게 지켜져야하는 사항이다.

[SoT 마스터문서]
# [SOT] SocialProof Master Document

## 1. Project Concept & Philosophy
* **정의**: SocialProof는 Web2 SNS 영향력을 검증 가능한 자격증명(VC)으로 바꾸고, 온체인 협업 이력과 결합해 인플루언서의 신뢰 등급(Trust Index)을 산출하는 평판 프로토콜이다.
* **설계 철학**:
  1. **Attestation-First**: 평판 기록은 스마트 컨트랙트 상태가 아닌 Attestation(증명) 기반으로 기록하여 철회/분쟁 관리를 용이하게 한다.
  2. **설명 가능한 AI/엔진**: Trust Index 모델은 블랙박스가 아니며, 사용자에게 점수의 근거(예: "최근 30일 게시 일관성 가점")를 투명하게 제공해야 한다.
  3. **온오프체인 분리**: 계약 생성, 정산 승인, Attestation과 같은 '사실'만 온체인(Polygon Amoy 등 EVM)에 기록하고, 무거운 점수 계산은 오프체인(Go 백엔드)이 담당한다.
  4. **Closed Loop MVP**: 처음부터 멀티플랫폼을 시도하지 않고, 'YouTube 연동 → 점수 계산 → 캠페인 정산 → 평판 갱신'이라는 단일 폐쇄 루프를 완성하는 것에 집중한다.

## 2. Architecture & Technical Standard
* **백엔드 (Core Logic)**: Go언어 중심 (동시성, 성능, 배포 단순성 목적).
  * HTTP API: `gin-gonic/gin`.
  * DB / SQL: `jackc/pgx`, `sqlc`, `golang-migrate`.
  * Job Queue: `hibiken/asynq` (Redis 기반).
  * EVM 연동: `go-ethereum`.
* **프론트엔드 (Client & Wallet UX)**: TypeScript 생태계 활용.
  * Web App: `Next.js`, `Tailwind CSS`, `shadcn/ui`.
  * Wallet & State: `wagmi`, `viem`, `@tanstack/react-query`, `zod`.
* **스마트 컨트랙트 (On-chain Fact)**: Solidity.
  * 개발 및 테스트: `Foundry`.
  * 표준: `OpenZeppelin Contracts`.
* **인프라**: PostgreSQL, Redis, S3-compatible storage, EVM RPC.

## 3. Storage & Repo Path Strategy
* **저장소 전략**: Monorepo 채택.
* **경로(Path) 매핑**:
  * `/apps/web/`: Next.js 프론트엔드
  * `/apps/api/`: Go API 게이트웨이
  * `/apps/worker/`: Go 백그라운드 잡 (Social API 수집)
  * `/apps/indexer/`: Go 체인 인덱서 (EVM 이벤트 리스너)
  * `/contracts/`: Foundry 기반 스마트 컨트랙트 (src, script, test)
  * `/packages/`: 공통 타입(shared-types), UI, ABI 모음
  * `/sql/`: DB 마이그레이션 및 SQLC 쿼리

## 4. Key Features & Implementation Status (Sprints)
* **Sprint 0 (기반)**: Monorepo 설정, Go API 및 Next.js 부팅, 지갑 서명(nonce 기반) 로그인.
* **Sprint 1 (수집)**: YouTube 연동, 채널 소유권 검증, `VerifiedSocialAccountCredential` (VC) 첫 발급. 수동 재동기화 기반 수집.
* **Sprint 2 (점수)**: Trust Engine 구현, Trust Index(총점, 하위 축 점수, 설명) 계산 및 대시보드 UI.
* **Sprint 3 (에스크로)**: `CampaignEscrow.sol` 작성, 브랜드 캠페인 생성/예치, 인플루언서 지원/제출 및 승인 정산 흐름.
* **Sprint 4 (평판 반영)**: 정산 후 광고주 Attestation 발행, Go 인덱서를 통한 체인 이벤트(`PaymentReleased`, `AttestationIssued`) 수집 및 점수 재계산.
* **Sprint 5 (확장/마감)**: Trust-based Whitelist(최소 점수 캠페인 필터링), 상세 툴팁, 데모 시나리오 완성.

## 5. Database Schema Details
* **users**: `id` (UUID), `wallet_address`, `role`.
* **profiles**: `user_id`, `display_name`, `category`.
* **social_accounts**: `platform`, `handle`, `verification_status`, `access_token_ref` (DB 평문 저장 금지).
* **social_metric_snapshots**: `follower_count`, `avg_views_30d`, `engagement_rate_30d`, `normalized_json`.
* **credentials**: `credential_type`, `issuer`, `subject_did`, `metadata_json`.
* **trust_score_snapshots**: `score_total`, `confidence`, 5개 축 하위 점수, `explanation_json`.
* **campaigns / applications / submissions**: 에스크로 계약 매핑 정보 및 승인 상태.
* **attestation_records**: `rating`, `delivered_on_time`, `recontract_intent`.

## 6. Algorithms: Trust Engine & Index
* **점수 계산 공식**:
  `Total = 100 * (0.35 * Influence + 0.30 * Reliability + 0.20 * Authenticity + 0.10 * Onchain + 0.05 * Freshness) * Confidence`
* **축별 정의**:
  * `Influence`: 조회수, ER, 게시 빈도 (도달력과 반응)
  * `Reliability`: 납기 준수, 승인율, 분쟁 이력
  * `Authenticity`: 팔로워/조회수 괴리 보정
  * `Onchain`: Attestation 수, 정산 성공률
  * `Freshness`: 최근 활동 기준 최신성
* **Time Decay (시간 감쇠)**: 오래된 데이터는 가중치를 줄임. `decayed_weight = exp(-lambda * age_days)` (lambda = ln(2) / half_life_days). 게시물 30일, 비즈니스 90일 반감기 적용.
* **Confidence 패널티**: 검증된 플랫폼이 부족하거나, 표본 부족, API 수집 실패 시 총점을 보수적으로 하향.

## 7. Class Structure, Interfaces & Design Patterns
* **Design Patterns**: 
  * Adapter Pattern: 외부 SNS API 연동 (`SocialAdapter`).
  * Event-driven Projection: 온체인 이벤트를 DB Projection으로 동기화 (`On-chain Indexer`).
* **Go Interfaces**:
  * `SocialAdapter`: `ExchangeCode()`, `FetchProfile()`, `FetchRecentMetrics()`, `VerifyOwnership()`
  * `TrustEngine`: `RecomputeUserScore(ctx, userID)`
  * `AttestationWriter`: `IssueCampaignAttestation()`
* **Solidity Interface (`ICampaignEscrow`)**:
  * `createCampaign(influencer, amount, deadline) payable returns (id)`
  * `submitProofHash(id, proofHash)`
  * `approveAndRelease(id)`
  * `rejectSubmission(id, reason)`

## 8. Troubleshooting & Anti-patterns
* **안티패턴 1**: 처음부터 멀티플랫폼(Instagram, TikTok 동시 연동), 멀티체인, ZK, DAO 등을 한번에 도입하려 하는 것. 실패 확률이 높으므로 MVP는 YouTube + EVM Testnet 단일 루프에 집중할 것.
* **안티패턴 2**: OAuth 토큰을 DB에 평문으로 저장하는 것. 반드시 KMS 또는 앱 레벨 암호화를 적용할 것.
* **안티패턴 3**: 모든 평가 데이터와 제출물 원본을 온체인에 기록하는 것. 비용 낭비이므로, 원본은 오브젝트 스토리지에 넣고 온체인에는 해시(proofHash)와 Attestation Reference만 기록할 것.
* **해결 전략 (TikTok/API 제한)**: 외부 API 실패 잦을 시 `Confidence` 축을 낮춰 점수를 조절하고, 초기엔 Webhook 대신 온디맨드 수동 재동기화를 통해 복잡도를 낮출 것.

# [SOT] SocialProof - On-chain Indexer Master Document

## 1. Module Concept & Philosophy
* **정의**: On-chain Indexer는 EVM 네트워크(예: Polygon Amoy)에서 발생하는 스마트 컨트랙트 상태 변화(이벤트)를 오프체인 백엔드 DB(PostgreSQL)로 동기화하는 백그라운드 데몬 서비스이다.
* **설계 철학**:
  1. **Event-driven Projection**: 온체인 데이터는 읽기/검색이 무겁고 느리므로, 모든 온체인 사실(Fact)은 인덱서를 통해 오프체인 DB의 `escrow_projection` 등으로 투영(Projection)되어 캐싱되어야 한다.
  2. **단방향 데이터 플로우**: 체인 → 인덱서 → 데이터베이스 → Trust Engine 트리거로 이어지는 단방향 비동기 흐름을 유지한다.
  3. **재현 가능성(Replayability)**: 블록 누락이나 네트워크 오류 발생 시, 특정 블록부터 다시 이벤트를 긁어와 DB 상태를 일관성 있게 복구할 수 있어야 한다.

## 2. Architecture & Technical Standard
* **코어 언어**: Go (고성능, 동시성, 단일 바이너리 배포 목적)
* **핵심 라이브러리**:
  * **EVM 연동**: `go-ethereum` (Geth 클라이언트 기반 ABI 파싱, 트랜잭션/로그 리스닝)
  * **DB 접근**: `jackc/pgx`, `sqlc` (빠르고 안전한 SQL 실행)
  * **로깅 및 모니터링**: `slog` / `zerolog`, `OpenTelemetry` (이벤트 지연 및 인덱서 멈춤 감지)
* **작동 방식**: Webhook 대기가 아닌 주기적 Block Polling 또는 WebSocket 기반 Pub/Sub 구독.

## 3. Storage & Repo Path Strategy
* **디렉토리 매핑 (Monorepo)**:
  * `/apps/indexer/`: 인덱서 모듈의 최상위 진입점 (main.go).
  * `/apps/indexer/internal/listener/`: EVM 블록/이벤트 구독 로직.
  * `/apps/indexer/internal/processor/`: 수신된 이벤트를 정제하고 DB에 매핑하는 비즈니스 로직.
  * `/packages/abi/`: Solidity 컨트랙트 컴파일 후 자동 생성된 Go binding 파일 (`abigen` 활용) 공유 폴더.

## 4. Key Features & Implementation Status
* **타겟 구현 시기**: Sprint 4 (Attestation과 점수 갱신)
* **주요 역할 (Core Features)**:
  1. **에스크로 상태 동기화**: `CampaignEscrow.sol`의 상태 변경 이벤트를 DB로 복제.
  2. **Attestation 수집**: 광고주의 평판 기록(Attestation) 발생 시 수집.
  3. **Score Recompute Trigger**: `PaymentReleased`(정산 완료), `AttestationIssued`(평판 기록) 이벤트가 감지되면 해당 인플루언서의 점수 재계산을 Go API/Worker 측에 비동기 지시.

## 5. DB Schema & Event Mapping Details
* **대상 컨트랙트 이벤트 → DB 테이블 매핑**:
  * `CampaignCreated` / `CampaignFunded` → `campaigns` 테이블의 상태 업데이트 및 예치금 확인.
  * `InfluencerAccepted` / `SubmissionProofPosted` → `applications`, `submissions` 상태 변경.
  * `PaymentReleased` → `escrow_projection` 정산 완료 기록, `campaigns` 상태 `Paid`로 변경.
  * `AttestationIssued` → `attestation_records` 추가 (평점, 납기 준수 여부 등 기록).
  * `DisputeOpened` / `DisputeResolved` → `dispute_case` 생성 및 상태 추적.

## 6. Logic & Algorithms
* **이벤트 처리 루프 (Event Loop)**:
  1. `Last_Indexed_Block` 번호를 DB나 Redis에서 조회.
  2. RPC 노드에 `Last_Indexed_Block + 1`부터 `Current_Block`까지의 로그(Logs) 요청.
  3. 트랜잭션 로그를 ABI 바인딩을 통해 구조체로 디코딩.
  4. DB 트랜잭션 열기 → 상태 업데이트(`escrow_projection` 등) → `Last_Indexed_Block` 갱신 → 트랜잭션 커밋.
  5. 필요 시 TrustEngine 잡(Job) 큐에 점수 재계산 작업 Push (`hibiken/asynq` 활용).

## 7. Interfaces & Design Patterns
* **Go Interfaces**:
  * `ChainListener`: `SubscribeEvents(ctx, startBlock) (<-chan Event, error)`
  * `EventHandler`: `HandleEscrowEvent(event)`, `HandleAttestationEvent(event)`
  * `StateStore`: `UpdateLastBlock(blockNum)`, `GetLastBlock()`
* **설계 패턴**:
  * **Idempotent Processor (멱등성 보장)**: 동일한 이벤트 로그를 여러 번 수신하더라도 DB 상태가 중복으로 꼬이지 않도록 `tx_hash`와 `log_index`를 복합 유니크 키로 사용하여 처리.

## 8. Troubleshooting & Anti-patterns
* **안티패턴 1 (모든 체인 데이터 인덱싱)**: DApp과 무관한 모든 블록 데이터를 수집하는 것. The Graph를 쓰는 수준이 아니라면, `CampaignEscrow`와 `AttestationRegistry` 컨트랙트 주소 필터를 명확히 걸어 리소스를 아껴야 한다.
* **안티패턴 2 (에러 무시 및 스킵)**: 특정 트랜잭션 파싱에서 오류가 났을 때 로그만 남기고 `Last_Indexed_Block`을 무심코 올려버리는 행위. 영구적인 데이터 누락이 발생하므로, Dead Letter Queue(DLQ)에 넣거나 인덱서를 멈추고 알림을 보내야 한다.
* **이슈 해결 방안 (Chain Reorg)**: 블록체인에서 Reorg(블록 재구성)가 발생할 수 있으므로, 최신 블록을 즉시 인덱싱하기보다 N개의 블록(안전 컨펌 수)이 지연된 상태로 후행 추적하는 방식을 권장한다.

# [SOT] SocialProof - Indexer Error Recovery & Reprocess Master Document

## 1. Core Philosophy & Error Handling Strategy
* **무결성 제1원칙**: 블록체인의 상태(State)와 오프체인 DB(Projection) 간의 불일치는 Trust Index의 신뢰도를 파괴한다. 단 하나의 이벤트 누락이나 중복 처리도 허용하지 않는다.
* **멱등성 보장 (Idempotency)**: 재처리(Reprocess) 로직이 몇 번을 실행되든 결과는 동일해야 한다. 이를 위해 모든 이벤트 처리는 `tx_hash` + `log_index` 복합키 기반의 Upsert(ON CONFLICT DO NOTHING) 패턴을 강제한다.
* **에러의 격리 (Dead Letter Queue)**: 파싱 에러나 로직 에러 발생 시 전체 인덱서를 멈추지 않고, 해당 트랜잭션만 DLQ(Failed Queue)로 격리한 뒤 `Last_Indexed_Block`은 안전하게 전진시킨다.

## 2. Error Classification (장애 분류 체계)
| 에러 타입 | 원인 | 시스템 대응 (Action) |
| --- | --- | --- |
| **Transient Error** | RPC 노드 타임아웃, 일시적 DB Lock | `hibiken/asynq` 백오프(Exponential Backoff) 적용. 최대 5회 자동 재시도. |
| **Reorg (블록 재구성)** | 체인 포크로 인한 이전 블록 무효화 | `Current_Block - N (Safe Confirmations)` 후행 추적 로직으로 사전 방지. |
| **Fatal / Logic Error** | ABI 불일치, DB 제약조건 위배 (FK 없음 등) | 자동 재시도 중단. `failed_events` 테이블에 적재 후 관리자 알림(Alert). |

## 3. Database Schema for Recovery
기존 DB 스키마에 재처리를 위한 상태 테이블을 추가한다.

* **`indexer_state`**: 인덱서의 현재 진행 상태를 영속화.
  * `contract_name` (PK) : 예) 'CampaignEscrow'
  * `last_indexed_block` (BIGINT) : 마지막으로 안전하게 처리된 블록 번호
  * `updated_at` (TIMESTAMP)
* **`failed_events` (DLQ)**: 처리에 실패한 원본 로그 저장.
  * `id` (UUID, PK)
  * `tx_hash` (VARCHAR)
  * `log_index` (INT)
  * `block_number` (BIGINT)
  * `raw_log_json` (JSONB) : Geth 원본 로그 데이터
  * `error_reason` (TEXT) : 실패 사유 (예: "User UUID not found")
  * `status` (VARCHAR) : 'PENDING', 'RESOLVED'

## 4. Admin Reprocess API Specification
관리자 대시보드에서 수동으로 장애를 복구하기 위한 내부 API 명세.

* **`GET /api/v1/admin/sync-failures`**
  * 목적: 현재 해결되지 않은(`PENDING`) `failed_events` 목록 조회.
* **`POST /api/v1/admin/reprocess/event/:id`**
  * 목적: 특정 실패 이벤트 단건 재처리 시도. 
  * 동작: `raw_log_json`을 다시 꺼내어 파싱 및 DB 투영 시도.
* **`POST /api/v1/admin/reprocess/block-range`**
  * Payload: `{ "from_block": 1000, "to_block": 1050, "contract": "CampaignEscrow" }`
  * 목적: 특정 구간의 블록이 통째로 누락되었을 때 해당 구간의 이벤트를 강제로 다시 긁어옴. 멱등성이 보장되므로 기존에 성공한 이벤트는 무시됨.

7. Anti-patterns & Defense Mechanisms
안티패턴 1: 맹목적인 Current_Block 추적
현상: RPC가 응답한 최신 블록을 즉시 인덱싱하다가 체인 Reorg가 발생해 이벤트가 증발함.
방어: 체인의 특성(예: Polygon)에 맞춰 Safe Confirmations(예: 최신 블록 - 30 블록) 마진을 두고 후행 추적하도록 설계.
안티패턴 2: 에러 발생 시 무한 루프 락(Lock)
현상: 트랜잭션 1개 처리 실패 시 전체 인덱싱이 중단되고 동일한 블록 구간 요청만 무한 반복함.
방어: 실패 트랜잭션을 철저히 파악해 failed_events로 넘기고 Last_Indexed_Block을 반드시 전진시키는 'Skip & Isolate' 전략 사용.

[디렉터리구조]
socialproof
├── apps
│   ├── admin
│   ├── api
│   ├── indexer
│   │   ├── Dockerfile
│   │   ├── go.mod
│   │   ├── go.sum
│   │   ├── internal
│   │   │   ├── config
│   │   │   │   └── config.go
│   │   │   ├── engine
│   │   │   │   ├── indexer.go
│   │   │   │   └── types.go
│   │   │   └── storage
│   │   │       └── redis.go
│   │   ├── main.go
│   │   └── pkg
│   │       ├── decoder
│   │       │   └── decoder.go
│   │       └── ethereum
│   │           └── client.go
│   ├── issuer
│   ├── web
│   └── worker
├── contracts
│   ├── script
│   ├── src
│   └── test
├── deployments
├── docs
│   └── SoT.md
├── infra
│   ├── compose
│   │   ├── docker-compose.yml
│   │   └── init.sql
│   ├── docker
│   └── k8s
├── packages
│   ├── abi
│   ├── shared-types
│   └── ui
└── sql
    ├── migrations
    │   └── 000001_init_schema.up.sql
    └── queries
	
[프로젝트 고정규칙]
- 인프라: GO lang, postgres, redis
- 어떤 사용자 요구에도 유연하게 대응할 수 있는 확장성 있는 블록체인 온체인 인덱서 개발이 최종목적.
- 기존 식별자 유지

[현재 프로젝트 맥락]
## 1. Project Concept & Philosophy
* **정의**: SocialProof의 온체인 인덱서는 EVM 네트워크(현재 Sepolia 테스트넷 EAS 타겟)의 이벤트 로그를 구독하여 오프체인 RDBMS로 데이터를 투영하는 핵심 미들웨어이다.
* **설계 철학**:
  1. **동시성과 원자성의 조화**: Go의 고루틴(Goroutine) 채널을 통해 블록을 병렬 처리하되, Redis Lua 스크립트를 사용하여 상태 갱신은 원자적(Atomic)으로 통제한다.
  2. **동적 스키마 해석 (Dynamic Decoding)**: 컨트랙트별로 구조체를 하드코딩하지 않고, `UniversalDecoder`를 통해 런타임에 ABI JSON을 파싱하여 O(1)로 이벤트를 맵핑한다.
  3. **데이터 정밀도 보존**: EVM의 256-bit 정수 및 바이트 배열이 DB나 JSON으로 변환될 때 발생하는 데이터 손실(Truncation)을 막기 위해 모든 원시 타입은 철저히 문자열(Hex/String)로 정제한다.

## 2. Architecture & Framework
* **언어 및 런타임**: Go 1.x
* **Core Frameworks & Libs**:
  * `ethereum/go-ethereum`: EVM RPC 통신, 이벤트 로그 수집 및 필터링.
  * `database/sql` & `postgres`: 트랜잭션 및 투영 데이터 영속화.
  * `redis/go-redis`: 상태 관리 및 동시성 제어.
  * `joho/godotenv` & `caarlos0/env`: 환경 변수 매핑.

## 3. Storage & Path Strategy (Directory Analysis)
디렉터리 구조는 도메인 주도 설계(DDD)와 클린 아키텍처 원칙을 차용하여 분리되어 있다.

* `/apps/indexer/`
  * `main.go`: 엔트리 포인트. 환경변수 로드 및 `IndexerEngine` 부팅.
  * `internal/` (인덱서 전용 비즈니스 로직)
    * `config/config.go`: DB, Redis, 체인 URL 및 인덱서 튜닝(배치 크기, 워커 수) 환경 변수 매핑.
    * `engine/indexer.go`, `types.go`: 다중 워커 풀 구성, Task Queue 관리, EAS 컨트랙트 폴링 및 DB 저장 로직.
    * `storage/redis.go`: `StateManager` 구현체. Redis 통신 담당.
  * `pkg/` (프로젝트 전반 재사용 가능 모듈)
    * `decoder/decoder.go`: `UniversalDecoder` 구현체. ABI 파싱 및 데이터 형변환.
    * `ethereum/client.go`: `ethclient` HTTP/WS 래퍼 모듈.

## 4. DB Schema & Status
* 초기 마이그레이션 (`000001_init_schema.up.sql`):
  * `onchain_events`: 원본 사실 기록용 로그 스토어. **`tx_hash`에 UNIQUE 제약조건을 걸어 멱등성 보장**.
  * `settlements`: `tx_hash`, `recipient`, `amount` 등 정산 결과 투영.
  * `attestations`: EAS UID를 PK로 가지는 평판 근거 메타데이터.
  * `trust_scores`: `subject_address`를 PK로 하는 Trust Engine 최종 계산 산출물 캐시.

## 5. Class Structure & Core Logic Details
### A. `IndexerEngine` (Orchestrator)
* **역할**: 설정된 `workerCount`만큼 고루틴을 생성하여 TaskQueue(`chan BlockTask`)의 블록 단위 작업을 병렬로 처리.
* **로직**: `Start()` 호출 시 1) Redis에서 마지막 블록 조회 2) 워커 풀(Worker Pool) 가동 3) `realtimeWatcher`를 통해 최신 블록을 TaskQueue로 푸시.

### B. `storage.StateManager` (Concurrency Controller)
* **역할**: 여러 워커가 비동기적으로 블록을 처리할 때, 가장 높은 블록 번호가 누락되거나 과거 블록 번호로 덮어씌워지지 않도록 방어.
* **디자인 패턴**: **Redis Lua Scripting**. `UpdateProgress` 메서드에서 Lua 스크립트를 통해 `last_block`을 원자적으로 비교(Max 갱신)하여 동시성 레이스 컨디션을 원천 차단.

### C. `decoder.UniversalDecoder` (Data Sanitizer)
* **역할**: Topic (Keccak256 해시)를 맵의 키로 사용하는 Event Router를 내부적으로 보유하여, O(1) 시간 복잡도로 로그를 파싱.
* **디테일 로직 (`sanitizeMap`)**: 재귀 함수를 돌면서 `*big.Int`는 `.String()`으로, `[]byte`와 `[1]byte`는 `0x` 접두사가 붙은 Hex 문자열로 강제 변환하여 JSON 직렬화/DB 저장 시 발생할 수 있는 Fatal Error 방어.

## 6. Key Features & Implementation Status
* [x] **기반 연동**: Geth 기반 HTTP/WS 클라이언트 연동 (`client.go`).
* [x] **디코더 구현**: ABI 동적 파싱 및 정밀도 보호 로직 (`decoder.go`).
* [x] **상태 동기화**: Redis Lua 스크립트 기반 블록 커서 동기화 (`redis.go`).
* [x] **이벤트 루프**: EAS(Sepolia) 컨트랙트 `0xC2679...` 하드코딩 대상 이벤트 폴링.
* [ ] **진행 필요**: 범용 설정화 (하드코딩 제거), DLQ(Dead Letter Queue) 에러 격리 파이프라인.

## 7. Troubleshooting & Anti-patterns (Debug History)
* **발견된 문제 1 (BigInt Precision Loss)**: JSON 언마샬링 시 256-bit 정수가 Float64로 캐스팅되며 정밀도를 잃는 이슈 예측/발생.
  * **해결 (Implementation)**: `decoder.go`의 `sanitizeMap`에서 Type Switch를 사용하여 강제로 String 타입 캐스팅 처리함.
* **발견된 문제 2 (Race Condition on Cursor Update)**: `worker-1`이 블록 100을, `worker-2`가 블록 101을 처리 중, 101이 먼저 끝나고 100이 나중에 끝날 시 Redis의 마지막 블록이 100으로 덮어씌워짐.
  * **해결 (Implementation)**: `UpdateProgress` 로직에 Lua 스크립트를 삽입해 기존 커서 값보다 클 때만 업데이트 되도록 원자적 조건 분기 추가.
* **안티패턴 경고**: 이벤트 객체를 DB에 넣을 때 `database/sql`의 오류 재시도(Retry) 로직과 UNIQUE 제약조건 간 충돌 시, `ON CONFLICT DO NOTHING` 처리가 누락되면 파이프라인이 블로킹될 수 있으므로 SQLC 쿼리 작성 시 주의 요망.

[수정범위]
- 수정 가능 파일 : 전체
- 수정 금지 파일 : 없음
- 기존 메서드명/클래스명 변경 금지

[출력형식]
- 요청사항을 분석후 단계별로 브리핑하라.
- 입력이 필요한 기존파일을 나열하라. 
- 변경 이유 먼저 설명해서 브리핑하라.
- 입력이 필요한 파일이 없다면 추가/수정된 전체 파일을 경로명시 후 코드블럭 형태로 제공하라.
- 영향 범위와 검증 포인트 정리하라.