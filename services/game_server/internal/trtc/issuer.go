package trtc

import (
	"errors"
	"regexp"

	"github.com/tencentyun/tls-sig-api-v2-golang/tencentyun"
)

var validIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Credentials struct {
	SDKAppID int    `json:"sdkAppId"`
	UserID   string `json:"userId"`
	RoomID   string `json:"roomId"`
	UserSig  string `json:"userSig"`
	ExpireIn int    `json:"expireIn"`
}

type CredentialIssuer interface {
	Issue(userID string, roomID string) (Credentials, error)
}

type TencentIssuer struct {
	sdkAppID int
	secret   string
	expire   int
}

func NewTencentIssuer(sdkAppID int, secret string, expire int) *TencentIssuer {
	return &TencentIssuer{sdkAppID: sdkAppID, secret: secret, expire: expire}
}

func (issuer *TencentIssuer) Issue(userID string, roomID string) (Credentials, error) {
	if !validIdentifier.MatchString(userID) {
		return Credentials{}, errors.New("invalid TRTC user ID")
	}
	if !validIdentifier.MatchString(roomID) {
		return Credentials{}, errors.New("invalid TRTC room ID")
	}

	signature, err := tencentyun.GenUserSig(
		issuer.sdkAppID,
		issuer.secret,
		userID,
		issuer.expire,
	)
	if err != nil {
		return Credentials{}, err
	}

	return Credentials{
		SDKAppID: issuer.sdkAppID,
		UserID:   userID,
		RoomID:   roomID,
		UserSig:  signature,
		ExpireIn: issuer.expire,
	}, nil
}
