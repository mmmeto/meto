package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"meto/internal/auth"
	"meto/internal/user"

	"github.com/gin-gonic/gin"
)

const (
	identityKey        = "meto.authenticatedIdentity"
	maxProfileBodySize = 16 * 1024
)

type UserService interface {
	SyncLogin(context.Context, user.Login) (user.Profile, error)
	GetByID(context.Context, string) (user.Profile, error)
	CompleteOnboarding(context.Context, string, user.Onboarding) (user.Profile, error)
	UpdateProfile(context.Context, string, user.ProfileUpdate) (user.Profile, error)
	UpdateCalendarImport(context.Context, string, user.CalendarImport) (user.Profile, error)
	UsernameAvailable(context.Context, string) (bool, error)
}

type onboardingRequest struct {
	DisplayName       string  `json:"displayName"`
	Username          string  `json:"username"`
	DefaultVisibility string  `json:"defaultVisibility"`
	TermsVersion      string  `json:"termsVersion"`
	PhotoStoragePath  *string `json:"photoStoragePath"`
}

type profileUpdateRequest struct {
	DisplayName       string  `json:"displayName"`
	Username          string  `json:"username"`
	DefaultVisibility string  `json:"defaultVisibility"`
	PhotoStoragePath  *string `json:"photoStoragePath"`
	RemovePhoto       bool    `json:"removePhoto"`
}

type calendarImportRequest struct {
	CalendarPermissionStatus  string `json:"calendarPermissionStatus"`
	HasImportedDeviceCalendar *bool  `json:"hasImportedDeviceCalendar"`
}

func authenticate(verifier auth.Verifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		headers := c.Request.Header.Values("Authorization")
		if len(headers) != 1 || len(headers[0]) > 16*1024 {
			unauthorized(c)
			return
		}
		parts := strings.Fields(headers[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(c)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		identity, err := verifier.Verify(ctx, parts[1])
		cancel()
		if err != nil {
			if errors.Is(err, auth.ErrInvalidToken) {
				unauthorized(c)
			} else {
				apiError(c, http.StatusServiceUnavailable, "auth_unavailable", "로그인 확인을 잠시 후 다시 시도해주세요.")
			}
			return
		}
		if identity.ID == "" {
			unauthorized(c)
			return
		}
		c.Set(identityKey, identity)
		c.Next()
	}
}

func unauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", "Bearer")
	apiError(c, http.StatusUnauthorized, "unauthorized", "유효한 로그인 토큰이 필요해요.")
}

func apiError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

func userError(c *gin.Context, err error, unavailable string) {
	switch {
	case errors.Is(err, user.ErrNotFound):
		apiError(c, http.StatusNotFound, "user_not_found", "로그인 API로 사용자 등록을 먼저 완료해주세요.")
	case errors.Is(err, user.ErrInvalidProfile):
		apiError(c, http.StatusBadRequest, "invalid_profile", "프로필 입력값을 확인해주세요.")
	case errors.Is(err, user.ErrUsernameTaken):
		apiError(c, http.StatusConflict, "username_taken", "이미 사용 중인 사용자 ID예요. 다른 ID를 정해주세요.")
	case errors.Is(err, user.ErrOnboardingCompleted):
		apiError(c, http.StatusConflict, "onboarding_completed", "이미 온보딩을 완료했어요.")
	case errors.Is(err, user.ErrTermsOutdated):
		apiError(c, http.StatusConflict, "terms_outdated", "최신 약관에 다시 동의해주세요.")
	case errors.Is(err, user.ErrOnboardingRequired):
		apiError(c, http.StatusConflict, "onboarding_required", "온보딩을 먼저 완료해주세요.")
	default:
		log.Printf("%s %s: %v", c.Request.Method, c.FullPath(), err)
		apiError(c, http.StatusServiceUnavailable, "database_unavailable", unavailable)
	}
}

func loginHandler(users UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := c.MustGet(identityKey).(auth.Identity)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		profile, err := users.SyncLogin(ctx, user.Login{
			ID: identity.ID, Email: identity.Email,
			DisplayName: identity.DisplayName, PhotoURL: identity.PhotoURL,
		})
		if err != nil {
			userError(c, err, "사용자 정보를 저장하지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, profile)
	}
}

func meHandler(users UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := c.MustGet(identityKey).(auth.Identity)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		profile, err := users.GetByID(ctx, identity.ID)
		if err != nil {
			userError(c, err, "사용자 정보를 불러오지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, profile)
	}
}

func onboardingHandler(users UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := c.MustGet(identityKey).(auth.Identity)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxProfileBodySize)
		var request onboardingRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			userError(c, user.ErrInvalidProfile, "")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		profile, err := users.CompleteOnboarding(ctx, identity.ID, user.Onboarding{
			DisplayName:       request.DisplayName,
			Username:          request.Username,
			DefaultVisibility: request.DefaultVisibility,
			TermsVersion:      request.TermsVersion,
			PhotoStoragePath:  request.PhotoStoragePath,
		})
		if err != nil {
			userError(c, err, "프로필을 저장하지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, profile)
	}
}

func updateProfileHandler(users UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := c.MustGet(identityKey).(auth.Identity)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxProfileBodySize)
		var request profileUpdateRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			userError(c, user.ErrInvalidProfile, "")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		profile, err := users.UpdateProfile(ctx, identity.ID, user.ProfileUpdate{
			DisplayName:       request.DisplayName,
			Username:          request.Username,
			DefaultVisibility: request.DefaultVisibility,
			PhotoStoragePath:  request.PhotoStoragePath,
			RemovePhoto:       request.RemovePhoto,
		})
		if err != nil {
			userError(c, err, "프로필을 저장하지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, profile)
	}
}

func calendarImportHandler(users UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := c.MustGet(identityKey).(auth.Identity)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxProfileBodySize)
		var request calendarImportRequest
		if err := c.ShouldBindJSON(&request); err != nil || request.HasImportedDeviceCalendar == nil {
			userError(c, user.ErrInvalidProfile, "")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		profile, err := users.UpdateCalendarImport(ctx, identity.ID, user.CalendarImport{
			PermissionStatus: request.CalendarPermissionStatus,
			HasImported:      *request.HasImportedDeviceCalendar,
		})
		if err != nil {
			userError(c, err, "캘린더 상태를 저장하지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, profile)
	}
}

func usernameAvailabilityHandler(users UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		available, err := users.UsernameAvailable(ctx, c.Param("username"))
		if err != nil {
			userError(c, err, "사용자 ID를 확인하지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, gin.H{"available": available})
	}
}
