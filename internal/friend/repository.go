package friend

import (
	"context"
	"errors"
	"fmt"

	"meto/internal/user"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

const friendQuery = `
	SELECT u.id, u.display_name, COALESCE(u.username, u.id), u.photo_url, u.photo_storage_path,
		CASE WHEN f.user_low = $1 THEN f.low_sharing_level ELSE f.high_sharing_level END,
		CASE WHEN f.user_low = $1 THEN f.high_sharing_level ELSE f.low_sharing_level END
	FROM friendships f
	JOIN users u ON u.id = CASE WHEN f.user_low = $1 THEN f.user_high ELSE f.user_low END
	WHERE (f.user_low = $1 OR f.user_high = $1)`

var requestQuery = `
	SELECT r.id, r.from_uid, r.to_uid, r.status, r.created_at, r.responded_at,
		` + publicColumns("sender") + `, ` + publicColumns("receiver") + `
	FROM friend_requests r
	JOIN users sender ON sender.id = r.from_uid
	JOIN users receiver ON receiver.id = r.to_uid`

func publicColumns(alias string) string {
	return fmt.Sprintf(`%[1]s.id, COALESCE(%[1]s.username, %[1]s.id), %[1]s.display_name,
		%[1]s.photo_url, %[1]s.photo_storage_path, %[1]s.created_at, %[1]s.updated_at`, alias)
}

func publicFields(profile *user.PublicProfile) []any {
	return []any{
		&profile.ID, &profile.Username, &profile.DisplayName,
		&profile.PhotoURL, &profile.PhotoStoragePath, &profile.CreatedAt, &profile.UpdatedAt,
	}
}

func scanFriend(row pgx.CollectableRow) (Friend, error) {
	friend := Friend{Status: statusConnected}
	err := row.Scan(
		&friend.ID, &friend.DisplayName, &friend.Username, &friend.PhotoURL, &friend.PhotoStoragePath,
		&friend.MySharingLevelToFriend, &friend.FriendSharingLevelToMe,
	)
	friend.DefaultSharingLevel = friend.MySharingLevelToFriend
	return friend, err
}

func scanRequest(row pgx.CollectableRow) (Request, error) {
	var request Request
	fields := []any{
		&request.ID, &request.FromUID, &request.ToUID, &request.Status,
		&request.CreatedAt, &request.RespondedAt,
	}
	fields = append(fields, publicFields(&request.FromUser)...)
	fields = append(fields, publicFields(&request.ToUser)...)
	return request, row.Scan(fields...)
}

func (r *Repository) ListFriends(ctx context.Context, userID string) ([]Friend, error) {
	rows, err := r.pool.Query(ctx, friendQuery+" ORDER BY u.display_name, u.id", userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanFriend)
}

func (r *Repository) GetFriend(ctx context.Context, userID, friendID string) (Friend, error) {
	rows, err := r.pool.Query(ctx, friendQuery+" AND u.id = $2", userID, friendID)
	if err != nil {
		return Friend{}, err
	}
	friend, err := pgx.CollectExactlyOneRow(rows, scanFriend)
	if errors.Is(err, pgx.ErrNoRows) {
		return Friend{}, ErrNotFriends
	}
	return friend, err
}

func (r *Repository) SetSharingLevel(ctx context.Context, userID, friendID, level string) error {
	low, high := orderedPair(userID, friendID)
	tag, err := r.pool.Exec(ctx, `
		UPDATE friendships SET
			low_sharing_level = CASE WHEN user_low = $1 THEN $2 ELSE low_sharing_level END,
			high_sharing_level = CASE WHEN user_high = $1 THEN $2 ELSE high_sharing_level END,
			updated_at = NOW()
		WHERE user_low = $3 AND user_high = $4`, userID, level, low, high)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFriends
	}
	return nil
}

