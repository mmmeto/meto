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
	"meto/internal/friend"
	"meto/internal/testutil"
	"meto/internal/user"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

type friendAPI struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router http.Handler
}

func newFriendAPI(t *testing.T) friendAPI {
	t.Helper()
	pool := testutil.Postgres(t)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(filepath.Join("..", "..", "migrations")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	verifier := verifierFunc(func(_ context.Context, token string) (auth.Identity, error) {
		return auth.Identity{ID: token}, nil
	})
	router, err := NewRouter(pool, verifier, user.NewService(user.NewRepository(pool)),
		friend.NewService(friend.NewRepository(pool)))
	if err != nil {
		t.Fatal(err)
	}
	return friendAPI{t: t, pool: pool, router: router}
}

func (a friendAPI) call(token, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	a.router.ServeHTTP(response, request)
	return response
}

func (a friendAPI) expect(response *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
	a.t.Helper()
	if response.Code != status {
		a.t.Fatalf("got %d, want %d: %s", response.Code, status, response.Body.String())
	}
	return response
}

func (a friendAPI) addUser(id, username string) {
	a.t.Helper()
	_, err := a.pool.Exec(a.t.Context(), `INSERT INTO users (id, email, display_name, username, onboarding_completed)
		VALUES ($1, 'private@example.test', $1, $2, TRUE)`, id, username)
	if err != nil {
		a.t.Fatal(err)
	}
}

func (a friendAPI) count(query string, args ...any) int {
	a.t.Helper()
	var count int
	if err := a.pool.QueryRow(a.t.Context(), query, args...).Scan(&count); err != nil {
		a.t.Fatal(err)
	}
	return count
}

func (a friendAPI) sendRequest(from, to string) string {
	a.t.Helper()
	response := a.expect(a.call(from, http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"`+to+`"}`), http.StatusCreated)
	var request friend.Request
	if err := json.Unmarshal(response.Body.Bytes(), &request); err != nil {
		a.t.Fatal(err)
	}
	return request.ID
}

func TestFriendFlowWithPostgres(t *testing.T) {
	api := newFriendAPI(t)
	const amy, zed = "amy", "Zed"
	api.addUser(amy, "amy_id")
	api.addUser(zed, "zed_id")

	id := api.sendRequest(zed, amy)
	received := api.expect(api.call(amy, http.MethodGet, "/api/v1/friend-requests/received", ""), http.StatusOK).Body.String()
	if !strings.Contains(received, id) || !strings.Contains(received, `"username":"zed_id"`) {
		t.Fatalf("unexpected received requests: %s", received)
	}
	if strings.Contains(received, "private@example.test") {
		t.Fatalf("request exposed an email: %s", received)
	}
	sent := api.expect(api.call(zed, http.MethodGet, "/api/v1/friend-requests/sent", ""), http.StatusOK).Body.String()
	if !strings.Contains(sent, id) {
		t.Fatalf("unexpected sent requests: %s", sent)
	}

	api.expect(api.call(amy, http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"Zed"}`), http.StatusConflict)
	api.expect(api.call(zed, http.MethodPost, "/api/v1/friend-requests/"+id+"/accept", ""), http.StatusNotFound)
	api.expect(api.call(amy, http.MethodPost, "/api/v1/friend-requests/"+id+"/accept", ""), http.StatusNoContent)
	api.expect(api.call(amy, http.MethodPost, "/api/v1/friend-requests/"+id+"/accept", ""), http.StatusNoContent)
	api.expect(api.call(amy, http.MethodPost, "/api/v1/friend-requests/"+id+"/reject", ""), http.StatusConflict)
	api.expect(api.call(zed, http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"amy"}`), http.StatusConflict)

	var list struct {
		Friends []friend.Friend `json:"friends"`
	}
	body := api.expect(api.call(amy, http.MethodGet, "/api/v1/friends", ""), http.StatusOK).Body
	if err := json.Unmarshal(body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Friends) != 1 || list.Friends[0].ID != zed || list.Friends[0].Status != "connected" ||
		list.Friends[0].MySharingLevelToFriend != friendDefaultLevel {
		t.Fatalf("unexpected friends: %+v", list.Friends)
	}
	if strings.Contains(body.String(), "private@example.test") {
		t.Fatalf("friend list exposed an email: %s", body.String())
	}

	var connection friend.Friend
	response := api.expect(api.call(amy, http.MethodPut, "/api/v1/friends/Zed/sharing-level", `{"level":"full_visible"}`), http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &connection); err != nil {
		t.Fatal(err)
	}
	if connection.MySharingLevelToFriend != "full_visible" || connection.FriendSharingLevelToMe != friendDefaultLevel {
		t.Fatalf("unexpected levels for amy: %+v", connection)
	}
	response = api.expect(api.call(zed, http.MethodPut, "/api/v1/friends/amy/sharing-level", `{"level":"hidden"}`), http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &connection); err != nil {
		t.Fatal(err)
	}
	if connection.MySharingLevelToFriend != "hidden" || connection.FriendSharingLevelToMe != "full_visible" {
		t.Fatalf("unexpected levels for zed: %+v", connection)
	}

	search := api.expect(api.call(amy, http.MethodGet, "/api/v1/users/search?username=@ZED_id", ""), http.StatusOK).Body.String()
	if !strings.Contains(search, `"id":"Zed"`) || strings.Contains(search, "private@example.test") {
		t.Fatalf("unexpected search result: %s", search)
	}
	self := api.expect(api.call(amy, http.MethodGet, "/api/v1/users/search?username=amy_id", ""), http.StatusOK).Body.String()
	if !strings.Contains(self, `"user":null`) {
		t.Fatalf("search returned the viewer: %s", self)
	}

	api.expect(api.call(amy, http.MethodPut, "/api/v1/blocks/Zed", ""), http.StatusNoContent)
	api.expect(api.call(amy, http.MethodPut, "/api/v1/blocks/Zed", ""), http.StatusNoContent)
	if got := api.count("SELECT COUNT(*) FROM friendships"); got != 0 {
		t.Fatalf("block kept the friendship: %d", got)
	}
	if got := api.count("SELECT COUNT(*) FROM reports WHERE report_type = 'block' AND reporter_id = 'amy'"); got != 1 {
		t.Fatalf("block reports = %d, want 1", got)
	}
	api.expect(api.call(zed, http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"amy"}`), http.StatusForbidden)
	api.expect(api.call(amy, http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"Zed"}`), http.StatusForbidden)
	for _, viewer := range []struct{ token, username string }{{amy, "zed_id"}, {zed, "amy_id"}} {
		result := api.expect(api.call(viewer.token, http.MethodGet, "/api/v1/users/search?username="+viewer.username, ""), http.StatusOK)
		if !strings.Contains(result.Body.String(), `"user":null`) {
			t.Fatalf("search crossed a block: %s", result.Body.String())
		}
	}
	blocked := api.expect(api.call(amy, http.MethodGet, "/api/v1/blocks", ""), http.StatusOK).Body.String()
	if !strings.Contains(blocked, `"id":"Zed"`) {
		t.Fatalf("unexpected blocked users: %s", blocked)
	}
	if other := api.expect(api.call(zed, http.MethodGet, "/api/v1/blocks", ""), http.StatusOK).Body.String(); !strings.Contains(other, `"users":[]`) {
		t.Fatalf("blocked user saw the block: %s", other)
	}

	api.expect(api.call(zed, http.MethodPut, "/api/v1/blocks/amy", ""), http.StatusNoContent)
	api.expect(api.call(amy, http.MethodDelete, "/api/v1/blocks/Zed", ""), http.StatusNoContent)
	api.expect(api.call(amy, http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"Zed"}`), http.StatusForbidden)
	api.expect(api.call(zed, http.MethodDelete, "/api/v1/blocks/amy", ""), http.StatusNoContent)

	resent := api.sendRequest(zed, amy)
	api.expect(api.call(amy, http.MethodPost, "/api/v1/friend-requests/"+resent+"/reject", ""), http.StatusNoContent)
	api.expect(api.call(amy, http.MethodPost, "/api/v1/friend-requests/"+resent+"/reject", ""), http.StatusNoContent)
	if got := api.count("SELECT COUNT(*) FROM friendships"); got != 0 {
		t.Fatalf("unblock or reject restored the friendship: %d", got)
	}
	api.sendRequest(zed, amy)

	if _, err := api.pool.Exec(t.Context(), "DELETE FROM users WHERE id = $1", zed); err != nil {
		t.Fatal(err)
	}
	if got := api.count("SELECT COUNT(*) FROM friend_requests"); got != 0 {
		t.Fatalf("deleted account left requests: %d", got)
	}
	if got := api.count("SELECT COUNT(*) FROM reports WHERE reporter_id = 'amy'"); got != 1 {
		t.Fatalf("deleted account removed reports: %d", got)
	}
}

