package user

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type storeStub struct {
	complete func(context.Context, string, Onboarding) (Profile, error)
	get      func(context.Context, string) (Profile, error)
}

func (s storeStub) SyncLogin(context.Context, Login) (Profile, error) {
	panic("unexpected login")
}

func (s storeStub) GetByID(ctx context.Context, id string) (Profile, error) {
	if s.get == nil {
		panic("unexpected read")
	}
	return s.get(ctx, id)
}

func (s storeStub) CompleteOnboarding(ctx context.Context, id string, input Onboarding) (Profile, error) {
	if s.complete == nil {
		panic("unexpected write")
	}
	return s.complete(ctx, id, input)
}

func validOnboarding() Onboarding {
	return Onboarding{
		DisplayName:       "지수",
		Username:          "jisu_01",
		DefaultVisibility: "time_only",
		TermsVersion:      CurrentTermsVersion,
	}
}

func TestCompleteOnboardingNormalizesInput(t *testing.T) {
	input := validOnboarding()
	input.DisplayName = "  지수 "
	input.Username = " Jisu_01\n"
	service := NewService(storeStub{
		complete: func(_ context.Context, id string, got Onboarding) (Profile, error) {
			if id != "uid" || got.DisplayName != "지수" || got.Username != "jisu_01" {
				t.Fatalf("input was not normalized: %s %+v", id, got)
			}
			return Profile{ID: id}, nil
		},
	})
	if _, err := service.CompleteOnboarding(t.Context(), "uid", input); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteOnboardingRejectsInvalidInputBeforeSaving(t *testing.T) {
	otherPath := "users/other/profile/avatar.jpg"
	for _, tt := range []struct {
		name   string
		change func(*Onboarding)
		want   error
	}{
		{"blank name", func(o *Onboarding) { o.DisplayName = "   " }, ErrInvalidProfile},
		{"long name", func(o *Onboarding) { o.DisplayName = strings.Repeat("가", 81) }, ErrInvalidProfile},
		{"multiline name", func(o *Onboarding) { o.DisplayName = "지\n수" }, ErrInvalidProfile},
		{"nul in name", func(o *Onboarding) { o.DisplayName = "지\x00수" }, ErrInvalidProfile},
		{"short username", func(o *Onboarding) { o.Username = "ab" }, ErrInvalidProfile},
		{"long username", func(o *Onboarding) { o.Username = strings.Repeat("a", 21) }, ErrInvalidProfile},
		{"username symbol", func(o *Onboarding) { o.Username = "ji-su" }, ErrInvalidProfile},
		{"unknown visibility", func(o *Onboarding) { o.DefaultVisibility = "public" }, ErrInvalidProfile},
		{"missing visibility", func(o *Onboarding) { o.DefaultVisibility = "" }, ErrInvalidProfile},
		{"another user's photo", func(o *Onboarding) { o.PhotoStoragePath = &otherPath }, ErrInvalidProfile},
		{"old terms", func(o *Onboarding) { o.TermsVersion = "2020-01-01" }, ErrTermsOutdated},
		{"missing terms", func(o *Onboarding) { o.TermsVersion = "" }, ErrTermsOutdated},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := validOnboarding()
			tt.change(&input)
			_, err := NewService(storeStub{}).CompleteOnboarding(t.Context(), "uid", input)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCompleteOnboardingAcceptsLimits(t *testing.T) {
	ownPath := "users/uid/profile/avatar.jpg"
	input := validOnboarding()
	input.DisplayName = strings.Repeat("가", 80)
	input.Username = strings.Repeat("a", 20)
	input.PhotoStoragePath = &ownPath
	service := NewService(storeStub{
		complete: func(context.Context, string, Onboarding) (Profile, error) { return Profile{}, nil },
	})
	if _, err := service.CompleteOnboarding(t.Context(), "uid", input); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteOnboardingWhenNothingWasUpdated(t *testing.T) {
	storeFailure := errors.New("store failed")
	for _, tt := range []struct {
		name        string
		existing    Profile
		existingErr error
		wantErr     error
	}{
		{"retry with the same username", Profile{Username: "jisu_01", OnboardingCompleted: true}, nil, nil},
		{"different username", Profile{Username: "another", OnboardingCompleted: true}, nil, ErrOnboardingCompleted},
		{"unregistered user", Profile{}, ErrNotFound, ErrNotFound},
		{"read failure", Profile{}, storeFailure, storeFailure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(storeStub{
				complete: func(context.Context, string, Onboarding) (Profile, error) {
					return Profile{}, ErrNotFound
				},
				get: func(context.Context, string) (Profile, error) { return tt.existing, tt.existingErr },
			})
			profile, err := service.CompleteOnboarding(t.Context(), "uid", validOnboarding())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && profile.Username != "jisu_01" {
				t.Fatalf("retry returned a different profile: %+v", profile)
			}
		})
	}
}

func TestCompleteOnboardingPassesStoreErrorsThrough(t *testing.T) {
	service := NewService(storeStub{
		complete: func(context.Context, string, Onboarding) (Profile, error) {
			return Profile{}, ErrUsernameTaken
		},
	})
	if _, err := service.CompleteOnboarding(t.Context(), "uid", validOnboarding()); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("got %v, want ErrUsernameTaken", err)
	}
}