func (r *Repository) FindByUsername(ctx context.Context, viewerID, username string) (*user.PublicProfile, error) {
	var profile user.PublicProfile
	err := r.pool.QueryRow(ctx, `
		SELECT `+publicColumns("u")+`
		FROM users u
		WHERE u.username = $2 AND u.id <> $1 AND u.onboarding_completed
			AND NOT EXISTS (
				SELECT 1 FROM user_blocks b
				WHERE (b.blocker_id = $1 AND b.blocked_id = u.id)
					OR (b.blocker_id = u.id AND b.blocked_id = $1)
			)`, viewerID, username).Scan(publicFields(&profile)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (r *Repository) ListReceivedRequests(ctx context.Context, userID string) ([]Request, error) {
	return r.listRequests(ctx, " WHERE r.to_uid = $1 AND r.status = 'pending' ORDER BY r.created_at DESC", userID)
}

func (r *Repository) ListSentRequests(ctx context.Context, userID string) ([]Request, error) {
	return r.listRequests(ctx, " WHERE r.from_uid = $1 AND r.status = 'pending' ORDER BY r.created_at DESC", userID)
}

func (r *Repository) listRequests(ctx context.Context, condition, userID string) ([]Request, error) {
	rows, err := r.pool.Query(ctx, requestQuery+condition, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanRequest)
}

func (r *Repository) GetRequest(ctx context.Context, id string) (Request, error) {
	rows, err := r.pool.Query(ctx, requestQuery+" WHERE r.id = $1", id)
	if err != nil {
		return Request{}, err
	}
	request, err := pgx.CollectExactlyOneRow(rows, scanRequest)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrRequestNotFound
	}
	return request, err
}

func (r *Repository) GetRequestPair(ctx context.Context, id string) (RequestPair, error) {
	var pair RequestPair
	err := r.pool.QueryRow(ctx, "SELECT from_uid, to_uid FROM friend_requests WHERE id = $1", id).
		Scan(&pair.FromUID, &pair.ToUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return RequestPair{}, ErrRequestNotFound
	}
	return pair, err
}

func (r *Repository) ListBlocked(ctx context.Context, userID string) ([]BlockedUser, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id, u.display_name, COALESCE(u.username, u.id)
		FROM user_blocks b
		JOIN users u ON u.id = b.blocked_id
		WHERE b.blocker_id = $1
		ORDER BY u.display_name, u.id`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (BlockedUser, error) {
		var blocked BlockedUser
		return blocked, row.Scan(&blocked.ID, &blocked.DisplayName, &blocked.Username)
	})
}

func (r *Repository) DeleteBlock(ctx context.Context, blockerID, blockedID string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2", blockerID, blockedID)
	return err
}

func (r *Repository) WithPair(ctx context.Context, firstID, secondID string, fn func(PairTx) error) error {
	low, high := orderedPair(firstID, secondID)
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", low+":"+high); err != nil {
			return err
		}
		return fn(pairTx{tx: tx})
	})
}

type pairTx struct {
	tx pgx.Tx
}

func (p pairTx) State(ctx context.Context, actorID, targetID string) (PairState, error) {
	low, high := orderedPair(actorID, targetID)
	var state PairState
	err := p.tx.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM users WHERE id = $1 AND onboarding_completed),
			EXISTS (SELECT 1 FROM users WHERE id = $2 AND onboarding_completed),
			EXISTS (SELECT 1 FROM user_blocks
				WHERE (blocker_id = $1 AND blocked_id = $2) OR (blocker_id = $2 AND blocked_id = $1)),
			EXISTS (SELECT 1 FROM friendships WHERE user_low = $3 AND user_high = $4),
			EXISTS (SELECT 1 FROM friend_requests WHERE status = 'pending'
				AND ((from_uid = $1 AND to_uid = $2) OR (from_uid = $2 AND to_uid = $1)))`,
		actorID, targetID, low, high).Scan(
		&state.ActorOnboarded, &state.TargetOnboarded, &state.Blocked, &state.Friends, &state.RequestPending,
	)
	return state, err
}

func (p pairTx) RequestStatus(ctx context.Context, id string) (string, error) {
	var status string
	err := p.tx.QueryRow(ctx, "SELECT status FROM friend_requests WHERE id = $1", id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrRequestNotFound
	}
	return status, err
}

func (p pairTx) CreateRequest(ctx context.Context, fromID, toID string) (string, error) {
	var id string
	err := p.tx.QueryRow(ctx,
		"INSERT INTO friend_requests (from_uid, to_uid) VALUES ($1, $2) RETURNING id", fromID, toID).Scan(&id)
	return id, err
}

func (p pairTx) RespondToRequest(ctx context.Context, id, status string) error {
	_, err := p.tx.Exec(ctx, "UPDATE friend_requests SET status = $2, responded_at = NOW() WHERE id = $1", id, status)
	return err
}

func (p pairTx) CreateFriendship(ctx context.Context, requestedBy, acceptedBy string) error {
	low, high := orderedPair(requestedBy, acceptedBy)
	_, err := p.tx.Exec(ctx,
		"INSERT INTO friendships (user_low, user_high, requested_by) VALUES ($1, $2, $3)", low, high, requestedBy)
	return err
}

func (p pairTx) Block(ctx context.Context, blockerID, blockedID string) error {
	tag, err := p.tx.Exec(ctx, `
		INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, blockerID, blockedID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := p.tx.Exec(ctx, `
		INSERT INTO reports (report_type, reporter_id, reported_user_id, reason)
		VALUES ('block', $1, $2, 'blocked')`, blockerID, blockedID); err != nil {
		return err
	}
	low, high := orderedPair(blockerID, blockedID)
	if _, err := p.tx.Exec(ctx, "DELETE FROM friendships WHERE user_low = $1 AND user_high = $2", low, high); err != nil {
		return err
	}
	_, err = p.tx.Exec(ctx, `
		DELETE FROM friend_requests
		WHERE status = 'pending'
			AND ((from_uid = $1 AND to_uid = $2) OR (from_uid = $2 AND to_uid = $1))`, blockerID, blockedID)
	return err
}