const friendDefaultLevel = "time_only"

func TestFriendTargetsAreValidatedWithPostgres(t *testing.T) {
	api := newFriendAPI(t)
	api.addUser("amy", "amy_id")
	if _, err := api.pool.Exec(t.Context(), "INSERT INTO users (id) VALUES ('pending-user')"); err != nil {
		t.Fatal(err)
	}
	api.expect(api.call("amy", http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"missing"}`), http.StatusNotFound)
	api.expect(api.call("amy", http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"pending-user"}`), http.StatusNotFound)
	api.expect(api.call("amy", http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"amy"}`), http.StatusBadRequest)
	api.expect(api.call("pending-user", http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"amy"}`), http.StatusConflict)
	api.expect(api.call("amy", http.MethodPut, "/api/v1/friends/missing/sharing-level", `{"level":"hidden"}`), http.StatusNotFound)
	api.expect(api.call("amy", http.MethodPut, "/api/v1/blocks/missing", ""), http.StatusNotFound)
	for _, junk := range []string{"%00", "%FF", "not-a-uuid"} {
		api.expect(api.call("amy", http.MethodPost, "/api/v1/friend-requests/"+junk+"/accept", ""), http.StatusNotFound)
		if junk != "not-a-uuid" {
			api.expect(api.call("amy", http.MethodPut, "/api/v1/blocks/"+junk, ""), http.StatusNotFound)
			api.expect(api.call("amy", http.MethodDelete, "/api/v1/blocks/"+junk, ""), http.StatusNoContent)
		}
	}
}

