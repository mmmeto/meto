package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error {
	return f(ctx)
}

func TestHealthDoesNotQueryDatabase(t *testing.T) {
	router, err := newStatusRouter(pingerFunc(func(context.Context) error {
		t.Fatal("health must not query the database")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}

func TestReadiness(t *testing.T) {
	for _, tt := range []struct {
		name       string
		dbErr      error
		wantCode   int
		wantStatus string
		wantDB     string
	}{
		{"connected", nil, http.StatusOK, "ready", "ok"},
		{"unavailable", errors.New("private connection details"), http.StatusServiceUnavailable, "not_ready", "unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			router, err := newStatusRouter(pingerFunc(func(context.Context) error { return tt.dbErr }))
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil))
			if response.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantCode)
			}
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 || body["status"] != tt.wantStatus || body["database"] != tt.wantDB {
				t.Fatalf("unexpected response: %v", body)
			}
		})
	}
}

func TestReadinessPropagatesRequestDeadline(t *testing.T) {
	router, err := newStatusRouter(pingerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	start := time.Now()
	router.ServeHTTP(response, request)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("request deadline was not propagated: elapsed %s", elapsed)
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}
