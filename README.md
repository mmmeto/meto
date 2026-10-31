# meto API

`w5-qpp` 프론트엔드와 연결할 Go + Gin 백엔드입니다.

## 로컬 실행

프로젝트 루트에서 실행합니다. Go 버전은 `go.mod`를 기준으로 합니다.

```bash
go mod download
go run ./cmd/api
```

기본 주소는 `http://localhost:8080`입니다. `Ctrl+C`로 종료합니다.
다른 포트를 사용하려면 다음과 같이 실행합니다.

```bash
HTTP_ADDR=:8082 go run ./cmd/api
```

## 상태 확인

서버를 실행한 상태에서 다른 터미널에서 호출합니다.

```bash
curl http://localhost:8080/api/v1/health
```

정상 응답:

```json
{"service":"meto-api","status":"ok"}
```

현재 상태 확인은 HTTP 서버의 응답만 확인합니다. DB와 인증은 아직 연결하지 않았습니다.

## 구조

```text
cmd/api/main.go   서버 실행 시작점
internal/         인증·친구·일정·채팅 구현을 추가할 위치
migrations/       DB 테이블 변경 SQL을 추가할 위치
```

다음 단계는 PostgreSQL 실행, pgx 연결, 사용자·인증 API 구현입니다.
프론트엔드 연결 시 실행 환경에 맞는 API 주소와 웹 CORS 설정을 추가합니다.
