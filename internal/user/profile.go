package user

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const CurrentTermsVersion = "2026-10-16"

const maxDisplayNameLength = 80

var (
	ErrInvalidProfile      = errors.New("프로필 입력값이 올바르지 않습니다")
	ErrUsernameTaken       = errors.New("이미 사용 중인 사용자명입니다")
	ErrOnboardingRequired  = errors.New("온보딩을 먼저 완료해야 합니다.")
	ErrOnboardingCompleted = errors.New("이미 온보딩을 완료했습니다")
	ErrTermsOutdated       = errors.New("동의한 약관이 최신 버전이 아닙니다")
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

// 사진 URL은 다른 사용자의 앱이 불러오는 값이므로 입력으로 받지 않습니다.
type Onboarding struct {
	DisplayName       string
	Username          string
	DefaultVisibility string
	TermsVersion      string
	PhotoStoragePath  *string
}

type ProfileUpdate struct {
	DisplayName       string
	Username          string
	DefaultVisibility string
	PhotoStoragePath  *string
	// 앱은 사진을 지울 때와 그대로 둘 때 모두 경로를 null로 보내므로 삭제 요청을 따로 받습니다.
	RemovePhoto bool
}

type CalendarImport struct {
	PermissionStatus string
	HasImported      bool
}

func normalizeUsername(raw string) (string, error) {
	username := strings.ToLower(strings.TrimSpace(raw))
	if !usernamePattern.MatchString(username) {
		return "", ErrInvalidProfile
	}
	return username, nil
}

// 앱과 Firestore 규칙의 입력 기준이 바뀌면 함께 맞춰야 합니다.
func normalizeProfile(userID, rawName, rawUsername, visibility string, photoPath *string) (string, string, error) {
	displayName := strings.TrimSpace(rawName)
	nameLength := utf8.RuneCountInString(displayName)
	if nameLength == 0 || nameLength > maxDisplayNameLength || strings.ContainsFunc(displayName, unicode.IsControl) {
		return "", "", ErrInvalidProfile
	}
	username, err := normalizeUsername(rawUsername)
	if err != nil {
		return "", "", err
	}

	switch visibility {
	case "hidden", "time_only", "title_visible", "full_visible":
	default:
		return "", "", ErrInvalidProfile
	}

	// 다른 사용자의 저장 경로를 자기 사진으로 등록하지 못하게 합니다.
	if photoPath != nil && *photoPath != "users/"+userID+"/profile/avatar.jpg" {
		return "", "", ErrInvalidProfile
	}

	return displayName, username, nil
}

func (o Onboarding) normalized(userID string) (Onboarding, error) {
	displayName, username, err := normalizeProfile(userID, o.DisplayName, o.Username, o.DefaultVisibility, o.PhotoStoragePath)
	if err != nil {
		return Onboarding{}, err
	}
	if o.TermsVersion != CurrentTermsVersion {
		return Onboarding{}, ErrTermsOutdated
	}
	o.DisplayName, o.Username = displayName, username
	return o, nil
}

func (p ProfileUpdate) normalized(userID string) (ProfileUpdate, error) {
	displayName, username, err := normalizeProfile(userID, p.DisplayName, p.Username, p.DefaultVisibility, p.PhotoStoragePath)
	if err != nil {
		return ProfileUpdate{}, err
	}
	if p.RemovePhoto && p.PhotoStoragePath != nil {
		return ProfileUpdate{}, ErrInvalidProfile
	}
	p.DisplayName, p.Username = displayName, username
	return p, nil
}

func (c CalendarImport) validate() error {
	switch c.PermissionStatus {
	case "unknown", "granted", "denied":
		return nil
	default:
		return ErrInvalidProfile
	}
}
