package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/calnode/calnode/internal/booking"
	"github.com/calnode/calnode/internal/llm"
)

// reasoningRe strips chain-of-thought that reasoning models (e.g. MiniMax M3) emit inline
// in the content. We never show this to the booker.
var reasoningRe = regexp.MustCompile(`(?s)<think>.*?</think>|<thinking>.*?</thinking>`)

func stripReasoning(s string) string {
	s = reasoningRe.ReplaceAllString(s, "")
	// Drop a dangling, unclosed reasoning block (truncated output).
	for _, tag := range []string{"<think>", "<thinking>"} {
		if i := strings.Index(s, tag); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

// Conversational booking assistant (PRD §8.11) — the public, user-facing AI feature. A
// booker chats in natural language; the LLM drives a NARROW, event-type-scoped tool set
// (find slots, book) over the same deterministic cores the rest of the app uses. The LLM
// never computes availability itself (it calls find_available_slots) and never sees raw
// calendar data — only computed time windows. The standard slot picker is always present
// as the fallback, so this is purely additive.
//
// Scope/safety: the tools are bound to the single event-type slug in the URL — the model
// cannot read or book anything else. This is deliberately NOT the full MCP surface.

const (
	assistantMaxMessages = 24  // cap conversation length (cost/abuse)
	assistantMaxIters    = 6   // cap tool-loop iterations per request
	assistantMaxTokens   = 700 // cap output tokens per LLM call
	assistantMaxSlots    = 40  // cap slots passed back to the model (token control)
)

// assistantBaseRules is the static, code-owned core of the assistant's system prompt —
// the tool-calling contract + style + safety rails. It is NOT admin-editable (editing it
// could break tool use or the data-boundary guarantees); admins customize via the
// appended "Additional instructions". Surfaced read-only in Settings → AI.
const assistantBaseRules = `Style: reply in 1–2 short sentences. Offer at most ~5 times, inline (e.g. "Wed 10:00, 10:30, or 11:00?") — never dump a full list, headings, tables, or emoji. Plain, warm, brief. Never reveal your reasoning or any <think> content.

Booking flow:
- To find times, ALWAYS call find_available_slots — never invent or calculate availability. Only offer times it returns.
- Collect the visitor's name and email (and answers to any required intake questions), confirm the time, then call book with an exact slot_start from find_available_slots.
- After booking, confirm in one short line.
- If you can't help, suggest the calendar picker on the page.`

type assistantMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

type assistantRequest struct {
	Messages []assistantMessage `json:"messages"`
	Timezone string             `json:"timezone"`
}

type assistantBooking struct {
	ID      string `json:"id"`
	StartAt string `json:"start_at"`
	EndAt   string `json:"end_at"`
	Status  string `json:"status"`
}

type assistantResponse struct {
	Reply    string            `json:"reply"`
	Booking  *assistantBooking `json:"booking,omitempty"`
	Fallback bool              `json:"fallback,omitempty"` // true → client should use the slot picker
}

// BookingAssistant handles POST /v1/event-types/{slug}/assistant (public, rate-limited).
func (h *Handler) BookingAssistant(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var req assistantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Stream token-by-token over SSE when the client asks for it; otherwise return the
	// one-shot JSON response (back-compat + non-streaming callers).
	sse := strings.Contains(r.Header.Get("Accept"), "text/event-stream")

	client := h.getLLM()
	if client == nil {
		h.assistantFallback(w, sse, "The booking assistant isn't available right now — please pick a time from the calendar below.")
		return
	}

	tz := strings.TrimSpace(req.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	if len(req.Messages) == 0 || len(req.Messages) > assistantMaxMessages {
		h.writeError(w, http.StatusBadRequest, "conversation is empty or too long")
		return
	}

	// Event-type context for the system prompt (active+public only). Doubles as the
	// not-found gate.
	sysPrompt, ok := h.assistantSystemPrompt(r.Context(), slug, tz)
	if !ok {
		h.writeError(w, http.StatusNotFound, "event type not found")
		return
	}

	var sendSSE func(any)
	if sse {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no") // don't let a proxy buffer the stream
		flusher, _ := w.(http.Flusher)
		sendSSE = func(obj any) {
			b, _ := json.Marshal(obj)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}

	// Build the message list: system + sanitized history (text only).
	msgs := []llm.Message{{Role: "system", Content: sysPrompt}}
	for _, m := range req.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
	}

	tools := h.assistantTools()
	var booked *assistantBooking

	for iter := 0; iter < assistantMaxIters; iter++ {
		chatReq := llm.ChatRequest{Messages: msgs, Tools: tools, MaxTokens: assistantMaxTokens}
		var res *llm.ChatResult
		var err error
		if sse {
			// Stream content deltas, stripping <think> as we go (recompute the cleaned
			// text each fragment and emit only the new suffix).
			var full strings.Builder
			var prevClean string
			res, err = client.ChatStream(r.Context(), chatReq, func(frag string) {
				full.WriteString(frag)
				clean := stripReasoning(full.String())
				if len(clean) > len(prevClean) {
					sendSSE(map[string]any{"type": "token", "text": clean[len(prevClean):]})
					prevClean = clean
				}
			})
		} else {
			res, err = client.Chat(r.Context(), chatReq)
		}
		if err != nil {
			h.logger.ErrorContext(r.Context(), "assistant: llm", "error", err)
			if sse {
				sendSSE(map[string]any{"type": "fallback", "text": "Sorry — I'm having trouble right now. Please use the calendar below."})
			} else {
				h.writeJSON(w, http.StatusOK, assistantResponse{Reply: "Sorry — I'm having trouble right now. Please pick a time from the calendar below.", Fallback: true})
			}
			return
		}
		res.Message.Content = stripReasoning(res.Message.Content)
		msgs = append(msgs, res.Message)

		if len(res.Message.ToolCalls) == 0 {
			if sse {
				sendSSE(map[string]any{"type": "done", "booking": booked})
			} else {
				h.writeJSON(w, http.StatusOK, assistantResponse{Reply: res.Message.Content, Booking: booked})
			}
			return
		}

		// Execute the model's tool calls and feed results back.
		for _, tc := range res.Message.ToolCalls {
			if sse {
				sendSSE(map[string]any{"type": "status", "text": assistantToolStatus(tc.Function.Name)})
			}
			result, bk := h.runAssistantTool(r.Context(), slug, tz, tc.Function.Name, tc.Function.Arguments)
			if bk != nil {
				booked = bk
			}
			msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: result})
		}
	}

	// Iteration cap hit without a final message — graceful fallback.
	if sse {
		sendSSE(map[string]any{"type": "fallback", "text": "Let's keep it simple — please pick a time from the calendar below."})
	} else {
		h.writeJSON(w, http.StatusOK, assistantResponse{Reply: "Let's keep it simple — please pick a time from the calendar below.", Booking: booked, Fallback: true})
	}
}

// assistantFallback responds with a "use the picker" message in the right format.
func (h *Handler) assistantFallback(w http.ResponseWriter, sse bool, msg string) {
	if !sse {
		h.writeJSON(w, http.StatusOK, assistantResponse{Reply: msg, Fallback: true})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	b, _ := json.Marshal(map[string]any{"type": "fallback", "text": msg})
	fmt.Fprintf(w, "data: %s\n\n", b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// assistantToolStatus is a short, booker-facing status shown while a tool runs.
func assistantToolStatus(name string) string {
	switch name {
	case "find_available_slots":
		return "Checking availability…"
	case "book":
		return "Booking…"
	default:
		return "Working…"
	}
}

// assistantSystemPrompt builds the system prompt from the (active+public) event type's
// public details + intake questions. Returns ok=false if the slug isn't bookable.
func (h *Handler) assistantSystemPrompt(ctx context.Context, slug, tz string) (string, bool) {
	et, err := h.loadBookableEventType(ctx, slug)
	if err != nil {
		return "", false
	}
	name, duration, locType := et.Name, et.DurationMinutes, et.LocationType

	// Required-question list so the model knows what to collect before booking.
	var qLines []string
	rows, qErr := h.db.QueryContext(ctx, `
		SELECT id, label, type, COALESCE(options, ''), required
		FROM event_type_questions WHERE event_type_id = ? ORDER BY position, id`, et.ID)
	if qErr == nil {
		defer rows.Close()
		for rows.Next() {
			var id, label, qtype, opts string
			var req int
			if rows.Scan(&id, &label, &qtype, &opts, &req) != nil {
				continue
			}
			line := fmt.Sprintf("- %q (question_id=%s, type=%s%s)", label, id, qtype, map[bool]string{true: ", REQUIRED", false: ""}[req != 0])
			if opts != "" {
				line += " options=" + opts
			}
			qLines = append(qLines, line)
		}
	}
	questions := "none"
	if len(qLines) > 0 {
		questions = "\n" + strings.Join(qLines, "\n")
	}

	today := time.Now().UTC().Format("2006-01-02")
	prompt := fmt.Sprintf(`You are a concise scheduling assistant helping a visitor book "%s" (a %d-minute %s meeting).
Today is %s. The visitor's timezone is %s — show times in that timezone and state it once early on; if they name a different timezone, use theirs.
Intake questions: %s

%s`, name, duration, locationLabel(locType, ""), today, tz, questions, assistantBaseRules)

	// Admin "Additional instructions" — appended, never replacing the rules above.
	var extra string
	_ = h.db.QueryRowContext(ctx, `SELECT llm_extra_instructions FROM server_settings WHERE id = 1`).Scan(&extra)
	if strings.TrimSpace(extra) != "" {
		prompt += "\n\nAdditional instructions from the host (follow these unless they conflict with the rules above):\n" + strings.TrimSpace(extra)
	}
	return prompt, true
}

// assistantTools is the narrow, event-type-scoped tool set exposed to the model.
func (h *Handler) assistantTools() []llm.Tool {
	return []llm.Tool{
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "find_available_slots",
				Description: "Return bookable time slots for this event type in the visitor's timezone. Optionally narrow by date range (YYYY-MM-DD).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"date_from": map[string]any{"type": "string", "description": "earliest date, YYYY-MM-DD (optional)"},
						"date_to":   map[string]any{"type": "string", "description": "latest date, YYYY-MM-DD (optional)"},
					},
				},
			},
		},
		{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        "book",
				Description: "Book a specific slot for this event type. Use only after confirming the time and collecting the visitor's details.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"slot_start": map[string]any{"type": "string", "description": "exact slot start (RFC3339) from find_available_slots"},
						"name":       map[string]any{"type": "string"},
						"email":      map[string]any{"type": "string"},
						"answers": map[string]any{
							"type":        "array",
							"description": "answers to intake questions",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"question_id": map[string]any{"type": "string"},
									"value":       map[string]any{"type": "string"},
								},
							},
						},
					},
					"required": []string{"slot_start", "name", "email"},
				},
			},
		},
	}
}

