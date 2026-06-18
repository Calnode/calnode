package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestCreateBooking_concurrentSameSlot proves the double-booking guard (§6.4)
// holds under contention: N goroutines race to book the identical slot, and the
// transaction serialization (single-writer connection) plus the partial unique
// index on (host_id, start_at) must yield exactly one 201 and N-1 409s — never
// two confirmed bookings for the same host at the same time.
func TestCreateBooking_concurrentSameSlot(t *testing.T) {
	h, key, _ := setupWorkspace(t)
	slug, _ := seedEventTypeHTTP(t, h, key)

	const n = 12
	body := fmt.Sprintf(`{"event_type_slug":%q,"start_at":"2026-06-15T09:00:00Z","name":"Alice","email":"alice@example.com"}`, slug)

	var wg sync.WaitGroup
	codes := make([]int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/bookings", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			<-start // line everyone up so the requests truly overlap
			h.CreateBooking(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	close(start)
	wg.Wait()

	var created, conflict, other int
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		default:
			other++
		}
	}
	if created != 1 {
		t.Errorf("created = %d; want exactly 1 (no double-booking)", created)
	}
	if conflict != n-1 {
		t.Errorf("conflict = %d; want %d", conflict, n-1)
	}
	if other != 0 {
		t.Errorf("got %d responses that were neither 201 nor 409", other)
	}
}
