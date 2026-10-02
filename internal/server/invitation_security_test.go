package server

import "testing"

func TestInvitationCredentialPathsAreRedacted(t *testing.T) {
	for _, path := range []string{"/s/secret-token", "/v1/schedule/secret-token/slots", "/v1/schedule/secret-token/book", "/manage/secret-token/slots"} {
		redacted := redactTokenPaths(path)
		if redacted == path {
			t.Fatalf("credential path was logged: %s", path)
		}
	}
	if got := redactTokenPaths("/v1/scheduling-invitations/resource-id"); got != "/v1/scheduling-invitations/resource-id" {
		t.Fatal("non-credential resource path altered")
	}
}
