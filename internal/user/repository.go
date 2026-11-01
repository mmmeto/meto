package user

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("user not found")

type Profile struct {
	ID                        string     `json:"id"`
	Email                     *string    `json:"email"`
	DisplayName               string     `json:"displayName"`
	Username                  string     `json:"username"`
	PhotoURL                  *string    `json:"photoURL"`
	PhotoStoragePath          *string    `json:"photoStoragePath"`
	DefaultVisibility         string     `json:"defaultVisibility"`
	CalendarPermissionStatus  string     `json:"calendarPermissionStatus"`
	HasImportedDeviceCalendar bool       `json:"hasImportedDeviceCalendar"`
	OnboardingCompleted       bool       `json:"onboardingCompleted"`
	TermsAcceptedVersion      *string    `json:"termsAcceptedVersion"`
	TermsAcceptedAt           *time.Time `json:"termsAcceptedAt"`
	LastLoginAt               *time.Time `json:"lastLoginAt"`
	CreatedAt                 time.Time  `json:"createdAt"`
	UpdatedAt                 time.Time  `json:"updatedAt"`
}

type Login struct {
	ID          string
	Email       *string
	DisplayName string
	PhotoURL    *string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const profileColumns = `id, email, display_name, COALESCE(username, id), photo_url,
	photo_storage_path, default_visibility, calendar_permission_status,
	has_imported_device_calendar, onboarding_completed, terms_accepted_version,
	terms_accepted_at, last_login_at, created_at, updated_at`

func (r *Repository) SyncLogin(ctx context.Context, input Login) (Profile, error) {
	// Only trusted token claims reach this query. A completed profile stays under user control.
	return scanProfile(r.pool.QueryRow(ctx, `
		INSERT INTO users (id, email, display_name, photo_url, last_login_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (id) DO UPDATE SET
			email = COALESCE(EXCLUDED.email, users.email),
			display_name = CASE WHEN users.onboarding_completed
				THEN users.display_name ELSE EXCLUDED.display_name END,
			photo_url = CASE WHEN users.onboarding_completed OR users.photo_storage_path IS NOT NULL
				THEN users.photo_url ELSE COALESCE(EXCLUDED.photo_url, users.photo_url) END,
			last_login_at = NOW(),
			updated_at = NOW()
		RETURNING `+profileColumns, input.ID, input.Email, input.DisplayName, input.PhotoURL))
}

func (r *Repository) GetByID(ctx context.Context, id string) (Profile, error) {
	return scanProfile(r.pool.QueryRow(ctx, "SELECT "+profileColumns+" FROM users WHERE id = $1", id))
}

func scanProfile(row pgx.Row) (Profile, error) {
	var profile Profile
	err := row.Scan(
		&profile.ID, &profile.Email, &profile.DisplayName, &profile.Username,
		&profile.PhotoURL, &profile.PhotoStoragePath, &profile.DefaultVisibility,
		&profile.CalendarPermissionStatus, &profile.HasImportedDeviceCalendar,
		&profile.OnboardingCompleted, &profile.TermsAcceptedVersion,
		&profile.TermsAcceptedAt, &profile.LastLoginAt, &profile.CreatedAt, &profile.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return profile, err
}
