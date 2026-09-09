package handler

import "testing"

// TestReplicaBucket covers every replica scheme Litestream accepts, because the Storage
// settings page used to trim a literal "s3://" and reported a `gcs://my-bucket/calnode`
// replica's bucket as "gcs:" — the first '/' it found was the one inside "://".
func TestReplicaBucket(t *testing.T) {
	cases := []struct {
		name    string
		replica string
		want    string
	}{
		{"s3", "s3://my-bucket/calnode", "my-bucket"},
		{"gcs", "gcs://my-bucket/calnode", "my-bucket"},
		{"azure blob storage", "abs://my-container/calnode", "my-container"},
		{"s3 with no path", "s3://my-bucket", "my-bucket"},
		{"gcs with no path", "gcs://my-bucket", "my-bucket"},
		{"deep path", "gcs://my-bucket/calnode/db/replica", "my-bucket"},
		{"dots and dashes in the bucket", "s3://calnode.backups-eu/db", "calnode.backups-eu"},
		// A file replica has no bucket, and saying so is more useful than inventing one.
		// The settings page renders an em dash for the empty value.
		{"file, absolute path", "file:///var/lib/calnode/backups", ""},
		{"bare absolute path", "/var/lib/calnode/backups", ""},
		// Not configured must stay empty: the page reports backups_configured separately,
		// from the raw value, so "" here has to mean "nothing to show" and nothing else.
		{"empty", "", ""},
		// Degenerate but well-defined rather than crashing or guessing.
		{"scheme only", "gcs://", ""},
		{"unknown scheme", "wat://some-bucket/path", "some-bucket"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := replicaBucket(c.replica); got != c.want {
				t.Errorf("replicaBucket(%q) = %q; want %q", c.replica, got, c.want)
			}
		})
	}
}
