package trtc

import "context"

type SessionResolver interface {
	ResolveUser(ctx context.Context, accessToken string) (string, error)
}

type VoiceMembership interface {
	CanJoinVoice(ctx context.Context, userID string, tableID string) (bool, error)
}

type AccessError struct {
	Code string
}

func (accessError AccessError) Error() string { return accessError.Code }

type AccessAuthorizer interface {
	AuthorizeVoice(
		ctx context.Context,
		accessToken string,
		requestedUserID string,
		tableID string,
	) error
}

type MembershipAuthorizer struct {
	Sessions   SessionResolver
	Membership VoiceMembership
}

func (authorizer MembershipAuthorizer) AuthorizeVoice(
	ctx context.Context,
	accessToken string,
	requestedUserID string,
	tableID string,
) error {
	if authorizer.Sessions == nil || authorizer.Membership == nil || accessToken == "" {
		return AccessError{Code: "authentication_required"}
	}
	userID, err := authorizer.Sessions.ResolveUser(ctx, accessToken)
	if err != nil || userID == "" {
		return AccessError{Code: "authentication_required"}
	}
	if userID != requestedUserID {
		return AccessError{Code: "permission_denied"}
	}
	allowed, err := authorizer.Membership.CanJoinVoice(ctx, userID, tableID)
	if err != nil || !allowed {
		return AccessError{Code: "permission_denied"}
	}
	return nil
}
