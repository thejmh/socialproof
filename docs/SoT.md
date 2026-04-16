[역할]
너는  프로젝트 신규설계AI가 아니라 기존 구조를 보존하는 유지보수 AI다.
후술할 SoT문서는 프로젝트 진행 전 과정에서 엄중하게 지켜져야하는 사항이다.

[SoT 마스터문서]
# [SOURCE OF TRUTH] SOCIALPROOF INDEXER PROJECT MASTER DOCUMENT

## 1. Project Overview & Philosophy
- **Project Name:** SocialProof (SP)
- **Concept:** 온체인 데이터를 수집하여 신뢰 점수 및 평판을 계산하기 위한 고성능 EVM 인덱서.
- **Philosophy:** "On-chain for Truth" - 위변조 불가능한 블록체인 데이터를 추출하여 오프체인 신뢰 엔진(Trust Engine)의 원천 데이터로 활용함.
- **Core Principle:** 이중 동기화(Dual-Sync) 아키텍처를 통한 과거 데이터의 신속한 백필과 실시간 블록의 즉각적인 반영.

## 2. Technology Stack & Framework
- **Language:** Go 1.25.1 (Strict toolchain management)
- **Framework/Lib:** - `go-ethereum` (ethclient): EVM Node 통신
  - `sql` (database/sql) & `lib/pq`: PostgreSQL 드라이버
  - `go-redis/v9`: Redis 클라이언트
  - `slog`: 구조화된 JSON 로깅
  - `godotenv`: 하이브리드 환경 변수 관리
- **Infrastructure:** - Docker & Docker Compose
  - PostgreSQL 15-alpine (Main DB)
  - Redis 7-alpine (Cursor & Real-time state)

## 3. System Architecture
### 3.1. Engine Structure (Producer-Consumer)
- **IndexerEngine:** 시스템의 핵심 컨트롤러.
  - **Real-time Watcher:** 최신 블록 헤더를 감시하여 `taskQueue`에 블록 번호 투입.
  - **Historical Backfill:** 설정된 `StartBlock`부터 `LatestBlock`까지 구간을 채우는 작업을 `taskQueue`에 투입.
  - **Workers:** 가변적인 고루틴(WorkerCount)이 `taskQueue`를 소비하며 `FilterLogs` 실행 및 DB 저장.
### 3.2. Design Patterns
- **Dependency Injection (DI):** `main`에서 DB와 클라이언트를 초기화 후 `IndexerEngine` 생성자에 주입. (Panic 방지 핵심)
- **Repository Pattern:** DB 저장 로직(`SaveEvent`)을 엔진 내부 메서드로 캡슐화.
- **Graceful Shutdown:** OS Signal(SIGINT, SIGTERM) 수신 시 `context.Cancel`을 통해 모든 워커의 안전한 종료 보장.

## 4. Database Schema (PostgreSQL)
### 4.1. Table: `onchain_events`
| Column | Type | Constraints | Description |
| :--- | :--- | :--- | :--- |
| id | BIGSERIAL | PRIMARY KEY | 자동 증가 고유 ID |
| block_number | BIGINT | NOT NULL | 이벤트 발생 블록 번호 |
| tx_hash | CHAR(66) | UNIQUE NOT NULL | 트랜잭션 해시 (고유 제약 조건) |
| event_type | VARCHAR(50) | NOT NULL | 이벤트 이름 (예: ONCHAIN_EVENT) |
| contract_address | CHAR(42) | NOT NULL | 발생 컨트랙트 주소 |
| caller_address | CHAR(42) | | 호출자 주소 (필요 시 조회) |
| raw_data | JSONB | | 이벤트 데이터 전문 (인덱싱 가능) |
| created_at | TIMESTAMPTZ | DEFAULT NOW() | DB 기록 시각 |

## 5. Caching & State Management (Redis)
- **Key:** `sp:indexer:v1:last_block`
- **Role:** - 인덱서의 현재 동기화 지점(Cursor) 저장.
  - 재시작 시 DB 전체 스캔 없이 마지막 지점부터 즉시 재개 가능.
- **Security:** `requirepass` 설정을 통한 접근 제어 적용.

## 6. Program Details & Algorithms
### 6.1. Progress Calculation Algorithm
- **Formula:** `(Current - Start) / (Latest - Start) * 100`
- **Components:** Redis의 `last_block` + RPC의 `LatestBlockHeader`.
### 6.2. Error Handling
- **Database:** `ON CONFLICT (tx_hash) DO NOTHING`을 통해 멱등성(Idempotency) 확보.
- **Connection:** DB 연결 실패 시 2초 간격으로 최대 5번 재시도하는 `Retry` 로직 내장.

## 7. Development & Debug History (Critical)
- **Issue 1 (Pathing):** Docker 빌드 시 `..`를 이용한 상위 경로 참조 불가. 
  - *Fix:* `context`를 루트(`.`)로 설정하고 `dockerfile` 내에서 절대 경로 사용.
- **Issue 2 (Panic):** `NewIndexerEngine`에 `*sql.DB`가 아닌 `connStr`을 넘겨 `db` 객체가 `nil`인 상태로 동작하여 패닉 발생.
  - *Fix:* 생성자 파라미터를 `*sql.DB`로 변경하고 `main`에서 주입 강제.
- **Issue 3 (Postgres Constraint):** `ON CONFLICT` 사용 시 기준이 되는 `UNIQUE` 제약 조건 누락으로 에러 발생.
  - *Fix:* `ALTER TABLE ... ADD CONSTRAINT unique_tx_hash UNIQUE (tx_hash)` 적용.
- **Issue 4 (Redis Auth):** 비밀번호 미설정으로 인한 보안 및 연결 실패.
  - *Fix:* `command: redis-server --requirepass` 추가 및 인덱서 환경 변수 매핑.

## 8. Execution Commands
- **Build & Run:** `docker-compose --env-file .env up --build`
- **DB Check:** `docker exec -it sp-db psql -U postgres -d socialproof`
- **Redis Check:** `docker exec -it sp-redis redis-cli -a ${REDIS_PASSWORD}`

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
│   │   │   └── engine
│   │   │       ├── cursor.go
│   │   │       ├── indexer.go
│   │   │       ├── stats.go
│   │   │       └── types.go
│   │   ├── main.go
│   │   └── pkg
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
- 프로젝트명 : SocialProof
- 목적: 프로젝트 내에서 온체인 인덱서 개발.
- 현재상태: 메인넷 접속, postgres 데이터 insert완료 후 redis integration 중.

[수정범위]
- 수정 가능 파일 : 전체
- 수정 금지 파일 : 없음
- 기존 메서드명/클래스명 변경 금지

[출력형식]
- 추가/변경이 필요한 파일을 나열하라. 
- 변경 이유 먼저 설명해서 브리핑하라.
- 마지막에 영향 범위와 검증 포인트 정리하라.
- 필요하다면 사용자로 부터 입력이 필요한 기존파일 목록을 나열해서 요청하라.
- 필요한 파일이 없다면 추가/수정된 전체 파일을 경로명시 후 코드블럭 형태로 제공하라.

[해결해야할 과제]
- 기능: redis에 마지막 접속기록을 저장. 현재 인덱싱 진행률(%) 저장.
- 기대결과: Redis Integration 시작.
- 요청사항: 추후 다양한 요청사항에 대응할 수 있도록 확장성 있게 개발하라.