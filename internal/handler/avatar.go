package handler

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

var avatarExts = []string{".jpg", ".png", ".gif", ".webp"}

// validUserID rejects any userID that isn't a lowercase hex UUID, preventing
// path traversal in ServeAvatar (e.g. "../../../etc/passwd").
var validUserID = regexp.MustCompile(`^[0-9a-f-]+$`)

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

	// Write to a temp file first; rename atomically on success so a failed
	// write never leaves a corrupt file at the final path.
	tmp, err := os.CreateTemp(avatarDir, "upload-*")
	if err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: create temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		tmp.Close()
		if !committed {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(sniff[:n]); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: write sniff", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := io.Copy(tmp, file); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: write body", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tmp.Close(); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: close temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	dest := filepath.Join(avatarDir, user.ID+ext)
	if err := os.Rename(tmpPath, dest); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: rename", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	committed = true

	// Remove old avatar files for this user (other extensions).
	for _, old := range avatarExts {
		if old != ext {
			_ = os.Remove(filepath.Join(avatarDir, user.ID+old))
		}
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

	// Reject anything that isn't a lowercase hex UUID to prevent path traversal.
	if !validUserID.MatchString(userID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	userID = filepath.Base(userID) // belt-and-suspenders: strip any path separators

	avatarDir := filepath.Join(h.dataDir, "avatars")
	for _, ext := range avatarExts {
		path := filepath.Join(avatarDir, userID+ext)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			continue
		}
		// private: browser may cache, but CDNs and shared caches must not.
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, path, fi.ModTime(), f)
		return
	}
	http.Error(w, "not found", http.StatusNotFound)
}
