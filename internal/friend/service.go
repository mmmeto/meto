package friend

import (
	"context"
	"errors"
	"strings"

	"meto/internal/user"
)

type Store interface {
	ListFriends(context.Context, string) ([]Friend, error)
	GetFriend(context.Context, string, string) (Friend, error)
	SetSharingLevel(context.Context, string, string, string) error
	FindByUsername(context.Context, string, string) (*user.PublicProfile, error)
	ListReceivedRequests(context.Context, string) ([]Request, error)
	ListSentRequests(context.Context, string) ([]Request, error)
	GetRequest(context.Context, string) (Request, error)
	GetRequestPair(context.Context, string) (RequestPair, error)
	ListBlocked(context.Context, string) ([]BlockedUser, error)
	DeleteBlock(context.Context, string, string) error
	WithPair(context.Context, string, string, func(PairTx) error) error
}

type PairTx interface {
	State(context.Context, string, string) (PairState, error)
	RequestStatus(context.Context, string) (string, error)
	CreateRequest(context.Context, string, string) (string, error)
	RespondToRequest(context.Context, string, string) error
	CreateFriendship(context.Context, string, string) error
	Block(context.Context, string, string) error
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) ListFriends(ctx context.Context, userID string) ([]Friend, error) {
	return s.store.ListFriends(ctx, userID)
}

func (s *Service) SetSharingLevel(ctx context.Context, userID, friendID, level string) (Friend, error) {
	if !user.ValidVisibility(level) {
		return Friend{}, ErrInvalidSharingLevel
	}
	if !validID(friendID) {
		return Friend{}, ErrNotFriends
	}
	if err := s.store.SetSharingLevel(ctx, userID, friendID, level); err != nil {
		return Friend{}, err
	}
	return s.store.GetFriend(ctx, userID, friendID)
}

func (s *Service) SearchByUsername(ctx context.Context, viewerID, raw string) (*user.PublicProfile, error) {
	username, err := user.NormalizeUsername(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
	if errors.Is(err, user.ErrInvalidProfile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.store.FindByUsername(ctx, viewerID, username)
}

func (s *Service) ListReceivedRequests(ctx context.Context, userID string) ([]Request, error) {
	return s.store.ListReceivedRequests(ctx, userID)
}

func (s *Service) ListSentRequests(ctx context.Context, userID string) ([]Request, error) {
	return s.store.ListSentRequests(ctx, userID)
}

func (s *Service) SendRequest(ctx context.Context, fromID, toID string) (Request, error) {
	if fromID == toID {
		return Request{}, ErrSelfTarget
	}
	if !validID(toID) {
		return Request{}, ErrUserNotFound
	}
	var requestID string
	err := s.store.WithPair(ctx, fromID, toID, func(tx PairTx) error {
		state, err := tx.State(ctx, fromID, toID)
		if err != nil {
			return err
		}
		switch {
		case !state.ActorOnboarded:
			return user.ErrOnboardingRequired
		case !state.TargetOnboarded:
			return ErrUserNotFound
		case state.Blocked:
			return ErrRequestUnavailable
		case state.Friends:
			return ErrAlreadyFriends
		case state.RequestPending:
			return ErrRequestPending
		}
		requestID, err = tx.CreateRequest(ctx, fromID, toID)
		return err
	})
	if err != nil {
		return Request{}, err
	}
	return s.store.GetRequest(ctx, requestID)
}

func (s *Service) AcceptRequest(ctx context.Context, userID, requestID string) error {
	return s.respond(ctx, userID, requestID, StatusAccepted)
}

func (s *Service) RejectRequest(ctx context.Context, userID, requestID string) error {
	return s.respond(ctx, userID, requestID, StatusRejected)
}

func (s *Service) respond(ctx context.Context, userID, requestID, status string) error {
	if !validID(requestID) {
		return ErrRequestNotFound
	}
	pair, err := s.store.GetRequestPair(ctx, requestID)
	if err != nil {
		return err
	}
	if pair.ToUID != userID {
		return ErrRequestNotFound
	}
	return s.store.WithPair(ctx, pair.FromUID, pair.ToUID, func(tx PairTx) error {
		current, err := tx.RequestStatus(ctx, requestID)
		if err != nil {
			return err
		}
		if current == status {
			return nil
		}
		if current != StatusPending {
			return ErrRequestClosed
		}
		if err := tx.RespondToRequest(ctx, requestID, status); err != nil {
			return err
		}
		if status == StatusAccepted {
			return tx.CreateFriendship(ctx, pair.FromUID, pair.ToUID)
		}
		return nil
	})
}

func (s *Service) ListBlocked(ctx context.Context, userID string) ([]BlockedUser, error) {
	return s.store.ListBlocked(ctx, userID)
}

func (s *Service) Block(ctx context.Context, userID, targetID string) error {
	if userID == targetID {
		return ErrSelfTarget
	}
	if !validID(targetID) {
		return ErrUserNotFound
	}
	return s.store.WithPair(ctx, userID, targetID, func(tx PairTx) error {
		state, err := tx.State(ctx, userID, targetID)
		if err != nil {
			return err
		}
		if !state.ActorOnboarded {
			return user.ErrOnboardingRequired
		}
		if !state.TargetOnboarded {
			return ErrUserNotFound
		}
		return tx.Block(ctx, userID, targetID)
	})
}

func (s *Service) Unblock(ctx context.Context, userID, targetID string) error {
	if !validID(targetID) {
		return nil
	}
	return s.store.DeleteBlock(ctx, userID, targetID)
}
