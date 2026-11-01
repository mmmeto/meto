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
		if token != "user-a" && token != "user-b" && token != "user-c" {
			return auth.Identity{}, auth.ErrInvalidToken
		}
		email := "same-address@example.test"
		return auth.Identity{ID: token, DisplayName: "Provider Name", Email: &email}, nil
	})
	router, err := NewRouter(pool, verifier, user.NewRepository(pool))
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
}
