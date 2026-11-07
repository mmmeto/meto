package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"meto/internal/auth"
	"meto/internal/user"

	"github.com/gin-gonic/gin"
)

type verifierFunc func(context.Context, string) (auth.Identity, error)

func (f verifierFunc) Verify(ctx context.Context, token string) (auth.Identity, error) {
	return f(ctx, token)
}

type userServiceStub struct {
	sync      func(context.Context, user.Login) (user.Profile, error)
	get       func(context.Context, string) (user.Profile, error)
	onboard   func(context.Context, string, user.Onboarding) (user.Profile, error)
	update    func(context.Context, string, user.ProfileUpdate) (user.Profile, error)
	calendar  func(context.Context, string, user.CalendarImport) (user.Profile, error)
	available func(context.Context, string) (bool, error)
}

func (s userServiceStub) SyncLogin(ctx context.Context, input user.Login) (user.Profile, error) {
	if s.sync == nil {
		panic("unexpected user write")
	}
	return s.sync(ctx, input)
}

func (s userServiceStub) GetByID(ctx context.Context, id string) (user.Profile, error) {
	if s.get == nil {
		panic("unexpected user read")
	}
	return s.get(ctx, id)
}

func (s userServiceStub) CompleteOnboarding(ctx context.Context, id string, input user.Onboarding) (user.Profile, error) {
	if s.onboard == nil {
		panic("unexpected onboarding")
	}
	return s.onboard(ctx, id, input)
}

func (s userServiceStub) UpdateProfile(ctx context.Context, id string, input user.ProfileUpdate) (user.Profile, error) {
	if s.update == nil {
		panic("unexpected profile update")
	}
	return s.update(ctx, id, input)
}

func (s userServiceStub) UpdateCalendarImport(ctx context.Context, id string, input user.CalendarImport) (user.Profile, error) {
	if s.calendar == nil {
		panic("unexpected calendar update")
	}
	return s.calendar(ctx, id, input)
}

func (s userServiceStub) UsernameAvailable(ctx context.Context, username string) (bool, error) {
	if s.available == nil {
		panic("unexpected username lookup")
	}
	return s.available(ctx, username)
}

func newStatusRouter(db Pinger) (*gin.Engine, error) {
	return NewRouter(db, verifierFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{}, auth.ErrInvalidToken
	}), userServiceStub{})
}

func userTestRouter(t *testing.T, verifier auth.Verifier, users UserService) *gin.Engine {
	t.Helper()
	router, err := NewRouter(pingerFunc(func(context.Context) error { return nil }), verifier, users)
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func validVerifier(context.Context, string) (auth.Identity, error) {
	return auth.Identity{ID: "verified-user", DisplayName: "Verified Name"}, nil
}

func TestProtectedRoutesRejectBadAuthorization(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodGet, "/api/v1/users/me"},
		{http.MethodPost, "/api/v1/users/me/onboarding"},
		{http.MethodPut, "/api/v1/users/me"},
		{http.MethodPut, "/api/v1/users/me/calendar-import"},
	} {
		for _, header := range []string{"", "Basic abc", "Bearer", "Bearer a b", "Bearer " + strings.Repeat("x", 17000), "Bearer invalid"} {
			router := userTestRouter(t, verifierFunc(func(context.Context, string) (auth.Identity, error) {
				return auth.Identity{}, auth.ErrInvalidToken
			}), userServiceStub{})
			request := httptest.NewRequest(route.method, route.path, nil)
			if header != "" {
				request.Header.Set("Authorization", header)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("%s: expected 401 with Bearer challenge, got %d", route.path, response.Code)
			}
		}
	}
}

