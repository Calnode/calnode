package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// createSession inserts a session row and sets the session cookie on w.
func (h *Handler) createSession(ctx context.Context, w http.ResponseWriter, userID string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	sessID := hex.EncodeToString(raw)
	expiresAt := time.Now().UTC().Add(sessionDuration).Format(time.RFC3339)
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		sessID, userID, expiresAt); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- HttpOnly/SameSite/Secure are all set; Secure is h.secureCookie (true whenever BASE_URL is https, false only for local http dev) rather than a literal, which gosec's static check can't verify
		Name:     sessionCookieName,
		Value:    sessID,
		Path:     "/",
		MaxAge:   int(sessionDuration.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.secureCookie,
	})
	return nil
}

// RevokeAllSessions handles POST /v1/auth/sessions/revoke-all.
//
// Body: `{"user_id": "..."}`, optional.
//
//   - Omitted (or naming the caller): signs the caller out everywhere **except the
//     session that made the request**. "Sign out my other devices" is the action people
//     actually want; dropping the current session too would log the operator out of the
//     page they clicked it on, which is what Logout is for. A caller authenticating with
//     an API key has no current session, so for them every session goes.
//   - Naming someone else: an offboarding tool. Admin-only, and mirroring roles.go's
//     tiers — an admin may revoke a member, only the owner may revoke another admin, and
//     nobody may revoke the owner's sessions but the owner (there is exactly one owner,
//     so that case is the self branch).
//
// It also deletes the target's MCP OAuth access tokens. An MCP connector authenticates
// with a bearer token rather than the session cookie (§19), so revoking sessions alone
// would leave an agent connected with exactly the authority that was just taken away —
// the failure mode being cut off from is a laptop that walked out of the building with a
// signed-in browser AND a connected agent on it.
func (h *Handler) RevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	actor, ok := userFromContext(r.Context())
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<10)
	var req struct {
		UserID string `json:"user_id"`
	}
	// An empty body is the common case (revoke my own), so EOF is not an error here.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	targetID := req.UserID
	self := targetID == "" || targetID == actor.ID
	if self {
		targetID = actor.ID
	} else {
		// The actor's capability class is checked before the target is looked up, so a
		// member cannot use this endpoint's 404 to probe which user ids exist.
		if !actor.IsAdmin {
			h.writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		var targetIsAdmin, targetIsOwner int
		err := h.db.QueryRowContext(r.Context(),
			`SELECT is_admin, is_owner FROM users WHERE id = ?`, targetID).
			Scan(&targetIsAdmin, &targetIsOwner)
		if err == sql.ErrNoRows {
			h.writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if err != nil {
			h.logger.ErrorContext(r.Context(), "revoke sessions: load target", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if targetIsOwner != 0 {
			h.writeError(w, http.StatusForbidden, "the owner's sessions can only be revoked by the owner")
			return
		}
		if targetIsAdmin != 0 && !actor.IsOwner {
			h.writeError(w, http.StatusForbidden, "only the workspace owner can revoke another admin's sessions")
			return
		}
	}

	// One transaction: a caller told "revoked" must not have kept an MCP token because
	// the second statement failed after the first committed.
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke sessions: begin tx", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback() //nolint:errcheck

	var sessionRes sql.Result
	if self {
		// The session spared is the one the caller AUTHENTICATED WITH, which is not the
		// same thing as the one it happened to send.
		//
		// ⛔ The API-key test mirrors RequireAuth's own precedence: it tries the key
		// first, so a request carrying both is an API-key request and its cookie played
		// no part in authenticating it. Reading the cookie unconditionally would spare a
		// session on the strength of a header the caller was not authenticated by — so a
		// script holding an API key and a stale cookie would ask to end all its sessions,
		// be told it had, and leave one alive. Silently, because the response counts what
		// was deleted and not what was kept.
		current := ""
		if extractAPIKey(r) == "" {
			if c, cerr := r.Cookie(sessionCookieName); cerr == nil {
				current = c.Value
			}
		}
		sessionRes, err = tx.ExecContext(r.Context(),
			`DELETE FROM sessions WHERE user_id = ? AND id <> ?`, targetID, current)
	} else {
		sessionRes, err = tx.ExecContext(r.Context(),
			`DELETE FROM sessions WHERE user_id = ?`, targetID)
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke sessions: delete sessions", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	tokenRes, err := tx.ExecContext(r.Context(),
		`DELETE FROM oauth_access_tokens WHERE user_id = ?`, targetID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "revoke sessions: delete oauth tokens", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := tx.Commit(); err != nil {
		h.logger.ErrorContext(r.Context(), "revoke sessions: commit", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	sessions, _ := sessionRes.RowsAffected()
	tokens, _ := tokenRes.RowsAffected()
	h.logger.InfoContext(r.Context(), "sessions revoked",
		"actor_id", actor.ID, "user_id", targetID, "self", self,
		"sessions", sessions, "oauth_tokens", tokens)

	h.writeJSON(w, http.StatusOK, map[string]any{
		"ok":                   true,
		"user_id":              targetID,
		"sessions_revoked":     sessions,
		"oauth_tokens_revoked": tokens,
	})
}
