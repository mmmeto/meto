package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"meto/internal/auth"
	"meto/internal/friend"
	"meto/internal/user"

	"github.com/gin-gonic/gin"
)

type FriendService interface {
	ListFriends(context.Context, string) ([]friend.Friend, error)
	SetSharingLevel(context.Context, string, string, string) (friend.Friend, error)
	SearchByUsername(context.Context, string, string) (*user.PublicProfile, error)
	ListReceivedRequests(context.Context, string) ([]friend.Request, error)
	ListSentRequests(context.Context, string) ([]friend.Request, error)
	SendRequest(context.Context, string, string) (friend.Request, error)
	AcceptRequest(context.Context, string, string) error
	RejectRequest(context.Context, string, string) error
	ListBlocked(context.Context, string) ([]friend.BlockedUser, error)
	Block(context.Context, string, string) error
	Unblock(context.Context, string, string) error
}

type friendRequestBody struct {
	ToUserID string `json:"toUserId"`
}

type sharingLevelBody struct {
	Level string `json:"level"`
}

func friendError(c *gin.Context, err error, unavailable string) {
	switch {
	case errors.Is(err, friend.ErrUserNotFound):
		apiError(c, http.StatusNotFound, "friend_target_not_found", "사용자를 찾을 수 없어요.")
	case errors.Is(err, friend.ErrSelfTarget):
		apiError(c, http.StatusBadRequest, "self_target", "자기 자신은 선택할 수 없어요.")
	case errors.Is(err, friend.ErrInvalidSharingLevel):
		apiError(c, http.StatusBadRequest, "invalid_sharing_level", "공개 범위를 확인해주세요.")
	case errors.Is(err, friend.ErrAlreadyFriends):
		apiError(c, http.StatusConflict, "already_friends", "이미 친구예요.")
	case errors.Is(err, friend.ErrRequestPending):
		apiError(c, http.StatusConflict, "friend_request_pending", "이미 요청을 보냈어요.")
	case errors.Is(err, friend.ErrRequestNotFound):
		apiError(c, http.StatusNotFound, "friend_request_not_found", "친구 요청을 찾을 수 없어요.")
	case errors.Is(err, friend.ErrRequestClosed):
		apiError(c, http.StatusConflict, "friend_request_closed", "처리할 수 없는 친구 요청이에요.")
	case errors.Is(err, friend.ErrRequestUnavailable):
		apiError(c, http.StatusForbidden, "friend_request_unavailable", "지금은 이 사용자에게 친구 요청을 보낼 수 없어요.")
	case errors.Is(err, friend.ErrNotFriends):
		apiError(c, http.StatusNotFound, "friendship_not_found", "친구 관계를 찾을 수 없어요.")
	default:
		userError(c, err, unavailable)
	}
}

func friendContext(c *gin.Context) (context.Context, context.CancelFunc, string) {
	identity := c.MustGet(identityKey).(auth.Identity)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	return ctx, cancel, identity.ID
}

func listFriendsHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		list, err := friends.ListFriends(ctx, userID)
		if err != nil {
			friendError(c, err, "친구 목록을 불러오지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, gin.H{"friends": list})
	}
}

func sharingLevelHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxProfileBodySize)
		var body sharingLevelBody
		if err := c.ShouldBindJSON(&body); err != nil {
			friendError(c, friend.ErrInvalidSharingLevel, "")
			return
		}
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		connection, err := friends.SetSharingLevel(ctx, userID, c.Param("userId"), body.Level)
		if err != nil {
			friendError(c, err, "공개 범위를 저장하지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, connection)
	}
}

func searchUserHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		profile, err := friends.SearchByUsername(ctx, userID, c.Query("username"))
		if err != nil {
			friendError(c, err, "사용자를 검색하지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": profile})
	}
}

func receivedRequestsHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		requests, err := friends.ListReceivedRequests(ctx, userID)
		if err != nil {
			friendError(c, err, "친구 요청을 불러오지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, gin.H{"requests": requests})
	}
}

func sentRequestsHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		requests, err := friends.ListSentRequests(ctx, userID)
		if err != nil {
			friendError(c, err, "친구 요청을 불러오지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, gin.H{"requests": requests})
	}
}

func sendRequestHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxProfileBodySize)
		var body friendRequestBody
		if err := c.ShouldBindJSON(&body); err != nil || body.ToUserID == "" {
			apiError(c, http.StatusBadRequest, "invalid_request", "친구 요청 내용을 확인해주세요.")
			return
		}
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		request, err := friends.SendRequest(ctx, userID, body.ToUserID)
		if err != nil {
			friendError(c, err, "친구 요청을 보내지 못했어요.")
			return
		}
		c.JSON(http.StatusCreated, request)
	}
}

func acceptRequestHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		if err := friends.AcceptRequest(ctx, userID, c.Param("requestId")); err != nil {
			friendError(c, err, "친구 요청을 수락하지 못했어요.")
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func rejectRequestHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		if err := friends.RejectRequest(ctx, userID, c.Param("requestId")); err != nil {
			friendError(c, err, "친구 요청을 거절하지 못했어요.")
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func listBlockedHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		blocked, err := friends.ListBlocked(ctx, userID)
		if err != nil {
			friendError(c, err, "차단 목록을 불러오지 못했어요.")
			return
		}
		c.JSON(http.StatusOK, gin.H{"users": blocked})
	}
}

func blockHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		if err := friends.Block(ctx, userID, c.Param("userId")); err != nil {
			friendError(c, err, "사용자를 차단하지 못했어요.")
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func unblockHandler(friends FriendService) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel, userID := friendContext(c)
		defer cancel()
		if err := friends.Unblock(ctx, userID, c.Param("userId")); err != nil {
			friendError(c, err, "차단을 해제하지 못했어요.")
			return
		}
		c.Status(http.StatusNoContent)
	}
}
