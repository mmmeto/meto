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
	// Emulator tokens are unsigned. This API only accepts production ID tokens.
	if os.Getenv("FIREBASE_AUTH_EMULATOR_HOST") != "" {
		return nil, errors.New("FIREBASE_AUTH_EMULATOR_HOST must be unset for this API server")
	}
	// ID token verification only needs Google's public certificates, not admin credentials.
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
