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
go run ./cmd/migrate up
go run ./cmd/api
```

기본 주소는 `http://localhost:8080`입니다. `Ctrl+C`로 종료합니다.
다른 포트를 사용하려면 다음과 같이 실행합니다.

```bash
HTTP_ADDR=:8082 go run ./cmd/api
```

서버는 프로젝트 루트의 `.env`를 읽습니다. 이미 설정된 환경변수가 우선합니다.
`DATABASE_URL`은 필수이며, 시작 시 10초 안에 DB 연결을 확인하지 못하면 종료합니다.
API 서버에는 `FIREBASE_PROJECT_ID`도 필요합니다. 기본 예시는 기존 `w5-qpp` 프로젝트 ID입니다.
마이그레이션 실행에는 Firebase 설정이 필요하지 않습니다.
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

## 마이그레이션

프로젝트 루트에서 실행합니다. API 서버를 켜기 전에 SQL 변경을 적용합니다.

```bash
go run ./cmd/migrate status
go run ./cmd/migrate up
go run ./cmd/migrate status
```

첫 실행 후 `Database version: 1, target version: 1`이 표시됩니다.
`up`은 적용되지 않은 SQL 파일만 순서대로 실행하고, 재실행 시 이미 적용한 파일은 건너뜁니다.
PostgreSQL 세션 잠금으로 여러 실행기가 같은 마이그레이션을 동시에 적용하지 않도록 합니다.
실행 전체는 최대 2분으로 제한하며 `Ctrl+C`와 `SIGTERM`으로 취소할 수 있습니다.

`00001_create_users.sql`은 기존 문자열 사용자 ID, 프로필, 일정 공개 범위,
캘린더 가져오기 상태, 온보딩 및 약관 동의 정보를 저장할 `users` 테이블을 만듭니다.
사용자명은 소문자·숫자·밑줄 3~20자이며 UNIQUE 제약조건으로 중복을 막습니다.
온보딩 완료 전에는 사용자명이 없어도 저장할 수 있습니다.
공개 범위의 기본값은 `time_only`입니다.
`updated_at`은 생성 시 기본값이 설정되며 이후 수정 쿼리에서 갱신해야 합니다.

적용한 SQL은 수정하지 않고 `00002_설명.sql`처럼 새 파일로 변경을 추가합니다.
SQL 파일의 `-- +goose Down`에는 되돌릴 SQL을 기록하지만 현재 실행기는 `up`, `status`만 제공합니다.

## 로그인 및 내 프로필 API

기존 앱의 Firebase Auth 로그인을 유지하고, Go API가 ID 토큰을 검증한 뒤 PostgreSQL에 사용자를 저장합니다.
인증 모듈은 `Verifier` 인터페이스로 분리해 이후 인증 제공자를 교체할 수 있도록 했습니다.
이번 변경에는 프론트 API 연결이나 기존 Firestore 프로필의 데이터 이전은 포함하지 않습니다.

| API | 동작 |
| --- | --- |
| `POST /api/v1/auth/login` | 검증된 UID로 사용자 생성 또는 로그인 정보 갱신 후 프로필 반환 |
| `GET /api/v1/users/me` | 검증된 UID에 해당하는 PostgreSQL 프로필 조회 |
| `POST /api/v1/users/me/onboarding` | 이름·사용자명·공개 범위·약관 동의를 저장하고 온보딩 완료 |

세 API 모두 다음 헤더가 필요합니다.

```text
Authorization: Bearer <Firebase ID token>
```

