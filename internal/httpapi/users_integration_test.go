package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"meto/internal/auth"
	"meto/internal/testutil"
	"meto/internal/user"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestUserAPIWithPostgres(t *testing.T) {
	pool := testutil.Postgres(t)
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(filepath.Join("..", "..", "migrations")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	verifier := verifierFunc(func(_ context.Context, token string) (auth.Identity, error) {
		if !strings.HasPrefix(token, "user-") {
			return auth.Identity{}, auth.ErrInvalidToken
		}
		email := "same-address@example.test"
		return auth.Identity{ID: token, DisplayName: "Provider Name", Email: &email}, nil
	})
	router, err := NewRouter(pool, verifier, user.NewService(user.NewRepository(pool)))
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, r)
		return response
	}
	readProfile := func(response *httptest.ResponseRecorder) user.Profile {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
		}
		var profile user.Profile
		if err := json.Unmarshal(response.Body.Bytes(), &profile); err != nil {
			t.Fatal(err)
		}
		return profile
	}
	if got := request(http.MethodGet, "/api/v1/users/me", "user-a", "").Code; got != http.StatusNotFound {
		t.Fatalf("unregistered user status = %d, want 404", got)
	}
	first := readProfile(request(http.MethodPost, "/api/v1/auth/login", "user-a", `{"id":"user-b"}`))
	if first.ID != "user-a" || first.Username != "user-a" || first.DefaultVisibility != "time_only" || first.LastLoginAt == nil {
		t.Fatalf("unexpected first login: %+v", first)
	}
	again := readProfile(request(http.MethodPost, "/api/v1/auth/login", "user-a", ""))
	if !again.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("repeated login changed the account creation time")
	}
	readProfile(request(http.MethodPost, "/api/v1/auth/login", "user-b", ""))
	me := readProfile(request(http.MethodGet, "/api/v1/users/me?id=user-b", "user-a", ""))
	if me.ID != "user-a" || !me.UpdatedAt.Equal(again.UpdatedAt) {
		t.Fatal("GET /me read a different account or modified the profile")
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM users").Scan(&count); err != nil || count != 2 {
		t.Fatalf("accounts with the same email must remain distinct: count=%d err=%v", count, err)
	}
	_, err = pool.Exec(t.Context(), `UPDATE users SET username = 'my_username', display_name = 'My Nickname',
		default_visibility = 'hidden', onboarding_completed = true,
		photo_url = 'https://example.test/custom.png', photo_storage_path = 'profiles/custom.png',
		terms_accepted_version = 'v1', terms_accepted_at = NOW(),
		calendar_permission_status = 'granted', has_imported_device_calendar = true WHERE id = 'user-a'`)
	if err != nil {
		t.Fatal(err)
	}
	saved := readProfile(request(http.MethodPost, "/api/v1/auth/login", "user-a", ""))
	if saved.DisplayName != "My Nickname" || saved.Username != "my_username" || saved.DefaultVisibility != "hidden" ||
		!saved.OnboardingCompleted || saved.PhotoURL == nil || *saved.PhotoURL != "https://example.test/custom.png" ||
		saved.TermsAcceptedVersion == nil || *saved.TermsAcceptedVersion != "v1" ||
		saved.CalendarPermissionStatus != "granted" || !saved.HasImportedDeviceCalendar {
		t.Fatalf("login overwrote the saved profile: %+v", saved)
	}

	var workers sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			response := request(http.MethodPost, "/api/v1/auth/login", "user-c", "")
			if response.Code != http.StatusOK {
				failures <- fmt.Errorf("concurrent login: %d %s", response.Code, response.Body.String())
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM users WHERE id = 'user-c'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent logins must create one user: count=%d err=%v", count, err)
	}

	onboarding := func(displayName, username string) string {
		return fmt.Sprintf(`{"displayName":%q,"username":%q,"defaultVisibility":"hidden","termsVersion":%q}`,
			displayName, username, user.CurrentTermsVersion)
	}
	const onboardingPath = "/api/v1/users/me/onboarding"
	if got := request(http.MethodPost, onboardingPath, "user-unregistered", onboarding("이름", "unregistered")).Code; got != http.StatusNotFound {
		t.Fatalf("unregistered onboarding status = %d, want 404", got)
	}
	readProfile(request(http.MethodPost, "/api/v1/auth/login", "user-d", ""))
	done := readProfile(request(http.MethodPost, onboardingPath, "user-d", `{"displayName":" 지수 ","username":" Jisu_01 ",
		"defaultVisibility":"title_visible","termsVersion":"`+user.CurrentTermsVersion+`",
		"photoStoragePath":"users/user-d/profile/avatar.jpg"}`))
	if done.DisplayName != "지수" || done.Username != "jisu_01" || done.DefaultVisibility != "title_visible" ||
		!done.OnboardingCompleted || done.TermsAcceptedAt == nil || done.TermsAcceptedVersion == nil ||
		*done.TermsAcceptedVersion != user.CurrentTermsVersion || done.PhotoStoragePath == nil {
		t.Fatalf("unexpected onboarding result: %+v", done)
	}
	retried := readProfile(request(http.MethodPost, onboardingPath, "user-d", onboarding("다른 이름", "jisu_01")))
	if retried.DisplayName != "지수" || !retried.UpdatedAt.Equal(done.UpdatedAt) {
		t.Fatalf("retry changed the saved profile: %+v", retried)
	}
	if got := request(http.MethodPost, onboardingPath, "user-d", onboarding("지수", "another_id")).Code; got != http.StatusConflict {
		t.Fatalf("second onboarding status = %d, want 409", got)
	}
	afterLogin := readProfile(request(http.MethodPost, "/api/v1/auth/login", "user-d", ""))
	if afterLogin.DisplayName != "지수" || afterLogin.Username != "jisu_01" {
		t.Fatalf("login overwrote the onboarded profile: %+v", afterLogin)
	}

	readProfile(request(http.MethodPost, "/api/v1/auth/login", "user-e", ""))
	taken := request(http.MethodPost, onboardingPath, "user-e", onboarding("다른 사람", "jisu_01"))
	if taken.Code != http.StatusConflict || !strings.Contains(taken.Body.String(), "username_taken") {
		t.Fatalf("duplicate username: %d %s", taken.Code, taken.Body.String())
	}
	if current := readProfile(request(http.MethodGet, "/api/v1/users/me", "user-e", "")); current.OnboardingCompleted {
		t.Fatal("failed onboarding must not change the profile")
	}

	// 같은 사용자명을 두고 경쟁하면 한 명만 성공하고, 나머지는 500이 아닌 409를 받아야 합니다.
	const racers = 8
	statuses := make(chan int, racers)
	for i := range racers {
		token := fmt.Sprintf("user-race-%d", i)
		readProfile(request(http.MethodPost, "/api/v1/auth/login", token, ""))
		workers.Add(1)
		go func() {
			defer workers.Done()
			statuses <- request(http.MethodPost, onboardingPath, token, onboarding("경쟁", "race_winner")).Code
		}()
	}
	workers.Wait()
	close(statuses)
	wins := 0
	for status := range statuses {
		switch status {
		case http.StatusOK:
			wins++
		case http.StatusConflict:
		default:
			t.Errorf("username race status = %d", status)
		}
	}
	if wins != 1 {
		t.Fatalf("username race winners = %d, want 1", wins)
	}

	// 같은 사용자의 중복 요청은 재시도이므로 모두 성공해야 합니다.
	readProfile(request(http.MethodPost, "/api/v1/auth/login", "user-f", ""))
	repeats := make(chan int, racers)
	for range racers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			repeats <- request(http.MethodPost, onboardingPath, "user-f", onboarding("재시도", "retry_user")).Code
		}()
	}
	workers.Wait()
	close(repeats)
	for status := range repeats {
		if status != http.StatusOK {
			t.Errorf("repeated onboarding status = %d, want 200", status)
		}
	}
}
