# [Stage 1: Builder]
# 1. 빌드 환경으로 Go 1.25 버전이 설치된 경량 Alpine 리눅스를 사용합니다.
FROM golang:1.25-alpine AS builder

# 2. 컨테이너 내부의 작업 디렉토리를 /app으로 설정합니다.
WORKDIR /app

# 3. 의존성 정의 파일인 go.mod와 go.sum을 먼저 복사합니다.
# 소스 코드보다 먼저 복사하는 이유는 '레이어 캐싱'을 통해 빌드 속도를 높이기 위함입니다.
COPY apps/indexer/go.mod apps/indexer/go.sum ./

# 4. 정의된 패키지들을 다운로드합니다. 소스 코드가 바뀌어도 패키지가 그대로면 이 단계는 캐시로 넘어갑니다.
RUN go mod download

# 5. 프로젝트 전체 소스 코드를 컨테이너 안으로 복사합니다.
COPY apps/indexer .
COPY .env .
COPY apps/indexer/contracts.json .

# [핵심] 빌드 단계에서는 모든 소스 코드와 의존성이 포함된 완전한 환경이 필요하지만, 최종 실행 단계에서는 빌드 도구와 불필요한 파일들을 제거하여 이미지를 최소화할 수 있습니다.

# 6. Go 프로그램을 빌드합니다.
# CGO_ENABLED=0: C 라이브러리 의존성 없이 정적 바이너리로 만듭니다. (실행 스테이지에서 중요)
# GOOS=linux: 대상 운영체제를 리눅스로 지정합니다.
# -o indexer: 결과물 파일 이름을 'indexer'로 정합니다.
RUN CGO_ENABLED=0 GOOS=linux go build -o indexer main.go

# [Stage 2: Final]
# 7. 실행 환경으로 가장 가벼운 alpine 리눅스를 사용합니다. (약 5MB 수준)
FROM alpine:latest

# 8. 온체인 통신(HTTPS)을 위한 인증서(ca-certificates)와 시간대 데이터(tzdata)를 설치합니다.
# 인덱서가 블록 타임스탬프를 다룰 때 시간대 설정은 필수입니다.
RUN apk --no-cache add ca-certificates tzdata

# 9. 실행 파일이 위치할 작업 디렉토리를 설정합니다.
WORKDIR /root/

# 10. [핵심] builder 단계(Stage 1)에서 공들여 만든 'indexer' 바이너리만 쏙 빼서 가져옵니다.
# 이 기법 덕분에 최종 이미지에는 수백 MB의 Go 빌드 툴이 포함되지 않습니다.
COPY --from=builder /app/indexer .

# 11. 로컬 설정 파일인 .env도 함께 복사합니다.(docker-compose env_file로 대체 가능)
COPY --from=builder /app/.env .
COPY --from=builder /app/contracts.json .

# 12. 컨테이너가 시작될 때 인덱서를 실행하도록 명령어를 지정합니다.
CMD ["./indexer"]