// runAssistantTool executes one model tool call against the deterministic cores, scoped to
// this slug. Returns the tool-result text (fed back to the model) and, if a booking was
// made, its summary.
func (h *Handler) runAssistantTool(ctx context.Context, slug, tz, name, argsJSON string) (string, *assistantBooking) {
	switch name {
	case "find_available_slots":
		var args struct {
			DateFrom string `json:"date_from"`
			DateTo   string `json:"date_to"`
		}
		_ = json.Unmarshal([]byte(argsJSON), &args)
		slots, _, err := h.computeSlots(ctx, slug, tz, args.DateFrom, args.DateTo)
		if err != nil {
			return "error: could not load availability", nil
		}
		if len(slots) == 0 {
			return `{"slots":[],"note":"no availability in that range"}`, nil
		}
		truncated := false
		if len(slots) > assistantMaxSlots {
			slots = slots[:assistantMaxSlots]
			truncated = true
		}
		out := struct {
			Slots     []slotJSON `json:"slots"`
			Truncated bool       `json:"truncated,omitempty"`
		}{slots, truncated}
		b, _ := json.Marshal(out)
		return string(b), nil

	case "book":
		var args struct {
			SlotStart string `json:"slot_start"`
			Name      string `json:"name"`
			Email     string `json:"email"`
			Answers   []struct {
				QuestionID string `json:"question_id"`
				Value      string `json:"value"`
			} `json:"answers"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "error: invalid arguments", nil
		}
		if args.SlotStart == "" || args.Name == "" || args.Email == "" {
			return "error: slot_start, name, and email are required to book", nil
		}
		startAt, err := time.Parse(time.RFC3339, args.SlotStart)
		if err != nil {
			return "error: slot_start must be an exact RFC3339 time from find_available_slots", nil
		}
		raw := make([]booking.Answer, len(args.Answers))
		for i, a := range args.Answers {
			raw[i] = booking.Answer{QuestionID: a.QuestionID, Value: a.Value}
		}
		b, err := h.createBookingForSlug(ctx, slug, startAt,
			booking.Attendee{Name: args.Name, Email: args.Email, IANATimezone: tz}, raw)
		if err != nil {
			return "error: " + assistantBookError(err), nil
		}
		return fmt.Sprintf("booked successfully: id=%s start=%s", b.ID, b.StartAt.UTC().Format(time.RFC3339)),
			&assistantBooking{ID: b.ID, StartAt: b.StartAt.UTC().Format(time.RFC3339), EndAt: b.EndAt.UTC().Format(time.RFC3339), Status: b.Status}

	default:
		return "error: unknown tool", nil
	}
}

// assistantBookError maps a createBookingForSlug error to a short, booker-facing message
// the model can relay.
func assistantBookError(err error) string {
	switch {
	case errors.Is(err, errEventTypeNotFound):
		return "this event type is no longer available"
	case errors.Is(err, booking.ErrDoubleBooked), errors.Is(err, errNoHostAvailable), errors.Is(err, errSlotUnavailable):
		return "that time was just taken — please choose another slot"
	case errors.Is(err, booking.ErrBookingLimitReached):
		return "you already have the maximum number of upcoming bookings for this event"
	default:
		var ae *answerError
		if errors.As(err, &ae) {
			return ae.msg
		}
		return "could not complete the booking"
	}
}
