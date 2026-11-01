package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"meto/internal/auth"

	"github.com/gin-gonic/gin"
)

type Pinger interface {
	Ping(context.Context) error
}

func NewRouter(db Pinger, verifier auth.Verifier, users UserStore) (*gin.Engine, error) {
	if db == nil || verifier == nil || users == nil {
		return nil, fmt.Errorf("database, auth verifier and user store are required")
	}
	router := gin.Default()
	if err := router.SetTrustedProxies(nil); err != nil {
		return nil, fmt.Errorf("configure trusted proxies: %w", err)
	}

	api := router.Group("/api/v1")
	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "meto-api",
		})
	})
	api.GET("/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":   "not_ready",
				"database": "unavailable",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":   "ready",
			"database": "ok",
		})
	})

	protected := api.Group("", authenticate(verifier))
	protected.POST("/auth/login", loginHandler(users))
	protected.GET("/users/me", meHandler(users))
	return router, nil
}
