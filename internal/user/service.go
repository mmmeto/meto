package user

import (
	"context"
	"errors"
)

type Store interface {
	SyncLogin(context.Context, Login) (Profile, error)
	GetByID(context.Context, string) (Profile, error)
	CompleteOnboarding(context.Context, string, Onboarding) (Profile, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) SyncLogin(ctx context.Context, input Login) (Profile, error) {
	return s.store.SyncLogin(ctx, input)
}

func (s *Service) GetByID(ctx context.Context, id string) (Profile, error) {
	return s.store.GetByID(ctx, id)
}

func (s *Service) CompleteOnboarding(ctx context.Context, id string, input Onboarding) (Profile, error) {
	input, err := input.normalized(id)
	if err != nil {
		return Profile{}, err
	}
	profile, err := s.store.CompleteOnboarding(ctx, id, input)
	if !errors.Is(err, ErrNotFound) {
		return profile, err
	}

	// 갱신된 행이 없으면 미등록이거나 이미 완료한 사용자입니다.
	// 응답을 받지 못한 앱의 재시도는 같은 사용자명으로 오므로 성공으로 처리합니다.
	existing, err := s.store.GetByID(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	if existing.Username != input.Username {
		return Profile{}, ErrOnboardingCompleted
	}
	return existing, nil
}
