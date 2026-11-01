-- +goose Up
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT,
    display_name TEXT NOT NULL DEFAULT '새 사용자',
    username TEXT UNIQUE,
    photo_url TEXT,
    photo_storage_path TEXT,
    default_visibility TEXT NOT NULL DEFAULT 'time_only',
    calendar_permission_status TEXT NOT NULL DEFAULT 'unknown',
    has_imported_device_calendar BOOLEAN NOT NULL DEFAULT FALSE,
    onboarding_completed BOOLEAN NOT NULL DEFAULT FALSE,
    terms_accepted_version TEXT,
    terms_accepted_at TIMESTAMPTZ,
    last_login_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT users_id_valid CHECK (char_length(btrim(id)) > 0),
    CONSTRAINT users_display_name_valid CHECK (char_length(btrim(display_name)) > 0),
    CONSTRAINT users_username_valid CHECK (username ~ '^[a-z0-9_]{3,20}$'),
    CONSTRAINT users_visibility_valid CHECK (
        default_visibility IN ('hidden', 'time_only', 'title_visible', 'full_visible')
    ),
    CONSTRAINT users_calendar_permission_valid CHECK (
        calendar_permission_status IN ('unknown', 'granted', 'denied')
    ),
    CONSTRAINT users_onboarding_valid CHECK (
        NOT onboarding_completed OR username IS NOT NULL
    )
);

-- +goose Down
DROP TABLE users;
