-- +goose Up
CREATE TABLE friend_requests (
                                 id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
                                 from_uid TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                                 to_uid TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                                 status TEXT NOT NULL DEFAULT 'pending',
                                 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                                 responded_at TIMESTAMPTZ,

                                 CONSTRAINT friend_requests_not_self CHECK (from_uid <> to_uid),
                                 CONSTRAINT friend_requests_status_valid CHECK (status IN ('pending', 'accepted', 'rejected'))
);

CREATE UNIQUE INDEX friend_requests_one_pending_per_pair ON friend_requests (
                                                                             LEAST(from_uid COLLATE "C", to_uid COLLATE "C"),
                                                                             GREATEST(from_uid COLLATE "C", to_uid COLLATE "C")
    ) WHERE status = 'pending';
CREATE INDEX friend_requests_pending_received ON friend_requests (to_uid) WHERE status = 'pending';
CREATE INDEX friend_requests_pending_sent ON friend_requests (from_uid) WHERE status = 'pending';

CREATE TABLE friendships (
                             user_low TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                             user_high TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                             requested_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                             low_sharing_level TEXT NOT NULL DEFAULT 'time_only',
                             high_sharing_level TEXT NOT NULL DEFAULT 'time_only',
                             connected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                             updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

                             PRIMARY KEY (user_low, user_high),
    -- Go의 문자열 비교는 바이트 순서이므로 DB 로케일과 무관하게 같은 순서로 비교합니다.
                             CONSTRAINT friendships_ordered CHECK (user_low COLLATE "C" < user_high COLLATE "C"),
                             CONSTRAINT friendships_requested_by_member CHECK (requested_by IN (user_low, user_high)),
                             CONSTRAINT friendships_low_level_valid CHECK (
                                 low_sharing_level IN ('hidden', 'time_only', 'title_visible', 'full_visible')
                                 ),
                             CONSTRAINT friendships_high_level_valid CHECK (
                                 high_sharing_level IN ('hidden', 'time_only', 'title_visible', 'full_visible')
                                 )
);

CREATE INDEX friendships_user_high ON friendships (user_high);

CREATE TABLE user_blocks (
                             blocker_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                             blocked_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                             created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

                             PRIMARY KEY (blocker_id, blocked_id),
                             CONSTRAINT user_blocks_not_self CHECK (blocker_id <> blocked_id)
);

CREATE INDEX user_blocks_blocked ON user_blocks (blocked_id);

CREATE TABLE reports (
                         id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
                         report_type TEXT NOT NULL,
                         reporter_id TEXT REFERENCES users(id) ON DELETE SET NULL,
                         reported_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
                         reason TEXT NOT NULL,
                         created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

                         CONSTRAINT reports_type_valid CHECK (report_type IN ('user', 'message', 'block'))
);

-- +goose Down
DROP TABLE reports;
DROP TABLE user_blocks;
DROP TABLE friendships;
DROP TABLE friend_requests;