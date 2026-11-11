package friend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"meto/internal/user"
)

type storeStub struct {
	Store
	tx      PairTx
	pair    RequestPair
	pairErr error
	lookup  func(string) (*user.PublicProfile, error)
	setErr  error
}

func (s storeStub) WithPair(_ context.Context, _, _ string, fn func(PairTx) error) error {
	return fn(s.tx)
}

func (s storeStub) GetRequest(_ context.Context, id string) (Request, error) {
	return Request{ID: id}, nil
}

func (s storeStub) GetRequestPair(context.Context, string) (RequestPair, error) {
	return s.pair, s.pairErr
}

func (s storeStub) FindByUsername(_ context.Context, _, username string) (*user.PublicProfile, error) {
	return s.lookup(username)
}

func (s storeStub) SetSharingLevel(context.Context, string, string, string) error {
	return s.setErr
}

type txStub struct {
	PairTx
	state      PairState
	status     string
	created    bool
	responded  string
	friendship bool
	blocked    bool
}

func (t *txStub) State(context.Context, string, string) (PairState, error) { return t.state, nil }

func (t *txStub) RequestStatus(context.Context, string) (string, error) { return t.status, nil }

func (t *txStub) CreateRequest(context.Context, string, string) (string, error) {
	t.created = true
	return "request-id", nil
}

func (t *txStub) RespondToRequest(_ context.Context, _, status string) error {
	t.responded = status
	return nil
}

func (t *txStub) CreateFriendship(context.Context, string, string) error {
	t.friendship = true
	return nil
}

func (t *txStub) Block(context.Context, string, string) error {
	t.blocked = true
	return nil
}

func readyState() PairState {
	return PairState{ActorOnboarded: true, TargetOnboarded: true}
}

func TestSendRequestRules(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*PairState)
		want   error
	}{
		{"allowed", func(*PairState) {}, nil},
		{"sender not onboarded", func(s *PairState) { s.ActorOnboarded = false }, user.ErrOnboardingRequired},
		{"target missing", func(s *PairState) { s.TargetOnboarded = false }, ErrUserNotFound},
		{"blocked", func(s *PairState) { s.Blocked = true }, ErrRequestUnavailable},
		{"already friends", func(s *PairState) { s.Friends = true }, ErrAlreadyFriends},
		{"pending", func(s *PairState) { s.RequestPending = true }, ErrRequestPending},
		{"block wins over pending", func(s *PairState) { s.Blocked, s.RequestPending = true, true }, ErrRequestUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := readyState()
			tt.change(&state)
			tx := &txStub{state: state}
			request, err := NewService(storeStub{tx: tx}).SendRequest(t.Context(), "a", "b")
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if tx.created != (tt.want == nil) {
				t.Fatalf("created = %v for error %v", tx.created, tt.want)
			}
			if tt.want == nil && request.ID != "request-id" {
				t.Fatalf("unexpected request: %+v", request)
			}
		})
	}
}

func TestTargetsAreCheckedBeforeTouchingStore(t *testing.T) {
	service := NewService(storeStub{})
	if _, err := service.SendRequest(t.Context(), "a", "a"); !errors.Is(err, ErrSelfTarget) {
		t.Fatalf("self request: %v", err)
	}
	if err := service.Block(t.Context(), "a", "a"); !errors.Is(err, ErrSelfTarget) {
		t.Fatalf("self block: %v", err)
	}
	for _, id := range []string{"", "a\x00b", "\xff", strings.Repeat("a", maxIDLength+1)} {
		if _, err := service.SendRequest(t.Context(), "a", id); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("request to %q: %v", id, err)
		}
		if err := service.Block(t.Context(), "a", id); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("block %q: %v", id, err)
		}
		if err := service.AcceptRequest(t.Context(), "a", id); !errors.Is(err, ErrRequestNotFound) {
			t.Fatalf("accept %q: %v", id, err)
		}
		if _, err := service.SetSharingLevel(t.Context(), "a", id, "hidden"); !errors.Is(err, ErrNotFriends) {
			t.Fatalf("level for %q: %v", id, err)
		}
		if err := service.Unblock(t.Context(), "a", id); err != nil {
			t.Fatalf("unblock %q: %v", id, err)
		}
	}
}

