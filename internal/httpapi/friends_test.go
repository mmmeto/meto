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
	"meto/internal/friend"
	"meto/internal/user"
)

type friendServiceStub struct {
	FriendService
	send   func(context.Context, string, string) (friend.Request, error)
	accept func(context.Context, string, string) error
	list   func(context.Context, string) ([]friend.Friend, error)
}

func (s friendServiceStub) SendRequest(ctx context.Context, from, to string) (friend.Request, error) {
	return s.send(ctx, from, to)
}

func (s friendServiceStub) AcceptRequest(ctx context.Context, userID, requestID string) error {
	return s.accept(ctx, userID, requestID)
}

func (s friendServiceStub) ListFriends(ctx context.Context, userID string) ([]friend.Friend, error) {
	return s.list(ctx, userID)
}

func friendTestRouter(t *testing.T, friends FriendService) http.Handler {
	t.Helper()
	router, err := NewRouter(pingerFunc(func(context.Context) error { return nil }),
		verifierFunc(validVerifier), userServiceStub{}, friends)
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func callAPI(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestFriendRoutesRequireAuthentication(t *testing.T) {
	router, err := NewRouter(pingerFunc(func(context.Context) error { return nil }),
		verifierFunc(func(context.Context, string) (auth.Identity, error) {
			return auth.Identity{}, auth.ErrInvalidToken
		}), userServiceStub{}, friendServiceStub{})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/friends"},
		{http.MethodPut, "/api/v1/friends/other/sharing-level"},
		{http.MethodGet, "/api/v1/users/search?username=jisu"},
		{http.MethodPost, "/api/v1/friend-requests"},
		{http.MethodGet, "/api/v1/friend-requests/received"},
		{http.MethodGet, "/api/v1/friend-requests/sent"},
		{http.MethodPost, "/api/v1/friend-requests/id/accept"},
		{http.MethodPost, "/api/v1/friend-requests/id/reject"},
		{http.MethodGet, "/api/v1/blocks"},
		{http.MethodPut, "/api/v1/blocks/other"},
		{http.MethodDelete, "/api/v1/blocks/other"},
	} {
		if got := callAPI(router, route.method, route.path, "").Code; got != http.StatusUnauthorized {
			t.Fatalf("%s %s: got %d, want 401", route.method, route.path, got)
		}
	}
}

func TestSendRequestUsesVerifiedSender(t *testing.T) {
	router := friendTestRouter(t, friendServiceStub{
		send: func(_ context.Context, from, to string) (friend.Request, error) {
			if from != "verified-user" || to != "target" {
				t.Fatalf("unexpected request: %s -> %s", from, to)
			}
			return friend.Request{ID: "id"}, nil
		},
	})
	response := callAPI(router, http.MethodPost, "/api/v1/friend-requests", `{"fromUid":"spoofed","toUserId":"target"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
}

func TestSendRequestRejectsUnreadableBody(t *testing.T) {
	router := friendTestRouter(t, friendServiceStub{})
	for _, body := range []string{"", "{", "{}", `{"toUserId":1}`} {
		if got := callAPI(router, http.MethodPost, "/api/v1/friend-requests", body).Code; got != http.StatusBadRequest {
			t.Fatalf("body %q: got %d, want 400", body, got)
		}
	}
}

func TestFriendErrors(t *testing.T) {
	for _, tt := range []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{friend.ErrUserNotFound, http.StatusNotFound, "friend_target_not_found"},
		{friend.ErrSelfTarget, http.StatusBadRequest, "self_target"},
		{friend.ErrAlreadyFriends, http.StatusConflict, "already_friends"},
		{friend.ErrRequestPending, http.StatusConflict, "friend_request_pending"},
		{friend.ErrRequestNotFound, http.StatusNotFound, "friend_request_not_found"},
		{friend.ErrRequestClosed, http.StatusConflict, "friend_request_closed"},
		{friend.ErrRequestUnavailable, http.StatusForbidden, "friend_request_unavailable"},
		{user.ErrOnboardingRequired, http.StatusConflict, "onboarding_required"},
		{errors.New("private connection details"), http.StatusServiceUnavailable, "database_unavailable"},
	} {
		t.Run(tt.wantCode, func(t *testing.T) {
			router := friendTestRouter(t, friendServiceStub{
				accept: func(context.Context, string, string) error { return tt.err },
			})
			response := callAPI(router, http.MethodPost, "/api/v1/friend-requests/id/accept", "")
			var body struct {
				Error struct{ Code string } `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != tt.wantStatus || body.Error.Code != tt.wantCode ||
				strings.Contains(response.Body.String(), "private connection details") {
				t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestEmptyFriendListIsNotNull(t *testing.T) {
	router := friendTestRouter(t, friendServiceStub{
		list: func(context.Context, string) ([]friend.Friend, error) { return []friend.Friend{}, nil },
	})
	response := callAPI(router, http.MethodGet, "/api/v1/friends", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"friends":[]`) {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
}
