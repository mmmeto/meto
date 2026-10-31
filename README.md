# meto API

`w5-qpp` 프론트엔드와 연결할 Go + Gin 백엔드입니다.

## 로컬 실행

프로젝트 루트에서 실행합니다. Go 버전은 `go.mod`를 기준으로 합니다.
PostgreSQL 실행을 위해 Docker Desktop을 켜둡니다.

처음 한 번 로컬 설정 파일을 만듭니다. 예시 값은 로컬 개발용입니다.

```bash
cp .env.example .env
```

DB를 실행한 다음 API 서버를 실행합니다.

```bash
docker compose up -d --wait
go mod download
go run ./cmd/api
```

기본 주소는 `http://localhost:8080`입니다. `Ctrl+C`로 종료합니다.
다른 포트를 사용하려면 다음과 같이 실행합니다.

```bash
HTTP_ADDR=:8082 go run ./cmd/api
```

서버는 프로젝트 루트의 `.env`를 읽습니다. 이미 설정된 환경변수가 우선합니다.
`DATABASE_URL`은 필수이며, 시작 시 10초 안에 DB 연결을 확인하지 못하면 종료합니다.
각 연결 시도는 최대 5초로 제한합니다.
기본 커넥션 풀은 `.env.example`의 URL 옵션으로 최대 10개 연결을 사용합니다.

`Ctrl+C` 또는 `SIGTERM`을 받으면 새 HTTP 요청 수신을 중단하고,
최대 10초 동안 진행 중인 요청을 기다린 뒤 DB 커넥션 풀을 닫습니다.

DB는 호스트의 `127.0.0.1:15432`로 연결하며, 컨테이너 내부 포트는 5432입니다.
다른 프로그램이 15432 포트를 사용 중이면 `.env`의 `POSTGRES_PORT`를 변경합니다.
`DATABASE_URL`의 포트도 해당 값을 사용하도록 구성되어 있습니다.

## 상태 확인

서버를 실행한 상태에서 다른 터미널에서 호출합니다.

```bash
curl http://localhost:8080/api/v1/health
```

정상 응답:

```json
{"service":"meto-api","status":"ok"}
```

`/health`는 DB와 무관하게 서버가 응답하는지만 확인합니다.
DB 연결까지 확인하려면 다음 API를 사용합니다.

```bash
curl -i http://localhost:8080/api/v1/ready
```

DB에 연결할 수 있으면 HTTP 200을 반환합니다.

```json
{"database":"ok","status":"ready"}
```

DB에 연결할 수 없거나 확인에 2초 이상 걸리면 HTTP 503을 반환합니다.
클라이언트 연결이 끊기면 DB 확인 작업에도 취소가 전달됩니다.

```json
{"database":"unavailable","status":"not_ready"}
```

## DB 관리

```bash
docker compose ps
docker compose logs postgres
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

DB 중지:

```bash
docker compose down
```

DB 데이터는 `postgres_data` 볼륨에 유지됩니다. 다시 `docker compose up -d --wait`로 시작합니다.
PostgreSQL 18 이미지에 맞춰 `/var/lib/postgresql`에 볼륨을 연결합니다.
사용자·비밀번호·DB 이름은 볼륨을 처음 초기화할 때 적용되므로,
기존 볼륨이 있는 상태에서 `.env`만 변경하면 DB 내부 설정이 바뀌지는 않습니다.

## 구조

```text
cmd/api/main.go                 서버 시작·종료 및 의존성 연결
internal/config/config.go      환경변수와 .env 로딩
internal/database/postgres.go   pgx 커넥션 풀과 초기 연결 확인
internal/httpapi/router.go      HTTP 라우팅과 상태 확인
migrations/                    DB 테이블 변경 SQL을 추가할 위치
compose.yaml                   로컬 PostgreSQL 실행 설정
.env.example                   로컬 설정 예시
```

## 검증

```bash
go test ./...
go vet ./...
```

다음 단계는 `w5-qpp` 사용자·인증 구조를 확인하고 첫 마이그레이션과 인증 API를 구현하는 것입니다.
현재는 DB 연결까지 구성했으며 테이블과 인증 기능은 아직 없습니다.
프론트엔드 연결 시 실행 환경에 맞는 API 주소와 웹 CORS 설정을 추가합니다.
