package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	firebase "firebase.google.com/go/v4"
	firebaseauth "firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

type FirebaseVerifier struct {
	client *firebaseauth.Client
}

func NewFirebaseVerifier(ctx context.Context, projectID string) (*FirebaseVerifier, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errors.New("FIREBASE_PROJECT_ID is required to start the API server")
	}
	// 에뮬레이터 토큰에는 서명이 없으므로, 실제 서비스에서 발급한 ID 토큰만 허용합니다.
	if os.Getenv("FIREBASE_AUTH_EMULATOR_HOST") != "" {
		return nil, errors.New("FIREBASE_AUTH_EMULATOR_HOST must be unset for this API server")
	}
	// ID 토큰 검증에는 관리자 자격증명 대신 Google의 공개 인증서만 필요합니다.
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID}, option.WithoutAuthentication())
	if err != nil {
		return nil, fmt.Errorf("initialize Firebase token verification: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize Firebase auth client: %w", err)
	}
	return &FirebaseVerifier{client: client}, nil
}

func (v *FirebaseVerifier) Verify(ctx context.Context, rawToken string) (Identity, error) {
	token, err := v.client.VerifyIDToken(ctx, rawToken)
	if err != nil {
		if firebaseauth.IsIDTokenInvalid(err) || firebaseauth.IsTenantIDMismatch(err) {
			return Identity{}, ErrInvalidToken
		}
		return Identity{}, ErrUnavailable
	}
	if token.UID == "" || token.Firebase.Tenant != "" {
		return Identity{}, ErrInvalidToken
	}
	name := "새 사용자"
	if claim := stringClaim(token.Claims, "name"); claim != nil {
		name = *claim
	}
	return Identity{
		ID:          token.UID,
		Email:       stringClaim(token.Claims, "email"),
		DisplayName: name,
		PhotoURL:    stringClaim(token.Claims, "picture"),
	}, nil
}

func stringClaim(claims map[string]interface{}, key string) *string {
	value, ok := claims[key].(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return nil
	}
	return &value
}
