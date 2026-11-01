package httpapi

import (
	"context"
	"encoding/json"
	"errors"
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

type userStoreStub struct {
	sync func(context.Context, user.Login) (user.Profile, error)
	get  func(context.Context, string) (user.Profile, error)
}

func (s userStoreStub) SyncLogin(ctx context.Context, input user.Login) (user.Profile, error) {
	if s.sync == nil {
		panic("unexpected user write")
	}
	return s.sync(ctx, input)
}

func (s userStoreStub) GetByID(ctx context.Context, id string) (user.Profile, error) {
	if s.get == nil {
		panic("unexpected user read")
	}
	return s.get(ctx, id)
}

func newStatusRouter(db Pinger) (*gin.Engine, error) {
	return NewRouter(db, verifierFunc(func(context.Context, string) (auth.Identity, error) {
		return auth.Identity{}, auth.ErrInvalidToken
	}), userStoreStub{})
}

func userTestRouter(t *testing.T, verifier auth.Verifier, users UserStore) *gin.Engine {
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
	} {
		for _, header := range []string{"", "Basic abc", "Bearer", "Bearer a b", "Bearer " + strings.Repeat("x", 17000), "Bearer invalid"} {
			router := userTestRouter(t, verifierFunc(func(context.Context, string) (auth.Identity, error) {
				return auth.Identity{}, auth.ErrInvalidToken
			}), userStoreStub{})
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
	router := userTestRouter(t, verifierFunc(validVerifier), userStoreStub{
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
	router := userTestRouter(t, verifierFunc(validVerifier), userStoreStub{
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
			}), userStoreStub{get: func(context.Context, string) (user.Profile, error) {
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

func TestDuplicateAuthorizationHeadersAreRejected(t *testing.T) {
	router := userTestRouter(t, verifierFunc(validVerifier), userStoreStub{})
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
	}), userStoreStub{})
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
