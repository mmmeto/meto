package user

import (
	"context"
	"errors"
)

type Store interface {
	SyncLogin(context.Context, Login) (Profile, error)
	GetByID(context.Context, string) (Profile, error)
	CompleteOnboarding(context.Context, string, Onboarding) (Profile, error)
	UpdateProfile(context.Context, string, ProfileUpdate) (Profile, error)
	UpdateCalendarImport(context.Context, string, CalendarImport) (Profile, error)
	UsernameExists(context.Context, string) (bool, error)
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

func (s *Service) UpdateProfile(ctx context.Context, id string, input ProfileUpdate) (Profile, error) {
	input, err := input.normalized(id)
	if err != nil {
		return Profile{}, err
	}
	profile, err := s.store.UpdateProfile(ctx, id, input)
	if !errors.Is(err, ErrNotFound) {
		return profile, err
	}

	// 갱신된 행이 없으면 미등록이거나 아직 온보딩 전인 사용자입니다.
	if _, err := s.store.GetByID(ctx, id); err != nil {
		return Profile{}, err
	}
	return Profile{}, ErrOnboardingRequired
}

func (s *Service) UpdateCalendarImport(ctx context.Context, id string, input CalendarImport) (Profile, error) {
	if err := input.validate(); err != nil {
		return Profile{}, err
	}
	return s.store.UpdateCalendarImport(ctx, id, input)
}

func (s *Service) UsernameAvailable(ctx context.Context, raw string) (bool, error) {
	username, err := NormalizeUsername(raw)
	if err != nil {
		return false, err
	}
	exists, err := s.store.UsernameExists(ctx, username)
	return !exists, err
}
