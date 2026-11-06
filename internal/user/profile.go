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

// Onboarding은 가입 마지막 단계에서 사용자가 직접 정한 프로필입니다.
// 사진 URL은 다른 사용자의 앱이 불러오는 값이므로 입력으로 받지 않습니다.
type Onboarding struct {
	DisplayName       string
	Username          string
	DefaultVisibility string
	TermsVersion      string
	PhotoStoragePath  *string
}

// normalized는 앱·Firestore 규칙과 같은 기준으로 입력을 정리하고 검증합니다.
func (o Onboarding) normalized(userID string) (Onboarding, error) {
	o.DisplayName = strings.TrimSpace(o.DisplayName)
	o.Username = strings.ToLower(strings.TrimSpace(o.Username))

	nameLength := utf8.RuneCountInString(o.DisplayName)
	if nameLength == 0 || nameLength > maxDisplayNameLength || strings.ContainsFunc(o.DisplayName, unicode.IsControl) {
		return Onboarding{}, ErrInvalidProfile
	}
	if !usernamePattern.MatchString(o.Username) {
		return Onboarding{}, ErrInvalidProfile
	}
	switch o.DefaultVisibility {
	case "hidden", "time_only", "title_visible", "full_visible":
	default:
		return Onboarding{}, ErrInvalidProfile
	}
	// 다른 사용자의 저장 경로를 자기 사진으로 등록하지 못하게 합니다.
	if o.PhotoStoragePath != nil && *o.PhotoStoragePath != "users/"+userID+"/profile/avatar.jpg" {
		return Onboarding{}, ErrInvalidProfile
	}
	if o.TermsVersion != CurrentTermsVersion {
		return Onboarding{}, ErrTermsOutdated
	}
	return o, nil
}
