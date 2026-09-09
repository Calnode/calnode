package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetStorageSettings_reportsTheBucketForEveryReplicaScheme pins the bug at the boundary
// it was reported from: the admin Storage page showed "gcs:" as the bucket name for a live
// GCS deployment. The parser has its own table test; this one proves the page is wired to it.
func TestGetStorageSettings_reportsTheBucketForEveryReplicaScheme(t *testing.T) {
	cases := []struct {
		name       string
		replica    string
		wantBucket string
		wantConfig bool
	}{
		{"gcs", "gcs://my-bucket/calnode", "my-bucket", true},
		{"s3", "s3://my-bucket/calnode", "my-bucket", true},
		{"azure blob storage", "abs://my-container/calnode", "my-container", true},
		// Configured, but with no bucket to name: the page renders an em dash.
		{"file", "file:///var/lib/calnode/backups", "", true},
		{"unset", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _, ownerKey, _ := setupWorkspaceWithDB(t)
			t.Setenv("LITESTREAM_REPLICA_URL", c.replica)

			req := authReq(http.MethodGet, "/v1/settings/storage", "", ownerKey)
			rec := httptest.NewRecorder()
			h.RequireAuth(h.GetStorageSettings)(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET storage settings: got %d; want 200 — %s", rec.Code, rec.Body.String())
			}

			var got struct {
				BackupsConfigured bool   `json:"backups_configured"`
				BackupsBucket     string `json:"backups_bucket"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v — %s", err, rec.Body.String())
			}
			if got.BackupsBucket != c.wantBucket {
				t.Errorf("backups_bucket for %q: got %q; want %q", c.replica, got.BackupsBucket, c.wantBucket)
			}
			if got.BackupsConfigured != c.wantConfig {
				t.Errorf("backups_configured for %q: got %v; want %v", c.replica, got.BackupsConfigured, c.wantConfig)
			}
		})
	}
}
