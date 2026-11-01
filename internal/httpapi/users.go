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

const identityKey = "meto.authenticatedIdentity"

type UserStore interface {
	SyncLogin(context.Context, user.Login) (user.Profile, error)
	GetByID(context.Context, string) (user.Profile, error)
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

func loginHandler(users UserStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := c.MustGet(identityKey).(auth.Identity)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		profile, err := users.SyncLogin(ctx, user.Login{
			ID: identity.ID, Email: identity.Email,
			DisplayName: identity.DisplayName, PhotoURL: identity.PhotoURL,
		})
		if err != nil {
			log.Printf("sync login user: %v", err)
			apiError(c, http.StatusServiceUnavailable, "database_unavailable", "사용자 정보를 저장하지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, profile)
	}
}

func meHandler(users UserStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity := c.MustGet(identityKey).(auth.Identity)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		profile, err := users.GetByID(ctx, identity.ID)
		if errors.Is(err, user.ErrNotFound) {
			apiError(c, http.StatusNotFound, "user_not_found", "로그인 API로 사용자 등록을 먼저 완료해주세요.")
			return
		}
		if err != nil {
			log.Printf("get current user: %v", err)
			apiError(c, http.StatusServiceUnavailable, "database_unavailable", "사용자 정보를 불러오지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, profile)
	}
}
