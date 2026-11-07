package user

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	uniqueViolationCode      = "23505"
	usernameUniqueConstraint = "users_username_key"
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
	// 검증된 토큰의 정보만 사용하며, 온보딩을 완료한 사용자의 프로필은 유지합니다.
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

// 사용자명 중복은 조회 후 저장하지 않고 UNIQUE 제약으로 판정해 동시 요청에도 한 명만 성공합니다.
func (r *Repository) CompleteOnboarding(ctx context.Context, id string, input Onboarding) (Profile, error) {
	profile, err := scanProfile(r.pool.QueryRow(ctx, `
		UPDATE users SET
			display_name = $2,
			username = $3,
			default_visibility = $4,
			photo_storage_path = COALESCE($5, photo_storage_path),
			terms_accepted_version = $6,
			terms_accepted_at = NOW(),
			onboarding_completed = TRUE,
			updated_at = NOW()
		WHERE id = $1 AND NOT onboarding_completed
		RETURNING `+profileColumns,
		id, input.DisplayName, input.Username, input.DefaultVisibility,
		input.PhotoStoragePath, input.TermsVersion))
	return profile, usernameConflict(err)
}

func (r *Repository) UpdateProfile(ctx context.Context, id string, input ProfileUpdate) (Profile, error) {
	profile, err := scanProfile(r.pool.QueryRow(ctx, `
		UPDATE users SET
			display_name = $2,
			username = $3,
			default_visibility = $4,
			photo_url = CASE WHEN $6 THEN NULL ELSE photo_url END,
			photo_storage_path = CASE WHEN $6 THEN NULL ELSE COALESCE($5, photo_storage_path) END,
			updated_at = NOW()
		WHERE id = $1 AND onboarding_completed
		RETURNING `+profileColumns,
		id, input.DisplayName, input.Username, input.DefaultVisibility,
		input.PhotoStoragePath, input.RemovePhoto))
	return profile, usernameConflict(err)
}

func (r *Repository) UpdateCalendarImport(ctx context.Context, id string, input CalendarImport) (Profile, error) {
	return scanProfile(r.pool.QueryRow(ctx, `
		UPDATE users SET
			calendar_permission_status = $2,
			has_imported_device_calendar = $3,
			updated_at = NOW()
		WHERE id = $1
		RETURNING `+profileColumns, id, input.PermissionStatus, input.HasImported))
}

func (r *Repository) UsernameExists(ctx context.Context, username string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE username = $1)", username).Scan(&exists)
	return exists, err
}

func usernameConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode && pgErr.ConstraintName == usernameUniqueConstraint {
		return ErrUsernameTaken
	}
	return err
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