func TestLoginUsesVerifiedClaimsInsteadOfRequestBody(t *testing.T) {
	router := userTestRouter(t, verifierFunc(validVerifier), userServiceStub{
		sync: func(_ context.Context, input user.Login) (user.Profile, error) {
			if input.ID != "verified-user" || input.DisplayName != "Verified Name" {
				t.Fatalf("untrusted login identity: %+v", input)
			}
			return user.Profile{ID: input.ID, DisplayName: input.DisplayName}, nil
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login?userId=someone-else",
		strings.NewReader(`{"id":"someone-else","displayName":"Spoofed Name"}`))
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected login response: %d %s", response.Code, response.Body.String())
	}
}

func TestMeOnlyReadsVerifiedUser(t *testing.T) {
	router := userTestRouter(t, verifierFunc(validVerifier), userServiceStub{
		get: func(_ context.Context, id string) (user.Profile, error) {
			if id != "verified-user" {
				t.Fatalf("attempted to read a different user: %s", id)
			}
			return user.Profile{ID: id}, nil
		},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me?id=someone-else", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var profile user.Profile
	if err := json.Unmarshal(response.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || profile.ID != "verified-user" {
		t.Fatalf("unexpected profile response: %d %+v", response.Code, profile)
	}
}

func TestUserAPIErrors(t *testing.T) {
	for _, tt := range []struct {
		name       string
		verifyErr  error
		storeErr   error
		wantStatus int
	}{
		{"invalid token", auth.ErrInvalidToken, nil, http.StatusUnauthorized},
		{"auth unavailable", auth.ErrUnavailable, nil, http.StatusServiceUnavailable},
		{"not registered", nil, user.ErrNotFound, http.StatusNotFound},
		{"database unavailable", nil, errors.New("private connection details"), http.StatusServiceUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			router := userTestRouter(t, verifierFunc(func(context.Context, string) (auth.Identity, error) {
				return auth.Identity{ID: "verified-user"}, tt.verifyErr
			}), userServiceStub{get: func(context.Context, string) (user.Profile, error) {
				return user.Profile{}, tt.storeErr
			}})
			request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
			request.Header.Set("Authorization", "Bearer valid-token")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.wantStatus || strings.Contains(response.Body.String(), "private connection details") {
				t.Fatalf("unexpected API error: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestOnboardingUsesVerifiedUserAndRequestBody(t *testing.T) {
	router := userTestRouter(t, verifierFunc(validVerifier), userServiceStub{
		onboard: func(_ context.Context, id string, input user.Onboarding) (user.Profile, error) {
			if id != "verified-user" {
				t.Fatalf("onboarded a different user: %s", id)
			}
			if input.DisplayName != "지수" || input.Username != "jisu_01" || input.DefaultVisibility != "hidden" ||
				input.TermsVersion != "v1" || input.PhotoStoragePath == nil || *input.PhotoStoragePath != "path" {
				t.Fatalf("request body was not passed on: %+v", input)
			}
			return user.Profile{ID: id, OnboardingCompleted: true}, nil
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/onboarding?id=someone-else", strings.NewReader(
		`{"id":"someone-else","displayName":"지수","username":"jisu_01","defaultVisibility":"hidden","termsVersion":"v1","photoStoragePath":"path"}`))
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	var profile user.Profile
	if err := json.Unmarshal(response.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || profile.ID != "verified-user" || !profile.OnboardingCompleted {
		t.Fatalf("unexpected onboarding response: %d %s", response.Code, response.Body.String())
	}
}

func TestOnboardingRejectsUnreadableBody(t *testing.T) {
	for name, body := range map[string]string{
		"empty":      "",
		"malformed":  "{",
		"wrong type": `{"username":1}`,
		"too large":  `{"displayName":"` + strings.Repeat("x", maxProfileBodySize) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			router := userTestRouter(t, verifierFunc(validVerifier), userServiceStub{})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/onboarding", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer valid-token")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestOnboardingErrors(t *testing.T) {
	for _, tt := range []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{user.ErrInvalidProfile, http.StatusBadRequest, "invalid_profile"},
		{user.ErrNotFound, http.StatusNotFound, "user_not_found"},
		{user.ErrUsernameTaken, http.StatusConflict, "username_taken"},
		{user.ErrOnboardingCompleted, http.StatusConflict, "onboarding_completed"},
		{user.ErrTermsOutdated, http.StatusConflict, "terms_outdated"},
		{fmt.Errorf("wrapped: %w", user.ErrUsernameTaken), http.StatusConflict, "username_taken"},
		{errors.New("private connection details"), http.StatusServiceUnavailable, "database_unavailable"},
	} {
		t.Run(tt.wantCode, func(t *testing.T) {
			router := userTestRouter(t, verifierFunc(validVerifier), userServiceStub{
				onboard: func(context.Context, string, user.Onboarding) (user.Profile, error) {
					return user.Profile{}, tt.err
				},
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/onboarding", strings.NewReader("{}"))
			request.Header.Set("Authorization", "Bearer valid-token")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			var body struct {
				Error struct{ Code string } `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != tt.wantStatus || body.Error.Code != tt.wantCode ||
				strings.Contains(response.Body.String(), "private connection details") {
				t.Fatalf("unexpected error response: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestDuplicateAuthorizationHeadersAreRejected(t *testing.T) {
	router := userTestRouter(t, verifierFunc(validVerifier), userServiceStub{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	request.Header.Add("Authorization", "Bearer token-one")
	request.Header.Add("Authorization", "Bearer token-two")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate headers: got %d, want 401", response.Code)
	}
}

func TestAuthenticationPropagatesRequestCancellation(t *testing.T) {
	router := userTestRouter(t, verifierFunc(func(ctx context.Context, _ string) (auth.Identity, error) {
		if ctx.Err() == nil {
			t.Fatal("request cancellation did not reach the verifier")
		}
		return auth.Identity{}, auth.ErrUnavailable
	}), userServiceStub{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("canceled verification: got %d, want 503", response.Code)
	}
}

func TestCalendarImportRequiresBothFields(t *testing.T) {
	router := userTestRouter(t, verifierFunc(validVerifier), userServiceStub{})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/users/me/calendar-import",
		strings.NewReader(`{"calendarPermissionStatus":"granted"}`))
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", response.Code)
	}
}

func TestUsernameAvailabilityNeedsNoToken(t *testing.T) {
	router := userTestRouter(t, verifierFunc(func(context.Context, string) (auth.Identity, error) {
		t.Fatal("availability must not verify a token")
		return auth.Identity{}, nil
	}), userServiceStub{
		available: func(_ context.Context, username string) (bool, error) { return username == "free_id", nil },
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/usernames/free_id/availability", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":true`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}
