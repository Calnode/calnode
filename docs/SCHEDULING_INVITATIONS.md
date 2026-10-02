# Scheduling invitations: external integration API

This implements the accepted initial Calnode scope of
[issue #92](https://github.com/Calnode/calnode/issues/92), following
[discussion #47](https://github.com/Calnode/calnode/discussions/47) and the
maintainer's five-slice order: records/tokens, duration policy, fixed hosts,
date windows, then booking/lifecycle webhooks. The external ticketing adapter
is a separate project. Calnode calculates availability and owns booking state.

## Authentication and ownership

Use the existing `X-API-Key` authentication, or an authenticated staff session.
An invitation creator must own the event type. Active private event types are
eligible; paid event types are rejected. Being an event-type host or workspace
administrator does not independently grant access to another owner's invitations.
Create/list/read/cancel are scoped to the creator, with 404 for cross-owner IDs.

The recommended restriction is `host_id`: one active member of that event type's
host set, retained as a required host. Arbitrary workspace users are rejected.
Omit it to snapshot the existing required/rotation/optional configuration and
rotation strategy. The foundation's `hosts [{user_id, role, priority}]` form is
also retained; do not supply it together with `host_id`. Both forms use the
existing host-role model and booking assignment service.

## Create, deliver and book

1. The owner configures an optional duration range through the event-type API
   or the existing event-type editor. Ordinary public bookings use the default.

   ```http
   PATCH /v1/event-types/customer-support
   X-API-Key: <owner-api-key>
   Content-Type: application/json

   {
     "duration_minutes": 30,
     "min_duration_minutes": 15,
     "max_duration_minutes": 90,
     "duration_increment_minutes": 15
   }
   ```

2. The calling system creates an immediately active invitation. Replace the
   example dates/expiration with future values and use an eligible host ID.

   ```http
   POST /v1/scheduling-invitations
   X-API-Key: <owner-api-key>
   Content-Type: application/json

   {
     "event_type_slug": "customer-support",
     "recipient": {"name": "Customer", "email": "customer@example.com"},
     "duration_minutes": 60,
     "host_id": "<eligible-host-id>",
     "available_from": "2027-03-14",
     "available_until": "2027-03-20",
     "availability_timezone": "Europe/Amsterdam",
     "expires_at": "2027-03-21T00:00:00Z",
     "delivery": "external",
     "external": {
       "system": "zammad",
       "reference": "ticket-123",
       "url": "https://tickets.example.com/tickets/123"
     }
   }
   ```

   The 201 response contains the invitation snapshot, `id`, `status: active`,
   and `scheduling_url: https://calnode.example.com/s/<opaque-token>`.
   The foundation's `token` response field is retained for compatibility.
   These bearer credentials are returned **only at issuance**. Later reads
   cannot reconstruct the URL. Save the response securely before delivering it.
   There is no create-request idempotency key for this initial API; after an
   ambiguous network failure, inspect the owner-scoped list before retrying.

3. The caller sends the URL externally. Calnode does not send the initial request
   email. `delivery` defaults to `external`; `calnode` and other modes return 400.
   `external.url` is metadata: Calnode never fetches it or includes it on the
   public invitation page.

4. The customer opens the link, selects their display timezone and a slot,
   answers current intake questions, and confirms. The existing booking template
   displays the authorized duration, location and host information. Recipient
   fields are read-only. Public submission accepts `start_at`, `timezone`,
   `answers [{question_id, value}]`, plus the existing `language` and `hp_extra`
   presentation/anti-bot fields. Recipient, duration, hosts, event type, window
   and external metadata cannot be supplied by the customer.

   ```http
   GET /v1/schedule/<token>/slots?from=2027-03-14&to=2027-03-20&tz=Europe/Amsterdam
   POST /v1/schedule/<token>/book
   Content-Type: application/json

   {"start_at":"2027-03-15T09:00:00Z","timezone":"Europe/Amsterdam","answers":[]}
   ```

   Slots include start/end and authorized candidate host IDs. The 201 booking
   response includes only booking ID, interval and status. Missing required
   intake answers return 400; a lost slot returns 409; a terminal invitation
   returns 410; invalid credentials return 404. Calendar-check failure returns
   503 and creates no booking. Slot listings can report `degraded: true` during
   a provider outage, following the existing scheduler convention; submission
   fails closed until calendars can be checked.

5. Calnode atomically creates one booking, records assigned hosts/recipient/intake,
   marks the invitation booked, and consumes the token. Conditional updates and
   a unique `bookings.scheduling_invitation_id` index protect against replay.
   Host/time overlaps are checked inside the same SQLite transaction, including
   competing invitations. Current calendar checks occur before the transaction;
   local hours, bookings and constraints are rechecked on its connection using
   the same slot engine. External calendar/network operations never run in the
   transaction. Existing meeting, calendar, confirmation and reminder work runs
   after commit with the existing best-effort/reconciliation conventions.

## Authenticated resource endpoints

| Method/path | Behavior |
| --- | --- |
| `POST /v1/scheduling-invitations` | Issue an active invitation and return its URL once |
| `GET /v1/scheduling-invitations?limit=25&offset=0` | Creator-scoped `items`, `total`, `limit`, `offset`; limit 1–100, non-negative offset; newest first |
| `GET /v1/scheduling-invitations/{id}` | Full authorized snapshot/status/external reference/optional booking ID, without token or URL |
| `GET /v1/scheduling-invitations/{id}/slots?from=...&to=...&timezone=UTC` | Authenticated availability preview |
| `POST /v1/scheduling-invitations/{id}/cancel` | Cancel an active, unexpired, unconsumed invitation; repeated cancellation is safe |

Booked or expired invitations cannot be cancelled by this resource endpoint.
Cancel an existing booking through its existing management flow. API errors use
the existing `{"error":"..."}` shape. Public slot requests must start no earlier
than 31 days before today; the normal future cap still applies.

## Duration policies

All durations and increments must be positive integers. Configure all three
policy fields together initially: minimum must not exceed maximum, default
duration must be inside the range, and `(duration - minimum) % increment == 0`.
The maximum is an upper bound and need not itself be an allowed increment.
No rounding or clamping occurs. Invitation creation validates and stores the
effective duration; omission or null snapshots the current default.

All-null policy fields mean fixed duration, including every pre-existing event
type. PATCH omission preserves existing values; a partial numeric PATCH is
validated against the full effective policy. Reset by explicitly sending all
three fields as null. A partial null reset is rejected. A duration-only update
to an explicit fixed policy (`min == max`) clears those bounds, preserving the
legacy client's ability to change its fixed default without stale limits.
Duration-only updates to a configured range must satisfy that range. Duplication
copies the policy. Turning the editor's range control off sends the all-null reset.
Slot interval stays distinct from appointment duration.

## Snapshots, live inputs and date boundaries

| Retained authorization | Live scheduling inputs |
| --- | --- |
| Recipient, effective duration, interval, buffers, host roles/pool, routing/rotation strategy, date window/timezone, external reference, location behavior | Host working hours and date overrides, calendar busy periods, existing bookings, event/host availability, intake questions, display name/branding, active-booking cap, confirmation preferences |
| Original minimum notice and future limit | Current event-type notice/future policy intersects the retained limits, taking the stricter value |

Event-type edits never rewrite retained values. A booked invitation remains as
the immutable authorization record linked from the booking. Management uses
that record and the booking's selected interval/assigned hosts, without requiring
the consumed invitation credential. Optional hosts assigned at booking retain
their seats when rescheduled, matching the existing multi-host management model.

Date-only `available_from` and `available_until` are **inclusive local calendar
dates** in `availability_timezone` (UTC by default; a valid IANA timezone is
required, and `Local` is rejected). The stored interval begins at local midnight
on the first date and ends at local midnight after the last date, converted to
UTC using calendar-date arithmetic. DST days can be 23 or 25 hours. RFC3339
timestamps are also accepted for precise bounds. Either bound may be omitted;
reversed or equal timestamp bounds are rejected.

The whole appointment must fit: start is at/after the lower bound, end is at/before
the upper bound. The upper bound excludes appointment starts at that instant;
an appointment may end exactly there. Buffers separate appointments from busy
intervals using existing slot-engine semantics; buffers need not fit inside
the invitation window or working hours. The window never bypasses notice,
future limits, working hours or conflicts. The same rules apply to listing,
booking and rescheduling, including interval-grid alignment.

Archived/disabled event types and archived required hosts prevent new bookings
and rescheduling; cancellation of an existing booking remains available.
Archived rotation/optional candidates are excluded before initial assignment;
an unavailable selected host never falls back to another pool. Foreign keys
preserve the source event type/users and existing delete endpoints advise
archiving when invitations reference them.

## Lifecycle events and signed booking correlation

Subscribe through the existing `POST /v1/webhooks` API or webhooks editor:

```json
{
  "url": "https://tickets.example.com/calnode/webhook",
  "events": [
    "scheduling_invitation.created", "scheduling_invitation.booked",
    "scheduling_invitation.cancelled", "scheduling_invitation.expired",
    "booking.created", "booking.rescheduled", "booking.cancelled"
  ]
}
```

Invitation lifecycle events go to the creator's subscriptions. Related booking
events go to the primary host's **and** creator's subscriptions, deduplicated when
they are the same owner. Ordinary booking subscriptions/payload shapes remain
unchanged. The creator therefore receives correlation even when another eligible
host was selected. Optional booking/attendee fields keep existing field-selection
semantics; invitation correlation, intervals, effective duration, assigned hosts
and available location information are mandatory for invitation booking events.

```http
X-Calnode-Event: booking.created
X-Calnode-Delivery: <stable-delivery-id>
X-Calnode-Signature: sha256=<HMAC-SHA256-of-exact-request-body>
```

```json
{
  "id": "<event-id>",
  "event": "booking.created",
  "created_at": "2027-03-15T08:00:00Z",
  "data": {
    "id": "<booking-id>",
    "booking_id": "<booking-id>",
    "scheduling_invitation_id": "<invitation-id>",
    "event_type_id": "<event-type-id>",
    "event_type_slug": "customer-support",
    "duration_minutes": 60,
    "start_at": "2027-03-15T09:00:00Z",
    "end_at": "2027-03-15T10:00:00Z",
    "status": "confirmed",
    "host_id": "<selected-host-id>",
    "hosts": [{"id":"<selected-host-id>","name":"Host","is_primary":true}],
    "location_type": "teams",
    "location_value": "https://teams.microsoft.com/...",
    "external": {
      "system":"zammad", "reference":"ticket-123",
      "url":"https://tickets.example.com/tickets/123"
    }
  }
}
```

Decode the hex secret returned by webhook creation into bytes, compute HMAC-SHA256
over the **unchanged body**, and compare signatures in constant time. Do not trust
unverified JSON. Preserve the existing envelope/signing scheme. New invitation
and correlated booking envelopes have a stable `id`; delivery retries retain
`X-Calnode-Delivery` and the same stored body/signature.

Invitation transitions write their payload to a transactional outbox via SQLite
triggers. Every worker poll conditionally expires eligible active invitations
and atomically moves pending outbox records into the existing delivery/jobs
tables. An expiration transition/event happens once, including after restart.
Historical invitation rows are not re-emitted by the upgrade. Transition-time
status and booking intervals are captured before later edits; meeting location
is enriched with whatever value exists when the outbox is dispatched.

Delivery is **at least once**, with the existing three-attempt policy (60 seconds,
then five minutes). Receivers must deduplicate by event/delivery ID. Ordering is
not guaranteed across asynchronous booking side effects, lifecycle outbox
dispatch, retries or different endpoints. `scheduling_invitation.booked` can
arrive before `booking.created` and before a generated meeting URL is available.
Reconcile current invitation status with the authenticated read API; do not
assume that receiving creation last means an invitation became active again.
Existing booking-side-effect enqueueing is best effort after commit, as before;
successful enqueueing uses durable delivery/retry jobs. Failed deliveries remain
visible in the existing webhook delivery log. Do not infer exactly-once receipt.
The remaining commit-to-enqueue recovery gap is tracked in
[the fork's booking-event durability issue](https://github.com/KitKat31337/calnode/issues/4).

Rescheduling/cancellation use the existing booking-management credential, keep
external correlation and retain duration/assigned hosts. Rescheduling enforces
the retained window and live policies/conflicts. Cancellation leaves the original
invitation **booked/consumed**; issue a new invitation if another appointment is
needed. Invitation expiration prevents initial booking, not management of an
already-created appointment. Host reassignment of invitation bookings is rejected.

## Security and current limitations

Tokens contain 32 cryptographically random bytes and persist only as SHA-256
hashes. The initial bearer grants scheduling authority for the named recipient;
it does not verify the visitor's identity. Treat the URL as a credential, use
HTTPS, protect it in the calling system, and redact paths in reverse-proxy logs.
Calnode redacts `/s/` and `/v1/schedule/` credentials from request logs. Credentials
never appear in webhooks. Token endpoints are rate limited (60 slot/page requests
and 20 submissions per IP per minute). Credential pages/API responses use
`no-store`, `Referrer-Policy: no-referrer`, and noindex/nofollow headers.

Invitation and related management pages omit analytics, arbitrary injected head
scripts, assistants, Markdown embeds and remote images. Same-origin branding and
legal links remain, with a restrictive CSP. Intake/current UI components and
normal booking localization are reused; new invitation terminal/error messages
are currently English. No polished invitation-management dashboard is included.

Deferred: verification codes, token replacement, draft/edit/activation flows,
initial invitation emails, payments, Zammad adapter, ticket synchronization,
and invitation retention/purging policy. Lost issuance responses cannot recover
their raw scheduling URL; cancel the record and create a new one when necessary.
External calendars can change between their final check and database commit;
this initial feature retains the existing best-effort provider boundary, while
local Calnode conflicts and single-use consumption are transactional.

Provider-fake tests require no live M365 credentials. Live Microsoft 365/Teams
creation, all-host calendar updates/cancellation, invitation/email/reminder
delivery and the separately implemented ticket adapter still need integration
testing before deployment.

## Migration coordination

Assume [directory-order PR #127](https://github.com/Calnode/calnode/pull/127)
lands first and reserves `00070_event_type_display_order.sql`. The unmerged
invitation migrations are consequently numbered `00071_scheduling_invitations.sql`
and `00072_invitation_booking_lifecycle.sql`. There are no duplicate versions in
this branch; directory implementation remains a separate upstream dependency.

Disposable migration tests upgrade populated databases at versions 69, 70
(directory ordering), and 71 (invitation foundation), check preservation,
backfills/uniqueness, and exercise down/re-upgrade. Version 72 backfills existing
invitations' location snapshot from their event type once at upgrade; newly issued
invitations snapshot it at creation.

This development line assumes clean databases. Earlier experimental builds used
invitation versions 00070/00071; their databases are not an upgrade target for
this renumbered line. Keep those databases with the previous build until a separate
compatibility upgrade is supplied if their data must be retained. Never rewrite
their applied Goose history. No existing developer database was reset or modified
to implement this numbering change.