로그인된 프론트의 `auth.currentUser.getIdToken()`으로 ID 토큰을 얻습니다.
카카오 로그인 교환 과정에서 받은 custom token이나 Google·Apple의 원본 토큰을 직접 보내면 안 됩니다.
API는 새 액세스 토큰이나 세션 쿠키를 발급하지 않으며, 각 요청의 Firebase ID 토큰을 검증합니다.
Google 공개 인증서로 서명·프로젝트 ID·발급자·만료를 검증하므로 서비스 계정 키는 필요하지 않습니다.
현재는 `VerifyIDToken`을 사용하며 토큰 폐기·계정 비활성화 여부의 추가 조회는 하지 않습니다.
해당 검사가 필요하면 관리자 자격증명과 `VerifyIDTokenAndCheckRevoked`를 함께 구성해야 합니다.
참고: [Firebase ID 토큰 검증](https://firebase.google.com/docs/auth/admin/verify-id-tokens).

이 서버는 서명 없는 에뮬레이터 토큰을 받지 않습니다.
`FIREBASE_AUTH_EMULATOR_HOST`가 설정되어 있으면 시작을 거부합니다.

실제 ID 토큰을 `FIREBASE_ID_TOKEN` 환경변수에 준비한 뒤 호출합니다.

```bash
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H "Authorization: Bearer $FIREBASE_ID_TOKEN"

curl http://localhost:8080/api/v1/users/me \
  -H "Authorization: Bearer $FIREBASE_ID_TOKEN"
```

`POST /auth/login`에는 요청 본문이 필요하지 않습니다.
사용자 ID·이메일·초기 표시 이름·사진은 검증된 토큰에서만 가져옵니다.
이메일이 같아도 UID가 다르면 별도 계정으로 취급하며 이메일로 계정을 합치지 않습니다.
이메일은 프로필 정보로 저장하며 별도의 이메일 인증 상태를 나타내지는 않습니다.
온보딩이 완료된 프로필의 이름·사진·사용자명·공개 범위·동의 정보는 재로그인 때 유지합니다.
동시 로그인도 PostgreSQL의 기본키와 UPSERT로 동일 UID의 사용자 한 명에 반영합니다.
`GET /users/me`는 데이터를 변경하지 않으며 경로나 쿼리에 지정한 다른 사용자 ID를 사용하지 않습니다.

응답은 기존 프론트 모델의 `id`, `username`, `displayName`, `photoURL`, `photoStoragePath`,
`defaultVisibility`, `calendarPermissionStatus`, `hasImportedDeviceCalendar`, `updatedAt` 필드를 포함합니다.
이메일·온보딩·약관·로그인 시각도 본인 응답에 포함합니다.
아직 사용자명을 설정하지 않은 계정은 기존 프론트 매퍼와 같이 UID를 `username`으로 반환합니다.
이는 사용자명을 예약하거나 온보딩을 완료한 것이 아니며 DB의 실제 사용자명은 NULL입니다.

| 상태 코드 | 의미 |
| --- | --- |
| 200 | 저장 또는 조회 성공 |
| 401 | 토큰 누락·형식 오류·검증 실패 |
| 404 | 인증은 성공했지만 아직 로그인 API로 등록하지 않은 사용자 |
| 503 | 공개 인증서 조회 또는 DB 처리 실패 |

## 온보딩 API

로그인 API로 사용자를 등록한 뒤 호출합니다. 대상 사용자는 요청 본문이 아니라 검증된 토큰으로 결정합니다.

```bash
curl -X POST http://localhost:8080/api/v1/users/me/onboarding \
  -H "Authorization: Bearer $FIREBASE_ID_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"displayName":"지수","username":"jisu_01","defaultVisibility":"title_visible","termsVersion":"2026-10-16"}'
```

| 필드 | 규칙 |
| --- | --- |
| `displayName` | 필수. 앞뒤 공백 제거 후 1~80자, 줄바꿈·제어문자 불가 |
| `username` | 필수. 앞뒤 공백 제거·소문자 변환 후 소문자·숫자·밑줄 3~20자 |
| `defaultVisibility` | 필수. `hidden`, `time_only`, `title_visible`, `full_visible` 중 하나 |
| `termsVersion` | 필수. 서버의 `CurrentTermsVersion`과 같아야 함 |
| `photoStoragePath` | 선택. `users/<본인 UID>/profile/avatar.jpg`만 허용, 생략하면 기존 값 유지 |

사진 URL은 다른 사용자의 앱이 불러오는 값이므로 입력으로 받지 않고 기존 값을 유지합니다.
사용자명 중복은 UNIQUE 제약으로 판정하므로 동시에 요청해도 한 명만 성공합니다.
온보딩을 마친 뒤 같은 사용자명으로 다시 요청하면 응답을 받지 못한 앱의 재시도로 보고,
저장된 프로필을 변경하지 않은 채 HTTP 200으로 반환합니다.
다른 사용자명으로 요청하면 `onboarding_completed`로 거절합니다.
프로필 수정은 이 API로 할 수 없습니다.

| 상태 코드 | `error.code` | 의미 |
| --- | --- | --- |
| 200 | | 온보딩 완료 또는 같은 내용의 재시도 |
| 400 | `invalid_profile` | 본문 형식 오류 또는 입력 규칙 위반 |
| 401 | `unauthorized` | 토큰 누락·형식 오류·검증 실패 |
| 404 | `user_not_found` | 로그인 API로 등록하지 않은 사용자 |
| 409 | `username_taken` | 다른 사용자가 쓰는 사용자명 |
| 409 | `onboarding_completed` | 이미 다른 사용자명으로 온보딩 완료 |
| 409 | `terms_outdated` | 서버의 최신 약관 버전과 다름 |
| 503 | `database_unavailable` | DB 처리 실패 |

## 오류 응답

오류는 `{"error":{"code":"...","message":"..."}}` 형태이며 토큰과 내부 오류 내용은 응답에 넣지 않습니다.
인증 확인에는 최대 5초, 사용자 DB 처리에는 최대 3초 제한을 적용하고 요청 취소도 전달합니다.

## 구조

```text
cmd/api/main.go                 서버 시작·종료 및 의존성 연결
cmd/migrate/main.go             마이그레이션 적용 및 버전 확인
internal/config/config.go      환경변수와 .env 로딩
internal/auth/                 로그인 토큰 검증 인터페이스와 Firebase 구현
internal/database/postgres.go   pgx 커넥션 풀과 초기 연결 확인
internal/httpapi/router.go      HTTP 라우팅과 상태 확인
internal/httpapi/users.go       인증 미들웨어, 사용자 API 핸들러, 도메인 오류의 HTTP 변환
internal/user/profile.go        프로필 입력 규칙과 도메인 오류
internal/user/service.go        온보딩 등 사용자 비즈니스 규칙
internal/user/repository.go     PostgreSQL 사용자 저장·조회
migrations/                    버전별 DB 테이블 변경 SQL
compose.yaml                   로컬 PostgreSQL 실행 설정
.env.example                   로컬 설정 예시
```

## 검증

```bash
go test ./...
go test -race ./...
go vet ./...
```

DB 통합 테스트는 `METO_TEST_DATABASE_URL`을 명시해야 실행합니다.
DB 이름이 `meto_test_`로 시작하는지 확인하고, 테스트마다 별도 스키마를 만들어 종료 시 제거합니다.
설정하지 않으면 해당 테스트만 건너뜁니다.
기본 로컬 접속 설정을 사용하는 경우:

```bash
docker compose exec postgres sh -c 'createdb -U "$POSTGRES_USER" meto_test_migrations'
METO_TEST_DATABASE_URL='postgres://meto:meto_local_password@127.0.0.1:15432/meto_test_migrations?sslmode=disable' go test -race ./... -v
```

테스트는 최초 적용과 재실행, 사용자명 중복·형식, 공개 범위, 온보딩 제약,
실패한 마이그레이션의 트랜잭션 롤백과 버전 유지를 확인합니다.
사용자 API 통합 테스트는 실제 PostgreSQL로 로그인·조회·계정 분리·프로필 보존·동시 UPSERT를 확인합니다.
이 통합 테스트의 인증 결과는 테스트용 검증기로 주입하며,
Firebase 검증 자체는 테스트 인증서와 실제 RSA 서명으로 별도 검증합니다.
기본 테스트에는 위조 서명·만료·다른 발급자·다른 프로젝트·에뮬레이터 모드 거부도 포함됩니다.
접속 설정을 변경했다면 테스트 URL도 해당 값으로 맞춥니다.

수동 확인 순서:

1. 실제 프론트 로그인 계정의 ID 토큰으로 `POST /auth/login`을 호출해 프로필을 확인합니다.
2. 같은 토큰으로 `GET /users/me`를 호출해 동일한 UID인지 확인합니다.
3. 토큰 없이 호출하면 401이 반환되는지 확인합니다.
4. 로그인 API를 다시 호출해 동일 사용자로 유지되는지 확인합니다.

다음 단계는 온보딩 프로필 저장 API와 프론트의 데이터 접근 구현 연결입니다.
프론트엔드 연결 시 실행 환경에 맞는 API 주소와 웹 CORS 설정을 추가합니다.
