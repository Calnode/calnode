<script lang="ts">
  import { onMount } from "svelte";
  import { page } from "$app/stores";
  import { goto } from "$app/navigation";
  import { base } from "$app/paths";
  import {
    api,
    type EventType,
    type EventTypeHost,
    type TeamMember,
    type Team,
    type CalendarStatus,
    type ZoomStatus,
    type SingleUseLink,
    type Question,
  } from "$lib/api";
  import { currentUser } from "$lib/stores";
  import { Button, buttonVariants } from "$lib/components/ui/button";
  import { Input } from "$lib/components/ui/input";
  import { Label } from "$lib/components/ui/label";
  import { Checkbox } from "$lib/components/ui/checkbox";
  import { Textarea } from "$lib/components/ui/textarea";
  import { Switch } from "$lib/components/ui/switch";
  import * as Select from "$lib/components/ui/select";
  import * as Tooltip from "$lib/components/ui/tooltip";
  import { toast } from "svelte-sonner";
  import { saveOnCmdS } from "$lib/save-shortcut";
  import QuestionsPanel from "$lib/components/event-types/QuestionsPanel.svelte";
  import EmbedPanel from "$lib/components/event-types/EmbedPanel.svelte";
  import BookingPreviewCard from "$lib/components/event-types/BookingPreviewCard.svelte";
  import { ConfirmDialog } from "$lib/components/ui/confirm-dialog";
  import { t, tr, localeTag } from "$lib/i18n";
  import LinkedText from "$lib/i18n/linked-text.svelte";

  // Ordered by expected usage. 'custom_video' is retired from the picker but the
  // backend still renders any legacy event types that use it.
  const LOCATION_TYPES = $derived([
    { value: "zoom", label: $t("eventTypeEditor.location.types.zoom") },
    { value: "teams", label: $t("eventTypeEditor.location.types.teams") },
    {
      value: "google_meet",
      label: $t("eventTypeEditor.location.types.googleMeet"),
    },
    { value: "livekit", label: $t("eventTypeEditor.location.types.livekit") },
    { value: "phone", label: $t("eventTypeEditor.location.types.phone") },
    { value: "link", label: $t("eventTypeEditor.location.types.link") },
    {
      value: "in_person",
      label: $t("eventTypeEditor.location.types.inPerson"),
    },
  ]);

  const LOCATION_NEEDS_VALUE = $derived<Record<string, string>>({
    link: $t("eventTypeEditor.location.labels.meetingUrl"),
    zoom: $t("eventTypeEditor.location.labels.zoomLink"),
    google_meet: $t("eventTypeEditor.location.labels.meetLink"),
    teams: $t("eventTypeEditor.location.labels.teamsLink"),
    phone: $t("eventTypeEditor.location.labels.phone"),
    in_person: $t("eventTypeEditor.location.labels.address"),
    custom_video: $t("eventTypeEditor.location.labels.meetingUrl"),
  });
  // Placeholder hint per type — in-person is the only optional value.
  const LOCATION_PLACEHOLDER = $derived<Record<string, string>>({
    zoom: $t("eventTypeEditor.location.placeholder.zoomRequired"),
    link: $t("eventTypeEditor.location.placeholder.linkRequired"),
    phone: $t("eventTypeEditor.location.placeholder.phoneRequired"),
    in_person: $t("eventTypeEditor.location.placeholder.addressOptional"),
  });

  const INVITE_DELIVERY = $derived([
    {
      value: "calendar",
      label: $t("eventTypeEditor.invite.delivery.calendar"),
    },
    { value: "calnode", label: $t("eventTypeEditor.invite.delivery.calnode") },
    { value: "none", label: $t("eventTypeEditor.invite.delivery.none") },
  ]);

  // ── Event type ───────────────────────────────────────────────────────────────
  let et = $state<EventType | null>(null);
  const TABS = $derived([
    { id: "general" as const, label: $t("eventTypeEditor.tabs.general") },
    { id: "limits" as const, label: "Limits & buffers" },
    { id: "hosts" as const, label: $t("eventTypeEditor.tabs.hosts") },
    { id: "payments" as const, label: $t("settings.nav.payments") },
    {
      id: "notifications" as const,
      label: $t("eventTypeEditor.tabs.notifications"),
    },
    { id: "questions" as const, label: "Booking form" },
    { id: "embed" as const, label: $t("eventTypeEditor.tabs.embed") },
  ]);
  let activeTab = $state<(typeof TABS)[number]["id"]>("general");
  let previewQuestions = $state<Question[]>([]);
  let etLoading = $state(true);
  let etError = $state("");
  let etSaving = $state(false);

  // Connected calendar (the owner's) — drives the meeting-link auto-generation hint.
  // Only fetched when the viewer owns the event type: the status endpoints describe the
  // caller's own connections, which say nothing about an event type an admin is editing
  // for someone else.
  let calStatus = $state<CalendarStatus | null>(null);
  // Owner's Zoom connection — drives the Zoom auto-mint hint.
  let zoomStatus = $state<ZoomStatus | null>(null);
  // Which connected provider can natively mint each online platform's link.
  const PLATFORM_PROVIDER: Record<string, string> = {
    google_meet: "google",
    teams: "microsoft",
  };
  const isOnlineMeeting = (t: string) => t === "google_meet" || t === "teams";
  // Any location that produces a join link the booker has to receive somehow.
  const isOnlineLocation = (t: string) =>
    isOnlineMeeting(t) || t === "zoom" || t === "livekit";

  let form = $state({
    display_order: 0,
    name: "",
    slug: "",
    description: "",
    duration_minutes: 30,
    slot_interval_minutes: 30,
    is_active: true,
    is_public: true,
    show_taken_slots: false,
    invite_delivery: "calendar" as EventType["invite_delivery"],
    location_type: "link",
    location_value: "",
    buffer_before_minutes: 0,
    buffer_after_minutes: 0,
    min_notice_minutes: 0,
    max_future_days: 60,
    max_active_bookings: 1,
    max_per_day: "" as number | "" | null,
    max_per_week: "" as number | "" | null,
    price_cents: 0,
    currency: "usd",
  });

  // "Your own system" means Calnode sends the booker nothing at all. When email is not
  // configured that is easy to pick by accident and impossible to notice later, so it is
  // confirmed once rather than applied silently.
  let confirmNoneOpen = $state(false);
  let noneAcknowledged = $state(false);
  // Remounts the Select when a choice is declined. bits-ui keeps the clicked option as its
  // internal selection even though the bound value did not change, so re-picking it fires no
  // change event and the dialog would never reopen.
  let inviteSelectReset = $state(0);

  function requestInviteDelivery(v: string | undefined) {
    if (!v) return;
    const mode = v as EventType["invite_delivery"];
    if (
      mode === "none" &&
      et?.invite_sender_ready === false &&
      !noneAcknowledged
    ) {
      confirmNoneOpen = true;
      inviteSelectReset += 1;
      return;
    }
    form.invite_delivery = mode;
    if (mode !== "none") noneAcknowledged = false;
  }

  // Price is edited in major units (e.g. dollars); stored as integer cents.
  let priceMajor = $state("0");
  let paidEnabled = $state(false);

  // Limits & buffers UX helpers (5-min dropdowns + toggles, mapped directly to existing DB columns)
  const BUFFER_OPTIONS = [
    { value: 0, label: "No buffer time" },
    { value: 5, label: "5 minutes" },
    { value: 10, label: "10 minutes" },
    { value: 15, label: "15 minutes" },
    { value: 20, label: "20 minutes" },
    { value: 25, label: "25 minutes" },
    { value: 30, label: "30 minutes" },
    { value: 45, label: "45 minutes" },
    { value: 60, label: "60 minutes" },
    { value: 90, label: "90 minutes" },
    { value: 120, label: "120 minutes" },
  ];
  const NOTICE_OPTIONS = [
    { value: 0, label: "No minimum notice" },
    { value: 5, label: "5 minutes" },
    { value: 10, label: "10 minutes" },
    { value: 15, label: "15 minutes" },
    { value: 30, label: "30 minutes" },
    { value: 45, label: "45 minutes" },
    { value: 60, label: "1 hour" },
    { value: 120, label: "2 hours" },
    { value: 180, label: "3 hours" },
    { value: 240, label: "4 hours" },
    { value: 360, label: "6 hours" },
    { value: 720, label: "12 hours" },
    { value: 1440, label: "24 hours" },
    { value: 2880, label: "48 hours" },
  ];
  const INTERVAL_OPTIONS = [5, 10, 15, 20, 25, 30, 45, 60];

  function optionLabel(
    opts: { value: number; label: string }[],
    v: number,
  ): string {
    return opts.find((o) => o.value === Number(v))?.label ?? `${v} minutes`;
  }

  let freqEnabled = $state(false);
  let freqOrder = $state<("day" | "week")[]>([]);
  let futureLimitEnabled = $state(false);
  let maxActiveEnabled = $state(false);

  function syncFreqFromForm() {
    const next: ("day" | "week")[] = [];
    if (
      form.max_per_day !== "" &&
      form.max_per_day !== null &&
      form.max_per_day !== undefined
    )
      next.push("day");
    if (
      form.max_per_week !== "" &&
      form.max_per_week !== null &&
      form.max_per_week !== undefined
    )
      next.push("week");
    freqOrder = next;
  }

  function setFreqRowUnit(idx: number, newUnit: "day" | "week") {
    const oldUnit = freqOrder[idx];
    if (!oldUnit || oldUnit === newUnit) return;
    const val = oldUnit === "day" ? form.max_per_day : form.max_per_week;
    if (oldUnit === "day") {
      form.max_per_day = "";
      form.max_per_week = val || 1;
    } else {
      form.max_per_week = "";
      form.max_per_day = val || 1;
    }
    freqOrder = freqOrder.map((u, i) => (i === idx ? newUnit : u));
  }

  function addFreqRow() {
    if (!freqOrder.includes("day")) {
      form.max_per_day = 1;
      freqOrder = [...freqOrder, "day"];
    } else if (!freqOrder.includes("week")) {
      form.max_per_week = 3;
      freqOrder = [...freqOrder, "week"];
    }
  }

  function removeFreqRow(unit: "day" | "week") {
    if (unit === "day") form.max_per_day = "";
    else form.max_per_week = "";
    freqOrder = freqOrder.filter((u) => u !== unit);
    if (freqOrder.length === 0) freqEnabled = false;
  }

  function toggleFreqEnabled(on: boolean) {
    freqEnabled = on;
    if (!on) {
      form.max_per_day = "";
      form.max_per_week = "";
      freqOrder = [];
    } else if (freqOrder.length === 0) {
      form.max_per_day = 1;
      freqOrder = ["day"];
    }
  }

  function toggleFutureLimit(on: boolean) {
    futureLimitEnabled = on;
    form.max_future_days = on
      ? Number(form.max_future_days) > 0
        ? Number(form.max_future_days)
        : 30
      : 0;
  }

  function toggleMaxActive(on: boolean) {
    maxActiveEnabled = on;
    form.max_active_bookings = on
      ? Number(form.max_active_bookings) > 0
        ? Number(form.max_active_bookings)
        : 1
      : 0;
  }

  function togglePaidEnabled(on: boolean) {
    paidEnabled = on;
    if (!on) {
      priceMajor = "0.00";
    } else if (!(Number(priceMajor) > 0)) {
      priceMajor = "25.00";
    }
  }

  // True when the connected calendar will auto-generate the chosen platform's link.
  const meetAutoGen = $derived(
    isOnlineMeeting(form.location_type) &&
      !!calStatus?.connected &&
      calStatus?.provider === PLATFORM_PROVIDER[form.location_type],
  );

  // True when the owner's connected Zoom account will auto-mint a meeting per booking.
  const zoomAutoGen = $derived(
    form.location_type === "zoom" && !!zoomStatus?.connected,
  );

  // ── Routing ──────────────────────────────────────────────────────────────────
  // The editor asks two plain questions — "who can host?" and (for a team)
  // "do they rotate or all attend?" — and derives routing_mode + host roles from
  // the answers. The engine and DB are unchanged; this is purely how roles are
  // authored.
  const RR_STRATEGIES = $derived([
    { value: "even", label: $t("eventTypeEditor.hosts.rr.even") },
    { value: "priority", label: $t("eventTypeEditor.hosts.rr.priority") },
    { value: "soonest", label: $t("eventTypeEditor.hosts.rr.soonest") },
  ]);
  type Strategy = "even" | "priority" | "soonest";
  let rrStrategy = $state<Strategy>("even");

  type Host = { user_id: string; name: string; email: string };
  type TogetherHost = Host & { optional: boolean };

  // Q1: just me, or specific people?  Q2 (people only): rotate, or all attend?
  let hostScope = $state<"me" | "people">("me");
  let staffing = $state<"rotate" | "together">("rotate");
  // Rotation pool (each "rotation"); together = required + optional join-if-free.
  let rotationHosts = $state<Host[]>([]);
  let togetherHosts = $state<TogetherHost[]>([]);
  let hostsLoaded = $state(false);
  let transferOwner = $state("");
  let transferring = $state(false);
  let transferHosts = $state<EventTypeHost[]>([]);
  async function transferEvent() {
    if (!transferOwner || !$currentUser) return;
    transferring = true;
    try {
      await api.post(`/v1/event-types/${slug}/transfer`, {
        expected_owner_id: $currentUser.id,
        new_owner_id: transferOwner,
      });
      toast.success(tr("eventTypeEditor.toast.transferred"));
      await goto(`${base}/event-types`);
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : tr("eventTypeEditor.error.transfer"),
      );
    } finally {
      transferring = false;
    }
  }

  let members = $state<TeamMember[]>([]);
  let teams = $state<Team[]>([]);

  // routing_mode is derived from the two answers — never set directly.
  const routingMode = $derived(
    hostScope === "me"
      ? "fixed"
      : staffing === "rotate"
        ? "round_robin"
        : "collective",
  );
  // An admin editing someone else's event type: "Just me" then means the owner, and the
  // owner's connections, not the admin's, decide which meeting links can be generated.
  const editingForOwner = $derived(
    !!et && et.owned === false && et.can_edit !== false,
  );
  const ownerLabel = $derived(
    et?.owner_name || et?.owner_email || $t("eventTypeEditor.ownerFallback"),
  );

  async function loadHosts() {
    try {
      const res = await api.get<{ items: EventTypeHost[] }>(
        `/v1/event-types/${slug}/hosts`,
      );
      const items = res.items ?? [];
      transferHosts = items.filter(
        (h) => h.role === "required" && h.user_id !== $currentUser?.id,
      );
      const toHost = (h: EventTypeHost): Host => ({
        user_id: h.user_id,
        name: h.name,
        email: h.email,
      });
      rotationHosts = items.filter((h) => h.role === "rotation").map(toHost);
      togetherHosts = items
        .filter((h) => h.role === "required" || h.role === "optional")
        .map((h) => ({ ...toHost(h), optional: h.role === "optional" }));
      hostsLoaded = true;
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.error.loadHosts"));
    }
  }

  async function loadMembers() {
    if (members.length > 0) return;
    try {
      members = await api.get<TeamMember[]>("/v1/users");
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.error.loadMembers"));
    }
  }

  async function loadTeams() {
    if (teams.length > 0) return;
    try {
      const res = await api.get<{ items: Team[] }>("/v1/teams");
      teams = res.items ?? [];
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.error.loadTeams"));
    }
  }

  function setScope(s: "me" | "people") {
    hostScope = s;
    if (s === "people") {
      if (!hostsLoaded) loadHosts();
      loadMembers();
      loadTeams();
    }
  }

  // Members already chosen in the *active* staffing list, so the pickers don't
  // re-offer them. Scoped to the visible list (rotation vs together): otherwise
  // hosts loaded for the other mode — e.g. a collective event's members still in
  // togetherHosts after switching to Rotate — would wrongly mark everyone
  // unavailable and make addOne skip every team member.
  const assignedIds = $derived(
    new Set(
      (staffing === "rotate" ? rotationHosts : togetherHosts).map(
        (h) => h.user_id,
      ),
    ),
  );
  // Any active member not already chosen — including the current user, who can
  // be a host like anyone else.
  const availableMembers = $derived(
    members.filter((m) => !m.archived && !assignedIds.has(m.id)),
  );

  type Target = "rotation" | "together";
  function addOne(target: Target, h: Host) {
    // Dedup against this target's own list, not the union — a person can sit in
    // only one staffing list, and the other list may hold stale members from a
    // previous mode.
    const list: { user_id: string }[] =
      target === "rotation" ? rotationHosts : togetherHosts;
    if (list.some((x) => x.user_id === h.user_id)) return;
    if (target === "rotation") rotationHosts = [...rotationHosts, h];
    else togetherHosts = [...togetherHosts, { ...h, optional: false }];
  }
  function addMember(target: Target, userId: string | undefined) {
    if (!userId) return;
    const m = members.find((x) => x.id === userId);
    if (m) addOne(target, { user_id: m.id, name: m.name, email: m.email });
  }
  async function addTeam(target: Target, teamId: string | undefined) {
    if (!teamId) return;
    try {
      const team = await api.get<Team>(`/v1/teams/${teamId}`);
      (team.members ?? [])
        .filter((tm) => !tm.archived)
        .forEach((tm) =>
          addOne(target, { user_id: tm.id, name: tm.name, email: tm.email }),
        );
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.error.loadTeamMembers"));
    }
  }
  function removePerson(target: Target, userId: string) {
    if (target === "rotation")
      rotationHosts = rotationHosts.filter((h) => h.user_id !== userId);
    else togetherHosts = togetherHosts.filter((h) => h.user_id !== userId);
  }
  function setOptional(userId: string, optional: boolean) {
    togetherHosts = togetherHosts.map((h) =>
      h.user_id === userId ? { ...h, optional } : h,
    );
  }
  // Reorder the rotation pool (priority = list position; index 0 is highest).
  function moveRotation(idx: number, dir: -1 | 1) {
    const list = [...rotationHosts];
    const j = idx + dir;
    if (j < 0 || j >= list.length) return;
    [list[idx], list[j]] = [list[j], list[idx]];
    rotationHosts = list;
  }

  // Notification / messaging state
  const REMINDER_OPTIONS = $derived([
    { value: 1, label: $t("eventTypeEditor.reminders.option.1h") },
    { value: 2, label: $t("eventTypeEditor.reminders.option.2h") },
    { value: 4, label: $t("eventTypeEditor.reminders.option.4h") },
    { value: 8, label: $t("eventTypeEditor.reminders.option.8h") },
    { value: 12, label: $t("eventTypeEditor.reminders.option.12h") },
    { value: 24, label: $t("eventTypeEditor.reminders.option.24h") },
    { value: 48, label: $t("eventTypeEditor.reminders.option.48h") },
    { value: 72, label: $t("eventTypeEditor.reminders.option.72h") },
    { value: 168, label: $t("eventTypeEditor.reminders.option.168h") },
  ]);
  let reminders = $state<number[]>([]);
  let msg_confirmation = $state("");
  let msg_cancellation = $state("");
  let msg_reschedule = $state("");
  let msg_reminder = $state("");
  // Assistant's opening chat line for this event type — blank keeps the built-in
  // (translated) default; set overrides it verbatim, same as the email notes above.
  let msg_greeting = $state("");
  // Optional custom subject lines (empty = built-in default subject).
  let subj_confirmation = $state("");
  let subj_cancellation = $state("");
  let subj_reschedule = $state("");
  let subj_reminder = $state("");
  // Track which message accordions are open
  let msgOpen = $state({
    confirmation: false,
    cancellation: false,
    reschedule: false,
    reminder: false,
  });
  // Track preview toggle per accordion
  let previewOpen = $state({
    confirmation: false,
    cancellation: false,
    reschedule: false,
    reminder: false,
  });
  // Test email send state per type
  type MsgKey = "confirmation" | "cancellation" | "reschedule" | "reminder";
  let testSending = $state<Partial<Record<MsgKey, boolean>>>({});
  let testSent = $state<Partial<Record<MsgKey, boolean>>>({});
  let testError = $state<Partial<Record<MsgKey, string>>>({});

  let allowPhoneCall = $state(false);
  const slug = $page.params.slug;

  async function loadET() {
    etError = "";
    try {
      et = await api.get<EventType>(`/v1/event-types/${slug}`);
      allowPhoneCall = et.allow_phone_call;
      form = {
        display_order: et.display_order,
        name: et.name,
        slug: et.slug,
        description: et.description ?? "",
        duration_minutes: et.duration_minutes,
        slot_interval_minutes: et.slot_interval_minutes,
        is_active: et.is_active,
        is_public: et.is_public,
        show_taken_slots: et.show_taken_slots ?? false,
        invite_delivery: et.invite_delivery ?? "calendar",
        location_type: et.location_type,
        location_value: et.location_value ?? "",
        buffer_before_minutes: et.buffer_before_minutes,
        buffer_after_minutes: et.buffer_after_minutes,
        min_notice_minutes: et.min_notice_minutes,
        max_future_days: et.max_future_days,
        max_active_bookings: et.max_active_bookings,
        max_per_day: et.max_per_day ?? "",
        max_per_week: et.max_per_week ?? "",
        price_cents: et.price_cents ?? 0,
        currency: et.currency ?? "usd",
      };
      priceMajor = ((et.price_cents ?? 0) / 100).toFixed(2);
      paidEnabled = (et.price_cents ?? 0) > 0;
      freqEnabled = !!(
        (et.max_per_day && et.max_per_day > 0) ||
        (et.max_per_week && et.max_per_week > 0)
      );
      syncFreqFromForm();
      futureLimitEnabled = (et.max_future_days ?? 0) > 0;
      maxActiveEnabled = (et.max_active_bookings ?? 0) > 0;
      reminders = et.reminders ?? [];
      if (et.routing_mode === "round_robin") {
        hostScope = "people";
        staffing = "rotate";
      } else if (et.routing_mode === "collective") {
        hostScope = "people";
        staffing = "together";
      } else {
        hostScope = "me";
      }
      rrStrategy = (
        ["even", "priority", "soonest"].includes(et.rr_strategy ?? "")
          ? et.rr_strategy
          : "even"
      ) as Strategy;
      msg_confirmation = et.msg_confirmation ?? "";
      msg_cancellation = et.msg_cancellation ?? "";
      msg_reschedule = et.msg_reschedule ?? "";
      msg_reminder = et.msg_reminder ?? "";
      msg_greeting = et.msg_greeting ?? "";
      subj_confirmation = et.subj_confirmation ?? "";
      subj_cancellation = et.subj_cancellation ?? "";
      subj_reschedule = et.subj_reschedule ?? "";
      subj_reminder = et.subj_reminder ?? "";
    } catch (e: any) {
      etError = e.message;
    } finally {
      etLoading = false;
    }
  }

  async function saveET() {
    if (!Number.isSafeInteger(form.display_order)) {
      toast.error(tr("eventTypeEditor.validation.directoryOrder"));
      return;
    }
    if (!form.name.trim()) {
      toast.error(tr("eventTypeEditor.validation.name"));
      return;
    }
    if (form.duration_minutes < 5) {
      toast.error(tr("eventTypeEditor.validation.duration"));
      return;
    }
    // Matches the API, which only requires a positive value. A stricter floor here would
    // make an event type configured below it via the API unsaveable from the editor -
    // including when the person is editing something else entirely.
    if (form.slot_interval_minutes < 1) {
      toast.error(tr("eventTypeEditor.validation.slotInterval"));
      return;
    }
    if (form.max_active_bookings < 0) {
      toast.error(tr("eventTypeEditor.validation.maxActive"));
      return;
    }
    const maxPerDay =
      form.max_per_day === "" || form.max_per_day === null
        ? null
        : Number(form.max_per_day);
    const maxPerWeek =
      form.max_per_week === "" || form.max_per_week === null
        ? null
        : Number(form.max_per_week);
    if (maxPerDay !== null && (!Number.isFinite(maxPerDay) || maxPerDay < 0)) {
      toast.error("Max bookings per day cannot be negative.");
      return;
    }
    if (
      maxPerWeek !== null &&
      (!Number.isFinite(maxPerWeek) || maxPerWeek < 0)
    ) {
      toast.error("Max bookings per week cannot be negative.");
      return;
    }
    if (routingMode === "round_robin" && rotationHosts.length === 0) {
      toast.error(tr("eventTypeEditor.validation.rotation"));
      return;
    }
    if (
      routingMode === "collective" &&
      !togetherHosts.some((h) => !h.optional)
    ) {
      toast.error(tr("eventTypeEditor.validation.collective"));
      return;
    }
    etSaving = true;
    try {
      const updated = await api.patch<EventType>(`/v1/event-types/${slug}`, {
        display_order: form.display_order,
        slug: form.slug.trim(),
        name: form.name.trim(),
        description: form.description.trim() || null,
        duration_minutes: Number(form.duration_minutes),
        slot_interval_minutes: Number(form.slot_interval_minutes),
        is_active: form.is_active,
        is_public: form.is_public,
        show_taken_slots: form.show_taken_slots,
        invite_delivery: form.invite_delivery,
        allow_phone_call: allowPhoneCall,
        location_type: form.location_type,
        location_value: form.location_value.trim() || null,
        buffer_before_minutes: Number(form.buffer_before_minutes),
        buffer_after_minutes: Number(form.buffer_after_minutes),
        min_notice_minutes: Number(form.min_notice_minutes),
        max_future_days: Number(form.max_future_days),
        max_active_bookings: Number(form.max_active_bookings),
        max_per_day: maxPerDay === 0 ? null : maxPerDay,
        max_per_week: maxPerWeek === 0 ? null : maxPerWeek,
        price_cents: Math.max(0, Math.round(Number(priceMajor) * 100)) || 0,
        currency: form.currency.trim().toLowerCase() || "usd",
        routing_mode: routingMode,
        rr_strategy: rrStrategy,
        reminders,
        // Not `|| null`: this form always saves the whole page state, so a blanked
        // field must send '' to actually clear it. The API treats null as "leave
        // unchanged" (for partial-PATCH callers), which would silently keep the old
        // value here — the field would look cleared in the UI but persist server-side.
        msg_confirmation: msg_confirmation.trim(),
        msg_cancellation: msg_cancellation.trim(),
        msg_reschedule: msg_reschedule.trim(),
        msg_reminder: msg_reminder.trim(),
        msg_greeting: msg_greeting.trim(),
        subj_confirmation: subj_confirmation.trim(),
        subj_cancellation: subj_cancellation.trim(),
        subj_reschedule: subj_reschedule.trim(),
        subj_reminder: subj_reminder.trim(),
      });
      // A rename moves the row out from under the name this page was loaded with, so
      // every request after the PATCH has to use the one the server just confirmed.
      const effSlug = updated?.slug || slug;

      if (routingMode === "round_robin") {
        await api.put(`/v1/event-types/${effSlug}/hosts`, {
          hosts: rotationHosts.map((hh, i) => ({
            user_id: hh.user_id,
            role: "rotation",
            priority: i,
          })),
        });
      } else if (routingMode === "collective") {
        await api.put(`/v1/event-types/${effSlug}/hosts`, {
          hosts: togetherHosts.map((hh, i) => ({
            user_id: hh.user_id,
            role: hh.optional ? "optional" : "required",
            priority: i,
          })),
        });
      } else {
        // The owner, not the person saving: an admin saving someone else's single-host
        // event type would otherwise make themselves its host.
        await api.put(`/v1/event-types/${effSlug}/hosts`, {
          hosts: [
            {
              user_id: et?.owner_id ?? $currentUser?.id,
              role: "required",
              priority: 0,
            },
          ],
        });
      }
      toast.success(tr("eventTypeEditor.toast.saved"));
      // The URL carries the slug, so a rename has to move the page too or a reload
      // lands on a 404. replaceState: the old address no longer resolves, and
      // leaving it in history is a back button that breaks.
      if (effSlug !== slug) {
        await goto(`${base}/event-types/${effSlug}`, { replaceState: true });
        return;
      }
      await loadET();
      await loadHosts();
    } catch (e: any) {
      toast.error(e.message || tr("eventTypeEditor.toast.saveFailed"));
    } finally {
      etSaving = false;
    }
  }

  async function sendTestEmail(type: MsgKey) {
    testSending = { ...testSending, [type]: true };
    testSent = { ...testSent, [type]: false };
    testError = { ...testError, [type]: "" };
    try {
      await api.post(`/v1/event-types/${slug}/test-email`, { type });
      testSent = { ...testSent, [type]: true };
      setTimeout(() => {
        testSent = { ...testSent, [type]: false };
      }, 4000);
    } catch (e: any) {
      testError = { ...testError, [type]: e.message };
    } finally {
      testSending = { ...testSending, [type]: false };
    }
  }

  function buildPreview(type: MsgKey, note: string): string {
    const name = et?.name ?? tr("eventTypeEditor.preview.sampleEventFallback");
    const loc = et?.location_value
      ? `\n${tr("eventTypeEditor.preview.locationLine", { value: et.location_value })}`
      : "";
    const dur = et?.duration_minutes ?? 30;
    const noteBlk = note.trim() ? `\n---\n${note.trim()}\n` : "";
    const start = tr("eventTypeEditor.preview.sampleStart");
    const prev = tr("eventTypeEditor.preview.samplePrev");
    const attendee = tr("eventTypeEditor.preview.sampleAttendee");
    const host = tr("eventTypeEditor.preview.sampleHost");
    // Compute end time by adding duration to the sample 14:00 start; format via
    // the admin locale so the preview stays in step with sampleStart.
    const endTotalMin = 14 * 60 + dur;
    const endH24 = Math.floor(endTotalMin / 60) % 24;
    const endMin = endTotalMin % 60;
    const endTime = new Date(
      Date.UTC(2026, 0, 1, endH24, endMin),
    ).toLocaleTimeString(localeTag(), {
      hour: "2-digit",
      minute: "2-digit",
      hour12: localeTag() !== "fr",
      timeZone: "UTC",
    });
    const end = tr("eventTypeEditor.preview.sampleEnd", { time: endTime });

    return tr(`eventTypeEditor.preview.${type}.body`, {
      attendee,
      event: name,
      host,
      start,
      end,
      location: loc,
      note: noteBlk,
      prev,
    });
  }

  onMount(async () => {
    await loadET();
    // Editor-only data — skip for read-only hosts.
    if (et?.can_edit === false) return;
    // Connected calendar — best-effort; drives the meeting-link hint only, and only
    // for the owner's own event types (see calStatus).
    if (et?.owned !== false) {
      api
        .get<CalendarStatus>("/v1/calendar/status")
        .then((s) => (calStatus = s))
        .catch(() => {});
      api
        .get<ZoomStatus>("/v1/zoom/status")
        .then((s) => (zoomStatus = s))
        .catch(() => {});
    }
    await loadHosts();
    if (hostScope === "people") {
      loadMembers();
      loadTeams();
    }
  });

  let mintingOneTime = $state(false);
  async function copyPublicLink() {
    const url = `${window.location.origin}/book/${form.slug || slug}`;
    try {
      await navigator.clipboard.writeText(url);
      toast.success("Booking link copied to clipboard");
    } catch {
      toast.error("Unable to copy automatically");
    }
  }
  async function mintOneTimeLink() {
    if (!et || mintingOneTime) return;
    mintingOneTime = true;
    try {
      const created = await api.post<SingleUseLink>(
        `/v1/event-types/${et.id}/single-use-links`,
        {},
      );
      const url = created.url?.startsWith("http")
        ? created.url
        : `${window.location.origin}/t/${created.token}`;
      await navigator.clipboard.writeText(url);
      toast.success("1-time link copied to clipboard");
    } catch (e: any) {
      toast.error(e.message || "Failed to create 1-time link");
    } finally {
      mintingOneTime = false;
    }
  }
