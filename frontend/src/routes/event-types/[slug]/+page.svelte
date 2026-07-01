<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { base } from '$app/paths';
	import { api, type EventType, type EventTypeHost, type TeamMember, type Team, type CalendarStatus, type ZoomStatus } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import * as Select from '$lib/components/ui/select';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import QuestionsPanel from '$lib/components/event-types/QuestionsPanel.svelte';
	import EmbedPanel from '$lib/components/event-types/EmbedPanel.svelte';

	// Ordered by expected usage. 'custom_video' is retired from the picker but the
	// backend still renders any legacy event types that use it.
	const LOCATION_TYPES = [
		{ value: 'zoom',         label: 'Zoom' },
		{ value: 'teams',        label: 'Microsoft Teams' },
		{ value: 'google_meet',  label: 'Google Meet' },
		{ value: 'livekit',      label: 'Calnode Video (LiveKit)' },
		{ value: 'phone',        label: 'Phone call' },
		{ value: 'link',         label: 'Video link' },
		{ value: 'in_person',    label: 'In person' },
	];

	const LOCATION_NEEDS_VALUE: Record<string, string> = {
		link: 'Meeting URL', zoom: 'Zoom link', google_meet: 'Meet link',
		teams: 'Teams link', phone: 'Phone number', in_person: 'Address', custom_video: 'Meeting URL',
	};
	// Placeholder hint per type — in-person is the only optional value.
	const LOCATION_PLACEHOLDER: Record<string, string> = {
		zoom: 'https://…zoom.us/j/… (required)', link: 'https://… (required)',
		phone: '+1 555 123 4567 (required)', in_person: 'Address (optional)',
	};

	// ── Event type ───────────────────────────────────────────────────────────────
	let et = $state<EventType | null>(null);
	const TABS = [
		{ id: 'general', label: 'General' },
		{ id: 'hosts', label: 'Hosts' },
		{ id: 'notifications', label: 'Notifications' },
		{ id: 'questions', label: 'Questions' },
		{ id: 'embed', label: 'Embed' }
	] as const;
	let activeTab = $state<(typeof TABS)[number]['id']>('general');
	let etLoading = $state(true);
	let etError = $state('');
	let etSaving = $state(false);

	// Connected calendar (the owner's) — drives the meeting-link auto-generation hint.
	let calStatus = $state<CalendarStatus | null>(null);
	// Owner's Zoom connection — drives the Zoom auto-mint hint.
	let zoomStatus = $state<ZoomStatus | null>(null);
	// Which connected provider can natively mint each online platform's link.
	const PLATFORM_PROVIDER: Record<string, string> = { google_meet: 'google', teams: 'microsoft' };
	const isOnlineMeeting = (t: string) => t === 'google_meet' || t === 'teams';

	let form = $state({
		name: '', description: '', duration_minutes: 30,
		is_active: true, is_public: true,
		location_type: 'link', location_value: '',
		buffer_before_minutes: 0, buffer_after_minutes: 0,
		min_notice_minutes: 0, max_future_days: 60,
		max_active_bookings: 1,
		price_cents: 0, currency: 'usd',
	});

	// Price is edited in major units (e.g. dollars); stored as integer cents.
	let priceMajor = $state('0');

	// True when the connected calendar will auto-generate the chosen platform's link.
	const meetAutoGen = $derived(
		isOnlineMeeting(form.location_type) &&
			!!calStatus?.connected &&
			calStatus?.provider === PLATFORM_PROVIDER[form.location_type]
	);

	// True when the owner's connected Zoom account will auto-mint a meeting per booking.
	const zoomAutoGen = $derived(form.location_type === 'zoom' && !!zoomStatus?.connected);

	// ── Routing ──────────────────────────────────────────────────────────────────
	// The editor asks two plain questions — "who can host?" and (for a team)
	// "do they rotate or all attend?" — and derives routing_mode + host roles from
	// the answers. The engine and DB are unchanged; this is purely how roles are
	// authored.
	const RR_STRATEGIES = [
		{ value: 'even',     label: 'Even — fewest upcoming bookings' },
		{ value: 'priority', label: 'Priority — top of the list first' },
		{ value: 'soonest',  label: 'Soonest availability' },
	];
	type Strategy = 'even' | 'priority' | 'soonest';
	let rrStrategy = $state<Strategy>('even');

	type Host = { user_id: string; name: string; email: string };
	type TogetherHost = Host & { optional: boolean };

	// Q1: just me, or specific people?  Q2 (people only): rotate, or all attend?
	let hostScope = $state<'me' | 'people'>('me');
	let staffing = $state<'rotate' | 'together'>('rotate');
	// Rotation pool (each "rotation"); together = required + optional join-if-free.
	let rotationHosts = $state<Host[]>([]);
	let togetherHosts = $state<TogetherHost[]>([]);
	let hostsLoaded = $state(false);
	let members = $state<TeamMember[]>([]);
	let teams = $state<Team[]>([]);

	// routing_mode is derived from the two answers — never set directly.
	const routingMode = $derived(
		hostScope === 'me' ? 'fixed' : staffing === 'rotate' ? 'round_robin' : 'collective'
	);

	async function loadHosts() {
		try {
			const res = await api.get<{ items: EventTypeHost[] }>(`/v1/event-types/${slug}/hosts`);
			const items = res.items ?? [];
			const toHost = (h: EventTypeHost): Host => ({ user_id: h.user_id, name: h.name, email: h.email });
			rotationHosts = items.filter((h) => h.role === 'rotation').map(toHost);
			togetherHosts = items
				.filter((h) => h.role === 'required' || h.role === 'optional')
				.map((h) => ({ ...toHost(h), optional: h.role === 'optional' }));
			hostsLoaded = true;
		} catch (e: any) {
			toast.error(e.message || 'Could not load hosts');
		}
	}

	async function loadMembers() {
		if (members.length > 0) return;
		try {
			members = await api.get<TeamMember[]>('/v1/users');
		} catch (e: any) {
			toast.error(e.message || 'Could not load members');
		}
	}

	async function loadTeams() {
		if (teams.length > 0) return;
		try {
			const res = await api.get<{ items: Team[] }>('/v1/teams');
			teams = res.items ?? [];
		} catch (e: any) {
			toast.error(e.message || 'Could not load teams');
		}
	}

	function setScope(s: 'me' | 'people') {
		hostScope = s;
		if (s === 'people') {
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
		new Set((staffing === 'rotate' ? rotationHosts : togetherHosts).map((h) => h.user_id))
	);
	// Any active member not already chosen — including the current user, who can
	// be a host like anyone else.
	const availableMembers = $derived(
		members.filter((m) => !m.archived && !assignedIds.has(m.id))
	);

	type Target = 'rotation' | 'together';
	function addOne(target: Target, h: Host) {
		// Dedup against this target's own list, not the union — a person can sit in
		// only one staffing list, and the other list may hold stale members from a
		// previous mode.
		const list: { user_id: string }[] = target === 'rotation' ? rotationHosts : togetherHosts;
		if (list.some((x) => x.user_id === h.user_id)) return;
		if (target === 'rotation') rotationHosts = [...rotationHosts, h];
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
				.forEach((tm) => addOne(target, { user_id: tm.id, name: tm.name, email: tm.email }));
		} catch (e: any) {
			toast.error(e.message || 'Could not load team members');
		}
	}
	function removePerson(target: Target, userId: string) {
		if (target === 'rotation') rotationHosts = rotationHosts.filter((h) => h.user_id !== userId);
		else togetherHosts = togetherHosts.filter((h) => h.user_id !== userId);
	}
	function setOptional(userId: string, optional: boolean) {
		togetherHosts = togetherHosts.map((h) => (h.user_id === userId ? { ...h, optional } : h));
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
	const REMINDER_OPTIONS = [
		{ value: 1, label: '1 hour before' },
		{ value: 2, label: '2 hours before' },
		{ value: 4, label: '4 hours before' },
		{ value: 8, label: '8 hours before' },
		{ value: 12, label: '12 hours before' },
		{ value: 24, label: '24 hours before' },
		{ value: 48, label: '2 days before' },
		{ value: 72, label: '3 days before' },
		{ value: 168, label: '1 week before' },
	];
	let reminders = $state<number[]>([]);
	let msg_confirmation = $state('');
	let msg_cancellation = $state('');
	let msg_reschedule = $state('');
	let msg_reminder = $state('');
	// Optional custom subject lines (empty = built-in default subject).
	let subj_confirmation = $state('');
	let subj_cancellation = $state('');
	let subj_reschedule = $state('');
	let subj_reminder = $state('');
	// Track which message accordions are open
	let msgOpen = $state({ confirmation: false, cancellation: false, reschedule: false, reminder: false });
	// Track preview toggle per accordion
	let previewOpen = $state({ confirmation: false, cancellation: false, reschedule: false, reminder: false });
	// Test email send state per type
	type MsgKey = 'confirmation' | 'cancellation' | 'reschedule' | 'reminder';
	let testSending = $state<Partial<Record<MsgKey, boolean>>>({});
	let testSent    = $state<Partial<Record<MsgKey, boolean>>>({});
	let testError   = $state<Partial<Record<MsgKey, string>>>({});

	const slug = $page.params.slug;

	async function loadET() {
		etError = '';
		try {
			et = await api.get<EventType>(`/v1/event-types/${slug}`);
			form = {
				name: et.name,
				description: et.description ?? '',
				duration_minutes: et.duration_minutes,
				is_active: et.is_active,
				is_public: et.is_public,
				location_type: et.location_type,
				location_value: et.location_value ?? '',
				buffer_before_minutes: et.buffer_before_minutes,
				buffer_after_minutes: et.buffer_after_minutes,
				min_notice_minutes: et.min_notice_minutes,
				max_future_days: et.max_future_days,
				max_active_bookings: et.max_active_bookings,
				price_cents: et.price_cents ?? 0,
				currency: et.currency ?? 'usd',
			};
			priceMajor = ((et.price_cents ?? 0) / 100).toFixed(2);
			reminders = et.reminders ?? [];
			if (et.routing_mode === 'round_robin') { hostScope = 'people'; staffing = 'rotate'; }
			else if (et.routing_mode === 'collective') { hostScope = 'people'; staffing = 'together'; }
			else { hostScope = 'me'; }
			rrStrategy = (['even', 'priority', 'soonest'].includes(et.rr_strategy ?? '')
				? et.rr_strategy : 'even') as Strategy;
			msg_confirmation = et.msg_confirmation ?? '';
			msg_cancellation = et.msg_cancellation ?? '';
			msg_reschedule = et.msg_reschedule ?? '';
			msg_reminder = et.msg_reminder ?? '';
			subj_confirmation = et.subj_confirmation ?? '';
			subj_cancellation = et.subj_cancellation ?? '';
			subj_reschedule = et.subj_reschedule ?? '';
			subj_reminder = et.subj_reminder ?? '';
		} catch (e: any) {
			etError = e.message;
		} finally {
			etLoading = false;
		}
	}

	async function saveET() {
		if (!form.name.trim()) { toast.error('Name is required.'); return; }
		if (form.duration_minutes < 5) { toast.error('Duration must be at least 5 minutes.'); return; }
		if (form.max_active_bookings < 0) { toast.error('Max active bookings cannot be negative (0 = unlimited).'); return; }
		if (routingMode === 'round_robin' && rotationHosts.length === 0) {
			toast.error('Add at least one person to the rotation'); return;
		}
		if (routingMode === 'collective' && !togetherHosts.some((h) => !h.optional)) {
			toast.error('Add at least one required host (someone who always attends)'); return;
		}
		etSaving = true;
		try {
			await api.patch(`/v1/event-types/${slug}`, {
				name: form.name.trim(),
				description: form.description.trim() || null,
				duration_minutes: Number(form.duration_minutes),
				is_active: form.is_active,
				is_public: form.is_public,
				location_type: form.location_type,
				location_value: form.location_value.trim() || null,
				buffer_before_minutes: Number(form.buffer_before_minutes),
				buffer_after_minutes: Number(form.buffer_after_minutes),
				min_notice_minutes: Number(form.min_notice_minutes),
				max_future_days: Number(form.max_future_days),
				max_active_bookings: Number(form.max_active_bookings),
				price_cents: Math.max(0, Math.round(Number(priceMajor) * 100)) || 0,
				currency: form.currency.trim().toLowerCase() || 'usd',
				routing_mode: routingMode,
				rr_strategy: rrStrategy,
				reminders,
				msg_confirmation: msg_confirmation.trim() || null,
				msg_cancellation: msg_cancellation.trim() || null,
				msg_reschedule: msg_reschedule.trim() || null,
				msg_reminder: msg_reminder.trim() || null,
				subj_confirmation: subj_confirmation.trim() || null,
				subj_cancellation: subj_cancellation.trim() || null,
				subj_reschedule: subj_reschedule.trim() || null,
				subj_reminder: subj_reminder.trim() || null,
			});
			if (routingMode === 'round_robin') {
				await api.put(`/v1/event-types/${slug}/hosts`, {
					hosts: rotationHosts.map((hh, i) => ({ user_id: hh.user_id, role: 'rotation', priority: i })),
				});
			} else if (routingMode === 'collective') {
				await api.put(`/v1/event-types/${slug}/hosts`, {
					hosts: togetherHosts.map((hh, i) => ({
						user_id: hh.user_id, role: hh.optional ? 'optional' : 'required', priority: i,
					})),
				});
			} else {
				await api.put(`/v1/event-types/${slug}/hosts`, {
					hosts: [{ user_id: $currentUser?.id, role: 'required', priority: 0 }],
				});
			}
			toast.success('Changes saved');
			await loadET();
		} catch (e: any) {
			toast.error(e.message || 'Could not save changes');
		} finally {
			etSaving = false;
		}
	}

	async function sendTestEmail(type: MsgKey) {
		testSending = { ...testSending, [type]: true };
		testSent    = { ...testSent,    [type]: false };
		testError   = { ...testError,   [type]: '' };
		try {
			await api.post(`/v1/event-types/${slug}/test-email`, { type });
			testSent = { ...testSent, [type]: true };
			setTimeout(() => { testSent = { ...testSent, [type]: false }; }, 4000);
		} catch (e: any) {
			testError = { ...testError, [type]: e.message };
		} finally {
			testSending = { ...testSending, [type]: false };
		}
	}

	function buildPreview(type: MsgKey, note: string): string {
		const name     = et?.name ?? 'My Event';
		const loc      = et?.location_value ? `\nLocation: ${et.location_value}` : '';
		const dur      = et?.duration_minutes ?? 30;
		const noteBlk  = note.trim() ? `\n---\n${note.trim()}\n` : '';
		const start    = 'Tomorrow, 2:00 PM UTC';
		const prev     = 'Today, 2:00 PM UTC';
		// Compute end time correctly by adding duration to 14:00.
		const endTotalMin = 14 * 60 + dur;
		const endH24      = Math.floor(endTotalMin / 60) % 24;
		const endMin      = endTotalMin % 60;
		const endPeriod   = endH24 < 12 ? 'AM' : 'PM';
		const endH12      = endH24 % 12 || 12;
		const end         = `Tomorrow, ${endH12}:${String(endMin).padStart(2, '0')} ${endPeriod} UTC`;

		switch (type) {
			case 'confirmation':
				return `Hi Alex Johnson,\n\nYour booking has been confirmed.\n\nEvent:    ${name}\nWith:     ${et?.name ?? 'Host'}\nStart:    ${start}\nEnd:      ${end}${loc}\n\nBooking reference: preview-test\n\nTo cancel, visit:\n[booking page]${noteBlk}\n— Calnode`;
			case 'cancellation':
				return `Hi Alex Johnson,\n\nYour booking has been cancelled.\n\nEvent:    ${name}\nWith:     ${et?.name ?? 'Host'}\nStart:    ${start}\nEnd:      ${end}\n\nTo rebook, visit:\n[booking page]${noteBlk}\n— Calnode`;
			case 'reschedule':
				return `Hi Alex Johnson,\n\nYour booking has been rescheduled.\n\nEvent:    ${name}\nWith:     ${et?.name ?? 'Host'}\nWas:      ${prev}\nNow:      ${start}\nEnd:      ${end}${loc}\n\nBooking reference: preview-test${noteBlk}\n— Calnode`;
			case 'reminder':
				return `Hi Alex Johnson,\n\nThis is a reminder that your booking is coming up.\n\nEvent:    ${name}\nWith:     ${et?.name ?? 'Host'}\nStart:    ${start}\nEnd:      ${end}${loc}\n\nBooking reference: preview-test${noteBlk}\n— Calnode`;
		}
	}

	let questionsPanel: ReturnType<typeof QuestionsPanel> | undefined = $state();

	onMount(async () => {
		await loadET();
		// Editor-only data (owner-scoped endpoints) — skip for read-only hosts.
		if (et?.owned === false) return;
		// Connected calendar — best-effort; drives the meeting-link hint only.
		api.get<CalendarStatus>('/v1/calendar/status').then((s) => (calStatus = s)).catch(() => {});
	api.get<ZoomStatus>('/v1/zoom/status').then((s) => (zoomStatus = s)).catch(() => {});
		questionsPanel?.loadQuestions();
		if (hostScope === 'people') {
			await loadHosts();
			loadMembers();
			loadTeams();
		}
	});

</script>

{#snippet hostPickers(target: Target, idPrefix: string)}
	<div class="grid grid-cols-2 gap-4">
		<div class="space-y-1.5">
			<Label for="{idPrefix}-add-member">Add member</Label>
			<Select.Root type="single" value="" onValueChange={(v) => addMember(target, v)}>
				<Select.Trigger id="{idPrefix}-add-member" class="w-full">Select a member…</Select.Trigger>
				<Select.Content>
					{#if availableMembers.length > 0}
						{#each availableMembers as m}
							<Select.Item value={m.id} label={m.name}>{m.name} · {m.email}</Select.Item>
						{/each}
					{:else}
						<div class="px-2 py-1.5 text-xs text-muted-foreground">No members available</div>
					{/if}
				</Select.Content>
			</Select.Root>
		</div>
		<div class="space-y-1.5">
			<Label for="{idPrefix}-add-team">Add team</Label>
			<Select.Root type="single" value="" onValueChange={(v) => addTeam(target, v)}>
				<Select.Trigger id="{idPrefix}-add-team" class="w-full">Select a team…</Select.Trigger>
				<Select.Content>
					{#if teams.length > 0}
						{#each teams as t}
							<Select.Item value={t.id} label={t.name}>{t.name} ({t.member_count})</Select.Item>
						{/each}
					{:else}
						<div class="px-2 py-1.5 text-xs text-muted-foreground">No teams</div>
					{/if}
				</Select.Content>
			</Select.Root>
		</div>
	</div>
{/snippet}

<svelte:head><title>{et?.name ?? slug} — Event Type — Calnode</title></svelte:head>
<svelte:window onkeydown={saveOnCmdS(saveET, () => !etSaving)} />

<div class="mb-8">
	<a href="{base}/event-types" class="mb-2 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
		<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
		Event Types
	</a>
	<div class="flex items-center gap-3">
		<h1 class="text-2xl font-semibold tracking-tight">{et?.name ?? slug}</h1>
		<Tooltip.Provider>
			<Tooltip.Root>
				<Tooltip.Trigger
					class={buttonVariants({ variant: 'ghost', size: 'icon' })}
					onclick={() => window.open(`/book/${slug}`, '_blank')}
				>
					<!-- External link icon (matches the event-types list) -->
					<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>
				</Tooltip.Trigger>
				<Tooltip.Content>Preview booking page</Tooltip.Content>
			</Tooltip.Root>
		</Tooltip.Provider>
	</div>
</div>

{#if etLoading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if etError}
	<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{etError}</p>
{:else if et && et.owned === false}

<!-- Read-only: the user hosts this event type but doesn't own it -->
<div class="mb-6 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800">
	{#if et.owner_email}
		<span class="font-medium">{et.owner_name || et.owner_email}</span> created this event type.
		<a href="mailto:{et.owner_email}?subject={encodeURIComponent('Change request: ' + et.name)}" class="font-medium underline">Message them</a> to request changes.
	{:else if et.owner_name}
		<span class="font-medium">{et.owner_name}</span> created this event type. Message them to request changes.
	{:else}
		This event type is managed by its owner. Contact them to request changes.
	{/if}
</div>
<div class="rounded-lg border bg-card p-6">
	<dl class="grid grid-cols-[140px_1fr] gap-x-4 gap-y-3 text-sm">
		<dt class="text-muted-foreground">Name</dt><dd class="font-medium">{et.name}</dd>
		{#if et.description}<dt class="text-muted-foreground">Description</dt><dd class="whitespace-pre-line">{et.description}</dd>{/if}
		<dt class="text-muted-foreground">Duration</dt><dd>{et.duration_minutes} min</dd>
		<dt class="text-muted-foreground">Location</dt><dd>{LOCATION_TYPES.find((l) => l.value === et?.location_type)?.label ?? et.location_type}{#if et.location_value} · {et.location_value}{/if}</dd>
		<dt class="text-muted-foreground">Routing</dt><dd class="capitalize">{et.routing_mode.replace('_', ' ')}</dd>
		<dt class="text-muted-foreground">Status</dt><dd>{et.is_active ? 'Active' : 'Inactive'} · {et.is_public ? 'Listed' : 'Unlisted (link only)'}</dd>
		<dt class="text-muted-foreground">Booking page</dt><dd><a href="/book/{et.slug}" target="_blank" rel="noopener" class="text-primary underline">/book/{et.slug}</a></dd>
	</dl>
</div>

{:else}

<div class="mb-6 flex gap-1 overflow-x-auto border-b">
	{#each TABS as t}
		<button
			type="button"
			onclick={() => (activeTab = t.id)}
			class="-mb-px shrink-0 border-b-2 px-3 py-2 text-sm font-medium transition-colors {activeTab === t.id ? 'border-foreground text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
		>
			{t.label}
		</button>
	{/each}
</div>

{#if activeTab === 'general'}
<!-- General Settings -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">General</h2>
	<div class="rounded-lg border bg-card p-6">
		<div class="grid grid-cols-2 gap-4">
			<div class="space-y-1.5">
				<Label for="et-name">Name</Label>
				<Input id="et-name" bind:value={form.name} />
			</div>
			<div class="space-y-1.5">
				<Label for="et-dur">Duration (minutes)</Label>
				<Input id="et-dur" type="number" min="5" step="5" bind:value={form.duration_minutes} />
			</div>
			<div class="col-span-2 space-y-1.5">
				<Label for="et-desc">Description</Label>
				<Textarea id="et-desc" bind:value={form.description} placeholder="Optional — supports **bold** and *italic* markdown" rows={3} class="resize-y" />
			</div>
			<div class="space-y-1.5">
				<p class="text-sm font-medium">Status</p>
				<div class="flex items-center gap-2">
					<Checkbox id="is-active" bind:checked={form.is_active} />
					<Label for="is-active" class="cursor-pointer font-normal">Active (accepting bookings)</Label>
				</div>
			</div>
			<div class="space-y-1.5">
				<p class="text-sm font-medium">Visibility</p>
				<div class="flex items-center gap-2">
					<Checkbox id="is-public" bind:checked={form.is_public} />
					<Label for="is-public" class="cursor-pointer font-normal">Public (visible in booking page)</Label>
				</div>
			</div>
		</div>

		<!-- Location -->
		<div class="mt-6 border-t pt-5">
			<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Location</p>
			<div class="grid grid-cols-2 gap-4">
				<div class="space-y-1.5">
					<Label for="et-loc">Type</Label>
					<Select.Root type="single" bind:value={form.location_type}>
						<Select.Trigger id="et-loc" class="w-full">
							{LOCATION_TYPES.find((lt) => lt.value === form.location_type)?.label ?? 'Select…'}
						</Select.Trigger>
						<Select.Content>
							{#each LOCATION_TYPES as lt}
								<Select.Item value={lt.value} label={lt.label}>{lt.label}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
				<div class="space-y-1.5">
					{#if isOnlineMeeting(form.location_type)}
						{@const platform = form.location_type === 'teams' ? 'Microsoft Teams' : 'Google Meet'}
						<Label for="et-loc-val">{platform} link</Label>
						{#if meetAutoGen}
							<p class="rounded-md border border-green-600/20 bg-green-50 px-3 py-2 text-sm text-green-700">
								A {platform} link is generated automatically for each booking from your connected calendar.{#if form.location_type === 'teams'} Personal Microsoft accounts can't generate Teams links — add one below as a fallback.{/if}
							</p>
						{:else}
							<p class="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
								{calStatus?.connected
									? `Your connected calendar is ${calStatus.provider === 'google' ? 'Google' : 'Microsoft'}, so a ${platform} link won't be generated automatically.`
									: `No calendar is connected, so a ${platform} link can't be generated automatically.`}
								Paste a link below and it'll be used for every booking.
							</p>
						{/if}
						<Input id="et-loc-val" bind:value={form.location_value} placeholder={meetAutoGen ? 'Optional fallback link' : `Paste a ${platform} link`} />
					{:else if form.location_type === 'zoom'}
						<Label for="et-loc-val">Zoom link</Label>
						{#if zoomAutoGen}
							<p class="rounded-md border border-green-600/20 bg-green-50 px-3 py-2 text-sm text-green-700">
								A Zoom meeting is created automatically for each booking under the assigned host's connected Zoom account.
							</p>
						{:else}
							<p class="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
								{zoomStatus?.configured
									? 'Connect your Zoom account on the Calendar page to auto-generate meeting links,'
									: 'Zoom isn’t set up for this workspace (an admin can add it in Settings → Zoom),'}
								or paste a Zoom link below to use for every booking.
							</p>
						{/if}
						<Input id="et-loc-val" bind:value={form.location_value} placeholder={zoomAutoGen ? 'Optional fallback link' : 'https://…zoom.us/j/…'} />
					{:else if form.location_type === 'livekit'}
						<Label>Video room</Label>
						<p class="rounded-md border border-green-600/20 bg-green-50 px-3 py-2 text-sm text-green-700">
							A secure video room is created automatically for each booking — no link needed. Guests join in the browser. (Set up the server in Settings → Video.)
						</p>
					{:else}
						<Label for="et-loc-val">{LOCATION_NEEDS_VALUE[form.location_type] ?? 'Details'}</Label>
						<Input id="et-loc-val" bind:value={form.location_value} placeholder={LOCATION_PLACEHOLDER[form.location_type] ?? 'Optional'} />
					{/if}
				</div>
			</div>
		</div>

		<!-- Price -->
		<div class="mt-6 border-t pt-5">
			<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Price</p>
			<div class="grid grid-cols-2 gap-4">
				<div class="space-y-1.5">
					<Label for="et-price">Amount</Label>
					<Input id="et-price" type="number" min="0" step="0.01" bind:value={priceMajor} placeholder="0.00" />
				</div>
				<div class="space-y-1.5">
					<Label for="et-currency">Currency</Label>
					<Input id="et-currency" type="text" maxlength={3} bind:value={form.currency} placeholder="usd" />
				</div>
			</div>
			<p class="mt-2 text-xs text-muted-foreground">
				Leave at 0 for a free event. A price sends bookers to Stripe Checkout before the slot is
				confirmed — requires <a href="/admin/settings/payments" class="underline">Stripe</a> to be connected.
			</p>
		</div>

		<!-- Scheduling -->
		<div class="mt-6 border-t pt-5">
			<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Scheduling</p>
			<div class="grid grid-cols-2 gap-4">
				<div class="space-y-1.5">
					<Label for="et-buf-before">Buffer before (min)</Label>
					<Input id="et-buf-before" type="number" min="0" step="5" bind:value={form.buffer_before_minutes} />
					<p class="text-xs text-muted-foreground">Blocked time before each meeting</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-buf-after">Buffer after (min)</Label>
					<Input id="et-buf-after" type="number" min="0" step="5" bind:value={form.buffer_after_minutes} />
					<p class="text-xs text-muted-foreground">Blocked time after each meeting</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-notice">Minimum notice (min)</Label>
					<Input id="et-notice" type="number" min="0" step="30" bind:value={form.min_notice_minutes} />
					<p class="text-xs text-muted-foreground">e.g. 60 = bookings must be 1h+ in future</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-future">Booking window (days)</Label>
					<Input id="et-future" type="number" min="0" bind:value={form.max_future_days} />
					<p class="text-xs text-muted-foreground">How far ahead people can book. 0 = unlimited</p>
				</div>
				<div class="space-y-1.5">
					<Label for="et-max-active">Max active bookings per person</Label>
					<Input id="et-max-active" type="number" min="0" bind:value={form.max_active_bookings} />
					<p class="text-xs text-muted-foreground">Upcoming bookings one attendee (by email) can hold. 0 = unlimited</p>
				</div>
			</div>
		</div>

	</div>
</div>
{/if}

{#if activeTab === 'hosts'}
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Hosts</h2>
	<div class="rounded-lg border bg-card p-6">
		<div>
			<p class="-mt-1 mb-4 text-sm text-muted-foreground">Who can host this event, and how meetings are staffed.</p>

			<!-- Q1 — who can host -->
			<div class="space-y-1.5">
				<Label>Who can host this event?</Label>
				<div class="inline-flex rounded-lg border bg-muted/40 p-0.5">
					<button type="button" onclick={() => setScope('me')}
						class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {hostScope === 'me' ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'}">
						Just me
					</button>
					<button type="button" onclick={() => setScope('people')}
						class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {hostScope === 'people' ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'}">
						Specific people
					</button>
				</div>
				<p class="text-xs text-muted-foreground">
					{#if hostScope === 'me'}
						Every booking goes to you.
					{:else}
						Pick who can take these bookings — add members individually or pull in a whole team.
					{/if}
				</p>
			</div>

			{#if hostScope === 'people'}
				<!-- Q2 — how the meeting is staffed -->
				<div class="mt-4 space-y-1.5">
					<Label>How should the meeting be staffed?</Label>
					<div class="inline-flex rounded-lg border bg-muted/40 p-0.5">
						<button type="button" onclick={() => (staffing = 'rotate')}
							class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {staffing === 'rotate' ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'}">
							Rotate between them
						</button>
						<button type="button" onclick={() => (staffing = 'together')}
							class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors {staffing === 'together' ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground'}">
							Everyone attends
						</button>
					</div>
					<p class="text-xs text-muted-foreground">
						{#if staffing === 'rotate'}
							One available person is booked per slot, spreading bookings across the group.
						{:else}
							Everyone joins the same meeting. A slot is offered only when all required people are free.
						{/if}
					</p>
				</div>

				{#if staffing === 'rotate'}
					<div class="mt-4 space-y-3">
						<div class="space-y-1.5">
							<Label for="rr-strategy">Who gets picked</Label>
							<Select.Root type="single" value={rrStrategy} onValueChange={(v) => { if (v) rrStrategy = v as Strategy; }}>
								<Select.Trigger id="rr-strategy" class="w-full">
									{RR_STRATEGIES.find((s) => s.value === rrStrategy)?.label ?? 'Select…'}
								</Select.Trigger>
								<Select.Content>
									{#each RR_STRATEGIES as s}
										<Select.Item value={s.value} label={s.label}>{s.label}</Select.Item>
									{/each}
								</Select.Content>
							</Select.Root>
							<p class="text-xs text-muted-foreground">
								{#if rrStrategy === 'priority'}
									The highest person in the list who's free is booked; fall down the list when they're busy.
								{:else if rrStrategy === 'soonest'}
									Offers the earliest slot anyone in the group has free.
								{:else}
									Spreads bookings evenly — whoever has the fewest upcoming meetings is booked.
								{/if}
							</p>
						</div>

						<p class="text-sm font-medium">People in the rotation</p>
						{#if rotationHosts.length > 0}
							<div class="space-y-2">
								{#each rotationHosts as h, i (h.user_id)}
									<div class="flex items-center justify-between gap-2 rounded-md border px-3 py-2">
										<div class="flex min-w-0 items-center gap-2">
											{#if rrStrategy === 'priority'}
												<span class="shrink-0 rounded bg-muted px-1.5 py-0.5 text-xs font-medium text-muted-foreground tabular-nums">{i + 1}</span>
											{/if}
											<div class="min-w-0">
												<div class="truncate text-sm font-medium">{h.name}</div>
												<div class="truncate text-xs text-muted-foreground">{h.email}</div>
											</div>
										</div>
										<div class="flex shrink-0 items-center gap-1">
											{#if rrStrategy === 'priority'}
												<Button type="button" variant="ghost" size="icon" disabled={i === 0} aria-label="Move up" onclick={() => moveRotation(i, -1)}>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="18 15 12 9 6 15"/></svg>
												</Button>
												<Button type="button" variant="ghost" size="icon" disabled={i === rotationHosts.length - 1} aria-label="Move down" onclick={() => moveRotation(i, 1)}>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>
												</Button>
											{/if}
											<Button type="button" variant="ghost" size="sm" onclick={() => removePerson('rotation', h.user_id)}>Remove</Button>
										</div>
									</div>
								{/each}
							</div>
						{:else}
							<p class="text-xs text-muted-foreground">Add people or a team to the rotation.</p>
						{/if}
						{@render hostPickers('rotation', 'rr')}
					</div>
				{:else}
					<div class="mt-4 space-y-3">
						<p class="text-sm font-medium">Who attends</p>
						{#if togetherHosts.length > 0}
							<div class="space-y-2">
								{#each togetherHosts as h (h.user_id)}
									<div class="flex items-center justify-between gap-2 rounded-md border px-3 py-2">
										<div class="min-w-0">
											<div class="truncate text-sm font-medium">{h.name}</div>
											<div class="truncate text-xs text-muted-foreground">{h.email}</div>
										</div>
										<div class="flex shrink-0 items-center gap-2">
											<div class="inline-flex rounded-md border p-0.5">
												<button type="button" onclick={() => setOptional(h.user_id, false)}
													class="rounded px-2 py-0.5 text-xs font-medium transition-colors {!h.optional ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}">
													Required
												</button>
												<button type="button" onclick={() => setOptional(h.user_id, true)}
													class="rounded px-2 py-0.5 text-xs font-medium transition-colors {h.optional ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}">
													Optional
												</button>
											</div>
											<Button type="button" variant="ghost" size="sm" onclick={() => removePerson('together', h.user_id)}>Remove</Button>
										</div>
									</div>
								{/each}
							</div>
						{:else}
							<p class="text-xs text-muted-foreground">Add the people who attend this meeting.</p>
						{/if}
						<p class="text-xs text-muted-foreground">
							<span class="font-medium text-foreground">Required</span> hosts always attend and must be free for a slot to open.
							<span class="font-medium text-foreground">Optional</span> hosts join only when they're free — they never block a slot.
						</p>
						{@render hostPickers('together', 'grp')}
					</div>
				{/if}
			{/if}
		</div>
	</div>
</div>
{/if}

{#if activeTab === 'notifications'}
<!-- Notifications -->
<div class="mb-8">
	<h2 class="mb-3 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Notifications</h2>
	<div class="rounded-lg border bg-card p-6 space-y-6">

		<!-- Reminders -->
		<div>
			<p class="mb-1 text-sm font-medium">Reminders</p>
			<p class="mb-3 text-xs text-muted-foreground">Send attendees a reminder email before the meeting. Defaults to 24 hours before if none are set.</p>
			<div class="space-y-2">
				{#each reminders as hb, i}
					<div class="flex items-center gap-2">
						<Select.Root
							type="single"
							value={String(hb)}
							onValueChange={(v) => {
								if (!v) return;
								const n = Number(v);
								reminders = reminders.map((r, idx) => idx === i ? n : r);
							}}
						>
							<Select.Trigger class="w-full">
								{REMINDER_OPTIONS.find((o) => o.value === hb)?.label ?? 'Select…'}
							</Select.Trigger>
							<Select.Content>
								{#each REMINDER_OPTIONS as opt}
									<Select.Item value={String(opt.value)} label={opt.label}>{opt.label}</Select.Item>
								{/each}
							</Select.Content>
						</Select.Root>
						<Button
							type="button"
							variant="ghost"
							size="icon"
							onclick={() => { reminders = reminders.filter((_, idx) => idx !== i); }}
						>
							<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
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
							const next = REMINDER_OPTIONS.find(o => !used.has(o.value));
							if (next) reminders = [...reminders, next.value];
						}}
					>
						+ Add reminder
					</Button>
				{/if}
			</div>
		</div>

		<!-- Custom messages -->
		<div class="border-t pt-5">
			<p class="mb-1 text-sm font-medium">Custom messages</p>
			<p class="mb-3 text-xs text-muted-foreground">Add an optional note appended to each email type.</p>
			<div class="space-y-2">
				{#each [
					{ key: 'confirmation' as const, label: 'Booking confirmation' },
					{ key: 'cancellation' as const, label: 'Cancellation notice' },
					{ key: 'reschedule' as const, label: 'Reschedule notice' },
					{ key: 'reminder' as const, label: 'Reminder email' },
				] as item}
					<div class="rounded-md border">
						<button
							type="button"
							class="flex w-full items-center justify-between px-4 py-3 text-sm font-medium hover:bg-muted/30 transition-colors"
							onclick={() => { msgOpen[item.key] = !msgOpen[item.key]; }}
						>
							<span>{item.label}</span>
							<svg
								xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24"
								fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
								class="transition-transform {msgOpen[item.key] ? 'rotate-180' : ''}"
							><polyline points="6 9 12 15 18 9"/></svg>
						</button>
						{#if msgOpen[item.key]}
							{@const note = item.key === 'confirmation' ? msg_confirmation
								: item.key === 'cancellation' ? msg_cancellation
								: item.key === 'reschedule'   ? msg_reschedule
								: msg_reminder}
							<div class="border-t px-4 pb-4 pt-3 space-y-3">
								<div class="space-y-1.5">
									<Label class="text-xs text-muted-foreground">Subject line <span class="font-normal">(optional — blank uses the default)</span></Label>
									{#if item.key === 'confirmation'}
										<Input bind:value={subj_confirmation} placeholder={`Booking confirmed: ${form.name}`} />
									{:else if item.key === 'cancellation'}
										<Input bind:value={subj_cancellation} placeholder={`Booking cancelled: ${form.name}`} />
									{:else if item.key === 'reschedule'}
										<Input bind:value={subj_reschedule} placeholder={`Booking rescheduled: ${form.name}`} />
									{:else if item.key === 'reminder'}
										<Input bind:value={subj_reminder} placeholder={`Reminder: ${form.name} is coming up`} />
									{/if}
								</div>
								{#if item.key === 'confirmation'}
									<Textarea bind:value={msg_confirmation} rows={3} placeholder="Add a custom note for attendees…" />
								{:else if item.key === 'cancellation'}
									<Textarea bind:value={msg_cancellation} rows={3} placeholder="Add a custom note for attendees…" />
								{:else if item.key === 'reschedule'}
									<Textarea bind:value={msg_reschedule} rows={3} placeholder="Add a custom note for attendees…" />
								{:else if item.key === 'reminder'}
									<Textarea bind:value={msg_reminder} rows={3} placeholder="Add a custom note for attendees…" />
								{/if}

								<!-- Preview toggle -->
								<button
									type="button"
									class="text-xs text-muted-foreground hover:text-foreground underline-offset-2 hover:underline"
									onclick={() => { previewOpen[item.key] = !previewOpen[item.key]; }}
								>{previewOpen[item.key] ? 'Hide preview' : 'Show email preview'}</button>

								{#if previewOpen[item.key]}
									<pre class="rounded-md border bg-muted/30 px-4 py-3 text-xs leading-relaxed whitespace-pre-wrap font-mono text-muted-foreground overflow-auto max-h-64">{buildPreview(item.key, note)}</pre>
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
										{testSending[item.key] ? 'Sending…' : 'Send test email'}
									</Button>
									{#if testSent[item.key]}
										<span class="text-xs text-green-600">Test email sent to your inbox.</span>
									{/if}
									{#if testError[item.key]}
										<span class="text-xs text-destructive">
											{#if testError[item.key] === 'Email is not configured on this server — add SMTP settings to enable sending'}
												SMTP is not configured — <a href="{base}/settings/email" class="underline">configure it in Settings</a>.
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

	</div>
</div>
{/if}

{#if activeTab === 'questions'}
	<QuestionsPanel bind:this={questionsPanel} slug={slug ?? ''} />
{/if}

{#if activeTab === 'embed'}
	<EmbedPanel slug={et?.slug ?? ''} />
{/if}

{#if activeTab === 'general' || activeTab === 'hosts' || activeTab === 'notifications'}
	<div class="sticky bottom-0 mt-4 flex justify-end border-t bg-background/90 py-3 backdrop-blur">
		<Button onclick={saveET} disabled={etSaving}>
			{etSaving ? 'Saving…' : 'Save changes'}
		</Button>
	</div>
{/if}

{/if}