func TestOppositeRequestsRaceWithPostgres(t *testing.T) {
	api := newFriendAPI(t)
	api.addUser("amy", "amy_id")
	api.addUser("Zed", "zed_id")
	for round := range 20 {
		var workers sync.WaitGroup
		statuses := make(chan int, 2)
		for _, pair := range [][2]string{{"amy", "Zed"}, {"Zed", "amy"}} {
			workers.Add(1)
			go func() {
				defer workers.Done()
				statuses <- api.call(pair[0], http.MethodPost, "/api/v1/friend-requests", `{"toUserId":"`+pair[1]+`"}`).Code
			}()
		}
		workers.Wait()
		close(statuses)
		created := 0
		for status := range statuses {
			switch status {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
			default:
				t.Fatalf("round %d: status %d", round, status)
			}
		}
		if created != 1 {
			t.Fatalf("round %d: created %d requests, want 1", round, created)
		}
		if _, err := api.pool.Exec(t.Context(), "DELETE FROM friend_requests"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAcceptAndBlockRaceWithPostgres(t *testing.T) {
	api := newFriendAPI(t)
	api.addUser("amy", "amy_id")
	api.addUser("Zed", "zed_id")
	for round := range 60 {
		id := api.sendRequest("Zed", "amy")
		var workers sync.WaitGroup
		workers.Add(2)
		go func() {
			defer workers.Done()
			api.call("amy", http.MethodPost, "/api/v1/friend-requests/"+id+"/accept", "")
		}()
		go func() {
			defer workers.Done()
			api.call("Zed", http.MethodPut, "/api/v1/blocks/amy", "")
		}()
		workers.Wait()
		if blocks, friendships := api.count("SELECT COUNT(*) FROM user_blocks"), api.count("SELECT COUNT(*) FROM friendships"); blocks != 1 || friendships != 0 {
			t.Fatalf("round %d: blocks=%d friendships=%d", round, blocks, friendships)
		}
		if _, err := api.pool.Exec(t.Context(), "DELETE FROM user_blocks; DELETE FROM friend_requests; DELETE FROM reports"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRepeatedAcceptRaceWithPostgres(t *testing.T) {
	api := newFriendAPI(t)
	api.addUser("amy", "amy_id")
	api.addUser("Zed", "zed_id")
	id := api.sendRequest("Zed", "amy")
	var workers sync.WaitGroup
	failures := make(chan string, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if got := api.call("amy", http.MethodPost, "/api/v1/friend-requests/"+id+"/accept", ""); got.Code != http.StatusNoContent {
				failures <- fmt.Sprintf("%d %s", got.Code, got.Body.String())
			}
		}()
	}
	workers.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if got := api.count("SELECT COUNT(*) FROM friendships"); got != 1 {
		t.Fatalf("friendships = %d, want 1", got)
	}
}
