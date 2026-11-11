package friend

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"meto/internal/user"
)

const (
	StatusPending  = "pending"
	StatusAccepted = "accepted"
	StatusRejected = "rejected"

	statusConnected = "connected"
	maxIDLength     = 128
)

var (
	ErrUserNotFound        = errors.New("대상 사용자를 찾을 수 없습니다")
	ErrSelfTarget          = errors.New("자기 자신은 대상으로 지정할 수 없습니다.")
	ErrAlreadyFriends      = errors.New("이미 친구입니다")
	ErrRequestPending      = errors.New("이미 진행 중인 친구요청이 있습니다.")
	ErrRequestNotFound     = errors.New("친구 요청을 찾을 수 없습니다.")
	ErrRequestClosed       = errors.New("이미 처리된 친구 요청입니다")
	ErrRequestUnavailable  = errors.New("친구 요청을 보낼 수 없는 사용자입니다")
	ErrNotFriends          = errors.New("친구가 아닙니다")
	ErrInvalidSharingLevel = errors.New("공개 범위가 올바르지 않습니다")
)

type Friend struct {
	ID                     string  `json:"id"`
	DisplayName            string  `json:"displayName"`
	Username               string  `json:"username"`
	PhotoURL               *string `json:"photoURL"`
	PhotoStoragePath       *string `json:"photoStoragePath"`
	Status                 string  `json:"status"`
	DefaultSharingLevel    string  `json:"defaultSharingLevel"`
	MySharingLevelToFriend string  `json:"mySharingLevelToFriend"`
	FriendSharingLevelToMe string  `json:"friendSharingLevelToMe"`
}

type Request struct {
	ID          string             `json:"id"`
	FromUID     string             `json:"fromUid"`
	ToUID       string             `json:"toUid"`
	Status      string             `json:"status"`
	CreatedAt   time.Time          `json:"createdAt"`
	RespondedAt *time.Time         `json:"respondedAt,omitempty"`
	FromUser    user.PublicProfile `json:"fromUser"`
	ToUser      user.PublicProfile `json:"toUser"`
}

type BlockedUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Username    string `json:"username"`
}

type RequestPair struct {
	FromUID string
	ToUID   string
}

type PairState struct {
	ActorOnboarded  bool
	TargetOnboarded bool
	Blocked         bool
	Friends         bool
	RequestPending  bool
}

func orderedPair(first, second string) (string, string) {
	if first < second {
		return first, second
	}
	return second, first
}

// 경로로 받은 값의 NULL, 잘못된 UTF-8은 Postgres 오류가 되므로 404로 응답
func validID(id string) bool {
	return id != "" && len(id) <= maxIDLength && utf8.ValidString(id) && !strings.ContainsRune(id, 0)
}
