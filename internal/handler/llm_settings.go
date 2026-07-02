package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/llm"
	"github.com/calnode/calnode/internal/secret"
)

// validateLLMEndpoint checks endpoint is an http(s) URL that doesn't (best-effort)
// resolve to a cloud-metadata address. Thin wrapper over the shared BYO-server-URL
// validator (see validateBYOServerURL) so this field can't drift from the CalDAV and
// LiveKit URL checks. Local/self-hosted LLM runtimes are allowed on purpose; the
// runtime client (internal/llm/client.go, via netutil.MetadataSafeTransport) re-checks
// on every real dial.
func validateLLMEndpoint(ctx context.Context, endpoint string) error {
	return validateBYOServerURL(ctx, endpoint, "endpoint", "http", "https")
}

// LLMConfig holds decrypted LLM-layer settings loaded from the DB. Endpoint empty ⇒ not
// configured; Enabled gates whether the optional AI features run.
type LLMConfig struct {
	Enabled  bool
	Endpoint string
	Model    string
	APIKey   string
}

// LoadLLMSettingsFromDB reads the optional LLM settings from server_settings and decrypts
// the api key. Returns nil (not an error) when the endpoint is empty (unconfigured).
func LoadLLMSettingsFromDB(db *sql.DB, encKey [32]byte) (*LLMConfig, error) {
	var endpoint, model, keyEnc string
	var enabled int
	err := db.QueryRow(`
		SELECT llm_endpoint, llm_model, llm_api_key_enc, llm_enabled
		FROM server_settings WHERE id = 1`).
		Scan(&endpoint, &model, &keyEnc, &enabled)
	if err == sql.ErrNoRows || endpoint == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var apiKey string
	if keyEnc != "" {
		if apiKey, err = secret.Decrypt(encKey, keyEnc); err != nil {
			return nil, fmt.Errorf("decrypt llm api key: %w", err)
		}
	}
	return &LLMConfig{Enabled: enabled != 0, Endpoint: endpoint, Model: model, APIKey: apiKey}, nil
}

// GetLLMSettings handles GET /v1/settings/llm (admin only). Never returns the api key.
func (h *Handler) GetLLMSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	var endpoint, model, keyEnc, extra string
	var enabled int
	err := h.db.QueryRowContext(r.Context(), `
		SELECT llm_endpoint, llm_model, llm_api_key_enc, llm_enabled, llm_extra_instructions
		FROM server_settings WHERE id = 1`).Scan(&endpoint, &model, &keyEnc, &enabled, &extra)
	if err != nil && err != sql.ErrNoRows {
		h.logger.ErrorContext(r.Context(), "llm settings: query", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"enabled":            enabled != 0,
		"endpoint":           endpoint,
		"model":              model,
		"api_key_set":        keyEnc != "",
		"configured":         endpoint != "",
		"active":             h.getLLM() != nil,
		"extra_instructions": extra,
		// Read-only: the code-owned base prompt, so admins can see what their instructions
		// are added to (they can't edit it — it's the tool-calling contract).
		"base_prompt": assistantBaseRules,
	})
}

// PatchLLMSettings handles PATCH /v1/settings/llm (admin only): save settings and
// hot-reload the live client. An empty api_key keeps the stored one.
func (h *Handler) PatchLLMSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Enabled           *bool   `json:"enabled"`
		Endpoint          *string `json:"endpoint"`
		Model             *string `json:"model"`
		APIKey            *string `json:"api_key"`
		ExtraInstructions *string `json:"extra_instructions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Endpoint != nil && *req.Endpoint != "" {
		if err := validateLLMEndpoint(r.Context(), *req.Endpoint); err != nil {
			h.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Apply only the provided fields (PATCH semantics) in one combined UPDATE, rather
	// than one round-trip per field.
	var set []string
	var args []any
	if req.Endpoint != nil {
		set = append(set, "llm_endpoint = ?")
		args = append(args, *req.Endpoint)
	}
	if req.Model != nil {
		set = append(set, "llm_model = ?")
		args = append(args, *req.Model)
	}
	if req.Enabled != nil {
		v := 0
		if *req.Enabled {
			v = 1
		}
		set = append(set, "llm_enabled = ?")
		args = append(args, v)
	}
	if req.APIKey != nil && *req.APIKey != "" {
		enc, err := secret.Encrypt(h.encKey, *req.APIKey)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "llm settings: encrypt key", "error", err)
			h.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		set = append(set, "llm_api_key_enc = ?")
		args = append(args, enc)
	}
	if req.ExtraInstructions != nil {
		v := *req.ExtraInstructions
		if len(v) > 4000 {
			v = v[:4000]
		}
		set = append(set, "llm_extra_instructions = ?")
		args = append(args, v)
	}
	if len(set) > 0 {
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE server_settings SET `+strings.Join(set, ", ")+`, updated_at = datetime('now') WHERE id = 1`, args...); err != nil { // #nosec G202 -- set is built above from hardcoded "col = ?" literals only; every value is bound via args...
			h.llmDBError(w, r, err)
			return
		}
	}

	// Hot-reload the live client from the now-current settings.
	if err := h.reloadLLM(r.Context()); err != nil {
		h.logger.ErrorContext(r.Context(), "llm settings: reload", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.GetLLMSettings(w, r)
}

// TestLLMSettings handles POST /v1/settings/llm/test (admin only): try a tiny completion
// against the posted settings (falling back to the stored key) and report ok / latency /
// error — so the admin can validate before enabling.
func (h *Handler) TestLLMSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var req struct {
		Endpoint string `json:"endpoint"`
		Model    string `json:"model"`
		APIKey   string `json:"api_key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Endpoint == "" || req.Model == "" {
		h.writeError(w, http.StatusBadRequest, "endpoint and model are required to test")
		return
	}
	if err := validateLLMEndpoint(r.Context(), req.Endpoint); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Empty key in the test request → reuse the stored key (so "test" works without
	// re-typing the secret).
	apiKey := req.APIKey
	if apiKey == "" {
		var keyEnc string
		_ = h.db.QueryRowContext(r.Context(), `SELECT llm_api_key_enc FROM server_settings WHERE id = 1`).Scan(&keyEnc)
		if keyEnc != "" {
			if dec, err := secret.Decrypt(h.encKey, keyEnc); err == nil {
				apiKey = dec
			}
		}
	}

	start := time.Now()
	err := llm.New(llm.Config{Endpoint: req.Endpoint, Model: req.Model, APIKey: apiKey}).Ping(r.Context())
	if err != nil {
		h.writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "latency_ms": time.Since(start).Milliseconds()})
}

// reloadLLM rebuilds the live client from current DB settings: a client when enabled and
// an endpoint is set, otherwise nil (AI off).
func (h *Handler) reloadLLM(ctx context.Context) error {
	cfg, err := LoadLLMSettingsFromDB(h.db, h.encKey)
	if err != nil {
		return err
	}
	if cfg == nil || !cfg.Enabled || cfg.Endpoint == "" {
		h.SetLLM(nil)
		return nil
	}
	h.SetLLM(llm.New(llm.Config{Endpoint: cfg.Endpoint, Model: cfg.Model, APIKey: cfg.APIKey}))
	return nil
}

func (h *Handler) llmDBError(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "llm settings: update", "error", err)
	h.writeError(w, http.StatusInternalServerError, "internal error")
}
