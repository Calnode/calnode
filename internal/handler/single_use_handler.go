package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/uid"
)

const defaultSingleUseExpiryHours = 168

type singleUseLinkJSON struct {
	ID              string  `json:"id"`
	Token           string  `json:"token"`
	URL             string  `json:"url"`
	EventTypeID     string  `json:"event_type_id"`
	CreatedByUserID string  `json:"created_by_user_id"`
	ExpiresAt       *string `json:"expires_at"`
	UsedAt          *string `json:"used_at"`
	BookingID       *string `json:"booking_id"`
	CreatedAt       string  `json:"created_at"`
}

func (h *Handler) singleUseURL(token string) string {
	base := strings.TrimRight(h.publicURL(), "/")
	if base == "" {
		return "/t/" + token
	}
	return base + "/t/" + token
}

func (h *Handler) resolveEventTypeForSingleUse(ctx context.Context, idOrSlug string, user AuthUser) (etID, etSlug string, err error) {
	isAdmin := 0
	if user.IsAdmin {
		isAdmin = 1
	}
	err = h.db.QueryRowContext(ctx, `
		SELECT id, slug
		FROM event_types
		WHERE (id = ? OR slug = ?)
		  AND (? = 1 OR user_id = ? OR id IN (SELECT event_type_id FROM event_type_hosts WHERE user_id = ?))`,
		idOrSlug, idOrSlug, isAdmin, user.ID, user.ID).Scan(&etID, &etSlug)
	return etID, etSlug, err
}

func generateSingleUseToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateSingleUseLink handles POST /v1/event-types/{id}/single-use-links.
func (h *Handler) CreateSingleUseLink(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	idOrSlug := r.PathValue("id")
	etID, _, err := h.resolveEventTypeForSingleUse(r.Context(), idOrSlug, user)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create single-use link: lookup event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var req struct {
		ExpiresInHours *int `json:"expires_in_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	expHours := defaultSingleUseExpiryHours
	if req.ExpiresInHours != nil {
		if *req.ExpiresInHours < 0 {
			h.writeError(w, http.StatusBadRequest, "expires_in_hours cannot be negative")
			return
		}
		expHours = *req.ExpiresInHours
	}

	token, err := generateSingleUseToken()
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create single-use link: generate token", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	id := uid.New()
	nowT := time.Now().UTC()
	nowStr := nowT.Format(time.RFC3339Nano)
	var expiresAtSQL sql.NullString
	var expiresAtPtr *string
	if expHours > 0 {
		expStr := nowT.Add(time.Duration(expHours) * time.Hour).Format(time.RFC3339Nano)
		expiresAtSQL = sql.NullString{String: expStr, Valid: true}
		expiresAtPtr = &expStr
	}

	if _, err := h.db.ExecContext(r.Context(), `
		INSERT INTO single_use_tokens
		  (id, token, event_type_id, created_by_user_id, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, token, etID, user.ID, expiresAtSQL, nowStr); err != nil {
		h.logger.ErrorContext(r.Context(), "create single-use link: insert", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusCreated, singleUseLinkJSON{
		ID:              id,
		Token:           token,
		URL:             h.singleUseURL(token),
		EventTypeID:     etID,
		CreatedByUserID: user.ID,
		ExpiresAt:       expiresAtPtr,
		CreatedAt:       nowStr,
	})
}

// ListSingleUseLinks handles GET /v1/event-types/{id}/single-use-links.
func (h *Handler) ListSingleUseLinks(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	idOrSlug := r.PathValue("id")
	etID, _, err := h.resolveEventTypeForSingleUse(r.Context(), idOrSlug, user)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list single-use links: lookup event type", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, token, event_type_id, created_by_user_id,
		       expires_at, used_at, booking_id, created_at
		FROM single_use_tokens
		WHERE event_type_id = ?
		ORDER BY created_at DESC`, etID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list single-use links: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	links := []singleUseLinkJSON{}
	for rows.Next() {
		var item singleUseLinkJSON
		var expiresAt, usedAt, bookingID sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Token, &item.EventTypeID, &item.CreatedByUserID,
			&expiresAt, &usedAt, &bookingID, &item.CreatedAt,
		); err != nil {
			h.logger.ErrorContext(r.Context(), "list single-use links: scan", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		item.URL = h.singleUseURL(item.Token)
		if expiresAt.Valid {
			v := expiresAt.String
			item.ExpiresAt = &v
		}
		if usedAt.Valid {
			v := usedAt.String
			item.UsedAt = &v
		}
		if bookingID.Valid {
			v := bookingID.String
			item.BookingID = &v
		}
		links = append(links, item)
	}
	if err := rows.Err(); err != nil {
		h.logger.ErrorContext(r.Context(), "list single-use links: rows", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"single_use_links": links})
}

// RevokeSingleUseLink handles DELETE /v1/single-use-links/{token}.
func (h *Handler) RevokeSingleUseLink(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		h.writeError(w, http.StatusNotFound, "single-use link not found")
		return
	}

	isAdmin := 0
	if user.IsAdmin {
		isAdmin = 1
	}

	var eventTypeID string
	var usedAt sql.NullString
	err := h.db.QueryRowContext(r.Context(), `
		SELECT s.event_type_id, s.used_at
		FROM single_use_tokens s
		JOIN event_types et ON et.id = s.event_type_id
		WHERE s.token = ?
		  AND (? = 1 OR et.user_id = ? OR s.created_by_user_id = ?
		       OR et.id IN (SELECT event_type_id FROM event_type_hosts WHERE user_id = ?))`,
		token, isAdmin, user.ID, user.ID, user.ID).Scan(&eventTypeID, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "single-use link not found")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke single-use link: lookup", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if usedAt.Valid {
		h.writeError(w, http.StatusConflict, "single-use link has already been used")
		return
	}

	res, err := h.db.ExecContext(r.Context(),
		`DELETE FROM single_use_tokens WHERE token = ? AND used_at IS NULL`, token)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke single-use link: delete", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		h.writeError(w, http.StatusConflict, "single-use link has already been used")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SingleUseBookPage handles GET /t/{token} by validating the ephemeral link and
// rendering the event type booking page prefilled for that token.
func (h *Handler) SingleUseBookPage(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		writeSingleUseGonePage(w)
		return
	}

	var (
		slug      string
		isActive  int
		expiresAt sql.NullString
		usedAt    sql.NullString
	)
	err := h.db.QueryRowContext(r.Context(), `
		SELECT et.slug, et.is_active,
		       s.expires_at, s.used_at
		FROM single_use_tokens s
		JOIN event_types et ON et.id = s.event_type_id
		WHERE s.token = ?`, token).
		Scan(&slug, &isActive, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeSingleUseGonePage(w)
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "single-use book page: lookup", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if isActive == 0 || usedAt.Valid {
		writeSingleUseGonePage(w)
		return
	}
	if expiresAt.Valid && expiresAt.String != "" {
		expT, parseErr := time.Parse(time.RFC3339Nano, expiresAt.String)
		if parseErr != nil {
			expT, parseErr = time.Parse(time.RFC3339, expiresAt.String)
		}
		if parseErr != nil || !expT.After(time.Now().UTC()) {
			writeSingleUseGonePage(w)
			return
		}
	}

	h.renderBookPage(w, r, slug, true, bookPrefill{
		SingleUseToken: token,
	})
}

func writeSingleUseGonePage(w http.ResponseWriter) {
	const msg = "This booking link has expired or has already been used"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusGone)
	_, _ = io.WriteString(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Link Unavailable</title><script>(function(){try{var m=localStorage.getItem('fp-theme')||'system';var d=m==='dark'||(m!=='light'&&window.matchMedia&&window.matchMedia('(prefers-color-scheme: dark)').matches);document.documentElement.classList.toggle('dark',d);document.documentElement.style.colorScheme=d?'dark':'light';}catch(e){}})();</script><style>:root{--bg:#f8fafc;--fg:#0f172a;--card:#ffffff;--border:#e2e8f0;--muted:#475569;--pill-bg:#ffffff;--pill-active:#0f172a;--pill-active-fg:#ffffff}html.dark{--bg:#09090b;--fg:#f4f4f5;--card:#18181b;--border:#27272a;--muted:#a1a1aa;--pill-bg:#18181b;--pill-active:#f4f4f5;--pill-active-fg:#09090b}body{font-family:system-ui,-apple-system,sans-serif;background:var(--bg);color:var(--fg);display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:24px;transition:background-color .18s ease,color .18s ease}.box{max-width:440px;background:var(--card);border:1px solid var(--border);border-radius:14px;padding:32px;text-align:center;box-shadow:0 4px 20px rgba(0,0,0,.08)}h1{font-size:20px;margin:0 0 12px;letter-spacing:-0.01em}p{font-size:15px;color:var(--muted);margin:0;line-height:1.55}.sw{position:fixed;top:14px;right:14px;display:inline-flex;gap:2px;padding:3px;border-radius:8px;border:1px solid var(--border);background:var(--pill-bg)}.sw button{border:0;background:transparent;color:var(--muted);font:inherit;font-size:12px;font-weight:500;padding:4px 9px;border-radius:6px;cursor:pointer}.sw button.active{background:var(--pill-active);color:var(--pill-active-fg)}@media(prefers-reduced-motion:reduce){*{transition:none!important}}</style></head><body><div class="sw" role="radiogroup" aria-label="Theme"><button type="button" data-t="light">Light</button><button type="button" data-t="dark">Dark</button><button type="button" data-t="system">System</button></div><main class="box"><h1>Booking Link Unavailable</h1><p>`+html.EscapeString(msg)+`</p></main><script>(function(){var K='fp-theme',mq=window.matchMedia?window.matchMedia('(prefers-color-scheme: dark)'):null;function gm(){try{var v=localStorage.getItem(K);return(v==='light'||v==='dark'||v==='system')?v:'system'}catch(e){return'system'}}function ap(m){var d=m==='dark'||(m!=='light'&&!!(mq&&mq.matches));document.documentElement.classList.toggle('dark',d);document.documentElement.style.colorScheme=d?'dark':'light';var bs=document.querySelectorAll('.sw [data-t]');for(var i=0;i<bs.length;i++){var a=bs[i].getAttribute('data-t')===m;bs[i].classList.toggle('active',a);bs[i].setAttribute('aria-checked',a?'true':'false');}}var bs=document.querySelectorAll('.sw [data-t]');for(var i=0;i<bs.length;i++){bs[i].addEventListener('click',function(){var n=this.getAttribute('data-t')||'system';try{localStorage.setItem(K,n)}catch(e){}ap(n);});}if(mq&&mq.addEventListener)mq.addEventListener('change',function(){ap(gm());});ap(gm());})();</script></body></html>`)
}
