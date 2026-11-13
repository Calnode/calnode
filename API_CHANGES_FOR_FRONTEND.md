# API Changes for Frontend (Forma Prime Fork)

This document describes the backend API additions for the SvelteKit 5 admin UI (`frontend/src/`) and public booking flows.

---

## 1. Event Type Daily & Weekly Frequency Caps

### Event Type Schema (`GET /v1/event-types`, `GET /v1/event-types/{slug}`, `POST /v1/event-types`, `PATCH /v1/event-types/{slug}`)

Two optional nullable integer fields are exposed on every event type payload:

```json
{
  "id": "...",
  "slug": "founder-intro",
  "name": "Founder Intro",
  "max_per_day": 2,
  "max_per_week": 6
}
```

- **Types:** `number | null`
- **Semantics:**
  - `null` or omitted: unlimited for that period.
  - Positive integer (`> 0`): maximum number of non-cancelled (`status != 'cancelled'`) confirmed bookings allowed per calendar day (`YYYY-MM-DD`) or ISO week (Monday–Sunday) in the event type owner's configured IANA timezone (`users.iana_timezone`).
  - `0` on `POST` or `PATCH`: normalized to `null` (unlimited).
  - Negative integer (`< 0`): rejected with `400 Bad Request` (`"max_per_day cannot be negative (null or 0 = unlimited)"` / `"max_per_week cannot be negative (null or 0 = unlimited)"`).
  - Omitting `max_per_day` / `max_per_week` in `PATCH` leaves the existing value unchanged; sending explicit `null` or `0` clears the cap to `null`.
- **Conflict Behavior:**
  - `GET /v1/event-types/{slug}/slots` omits any slots whose day or ISO week has already reached its cap.
  - `POST /v1/bookings` returns `409 Conflict` (`{"error": "daily or weekly booking limit reached for this event type"}`) if a concurrent booking fills the cap.

---

## 2. Native Single-Use (Ephemeral) Booking Links

All admin endpoints require standard session or API key authentication (`RequireAuth`). `{id}` in `/v1/event-types/{id}/single-use-links` accepts either the event type's UUID `id` or `slug`, scoped to admins, the event type owner, or assigned hosts.

### `POST /v1/event-types/{id}/single-use-links`

Generates a cryptographically random URL-safe single-use link for the event type.

**Request Body:**
```json
{
  "prefill_name": "Jane Founder",
  "prefill_email": "jane@startup.io",
  "deal_id": "deal_123",
  "expires_in_hours": 168
}
```
- All fields are optional.
- `expires_in_hours`: defaults to `168` (7 days) when omitted. `0` creates a non-expiring single-use link (`expires_at: null`). Negative values return `400 Bad Request`.

**Response (`201 Created`):**
```json
{
  "id": "01J...",
  "token": "xYz...",
  "url": "https://cal.example.com/t/xYz...",
  "event_type_id": "01J...",
  "created_by_user_id": "01J...",
  "prefill_name": "Jane Founder",
  "prefill_email": "jane@startup.io",
  "deal_id": "deal_123",
  "expires_at": "2026-11-20T12:00:00Z",
  "used_at": null,
  "booking_id": null,
  "created_at": "2026-11-13T12:00:00Z"
}
```
- `url` uses `<PUBLIC_BASE_URL || BASE_URL>/t/<token>` (or `/t/<token>` if no base URL is configured).

### `GET /v1/event-types/{id}/single-use-links`

Lists tokens generated for the event type ordered newest-first (`created_at DESC`).

**Response (`200 OK`):**
```json
{
  "single_use_links": [
    {
      "id": "01J...",
      "token": "xYz...",
      "url": "https://cal.example.com/t/xYz...",
      "event_type_id": "01J...",
      "created_by_user_id": "01J...",
      "prefill_name": "Jane Founder",
      "prefill_email": "jane@startup.io",
      "deal_id": "deal_123",
      "expires_at": "2026-11-20T12:00:00Z",
      "used_at": null,
      "booking_id": null,
      "created_at": "2026-11-13T12:00:00Z"
    }
  ]
}
```

### `DELETE /v1/single-use-links/{token}`

Revokes an unused single-use token.

- `204 No Content`: token deleted.
- `404 Not Found`: token does not exist or caller lacks access to its event type.
- `409 Conflict`: token has already been used to create a booking.

### Public Token Entry & Booking Flow

- **`GET /t/{token}`**
  - Renders the public booking page (`book.html`) with `#f-name` and `#f-email` prefilled, `SINGLE_USE_TOKEN` injected, and any matching `dealId` / `deal_id` intake question prefilled (explicit `?name=`, `?email=`, `?dealId=`/`?deal_id=` URL query params override token defaults).
  - Works for both public and private (`is_public = false`) active event types.
  - Returns `410 Gone` HTML (`"This booking link has expired or has already been used"`) if the token is missing, revoked, already used, expired, or its event type is inactive.
- **`POST /v1/bookings`**
  - Accepts optional `"single_use_token": "<token>"` in the JSON body.
  - Atomically marks the token used (`used_at`, `booking_id`) inside the SQLite booking creation transaction, or rolls back with `410 Gone` if already used/expired.
  - Resulting bookings use their own manage token on `/manage/{token}` and `/v1/bookings/{id}/reschedule` without modifying `single_use_tokens`.

---

## 3. Environment Variables (Auth & OAuth)

- `MICROSOFT_TENANT_ID`: Microsoft Entra tenant authority (`"common"` by default when unset; falls back to `MICROSOFT_TENANT` if only that legacy var is set).
- `ALLOWED_SIGNUP_DOMAINS`: Comma-separated list of domains (e.g., `formaprime.vc`) permitted to auto-provision `"member"` accounts on OAuth login. Matching domains succeed with a session; non-matching domains are rejected with `403 Forbidden`. When empty/unset, self-provisioning stays disabled (`/admin/login?error=no_account`).
