package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/calnode/calnode/internal/livekit"
)

func TestPresignS3Get(t *testing.T) {
	s3 := livekit.S3Config{
		AccessKey: "AKIAEXAMPLE",
		Secret:    "secretkey",
		Region:    "auto",
		Endpoint:  "https://acct.r2.cloudflarestorage.com",
		Bucket:    "calnode",
	}
	fixed := time.Date(2026, 6, 25, 1, 2, 3, 0, time.UTC)
	u := presignS3Get(s3, "recordings/booking-x/file.mp4", 15*time.Minute, fixed)

	if !strings.HasPrefix(u, "https://acct.r2.cloudflarestorage.com/calnode/recordings/booking-x/file.mp4?") {
		t.Fatalf("URL prefix wrong: %s", u)
	}
	for _, want := range []string{
		"X-Amz-Algorithm=AWS4-HMAC-SHA256",
		"X-Amz-Credential=AKIAEXAMPLE%2F20260625%2Fauto%2Fs3%2Faws4_request",
		"X-Amz-Date=20260625T010203Z",
		"X-Amz-Expires=900",
		"X-Amz-SignedHeaders=host",
		"&X-Amz-Signature=",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("missing %q in %s", want, u)
		}
	}
	sig := func(s string) string { return s[strings.Index(s, "X-Amz-Signature=")+16:] }
	if len(sig(u)) != 64 {
		t.Errorf("signature should be 64 hex chars, got %d", len(sig(u)))
	}
	// Deterministic for the same inputs.
	if presignS3Get(s3, "recordings/booking-x/file.mp4", 15*time.Minute, fixed) != u {
		t.Error("presign should be deterministic")
	}
	// A different key changes the signature.
	if sig(presignS3Get(s3, "other.mp4", 15*time.Minute, fixed)) == sig(u) {
		t.Error("signature must depend on the key")
	}
}

func TestPresignS3Get_awsHost(t *testing.T) {
	// No endpoint → AWS regional host, path-style.
	u := presignS3Get(livekit.S3Config{AccessKey: "K", Secret: "S", Region: "us-west-2", Bucket: "b"},
		"k.mp4", time.Minute, time.Unix(0, 0).UTC())
	if !strings.HasPrefix(u, "https://s3.us-west-2.amazonaws.com/b/k.mp4?") {
		t.Errorf("AWS host wrong: %s", u)
	}
}