</script>

{#snippet hostPickers(target: Target, idPrefix: string)}
  <div class="grid grid-cols-2 gap-4">
    <div class="space-y-1.5">
      <Label for="{idPrefix}-add-member"
        >{$t("eventTypeEditor.hosts.addMember")}</Label
      >
      <Select.Root
        type="single"
        value=""
        onValueChange={(v) => addMember(target, v)}
      >
        <Select.Trigger id="{idPrefix}-add-member" class="w-full"
          >{$t("eventTypeEditor.hosts.selectMember")}</Select.Trigger
        >
        <Select.Content>
          {#if availableMembers.length > 0}
            {#each availableMembers as m}
              <Select.Item value={m.id} label={m.name}
                >{m.name} · {m.email}</Select.Item
              >
            {/each}
          {:else}
            <div class="px-2 py-1.5 text-xs text-muted-foreground">
              {$t("eventTypeEditor.hosts.noMembers")}
            </div>
          {/if}
        </Select.Content>
      </Select.Root>
    </div>
    <div class="space-y-1.5">
      <Label for="{idPrefix}-add-team"
        >{$t("eventTypeEditor.hosts.addTeam")}</Label
      >
      <Select.Root
        type="single"
        value=""
        onValueChange={(v) => addTeam(target, v)}
      >
        <Select.Trigger id="{idPrefix}-add-team" class="w-full"
          >{$t("eventTypeEditor.hosts.selectTeam")}</Select.Trigger
        >
        <Select.Content>
          {#if teams.length > 0}
            {#each teams as tm}
              <Select.Item value={tm.id} label={tm.name}
                >{tm.name} ({tm.member_count})</Select.Item
              >
            {/each}
          {:else}
            <div class="px-2 py-1.5 text-xs text-muted-foreground">
              {$t("eventTypeEditor.hosts.noTeams")}
            </div>
          {/if}
        </Select.Content>
      </Select.Root>
    </div>
  </div>
{/snippet}

<svelte:head
  ><title
    >{$t("eventTypeEditor.headTitle", { name: et?.name ?? slug ?? "" })}</title
  ></svelte:head
>
<svelte:window onkeydown={saveOnCmdS(saveET, () => !etSaving)} />

<div
  class="sticky top-0 z-20 -mx-4 mb-6 border-b bg-background/95 px-4 py-3 backdrop-blur-sm sm:-mx-6 sm:px-6 lg:-mx-8 lg:px-8"
>
  <div class="flex flex-wrap items-center justify-between gap-3">
    <div class="flex min-w-0 items-center gap-3">
      <a
        href="{base}/event-types"
        class="inline-flex items-center gap-1 rounded-md px-2 py-1 text-sm text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"><polyline points="15 18 9 12 15 6" /></svg
        >
        {$t("eventTypeEditor.back")}
      </a>
      <h1 class="truncate text-lg font-semibold tracking-tight sm:text-xl">
        {form.name || et?.name || slug}
      </h1>
    </div>

    {#if et && et.can_edit !== false}
      <div class="flex flex-wrap items-center gap-2">
        <div
          class="flex items-center gap-2 rounded-md border bg-card px-2.5 py-1"
        >
          <span class="text-xs text-muted-foreground"
            >{form.is_active ? "Active" : "Inactive"}</span
          >
          <Switch bind:checked={form.is_active} />
        </div>
        <Button
          variant="ghost"
          size="icon"
          onclick={() => window.open(`/book/${form.slug || slug}`, "_blank")}
          title={$t("eventTypeEditor.previewTooltip")}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            ><path
              d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"
            /><polyline points="15 3 21 3 21 9" /><line
              x1="10"
              y1="14"
              x2="21"
              y2="3"
            /></svg
          >
        </Button>
        <Button variant="outline" size="sm" onclick={copyPublicLink}
          >Copy Link</Button
        >
        <Button
          variant="outline"
          size="sm"
          onclick={mintOneTimeLink}
          disabled={mintingOneTime || !form.is_active}
        >
          {mintingOneTime ? "Copying..." : "1-Time Link"}
        </Button>
        <Button size="sm" onclick={saveET} disabled={etSaving}>
          {etSaving ? "Saving..." : "Save"}
        </Button>
      </div>
    {/if}
  </div>
</div>

{#if etLoading}
  <p class="py-8 text-sm text-muted-foreground">{$t("common.loading")}</p>
{:else if etError}
  <p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
    {etError}
  </p>
{:else if et && et.can_edit === false}
  <!-- Read-only: the user hosts this event type but doesn't own it -->
  <div
    class="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800"
  >
    {#if et.owner_email}
      <span class="font-medium">{et.owner_name || et.owner_email}</span>
      {$t("eventTypeEditor.readOnly.banner")}
      <a
        href="mailto:{et.owner_email}?subject={encodeURIComponent(
          'Change request: ' + et.name,
        )}"
        class="font-medium underline"
        >{$t("eventTypeEditor.readOnly.messageThem")}</a
      >
      {$t("eventTypeEditor.readOnly.messageRequest")}
    {:else if et.owner_name}
      <span class="font-medium">{et.owner_name}</span>
      {$t("eventTypeEditor.readOnly.banner")}
      {$t("eventTypeEditor.readOnly.messageThem")}
      {$t("eventTypeEditor.readOnly.messageRequest")}
    {:else}
      {$t("eventTypeEditor.readOnly.managedByOwner")}
    {/if}
  </div>
  <div class="rounded-lg border bg-card p-6">
    <dl class="grid grid-cols-[140px_1fr] gap-x-4 gap-y-3 text-sm">
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.name")}
      </dt>
      <dd class="font-medium">{et.name}</dd>
      {#if et.description}<dt class="text-muted-foreground">
          {$t("eventTypeEditor.readOnly.labels.description")}
        </dt>
        <dd class="whitespace-pre-line">{et.description}</dd>{/if}
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.duration")}
      </dt>
      <dd>
        {et.duration_minutes}{$t("eventTypeEditor.readOnly.durationSuffix")}
      </dd>
      {#if et.slot_interval_minutes !== et.duration_minutes}
        <dt class="text-muted-foreground">
          {$t("eventTypeEditor.readOnly.labels.slotInterval")}
        </dt>
        <dd>
          {et.slot_interval_minutes}{$t(
            "eventTypeEditor.readOnly.durationSuffix",
          )}
        </dd>
      {/if}
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.location")}
      </dt>
      <dd>
        {LOCATION_TYPES.find((l) => l.value === et?.location_type)?.label ??
          et.location_type}{#if et.location_value}
          · {et.location_value}{/if}
      </dd>
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.routing")}
      </dt>
      <dd class="capitalize">{et.routing_mode.replace("_", " ")}</dd>
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.status")}
      </dt>
      <dd>
        {et.is_active
          ? $t("eventTypeEditor.readOnly.status.active")
          : $t("eventTypeEditor.readOnly.status.inactive")} · {et.is_public
          ? $t("eventTypeEditor.readOnly.status.listed")
          : $t("eventTypeEditor.readOnly.status.unlisted")}
      </dd>
      <dt class="text-muted-foreground">
        {$t("eventTypeEditor.readOnly.labels.bookingPage")}
      </dt>
      <dd>
        <a
          href="/book/{et.slug}"
          target="_blank"
          rel="noopener"
          class="text-primary underline">/book/{et.slug}</a
        >
      </dd>
    </dl>
  </div>
{:else}
  {#if editingForOwner}
    <div
      class="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-800/60 dark:bg-amber-950/40 dark:text-amber-200"
    >
      {$t("eventTypeEditor.adminEditing.banner", { owner: ownerLabel })}
    </div>
  {/if}

  <div
    class="flex flex-col gap-6 pb-16 lg:grid lg:grid-cols-[208px_minmax(480px,1fr)] lg:items-start xl:grid-cols-[208px_minmax(480px,1fr)_320px]"
  >
    <!-- Sub-Navigation Rail (Left) -->
    <nav
      class="flex shrink-0 gap-1 overflow-x-auto pb-2 lg:flex-col lg:overflow-visible lg:pb-0"
      aria-label="Event type sections"
    >
      <p
        class="hidden px-3 pb-1 pt-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/70 lg:block"
      >
        Setup
      </p>
      {#each TABS as tab}
        <button
          type="button"
          onclick={() => (activeTab = tab.id)}
          class="shrink-0 rounded-md px-3 py-2 text-left text-sm font-medium transition-colors {activeTab ===
          tab.id
            ? 'bg-secondary text-secondary-foreground'
            : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}"
        >
          {tab.label}
        </button>
      {/each}
    </nav>

    <!-- Focused Settings Pane (Center) -->
    <div class="min-w-0 w-full">
      {#snippet infoTip(text: string)}
        <Tooltip.Provider>
          <Tooltip.Root>
            <Tooltip.Trigger
              type="button"
              class="inline-flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-muted-foreground/70 hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              aria-label={text}
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                width="13"
                height="13"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
                ><circle cx="12" cy="12" r="10" /><path d="M12 16v-4" /><path
                  d="M12 8h.01"
                /></svg
              >
            </Tooltip.Trigger>
            <Tooltip.Content class="max-w-xs text-xs leading-relaxed"
              >{text}</Tooltip.Content
            >
          </Tooltip.Root>
        </Tooltip.Provider>
      {/snippet}

      {#if activeTab === "general"}
        <!-- General Settings -->
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {$t("eventTypeEditor.general.section")}
          </h2>
          <div class="rounded-lg border bg-card p-4 sm:p-6">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div class="space-y-1.5 min-w-0">
                <Label for="et-name">{$t("eventTypeEditor.general.name")}</Label
                >
                <Input id="et-name" bind:value={form.name} />
              </div>
              <div class="space-y-1.5 min-w-0">
                <div class="flex items-center gap-1.5">
                  <Label for="et-display-order"
                    >{$t("eventTypeEditor.general.directoryOrder")}</Label
                  >
                  {@render infoTip(
                    $t("eventTypeEditor.general.directoryOrderHint"),
                  )}
                </div>
                <Input
                  id="et-display-order"
                  type="number"
                  step="1"
                  bind:value={form.display_order}
                />
              </div>
              <div class="space-y-1.5 sm:col-span-2 min-w-0">
                <div class="flex items-center gap-1.5">
                  <Label for="et-slug"
                    >{$t("eventTypeEditor.general.bookingLink")}</Label
                  >
                  {@render infoTip($t("eventTypeEditor.general.slugHint"))}
                </div>
                <div class="flex items-center gap-1.5">
                  <span class="text-sm text-muted-foreground whitespace-nowrap"
                    >{$t("eventTypeEditor.general.bookingLinkPrefix")}</span
                  >
                  <Input id="et-slug" class="min-w-0" bind:value={form.slug} />
                </div>
              </div>
              <div class="space-y-1.5 min-w-0">
                <div class="flex items-center gap-1.5">
                  <Label for="et-dur"
                    >{$t("eventTypeEditor.general.duration")}</Label
                  >
                  {@render infoTip($t("eventTypeEditor.general.durationHint"))}
                </div>
                <Input
                  id="et-dur"
                  type="number"
                  min="5"
                  step="5"
                  bind:value={form.duration_minutes}
                />
              </div>
              <div class="space-y-1.5 min-w-0">
                <div class="flex items-center gap-1.5">
                  <Label for="et-slot"
                    >{$t("eventTypeEditor.general.slotInterval")}</Label
                  >
                  {@render infoTip(
                    $t("eventTypeEditor.general.slotIntervalHint"),
                  )}
                </div>
                <Select.Root
                  type="single"
                  value={String(form.slot_interval_minutes)}
                  onValueChange={(v) => {
                    if (v) form.slot_interval_minutes = Number(v);
                  }}
                >
                  <Select.Trigger
                    id="et-slot"
                    class="w-full min-w-0 overflow-hidden"
                  >
                    <span class="truncate"
                      >{form.slot_interval_minutes === form.duration_minutes
                        ? `Use event length (${form.duration_minutes} min)`
                        : `${form.slot_interval_minutes} minutes`}</span
                    >
                  </Select.Trigger>
                  <Select.Content>
                    {#each INTERVAL_OPTIONS as mins}
                      <Select.Item value={String(mins)} label="{mins} minutes"
                        >{mins} minutes{mins === form.duration_minutes
                          ? " (default)"
                          : ""}</Select.Item
                      >
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
              <div class="sm:col-span-2 space-y-1.5 min-w-0">
                <Label for="et-desc"
                  >{$t("eventTypeEditor.general.description")}</Label
                >
                <Textarea
                  id="et-desc"
                  bind:value={form.description}
                  placeholder={$t(
                    "eventTypeEditor.general.descriptionPlaceholder",
                  )}
                  rows={3}
                  class="resize-y"
                />
              </div>
              <div
                class="flex items-center justify-between gap-3 rounded-md border px-3 py-2.5 min-w-0"
              >
                <Label for="is-active" class="cursor-pointer font-normal"
                  >{$t("eventTypeEditor.general.active")}</Label
                >
                <Switch id="is-active" bind:checked={form.is_active} />
              </div>
              <div
                class="flex items-center justify-between gap-3 rounded-md border px-3 py-2.5 min-w-0"
              >
                <Label for="is-public" class="cursor-pointer font-normal"
                  >{$t("eventTypeEditor.general.public")}</Label
                >
                <Switch id="is-public" bind:checked={form.is_public} />
              </div>
            </div>

            <div class="mt-4">
              <div
                class="flex items-center justify-between gap-3 rounded-md border px-3 py-2.5"
              >
                <div class="flex items-center gap-1.5 min-w-0">
                  <Label for="show-taken" class="cursor-pointer font-normal"
                    >{$t("eventTypeEditor.general.showTaken")}</Label
                  >
                  {@render infoTip(
                    $t("eventTypeEditor.general.showTakenHint") +
                      (form.show_taken_slots
                        ? " " + $t("eventTypeEditor.general.showTakenWarning")
                        : ""),
                  )}
                </div>
                <Switch id="show-taken" bind:checked={form.show_taken_slots} />
              </div>
            </div>

            {#if isOnlineMeeting(form.location_type)}
              <div
                class="mt-4 flex items-center justify-between rounded-md border px-3 py-2.5"
              >
                <Label for="allow-phone" class="cursor-pointer font-normal"
                  >{$t("eventTypeEditor.general.allowPhoneCall")}</Label
                >
                <Switch id="allow-phone" bind:checked={allowPhoneCall} />
              </div>
            {/if}
            <!-- Location -->
            <div class="mt-6 border-t pt-5">
              <p
                class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
              >
                {$t("eventTypeEditor.location.section")}
              </p>
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <div class="space-y-1.5 min-w-0">
                  <Label for="et-loc"
                    >{$t("eventTypeEditor.location.type")}</Label
                  >
                  <Select.Root type="single" bind:value={form.location_type}>
                    <Select.Trigger
                      id="et-loc"
                      class="w-full min-w-0 overflow-hidden"
                    >
                      <span class="truncate"
                        >{LOCATION_TYPES.find(
                          (lt) => lt.value === form.location_type,
                        )?.label ?? $t("common.select")}</span
                      >
                    </Select.Trigger>
                    <Select.Content>
                      {#each LOCATION_TYPES as lt}
                        <Select.Item value={lt.value} label={lt.label}
                          >{lt.label}</Select.Item
                        >
                      {/each}
                    </Select.Content>
                  </Select.Root>
                </div>
                <div class="space-y-1.5 min-w-0">
                  {#if isOnlineMeeting(form.location_type)}
                    {@const platform =
                      form.location_type === "teams"
                        ? $t("eventTypeEditor.location.types.teams")
                        : $t("eventTypeEditor.location.types.googleMeet")}
                    {@const meetHint = editingForOwner
                      ? $t("eventTypeEditor.location.hint.meet.ownerEditing", {
                          platform,
                          owner: ownerLabel,
                        })
                      : meetAutoGen
                        ? $t("eventTypeEditor.location.hint.meet.autoGen", {
                            platform,
                          }) +
                          (form.location_type === "teams"
                            ? " " +
                              $t(
                                "eventTypeEditor.location.hint.meet.autoGenTeamsFallback",
                              )
                            : "")
                        : (calStatus?.connected
                            ? $t(
                                calStatus.provider === "google"
                                  ? "eventTypeEditor.location.hint.meet.manualGoogle"
                                  : "eventTypeEditor.location.hint.meet.manualMicrosoft",
                                { platform },
                              )
                            : $t(
                                "eventTypeEditor.location.hint.meet.noCalendar",
                                { platform },
                              )) +
                          " " +
                          $t(
                            "eventTypeEditor.location.hint.meet.pasteFallback",
                          )}
                    <div class="flex items-center gap-1.5">
                      <Label for="et-loc-val"
                        >{form.location_type === "teams"
                          ? $t("eventTypeEditor.location.labels.teamsLink")
                          : $t(
                              "eventTypeEditor.location.labels.meetLink",
                            )}</Label
                      >
                      {@render infoTip(meetHint)}
                    </div>
                    <Input
                      id="et-loc-val"
                      bind:value={form.location_value}
                      placeholder={meetAutoGen
                        ? $t(
                            "eventTypeEditor.location.placeholder.optionalFallback",
                          )
                        : $t("eventTypeEditor.location.placeholder.pasteMeet", {
                            platform,
                          })}
                    />
                  {:else if form.location_type === "zoom"}
                    {@const zoomHint = editingForOwner
                      ? $t("eventTypeEditor.location.hint.zoom.ownerEditing", {
                          owner: ownerLabel,
                        })
                      : zoomAutoGen
                        ? $t("eventTypeEditor.location.hint.zoom.autoGen")
                        : (zoomStatus?.configured
                            ? $t(
                                "eventTypeEditor.location.hint.zoom.connectCalendar",
                              )
                            : $t(
                                "eventTypeEditor.location.hint.zoom.notConfigured",
                              )) +
                          " " +
                          $t(
                            "eventTypeEditor.location.hint.zoom.pasteFallback",
                          )}
                    <div class="flex items-center gap-1.5">
                      <Label for="et-loc-val"
                        >{$t("eventTypeEditor.location.labels.zoomLink")}</Label
                      >
                      {@render infoTip(zoomHint)}
                    </div>
                    <Input
                      id="et-loc-val"
                      bind:value={form.location_value}
                      placeholder={zoomAutoGen
                        ? $t(
                            "eventTypeEditor.location.placeholder.optionalFallback",
                          )
                        : $t("eventTypeEditor.location.placeholder.pasteZoom")}
                    />
                  {:else if form.location_type === "livekit"}
                    <div class="flex items-center gap-1.5">
                      <Label
                        >{$t(
                          "eventTypeEditor.location.labels.videoRoom",
                        )}</Label
                      >
                      {@render infoTip(
                        $t("eventTypeEditor.location.hint.livekit"),
                      )}
                    </div>
                    <p
                      class="rounded-md border bg-muted/30 px-3 py-2 text-xs text-muted-foreground"
                    >
                      Auto-generated per booking
                    </p>
                  {:else}
                    <Label for="et-loc-val"
                      >{LOCATION_NEEDS_VALUE[form.location_type] ??
                        $t("eventTypeEditor.location.labels.details")}</Label
                    >
                    <Input
                      id="et-loc-val"
                      bind:value={form.location_value}
                      placeholder={LOCATION_PLACEHOLDER[form.location_type] ??
                        $t("eventTypeEditor.location.placeholder.optional")}
                    />
                  {/if}
                </div>
              </div>
            </div>

            <!-- Calendar invites -->
            <div class="mt-6 border-t pt-5">
              <p
                class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground"
              >
                {$t("eventTypeEditor.invite.section")}
              </p>
              <div class="grid grid-cols-2 gap-4">
                <div class="space-y-1.5">
                  <Label for="et-invite"
                    >{$t("eventTypeEditor.invite.sentBy")}</Label
                  >
                  {#key inviteSelectReset}
                    <Select.Root
                      type="single"
                      value={form.invite_delivery}
                      onValueChange={requestInviteDelivery}
                    >
                      <Select.Trigger id="et-invite" class="w-full">
                        {INVITE_DELIVERY.find(
                          (o) => o.value === form.invite_delivery,
                        )?.label ?? $t("common.select")}
                      </Select.Trigger>
                      <Select.Content>
                        {#each INVITE_DELIVERY as o}
                          <Select.Item value={o.value} label={o.label}
                            >{o.label}</Select.Item
                          >
                        {/each}
                      </Select.Content>
                    </Select.Root>
                  {/key}
                </div>
                <p class="self-end text-xs text-muted-foreground">
                  {#if form.invite_delivery === "calnode"}
                    <LinkedText
                      text={$t("eventTypeEditor.invite.hint.calnode")}
                      links={{
                        email: {
                          href: `${base}/settings/email`,
                          text: $t("eventTypeEditor.invite.settingsEmailLink"),
                          class: "underline",
                        },
                      }}
                    />
                  {:else if form.invite_delivery === "none"}
                    {$t("eventTypeEditor.invite.hint.none")}
                  {:else}
                    {$t("eventTypeEditor.invite.hint.calendar")}
                  {/if}
                </p>
              </div>
              <p class="mt-2 text-xs text-muted-foreground">
                {$t("eventTypeEditor.invite.appliesNewOnly")}
              </p>
              {#if form.invite_delivery === "calnode" && et?.invite_sender_ready === false}
                <p
                  class="mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800"
                >
                  <LinkedText
                    text={$t("eventTypeEditor.invite.emailNotSetup")}
                    links={{
                      email: {
                        href: `${base}/settings/email`,
                        text: $t("eventTypeEditor.invite.settingsEmailLink"),
                        class: "underline",
                      },
                    }}
                  />
                </p>
              {/if}
              {#if form.invite_delivery === "none" && isOnlineLocation(form.location_type)}
                <p
                  class="mt-3 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800"
                >
                  {$t("eventTypeEditor.invite.onlineNoneWarning")}
                </p>
              {/if}
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "limits"}
        <!-- Limits & Buffers -->
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            Limits & buffers
          </h2>
          <div class="rounded-lg border bg-card p-4 sm:p-6 space-y-5">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div class="space-y-1.5 min-w-0">
                <Label for="et-buf-before"
                  >{$t("eventTypeEditor.scheduling.bufferBefore")}</Label
                >
                <Select.Root
                  type="single"
                  value={String(form.buffer_before_minutes)}
                  onValueChange={(v) => {
                    if (v !== undefined) form.buffer_before_minutes = Number(v);
                  }}
                >
                  <Select.Trigger id="et-buf-before" class="w-full min-w-0"
                    >{optionLabel(
                      BUFFER_OPTIONS,
                      form.buffer_before_minutes,
                    )}</Select.Trigger
                  >
                  <Select.Content>
                    {#each BUFFER_OPTIONS as opt}
                      <Select.Item value={String(opt.value)} label={opt.label}
                        >{opt.label}</Select.Item
                      >
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
              <div class="space-y-1.5 min-w-0">
                <Label for="et-buf-after"
                  >{$t("eventTypeEditor.scheduling.bufferAfter")}</Label
                >
                <Select.Root
                  type="single"
                  value={String(form.buffer_after_minutes)}
                  onValueChange={(v) => {
                    if (v !== undefined) form.buffer_after_minutes = Number(v);
                  }}
                >
                  <Select.Trigger id="et-buf-after" class="w-full min-w-0"
                    >{optionLabel(
                      BUFFER_OPTIONS,
                      form.buffer_after_minutes,
                    )}</Select.Trigger
                  >
                  <Select.Content>
                    {#each BUFFER_OPTIONS as opt}
                      <Select.Item value={String(opt.value)} label={opt.label}
                        >{opt.label}</Select.Item
                      >
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
              <div class="space-y-1.5 sm:col-span-2 min-w-0">
                <Label for="et-notice"
                  >{$t("eventTypeEditor.scheduling.minNotice")}</Label
                >
                <Select.Root
                  type="single"
                  value={String(form.min_notice_minutes)}
                  onValueChange={(v) => {
                    if (v !== undefined) form.min_notice_minutes = Number(v);
                  }}
                >
                  <Select.Trigger id="et-notice" class="w-full min-w-0"
                    >{optionLabel(
                      NOTICE_OPTIONS,
                      form.min_notice_minutes,
                    )}</Select.Trigger
                  >
                  <Select.Content>
                    {#each NOTICE_OPTIONS as opt}
                      <Select.Item value={String(opt.value)} label={opt.label}
                        >{opt.label}</Select.Item
                      >
                    {/each}
                  </Select.Content>
                </Select.Root>
              </div>
            </div>

            <!-- Limit booking frequency (dynamic per-row dropdown: Per Day / Per Week) -->
            <div class="rounded-lg border p-4 space-y-3">
              <div class="flex items-center justify-between gap-3">
                <div class="flex items-center gap-1.5 min-w-0">
                  <p class="text-sm font-medium">Limit booking frequency</p>
                  {@render infoTip(
                    "Limit how many times this event can be booked per day and/or per week.",
                  )}
                </div>
                <Switch
                  checked={freqEnabled}
                  onCheckedChange={toggleFreqEnabled}
                />
              </div>
              {#if freqEnabled}
                <div class="space-y-2.5 pt-1">
                  {#each freqOrder as unit, idx (unit)}
                    {@const availableUnits = (["day", "week"] as const).filter(
                      (u) => u === unit || !freqOrder.includes(u),
                    )}
                    <div class="flex items-center gap-2">
                      {#if unit === "day"}
                        <Input
                          type="number"
                          min="1"
                          class="w-20 shrink-0"
                          bind:value={form.max_per_day}
                          aria-label="Max bookings per day"
                        />
                      {:else}
                        <Input
                          type="number"
                          min="1"
                          class="w-20 shrink-0"
                          bind:value={form.max_per_week}
                          aria-label="Max bookings per week"
                        />
                      {/if}
                      <Select.Root
                        type="single"
                        value={unit}
                        onValueChange={(v) => {
                          if (v) setFreqRowUnit(idx, v as "day" | "week");
                        }}
                      >
                        <Select.Trigger
                          class="w-40 min-w-0"
                          aria-label="Frequency period"
                        >
                          {unit === "day" ? "Per Day" : "Per Week"}
                        </Select.Trigger>
                        <Select.Content>
                          {#each availableUnits as opt}
                            <Select.Item
                              value={opt}
                              label={opt === "day" ? "Per Day" : "Per Week"}
                            >
                              {opt === "day" ? "Per Day" : "Per Week"}
                            </Select.Item>
                          {/each}
                        </Select.Content>
                      </Select.Root>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        class="shrink-0 text-destructive hover:text-destructive"
                        onclick={() => removeFreqRow(unit)}
                        aria-label="Remove limit"
                      >
                        <svg
                          xmlns="http://www.w3.org/2000/svg"
                          width="15"
                          height="15"
                          viewBox="0 0 24 24"
                          fill="none"
                          stroke="currentColor"
                          stroke-width="2"
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          ><polyline points="3 6 5 6 21 6" /><path
                            d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"
                          /><path d="M10 11v6" /><path d="M14 11v6" /></svg
                        >
                      </Button>
                    </div>
                  {/each}
                  {#if freqOrder.length < 2}
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      class="h-8 px-2 text-xs"
                      onclick={addFreqRow}
                    >
                      + Add limit
                    </Button>
                  {/if}
                </div>
              {/if}
            </div>

            <!-- Limit future bookings -->
            <div class="rounded-lg border p-4 space-y-3">
              <div class="flex items-center justify-between gap-3">
                <div class="flex items-center gap-1.5 min-w-0">
                  <p class="text-sm font-medium">Limit future bookings</p>
                  {@render infoTip(
                    "Limit how far in the future (calendar days) this event can be booked.",
                  )}
                </div>
                <Switch
                  checked={futureLimitEnabled}
                  onCheckedChange={toggleFutureLimit}
                />
              </div>
              {#if futureLimitEnabled}
                <div class="flex items-center gap-2 pt-1">
                  <Input
                    id="et-future"
                    type="number"
                    min="1"
                    class="w-24"
                    bind:value={form.max_future_days}
                  />
                  <span class="text-sm text-muted-foreground"
                    >calendar days into the future</span
                  >
                </div>
              {/if}
            </div>

            <!-- Limit upcoming bookings per booker -->
            <div class="rounded-lg border p-4 space-y-3">
              <div class="flex items-center justify-between gap-3">
                <div class="flex items-center gap-1.5 min-w-0">
                  <p class="text-sm font-medium">
                    Limit upcoming bookings per booker
                  </p>
                  {@render infoTip(
                    "Limit the number of active bookings a booker can hold at once.",
                  )}
                </div>
                <Switch
                  checked={maxActiveEnabled}
                  onCheckedChange={toggleMaxActive}
                />
              </div>
              {#if maxActiveEnabled}
                <div class="flex items-center gap-2 pt-1">
                  <Input
                    id="et-max-active"
                    type="number"
                    min="1"
                    class="w-24"
                    bind:value={form.max_active_bookings}
                  />
                  <span class="text-sm text-muted-foreground"
                    >active bookings</span
                  >
                </div>
              {/if}
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "hosts"}
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {$t("eventTypeEditor.tabs.hosts")}
          </h2>
          <div class="rounded-lg border bg-card p-6">
            <div>
              <!-- Q1 — Hosting -->
              <div class="space-y-1.5">
                <Label>Hosting</Label>
                <div class="inline-flex rounded-lg border bg-muted/40 p-0.5">
                  <button
                    type="button"
                    onclick={() => setScope("me")}
                    class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {hostScope ===
                    'me'
                      ? 'bg-background shadow-sm'
                      : 'text-muted-foreground hover:text-foreground'}"
                  >
                    {editingForOwner
                      ? $t("eventTypeEditor.hosts.justOwner", {
                          owner: ownerLabel,
                        })
                      : "Just me"}
                  </button>
                  <button
                    type="button"
                    onclick={() => setScope("people")}
                    class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {hostScope ===
                    'people'
                      ? 'bg-background shadow-sm'
                      : 'text-muted-foreground hover:text-foreground'}"
                  >
                    Multiple hosts
                  </button>
                </div>
              </div>

              {#if hostScope === "people"}
                <!-- Q2 — Assignment -->
                <div class="mt-4 space-y-1.5">
                  <Label>Assignment</Label>
                  <div class="inline-flex rounded-lg border bg-muted/40 p-0.5">
                    <button
                      type="button"
                      onclick={() => (staffing = "rotate")}
                      class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {staffing ===
                      'rotate'
                        ? 'bg-background shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'}"
                    >
                      Round robin
                    </button>
                    <button
                      type="button"
                      onclick={() => (staffing = "together")}
                      class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {staffing ===
                      'together'
                        ? 'bg-background shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'}"
                    >
                      Collective (all attend)
                    </button>
                  </div>
                </div>

                {#if staffing === "rotate"}
                  <div class="mt-4 space-y-3">
                    <div class="space-y-1.5">
                      <Label for="rr-strategy">Round-robin method</Label>
                      <Select.Root
                        type="single"
                        value={rrStrategy}
                        onValueChange={(v) => {
                          if (v) rrStrategy = v as Strategy;
                        }}
                      >
                        <Select.Trigger id="rr-strategy" class="w-full">
                          {RR_STRATEGIES.find((s) => s.value === rrStrategy)
                            ?.label ?? $t("common.select")}
                        </Select.Trigger>
                        <Select.Content>
                          {#each RR_STRATEGIES as s}
                            <Select.Item value={s.value} label={s.label}
                              >{s.label}</Select.Item
                            >
                          {/each}
                        </Select.Content>
                      </Select.Root>
                    </div>

                    <p class="text-sm font-medium">Team members</p>
                    {#if rotationHosts.length > 0}
                      <div class="space-y-2">
                        {#each rotationHosts as h, i (h.user_id)}
                          <div
                            class="flex items-center justify-between gap-2 rounded-md border px-3 py-2"
                          >
                            <div class="flex min-w-0 items-center gap-2">
                              {#if rrStrategy === "priority"}
                                <span
                                  class="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground tabular-nums"
                                  >{i + 1}</span
                                >
                              {/if}
                              <div class="min-w-0">
                                <div class="truncate text-sm font-medium">
                                  {h.name}
                                </div>
                                <div
                                  class="truncate text-xs text-muted-foreground"
                                >
                                  {h.email}
                                </div>
                              </div>
                            </div>
                            <div class="flex shrink-0 items-center gap-1">
                              {#if rrStrategy === "priority"}
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="icon"
                                  disabled={i === 0}
                                  aria-label={$t(
                                    "eventTypeEditor.hosts.moveUp",
                                  )}
                                  onclick={() => moveRotation(i, -1)}
                                >
                                  <svg
                                    xmlns="http://www.w3.org/2000/svg"
                                    width="16"
                                    height="16"
                                    viewBox="0 0 24 24"
                                    fill="none"
                                    stroke="currentColor"
                                    stroke-width="2"
                                    stroke-linecap="round"
                                    stroke-linejoin="round"
                                    ><polyline points="18 15 12 9 6 15" /></svg
                                  >
                                </Button>
                                <Button
                                  type="button"
                                  variant="ghost"
                                  size="icon"
                                  disabled={i === rotationHosts.length - 1}
                                  aria-label={$t(
                                    "eventTypeEditor.hosts.moveDown",
                                  )}
                                  onclick={() => moveRotation(i, 1)}
                                >
                                  <svg
                                    xmlns="http://www.w3.org/2000/svg"
                                    width="16"
                                    height="16"
                                    viewBox="0 0 24 24"
                                    fill="none"
                                    stroke="currentColor"
                                    stroke-width="2"
                                    stroke-linecap="round"
                                    stroke-linejoin="round"
                                    ><polyline points="6 9 12 15 18 9" /></svg
                                  >
                                </Button>
                              {/if}
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                onclick={() =>
                                  removePerson("rotation", h.user_id)}
                                >{$t("common.remove")}</Button
                              >
                            </div>
                          </div>
                        {/each}
                      </div>
                    {:else}
                      <p class="text-xs text-muted-foreground">
                        {$t("eventTypeEditor.hosts.rotationEmpty")}
                      </p>
                    {/if}
                    {@render hostPickers("rotation", "rr")}
                  </div>
                {:else}
                  <div class="mt-4 space-y-3">
                    <p class="text-sm font-medium">
                      {$t("eventTypeEditor.hosts.whoAttends")}
                    </p>
                    {#if togetherHosts.length > 0}
                      <div class="space-y-2">
                        {#each togetherHosts as h (h.user_id)}
                          <div
                            class="flex items-center justify-between gap-2 rounded-md border px-3 py-2"
                          >
                            <div class="min-w-0">
                              <div class="truncate text-sm font-medium">
                                {h.name}
                              </div>
                              <div
                                class="truncate text-xs text-muted-foreground"
                              >
                                {h.email}
                              </div>
                            </div>
                            <div class="flex shrink-0 items-center gap-2">
                              <div class="inline-flex rounded-md border p-0.5">
                                <button
                                  type="button"
                                  onclick={() => setOptional(h.user_id, false)}
                                  class="rounded px-2 py-0.5 text-xs font-medium transition-colors {!h.optional
                                    ? 'bg-secondary text-secondary-foreground'
                                    : 'text-muted-foreground hover:text-foreground'}"
                                >
                                  {$t("eventTypeEditor.hosts.required")}
                                </button>
                                <button
                                  type="button"
                                  onclick={() => setOptional(h.user_id, true)}
                                  class="rounded px-2 py-0.5 text-xs font-medium transition-colors {h.optional
                                    ? 'bg-secondary text-secondary-foreground'
                                    : 'text-muted-foreground hover:text-foreground'}"
                                >
                                  {$t("eventTypeEditor.hosts.optional")}
                                </button>
                              </div>
                              <Button
                                type="button"
                                variant="ghost"
                                size="sm"
                                onclick={() =>
                                  removePerson("together", h.user_id)}
                                >{$t("common.remove")}</Button
                              >
                            </div>
                          </div>
                        {/each}
                      </div>
                    {:else}
                      <p class="text-xs text-muted-foreground">
                        {$t("eventTypeEditor.hosts.togetherEmpty")}
                      </p>
                    {/if}
                    <p class="text-xs text-muted-foreground">
                      {$t("eventTypeEditor.hosts.requiredExplain")}
                      {$t("eventTypeEditor.hosts.optionalExplain")}
                    </p>
                    {@render hostPickers("together", "grp")}
                  </div>
                {/if}
              {/if}
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "notifications"}
        <!-- Notifications -->
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {$t("eventTypeEditor.tabs.notifications")}
          </h2>
          <div class="rounded-lg border bg-card p-6 space-y-6">
            <!-- Reminders -->
            <div>
              <div class="mb-3 flex items-center gap-1.5">
                <p class="text-sm font-medium">
                  {$t("eventTypeEditor.notifications.reminders")}
                </p>
                {@render infoTip(
                  $t("eventTypeEditor.notifications.remindersHint"),
                )}
              </div>
              <div class="space-y-2">
                {#each reminders as hb, i}
                  <div class="flex items-center gap-2">
                    <Select.Root
                      type="single"
                      value={String(hb)}
                      onValueChange={(v) => {
                        if (!v) return;
                        const n = Number(v);
                        reminders = reminders.map((r, idx) =>
                          idx === i ? n : r,
                        );
                      }}
                    >
                      <Select.Trigger class="w-full">
                        {REMINDER_OPTIONS.find((o) => o.value === hb)?.label ??
                          $t("common.select")}
                      </Select.Trigger>
                      <Select.Content>
                        {#each REMINDER_OPTIONS as opt}
                          <Select.Item
                            value={String(opt.value)}
                            label={opt.label}>{opt.label}</Select.Item
                          >
                        {/each}
                      </Select.Content>
                    </Select.Root>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onclick={() => {
                        reminders = reminders.filter((_, idx) => idx !== i);
                      }}
                    >
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        width="16"
                        height="16"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        ><line x1="18" y1="6" x2="6" y2="18" /><line
                          x1="6"
                          y1="6"
                          x2="18"
                          y2="18"
                        /></svg
                      >
                    </Button>
                  </div>
                {/each}
                {#if reminders.length < 5}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onclick={() => {
                      const used = new Set(reminders);
                      const next = REMINDER_OPTIONS.find(
                        (o) => !used.has(o.value),
                      );
                      if (next) reminders = [...reminders, next.value];
                    }}
                  >
                    {$t("eventTypeEditor.notifications.addReminder")}
                  </Button>
                {/if}
              </div>
            </div>

            <!-- Custom messages -->
            <div class="border-t pt-5">
              <div class="mb-3 flex items-center gap-1.5">
                <p class="text-sm font-medium">
                  {$t("eventTypeEditor.notifications.customMessages")}
                </p>
                {@render infoTip(
                  $t("eventTypeEditor.notifications.customMessagesHint"),
                )}
              </div>
              <div class="space-y-2">
                {#each [{ key: "confirmation" as const, label: $t("eventTypeEditor.notifications.msg.confirmation") }, { key: "cancellation" as const, label: $t("eventTypeEditor.notifications.msg.cancellation") }, { key: "reschedule" as const, label: $t("eventTypeEditor.notifications.msg.reschedule") }, { key: "reminder" as const, label: $t("eventTypeEditor.notifications.msg.reminder") }] as item}
                  <div class="rounded-md border">
                    <button
                      type="button"
                      class="flex w-full items-center justify-between px-4 py-3 text-sm font-medium hover:bg-muted/30 transition-colors"
                      onclick={() => {
                        msgOpen[item.key] = !msgOpen[item.key];
                      }}
                    >
                      <span>{item.label}</span>
                      <svg
                        xmlns="http://www.w3.org/2000/svg"
                        width="14"
                        height="14"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        stroke-width="2"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        class="transition-transform {msgOpen[item.key]
                          ? 'rotate-180'
                          : ''}"><polyline points="6 9 12 15 18 9" /></svg
                      >
                    </button>
                    {#if msgOpen[item.key]}
                      {@const note =
                        item.key === "confirmation"
                          ? msg_confirmation
                          : item.key === "cancellation"
                            ? msg_cancellation
                            : item.key === "reschedule"
                              ? msg_reschedule
                              : msg_reminder}
                      <div class="border-t px-4 pb-4 pt-3 space-y-3">
                        <div class="space-y-1.5">
                          <Label class="text-xs text-muted-foreground"
                            >{$t(
                              "eventTypeEditor.notifications.subjectOptional",
                            )}</Label
                          >
                          {#if item.key === "confirmation"}
                            <Input
                              bind:value={subj_confirmation}
                              placeholder={$t(
                                "eventTypeEditor.notifications.subjectPlaceholder.confirmation",
                                { name: form.name },
                              )}
                            />
                          {:else if item.key === "cancellation"}
                            <Input
                              bind:value={subj_cancellation}
                              placeholder={$t(
                                "eventTypeEditor.notifications.subjectPlaceholder.cancellation",
                                { name: form.name },
                              )}
                            />
                          {:else if item.key === "reschedule"}
                            <Input
                              bind:value={subj_reschedule}
                              placeholder={$t(
                                "eventTypeEditor.notifications.subjectPlaceholder.reschedule",
                                { name: form.name },
                              )}
                            />
                          {:else if item.key === "reminder"}
                            <Input
                              bind:value={subj_reminder}
                              placeholder={$t(
                                "eventTypeEditor.notifications.subjectPlaceholder.reminder",
                                { name: form.name },
                              )}
                            />
                          {/if}
                        </div>
                        {#if item.key === "confirmation"}
                          <Textarea
                            bind:value={msg_confirmation}
                            rows={3}
                            placeholder={$t(
                              "eventTypeEditor.notifications.notePlaceholder",
                            )}
                          />
                        {:else if item.key === "cancellation"}
                          <Textarea
                            bind:value={msg_cancellation}
                            rows={3}
                            placeholder={$t(
                              "eventTypeEditor.notifications.notePlaceholder",
                            )}
                          />
                        {:else if item.key === "reschedule"}
                          <Textarea
                            bind:value={msg_reschedule}
                            rows={3}
                            placeholder={$t(
                              "eventTypeEditor.notifications.notePlaceholder",
                            )}
                          />
                        {:else if item.key === "reminder"}
                          <Textarea
                            bind:value={msg_reminder}
                            rows={3}
                            placeholder={$t(
                              "eventTypeEditor.notifications.notePlaceholder",
                            )}
                          />
                        {/if}

                        <!-- Preview toggle -->
                        <button
                          type="button"
                          class="text-xs text-muted-foreground hover:text-foreground underline-offset-2 hover:underline"
                          onclick={() => {
                            previewOpen[item.key] = !previewOpen[item.key];
                          }}
                          >{previewOpen[item.key]
                            ? $t("eventTypeEditor.notifications.hidePreview")
                            : $t(
                                "eventTypeEditor.notifications.showPreview",
                              )}</button
                        >

                        {#if previewOpen[item.key]}
                          <pre
                            class="rounded-md border bg-muted/30 px-4 py-3 text-xs leading-relaxed whitespace-pre-wrap font-mono text-muted-foreground overflow-auto max-h-64">{buildPreview(
                              item.key,
                              note,
                            )}</pre>
                        {/if}

                        <!-- Send test button -->
                        <div class="flex items-center gap-3">
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={testSending[item.key]}
                            onclick={() => sendTestEmail(item.key)}
                          >
                            {testSending[item.key]
                              ? $t("eventTypeEditor.notifications.sendingTest")
                              : $t("eventTypeEditor.notifications.sendTest")}
                          </Button>
                          {#if testSent[item.key]}
                            <span class="text-xs text-green-600"
                              >{$t(
                                "eventTypeEditor.notifications.testSent",
                              )}</span
                            >
                          {/if}
                          {#if testError[item.key]}
                            <span class="text-xs text-destructive">
                              {#if testError[item.key] === "Email is not configured on this server — add SMTP settings to enable sending"}
                                <LinkedText
                                  text={$t(
                                    "eventTypeEditor.notifications.smtpNotConfigured",
                                  )}
                                  links={{
                                    smtp: {
                                      href: `${base}/settings/email`,
                                      text: $t(
                                        "eventTypeEditor.notifications.smtpNotConfiguredLink",
                                      ),
                                      class: "underline",
                                    },
                                  }}
                                />
                              {:else}
                                {testError[item.key]}
                              {/if}
                            </span>
                          {/if}
                        </div>
                      </div>
                    {/if}
                  </div>
                {/each}
              </div>
            </div>

            <!-- Assistant greeting -->
            <div class="border-t pt-5">
              <div class="mb-3 flex items-center gap-1.5">
                <p class="text-sm font-medium">
                  {$t("eventTypeEditor.notifications.assistantGreeting")}
                </p>
                {@render infoTip(
                  $t("eventTypeEditor.notifications.assistantGreetingHint"),
                )}
              </div>
              <Textarea
                bind:value={msg_greeting}
                rows={2}
                placeholder={$t(
                  "eventTypeEditor.notifications.greetingPlaceholder",
                )}
              />
            </div>
          </div>
        </div>
      {/if}

      {#if activeTab === "hosts" && $currentUser?.is_admin && et?.owned !== false}
        <div class="mt-6 space-y-3 border-t pt-5">
          <Label for="transfer-owner"
            >{$t("eventTypeEditor.transfer.title")}</Label
          >
          <p class="text-xs text-muted-foreground">
            Reassign ownership to a saved required host (only available with no
            upcoming bookings).
          </p>
          <select
            id="transfer-owner"
            bind:value={transferOwner}
            class="h-9 w-full rounded-md border bg-background px-3 text-sm"
          >
            <option value="">{$t("eventTypeEditor.transfer.select")}</option>
            {#each transferHosts as host}<option value={host.user_id}
                >{host.name || host.email}</option
              >{/each}
          </select>
          <Button
            variant="outline"
            disabled={!transferOwner || transferring}
            onclick={transferEvent}
            >{transferring
              ? $t("eventTypeEditor.transfer.inProgress")
              : $t("eventTypeEditor.transfer.button")}</Button
          >
        </div>
      {/if}

      {#if activeTab === "payments"}
        <div class="mb-8">
          <h2
            class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {$t("settings.nav.payments")}
          </h2>
          <div class="rounded-lg border bg-card p-6 space-y-4">
            <div class="flex items-center justify-between gap-3">
              <div>
                <p class="text-sm font-medium">Paid booking</p>
                <p class="text-xs text-muted-foreground">
                  Require payment via Stripe when someone books this event.
                </p>
              </div>
              <Switch
                checked={paidEnabled}
                onCheckedChange={togglePaidEnabled}
              />
            </div>
            {#if paidEnabled}
              <div class="grid grid-cols-1 sm:grid-cols-2 gap-4 border-t pt-4">
                <div class="space-y-1.5">
                  <Label for="et-price"
                    >{$t("eventTypeEditor.price.amount")}</Label
                  >
                  <Input
                    id="et-price"
                    type="number"
                    min="0"
                    step="0.01"
                    bind:value={priceMajor}
                    placeholder="0.00"
                  />
                </div>
                <div class="space-y-1.5">
                  <Label for="et-currency"
                    >{$t("eventTypeEditor.price.currency")}</Label
                  >
                  <Input
                    id="et-currency"
                    type="text"
                    maxlength={3}
                    bind:value={form.currency}
                    placeholder={$t(
                      "eventTypeEditor.price.currencyPlaceholder",
                    )}
                  />
                </div>
              </div>
            {/if}
            <p class="text-xs text-muted-foreground">
              <LinkedText
                text={$t("eventTypeEditor.price.hint")}
                links={{
                  stripe: {
                    href: "/admin/settings/payments",
                    text: $t("eventTypeEditor.price.stripeLink"),
                    class: "underline",
                  },
                }}
              />
            </p>
          </div>
        </div>
      {/if}

      {#if activeTab === "questions"}
        <QuestionsPanel
          slug={slug ?? ""}
          allowPhoneCall={allowPhoneCall && isOnlineMeeting(form.location_type)}
          onQuestionsChange={(items) => (previewQuestions = items)}
        />
      {/if}

      {#if activeTab === "embed"}
        <EmbedPanel slug={et?.slug ?? ""} />
      {/if}
    </div>

    <!-- Live Interactive Booking Preview (Right, hidden on mobile/tablet < 1280px) -->
    <aside class="hidden w-80 shrink-0 xl:block">
      <BookingPreviewCard
        name={form.name}
        description={form.description}
        duration={form.duration_minutes}
        locationType={form.location_type}
        locationValue={form.location_value}
        hostName={$currentUser?.name ?? "Host"}
        hostAvatar={$currentUser?.avatar_url ?? ""}
        mode={activeTab === "questions" ? "form" : "calendar"}
        allowPhoneCall={allowPhoneCall && isOnlineMeeting(form.location_type)}
        questions={previewQuestions}
      />
    </aside>
  </div>

  <ConfirmDialog
    bind:open={confirmNoneOpen}
    title={$t("eventTypeEditor.confirmNone.title")}
    description={$t("eventTypeEditor.confirmNone.description")}
    confirmText={$t("eventTypeEditor.confirmNone.confirm")}
    cancelText={$t("common.cancel")}
    onConfirm={() => {
      form.invite_delivery = "none";
      noneAcknowledged = true;
    }}
  />
{/if}
