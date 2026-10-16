package handler_test

import "testing"

func TestProfileBookingAccent(t *testing.T) {
	h, db, key, userID := setupWorkspaceWithDB(t)
	for _, body := range []string{`{"booking_accent":"red"}`, `{"booking_accent":"#fff;background:red"}`} {
		if rec := patchMe(t, h, body, key); rec.Code != 400 {
			t.Fatalf("invalid accent accepted: %s", rec.Body)
		}
	}
	if rec := patchMe(t, h, `{"booking_accent":"#FFE500"}`, key); rec.Code != 200 {
		t.Fatal(rec.Body)
	}
	var accent string
	if err := db.QueryRow(`SELECT booking_accent FROM users WHERE id = ?`, userID).Scan(&accent); err != nil {
		t.Fatal(err)
	}
	if accent != "#ffe500" {
		t.Fatalf("stored accent %q", accent)
	}
}