func TestRespondingToRequest(t *testing.T) {
	pair := RequestPair{FromUID: "a", ToUID: "b"}
	for _, tt := range []struct {
		name           string
		status         string
		accept         bool
		want           error
		wantResponded  string
		wantFriendship bool
	}{
		{"accept", StatusPending, true, nil, StatusAccepted, true},
		{"reject", StatusPending, false, nil, StatusRejected, false},
		{"accept retry", StatusAccepted, true, nil, "", false},
		{"reject retry", StatusRejected, false, nil, "", false},
		{"accept rejected", StatusRejected, true, ErrRequestClosed, "", false},
		{"reject accepted", StatusAccepted, false, ErrRequestClosed, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx := &txStub{status: tt.status}
			service := NewService(storeStub{tx: tx, pair: pair})
			respond := service.RejectRequest
			if tt.accept {
				respond = service.AcceptRequest
			}
			if err := respond(t.Context(), "b", "id"); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if tx.responded != tt.wantResponded || tx.friendship != tt.wantFriendship {
				t.Fatalf("unexpected writes: %+v", tx)
			}
		})
	}
}

func TestOnlyTheReceiverCanRespond(t *testing.T) {
	tx := &txStub{status: StatusPending}
	service := NewService(storeStub{tx: tx, pair: RequestPair{FromUID: "a", ToUID: "b"}})
	for _, actor := range []string{"a", "stranger"} {
		if err := service.AcceptRequest(t.Context(), actor, "id"); !errors.Is(err, ErrRequestNotFound) {
			t.Fatalf("%s: got %v", actor, err)
		}
	}
	if tx.friendship {
		t.Fatal("a non-receiver created a friendship")
	}
}

func TestBlockRules(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*PairState)
		want   error
	}{
		{"allowed", func(*PairState) {}, nil},
		{"already blocked by the other side", func(s *PairState) { s.Blocked = true }, nil},
		{"actor not onboarded", func(s *PairState) { s.ActorOnboarded = false }, user.ErrOnboardingRequired},
		{"target missing", func(s *PairState) { s.TargetOnboarded = false }, ErrUserNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := readyState()
			tt.change(&state)
			tx := &txStub{state: state}
			if err := NewService(storeStub{tx: tx}).Block(t.Context(), "a", "b"); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
			if tx.blocked != (tt.want == nil) {
				t.Fatalf("blocked = %v", tx.blocked)
			}
		})
	}
}

func TestSearchNormalizesUsername(t *testing.T) {
	service := NewService(storeStub{lookup: func(username string) (*user.PublicProfile, error) {
		if username != "jisu_01" {
			t.Fatalf("lookup was not normalized: %q", username)
		}
		return &user.PublicProfile{ID: "found"}, nil
	}})
	profile, err := service.SearchByUsername(t.Context(), "a", " @Jisu_01 ")
	if err != nil || profile == nil || profile.ID != "found" {
		t.Fatalf("got %+v, %v", profile, err)
	}
	for _, raw := range []string{"", "@", "a!"} {
		if profile, err := NewService(storeStub{}).SearchByUsername(t.Context(), "a", raw); profile != nil || err != nil {
			t.Fatalf("search %q: %+v %v", raw, profile, err)
		}
	}
}

func TestSetSharingLevelRejectsUnknownLevel(t *testing.T) {
	if _, err := NewService(storeStub{}).SetSharingLevel(t.Context(), "a", "b", "public"); !errors.Is(err, ErrInvalidSharingLevel) {
		t.Fatalf("got %v", err)
	}
}

func TestOrderedPairMatchesByteOrder(t *testing.T) {
	if low, high := orderedPair("amy", "Zed"); low != "Zed" || high != "amy" {
		t.Fatalf("got %s %s", low, high)
	}
}
