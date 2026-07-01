package handler

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

// avatarExts covers formats that may have been saved before the JPEG migration.
var avatarExts = []string{".jpg", ".png", ".gif", ".webp"}

// validUserID rejects any userID that isn't a lowercase hex UUID, preventing
// path traversal in ServeAvatar (e.g. "../../../etc/passwd").
var validUserID = regexp.MustCompile(`^[0-9a-f-]+$`)

// UploadAvatar handles POST /v1/users/me/avatar.
// Accepts multipart/form-data with an "avatar" file field (JPEG/PNG/GIF/WebP, ≤5 MB).
// The image is decoded, resized to at most 400×400, and re-encoded as JPEG 88%.
func (h *Handler) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20+1024)

	if err := r.ParseMultipartForm(5 << 20); err != nil { // #nosec G120 -- bounded by the MaxBytesReader above; the body can't exceed ~5MB
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
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		h.writeError(w, http.StatusBadRequest, "avatar must be JPEG, PNG, GIF, or WebP")
		return
	}

	// Reassemble full stream so image.Decode sees it from the start.
	var buf bytes.Buffer
	buf.Write(sniff[:n])
	if _, err := buf.ReadFrom(file); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: read body", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	img, _, err := image.Decode(&buf)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: decode image", "error", err)
		h.writeError(w, http.StatusBadRequest, "could not decode image")
		return
	}

	// Fit within 400×400, preserving aspect ratio; never upscale.
	resized := imaging.Fit(img, 400, 400, imaging.Lanczos)

	// Encode to JPEG.
	var out bytes.Buffer
	if err := jpeg.Encode(&out, resized, &jpeg.Options{Quality: 88}); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: encode jpeg", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	avatarDir := filepath.Join(h.dataDir, "avatars")
	if err := os.MkdirAll(avatarDir, 0o750); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: mkdir", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	dest := filepath.Join(avatarDir, user.ID+".jpg")
	tmp, err := os.CreateTemp(avatarDir, "upload-*.jpg")
	if err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: create temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		tmp.Close() // #nosec G104 -- file already written/renamed by this point; nothing actionable
		if !committed {
			if rerr := os.Remove(tmpPath); rerr != nil && !os.IsNotExist(rerr) {
				h.logger.Warn("avatar: cleanup temp file", "error", rerr, "path", tmpPath)
			}
		}
	}()

	if _, err := tmp.Write(out.Bytes()); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: write temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tmp.Close(); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: close temp", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		h.logger.ErrorContext(r.Context(), "avatar: rename", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	committed = true

	// Remove any legacy files saved under other extensions.
	for _, ext := range []string{".png", ".gif", ".webp"} {
		_ = os.Remove(filepath.Join(avatarDir, user.ID+ext))
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

	if !validUserID.MatchString(userID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	userID = filepath.Base(userID)

	avatarDir := filepath.Join(h.dataDir, "avatars")
	for _, ext := range avatarExts {
		path := filepath.Join(avatarDir, userID+ext)
		f, err := os.Open(path) // #nosec G304 -- userID is regex-validated (validUserID) and filepath.Base'd above; ext comes from the fixed avatarExts list, never user input
		if err != nil {
			continue
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			continue
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, path, fi.ModTime(), f)
		return
	}
	http.Error(w, "not found", http.StatusNotFound)
}
