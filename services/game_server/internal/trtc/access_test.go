package trtc

import (
	"context"
	"errors"
	"testing"
)

type fakeSessionResolver struct {
	userID string
	err    error
}

func (resolver fakeSessionResolver) ResolveUser(context.Context, string) (string, error) {
	return resolver.userID, resolver.err
}

type fakeVoiceMembership struct {
	allowed bool
	err     error
}

func (membership fakeVoiceMembership) CanJoinVoice(context.Context, string, string) (bool, error) {
	return membership.allowed, membership.err
}

func TestMembershipAuthorizerBindsSessionUserAndTableMembership(t *testing.T) {
	authorizer := MembershipAuthorizer{
		Sessions:   fakeSessionResolver{userID: "user_1"},
		Membership: fakeVoiceMembership{allowed: true},
	}
	if err := authorizer.AuthorizeVoice(context.Background(), "token", "user_1", "table_1"); err != nil {
		t.Fatalf("AuthorizeVoice: %v", err)
	}

	tests := []struct {
		name       string
		authorizer MembershipAuthorizer
		token      string
		userID     string
		wantCode   string
	}{
		{name: "missing token", authorizer: authorizer, userID: "user_1", wantCode: "authentication_required"},
		{name: "session failure", authorizer: MembershipAuthorizer{Sessions: fakeSessionResolver{err: errors.New("expired")}, Membership: fakeVoiceMembership{allowed: true}}, token: "token", userID: "user_1", wantCode: "authentication_required"},
		{name: "different requested user", authorizer: authorizer, token: "token", userID: "user_2", wantCode: "permission_denied"},
		{name: "not at table", authorizer: MembershipAuthorizer{Sessions: fakeSessionResolver{userID: "user_1"}, Membership: fakeVoiceMembership{}}, token: "token", userID: "user_1", wantCode: "permission_denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.authorizer.AuthorizeVoice(context.Background(), test.token, test.userID, "table_1")
			var accessError AccessError
			if !errors.As(err, &accessError) || accessError.Code != test.wantCode {
				t.Fatalf("error=%v, want %s", err, test.wantCode)
			}
		})
	}
}
