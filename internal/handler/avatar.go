package handler

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
)

var avatarExts = []string{".jpg", ".png", ".gif", ".webp"}

// UploadAvatar handles POST /v1/users/me/avatar.
// Accepts multipart/form-data with an "avatar" file field (JPEG/PNG/GIF/WebP, ≤5 MB).
func (h *Handler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20+1024)

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		h.writeError(w, http.StatusBadRequest, "avatar must be ≤5 MB")
		return
	}

	file, _, err := r.FormFile("avatar")
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "avatar field required")
		return
	}
	defer file.Close()

	// Detect content type from the first 512 bytes.
	sniff := make([]byte, 512)
	n, _ := file.Read(sniff)
	ct := http.DetectContentType(sniff[:n])

	var ext string
	switch ct {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	default:
		h.writeError(w, http.StatusBadRequest, "avatar must be JPEG, PNG, GIF, or WebP")
		return
	}

	avatarDir := filepath.Join(h.dataDir, "avatars")
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: mkdir", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Remove old avatar files for this user (other extensions).
	for _, old := range avatarExts {
		_ = os.Remove(filepath.Join(avatarDir, user.ID+old))
	}

	dest := filepath.Join(avatarDir, user.ID+ext)
	f, err := os.Create(dest)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: create", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer f.Close()

	if _, err := f.Write(sniff[:n]); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: write sniff", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := io.Copy(f, file); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: write body", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	avatarURL := "/avatars/" + user.ID
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET avatar_url = ? WHERE id = ?`, avatarURL, user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: update db", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]string{"avatar_url": avatarURL})
}

// DeleteAvatar handles DELETE /v1/users/me/avatar.
func (h *Handler) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	avatarDir := filepath.Join(h.dataDir, "avatars")
	for _, ext := range avatarExts {
		_ = os.Remove(filepath.Join(avatarDir, user.ID+ext))
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE users SET avatar_url = NULL WHERE id = ?`, user.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: delete db", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ServeAvatar handles GET /avatars/{userID}.
// Public — no auth required. Scans for the stored file across supported extensions.
func (h *Handler) ServeAvatar(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userID")
	avatarDir := filepath.Join(h.dataDir, "avatars")
	for _, ext := range avatarExts {
		path := filepath.Join(avatarDir, userID+ext)
		if _, err := os.Stat(path); err == nil {
			w.Header().Set("Cache-Control", "public, max-age=86400")
			http.ServeFile(w, r, path)
			return
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
}
