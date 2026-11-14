<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Booking } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { prefs, fmtDateTime, fmtTime } from '$lib/prefs';
	import { t } from '$lib/i18n';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import * as Select from '$lib/components/ui/select';
	import { DatePicker } from '$lib/components/ui/date-picker';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import * as Dialog from '$lib/components/ui/dialog';

	let items: Booking[] = $state([]);
	let loading = $state(true);
	let error = $state('');

	// Members see only their own hosted bookings. Owners/admins can switch to a
	// workspace-wide view (?scope=all) for oversight.
	const isAdmin = $derived($currentUser?.is_admin ?? false);
	let scope = $state<'mine' | 'all'>('mine');

	// Filtering, sorting and paging all happen in SQL now. They used to happen here,
	// over a response that contained every booking the user could see - which meant
	// the page got slower with every booking ever made, and the server did unbounded
	// work to render 25 rows.
	let timeFilter = $state<'upcoming' | 'past'>('upcoming');
	let fEventType = $state('');
	let fHost = $state('');
	let fTeam = $state('');
	let fStatus = $state('');

	const PAGE_SIZE = 25;
	let offset = $state(0);
	let total = $state(0);
	let counts = $state({ upcoming: 0, past: 0 });

	// Options for the filter selects, fetched once.
	let eventTypes = $state<{ slug: string; name: string }[]>([]);
	let members = $state<{ id: string; name: string }[]>([]);
	let teams = $state<{ id: string; name: string }[]>([]);

	const hasFilters = $derived(!!(fEventType || fHost || fTeam || fStatus));
	const pageStart = $derived(total === 0 ? 0 : offset + 1);
	const pageEnd = $derived(Math.min(offset + items.length, total));
	const eventTypeName = $derived(
		(slug: string) => eventTypes.find((e) => e.slug === slug)?.name ?? slug
	);

	let reschedulingId = $state<string | null>(null);
	let reschedulingSlug = $state('');
	let rescheduleDate = $state('');
	let slots: { start: string; end: string }[] = $state([]);
	let slotsLoading = $state(false);
	let slotsError = $state('');
	let selectedSlot = $state('');
	let rescheduling = $state(false);
	let rescheduleError = $state('');

	type AnswerItem = { label: string; type: string; value: string };
	let expandedId = $state<string | null>(null);
	let answersCache: Record<string, AnswerItem[]> = $state({});
	let answersLoading: Record<string, boolean> = $state({});

	async function toggleExpand(id: string) {
		if (expandedId === id) { expandedId = null; return; }
		expandedId = id;
		if (answersCache[id] === undefined) {
			answersLoading[id] = true;
			try {
				const res = await api.get<{ items: AnswerItem[] }>(`/v1/bookings/${id}/answers`);
				answersCache[id] = res.items ?? [];
			} catch {
				answersCache[id] = [];
			}
			answersLoading[id] = false;
		}
	}

	function query(): string {
		const p = new URLSearchParams();
		if (scope === 'all' && isAdmin) p.set('scope', 'all');
		p.set('when', timeFilter);
		// Past reads most-recent-first, upcoming soonest-first. Server-side now: sorting
		// a page in the browser would only ever sort that page.
		p.set('order', timeFilter === 'past' ? 'desc' : 'asc');
		if (fEventType) p.set('event_type', fEventType);
		if (fHost) p.set('host', fHost);
		if (fTeam) p.set('team', fTeam);
		if (fStatus) p.set('status', fStatus);
		p.set('limit', String(PAGE_SIZE));
		p.set('offset', String(offset));
		return p.toString();
	}

	// Filter changes can outrun their responses: pick an event type, then a status a
	// moment later, and the slower first reply would otherwise land last and overwrite
	// the newer one. Only the most recent request is allowed to apply.
	let loadSeq = 0;

	async function load() {
		const seq = ++loadSeq;
		try {
			const res = await api.get<{
				items: Booking[];
				total: number;
				counts: { upcoming: number; past: number };
			}>(`/v1/bookings?${query()}`);
			if (seq !== loadSeq) return;
			items = res.items ?? [];
			total = res.total ?? 0;
			counts = res.counts ?? { upcoming: 0, past: 0 };
			error = '';

			// The current page can fall off the end of the result set: cancel the only
			// booking on the last page and offset now points past it, which renders an
			// empty table under a "Showing 26-25 of 25" label with Next still enabled.
			// Step back to the last real page instead. offset > 0 bounds the recursion.
			if (items.length === 0 && offset > 0 && total > 0) {
				offset = Math.max(0, (Math.ceil(total / PAGE_SIZE) - 1) * PAGE_SIZE);
				await load();
				return;
			}
		} catch (e: any) {
			if (seq !== loadSeq) return;
			error = e.message;
		} finally {
			if (seq === loadSeq) loading = false;
		}
	}

	// Any change to what is being asked for starts again at the first page - staying on
	// page 3 of a filter that now matches four things shows an empty table.
	async function reload() {
		offset = 0;
		loading = true;
		await load();
	}

	async function setScope(s: 'mine' | 'all') {
		if (scope === s) return;
		scope = s;
		// Host and team only mean anything across the workspace.
		if (s === 'mine') { fHost = ''; fTeam = ''; }
		await reload();
	}

	async function setTimeFilter(next: 'upcoming' | 'past') {
		if (timeFilter === next) return;
		timeFilter = next;
		await reload();
	}

	function clearFilters() {
		fEventType = fHost = fTeam = fStatus = '';
		reload();
	}

	async function goTo(newOffset: number) {
		offset = Math.max(0, newOffset);
		loading = true;
		await load();
	}

	// Filter options. Failures are silent: a missing dropdown is a smaller problem than
	// an error banner over a working table, and members can't list users anyway.
	async function loadFilterOptions() {
		try {
			const res = await api.get<{ items: { slug: string; name: string }[] }>('/v1/event-types');
			eventTypes = res.items ?? [];
		} catch { /* leave the dropdown empty */ }
		if (!isAdmin) return;
		try {
			// /v1/users returns a bare array, not an { items } envelope like the others.
			// Archived members are excluded by default, which is what we want here.
			members = (await api.get<{ id: string; name: string }[]>('/v1/users')) ?? [];
		} catch { /* leave the dropdown empty */ }
		try {
			const res = await api.get<{ items: { id: string; name: string }[] }>('/v1/teams');
			teams = res.items ?? [];
		} catch { /* leave the dropdown empty */ }
	}

	// Refresh re-fetches in place (no full-page "Loading…" flash) — just spins the button.
	let refreshing = $state(false);
	async function refresh() {
		if (refreshing) return;
		refreshing = true;
		error = '';
		await load();
		refreshing = false;
	}

	onMount(() => {
		load();
		loadFilterOptions();
	});

	let confirmOpen = $state(false);
	let pendingCancelId = $state<string | null>(null);

	function requestCancel(id: string) {
		pendingCancelId = id;
		confirmOpen = true;
	}

	async function cancel() {
		const id = pendingCancelId;
		if (!id) return;
		try {
			await api.post(`/v1/bookings/${id}/cancel`, {});
			await load();
		} catch (e: any) {
			error = e.message;
		} finally {
			confirmOpen = false;
			pendingCancelId = null;
		}
	}

	function startReschedule(b: Booking) {
		reschedulingId = b.id;
		reschedulingSlug = b.event_type_slug ?? '';
		rescheduleDate = '';
		slots = [];
		selectedSlot = '';
		rescheduleError = '';
		slotsError = '';
	}

	function cancelReschedule() {
		reschedulingId = null;
	}

	$effect(() => {
		if (rescheduleDate) loadSlots();
	});

	async function loadSlots() {
		if (!rescheduleDate) return;
		slotsLoading = true;
		slotsError = '';
		slots = [];
		selectedSlot = '';
		try {
			const tz = $prefs.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone;
			const res = await api.get<{ slots: { start: string; end: string }[] }>(
				`/v1/event-types/${reschedulingSlug}/slots?from=${rescheduleDate}&to=${rescheduleDate}&tz=${encodeURIComponent(tz)}`
			);
			slots = res.slots ?? [];
		} catch (e: any) {
			slotsError = e.message;
		} finally {
			slotsLoading = false;
		}
	}

	async function confirmReschedule() {
		if (!selectedSlot || !reschedulingId) return;
		rescheduling = true;
		rescheduleError = '';
		try {
			await api.patch(`/v1/bookings/${reschedulingId}/reschedule`, { start_at: selectedSlot });
			reschedulingId = null;
			await load();
		} catch (e: any) {
			rescheduleError = e.message;
		} finally {
			rescheduling = false;
		}
	}

	function fmt(iso: string) { return fmtDateTime(iso, $prefs); }
	function fmtSlotTime(iso: string) { return fmtTime(iso, $prefs); }
	function fmtMoney(cents?: number, cur?: string) {
		if (!cents) return '';
		const amt = (cents / 100).toFixed(2);
		const c = (cur || 'usd').toUpperCase();
		const sym: Record<string, string> = { USD: '$', EUR: '€', GBP: '£', AUD: 'A$', CAD: 'C$', NZD: 'NZ$' };
		return sym[c] ? sym[c] + amt : amt + ' ' + c;
	}
	const payLabel = $derived<Record<string, string>>({
		paid: $t('bookings.payment.paid'),
		refunded: $t('bookings.payment.refunded'),
		pending: $t('bookings.payment.pending')
	});

	function bookingStatusLabel(status: string): string {
		if (status === 'confirmed') return $t('bookings.status.confirmed');
		if (status === 'cancelled') return $t('bookings.status.cancelled');
		if (status === 'rescheduled') return $t('bookings.filter.status.rescheduled');
		return status;
	}

	const statusFilterLabel = $derived(
		fStatus === 'confirmed' || fStatus === 'rescheduled' || fStatus === 'cancelled'
			? $t(`bookings.filter.status.${fStatus}`)
			: $t('bookings.filter.anyStatus')
	);

	function todayISO() {
		return new Date().toISOString().slice(0, 10);
	}

	// --- List/Calendar view & client-side sorting (zero backend changes) ---
	let viewMode = $state<'list' | 'calendar'>('list');
	let sortField = $state<'start_at' | 'attendee' | 'event'>('start_at');
	let sortDir = $state<'asc' | 'desc'>('asc');

	function toggleSort(field: 'start_at' | 'attendee' | 'event') {
		if (sortField === field) {
			sortDir = sortDir === 'asc' ? 'desc' : 'asc';
		} else {
			sortField = field;
			sortDir = 'asc';
		}
	}

	const sortedItems = $derived.by(() => {
		const arr = [...items];
		const mult = sortDir === 'asc' ? 1 : -1;
		arr.sort((a, b) => {
			if (sortField === 'attendee') {
				const an = (a.attendees?.[0]?.name ?? '').toLowerCase();
				const bn = (b.attendees?.[0]?.name ?? '').toLowerCase();
				return an.localeCompare(bn) * mult;
			}
			if (sortField === 'event') {
				return (a.event_type_slug ?? '').localeCompare(b.event_type_slug ?? '') * mult;
			}
			return (new Date(a.start_at).getTime() - new Date(b.start_at).getTime()) * mult;
		});
		return arr;
	});

	let calScale = $state<'week' | 'month'>('week');
	let calAnchor = $state(new Date());
	let selectedCalDay = $state(todayISO());
	let detailBooking = $state<Booking | null>(null);

	function localISO(d: Date): string {
		const y = d.getFullYear();
		const m = String(d.getMonth() + 1).padStart(2, '0');
		const day = String(d.getDate()).padStart(2, '0');
		return `${y}-${m}-${day}`;
	}

	function shiftCal(delta: number) {
		const next = new Date(calAnchor);
		if (calScale === 'week') next.setDate(next.getDate() + delta * 7);
		else next.setMonth(next.getMonth() + delta);
		calAnchor = next;
	}

	function goCalToday() {
		const now = new Date();
		calAnchor = now;
		selectedCalDay = localISO(now);
	}

	const calHeaderLabel = $derived.by(() => {
		if (calScale === 'month') {
			return calAnchor.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
		}
		const start = new Date(calAnchor);
		start.setDate(start.getDate() - start.getDay());
		const end = new Date(start);
		end.setDate(start.getDate() + 6);
		if (start.getMonth() === end.getMonth()) {
			return `${start.toLocaleDateString(undefined, { month: 'short' })} ${start.getDate()}–${end.getDate()}, ${end.getFullYear()}`;
		}
		return `${start.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} – ${end.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}`;
	});

	const weekDays = $derived.by(() => {
		const start = new Date(calAnchor);
		start.setHours(0, 0, 0, 0);
		start.setDate(start.getDate() - start.getDay());
		return Array.from({ length: 7 }, (_, i) => {
			const d = new Date(start);
			d.setDate(start.getDate() + i);
			return {
				iso: localISO(d),
				short: d.toLocaleDateString(undefined, { weekday: 'short' }),
				day: d.getDate()
			};
		});
	});

	const monthCells = $derived.by(() => {
		const y = calAnchor.getFullYear();
		const m = calAnchor.getMonth();
		const first = new Date(y, m, 1);
		const startDow = first.getDay();
		const daysInMonth = new Date(y, m + 1, 0).getDate();
		const cells: { iso: string; day: number | null }[] = [];
		for (let i = 0; i < startDow; i++) cells.push({ iso: '', day: null });
		for (let d = 1; d <= daysInMonth; d++) {
			cells.push({ iso: `${y}-${String(m + 1).padStart(2, '0')}-${String(d).padStart(2, '0')}`, day: d });
		}
		return cells;
	});

	const bookingsByDate = $derived.by(() => {
		const map = new Map<string, Booking[]>();
		for (const b of sortedItems) {
			const iso = localISO(new Date(b.start_at));
			const list = map.get(iso) ?? [];
			list.push(b);
			map.set(iso, list);
		}
		return map;
	});

	function fmtShortTime(iso: string): string {
		return new Date(iso).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
	}

	async function selectStatusTab(tab: 'upcoming' | 'past' | 'cancelled') {
		if (tab === 'cancelled') {
			fStatus = 'cancelled';
			await reload();
		} else {
			if (fStatus === 'cancelled') fStatus = '';
			timeFilter = tab;
			await reload();
		}
	}
</script>

<svelte:head><title>{$t('bookings.headTitle')}</title></svelte:head>

<div class="mb-6 flex flex-wrap items-start justify-between gap-4">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">{$t('bookings.title')}</h1>
		<p class="mt-1 text-sm text-muted-foreground">
			{scope === 'all' ? $t('bookings.subtitle.all') : $t('bookings.subtitle.mine')}
		</p>
	</div>
	<div class="flex flex-wrap items-center gap-2">
		<Button variant="outline" size="sm" onclick={refresh} disabled={refreshing} aria-label={$t('bookings.refreshAria')}>
			<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class={refreshing ? 'animate-spin' : ''}><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M3 21v-5h5"/></svg>
			{$t('bookings.refresh')}
		</Button>
		{#if isAdmin}
			<div class="inline-flex rounded-md border p-0.5">
				<button
					class="rounded px-3 py-1 text-sm font-medium transition-colors {scope === 'mine' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
					onclick={() => setScope('mine')}
				>{$t('bookings.scope.mine')}</button>
				<button
					class="rounded px-3 py-1 text-sm font-medium transition-colors {scope === 'all' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
					onclick={() => setScope('all')}
				>{$t('bookings.scope.all')}</button>
			</div>
		{/if}
		<!-- List / Calendar view switcher -->
		<div class="inline-flex rounded-md border p-0.5" role="group" aria-label="View mode">
			<button
				type="button"
				onclick={() => (viewMode = 'list')}
				class="inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-sm font-medium transition-colors {viewMode === 'list' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
				aria-label="List view"
			>
				<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>
				<span class="hidden sm:inline">List</span>
			</button>
			<button
				type="button"
				onclick={() => (viewMode = 'calendar')}
				class="inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-sm font-medium transition-colors {viewMode === 'calendar' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}"
				aria-label="Calendar view"
			>
				<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
				<span class="hidden sm:inline">Calendar</span>
			</button>
		</div>
	</div>
</div>

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

<!-- "No bookings yet" is a claim about the workspace, so it must not be shown when the
     request failed and the counts are simply unknown - that reads as data loss. -->
{#if counts.upcoming === 0 && counts.past === 0 && !hasFilters && !loading && !error && viewMode === 'list'}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">{$t('bookings.empty.title')}</p>
		<p class="mt-1 text-sm text-muted-foreground">{$t('bookings.empty.description')}</p>
	</div>
{:else}
	<div class="mb-4 flex flex-wrap items-center gap-2">
		<div class="inline-flex flex-wrap rounded-md border p-0.5 text-sm">
			<button type="button" class="rounded px-3 py-1 transition-colors {timeFilter === 'upcoming' && fStatus !== 'cancelled' ? 'bg-muted font-medium' : 'text-muted-foreground hover:text-foreground'}" onclick={() => selectStatusTab('upcoming')}>{$t('bookings.filter.upcoming', { count: counts.upcoming })}</button>
			<button type="button" class="rounded px-3 py-1 transition-colors {timeFilter === 'past' && fStatus !== 'cancelled' ? 'bg-muted font-medium' : 'text-muted-foreground hover:text-foreground'}" onclick={() => selectStatusTab('past')}>{$t('bookings.filter.past', { count: counts.past })}</button>
			<button type="button" class="rounded px-3 py-1 transition-colors {fStatus === 'cancelled' ? 'bg-muted font-medium' : 'text-muted-foreground hover:text-foreground'}" onclick={() => selectStatusTab('cancelled')}>Canceled</button>
		</div>

		<div class="ml-auto flex flex-wrap items-center gap-2">
			{#if viewMode === 'list'}
				<Button
					variant="outline"
					size="sm"
					class="h-9 gap-1.5 text-xs"
					onclick={() => toggleSort('start_at')}
					title="Toggle sort order"
				>
					<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m3 16 4 4 4-4"/><path d="M7 20V4"/><path d="m21 8-4-4-4 4"/><path d="M17 4v16"/></svg>
					{sortField === 'start_at' ? (sortDir === 'asc' ? 'Soonest' : 'Latest') : sortField === 'attendee' ? `Name (${sortDir})` : `Event (${sortDir})`}
				</Button>
			{/if}
			<Select.Root type="single" bind:value={fEventType} onValueChange={reload}>
				<Select.Trigger class="h-9 w-[170px]" aria-label={$t('bookings.filter.eventTypeAria')}>
					{fEventType ? eventTypeName(fEventType) : $t('bookings.filter.allEventTypes')}
				</Select.Trigger>
				<Select.Content>
					<Select.Item value="" label={$t('bookings.filter.allEventTypes')}>{$t('bookings.filter.allEventTypes')}</Select.Item>
					{#each eventTypes as et}
						<Select.Item value={et.slug} label={et.name}>{et.name}</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>

			{#if isAdmin && scope === 'all'}
				<Select.Root type="single" bind:value={fHost} onValueChange={reload}>
					<Select.Trigger class="h-9 w-[150px]" aria-label={$t('bookings.filter.hostAria')}>
						{members.find((m) => m.id === fHost)?.name ?? $t('bookings.filter.allHosts')}
					</Select.Trigger>
					<Select.Content>
						<Select.Item value="" label={$t('bookings.filter.allHosts')}>{$t('bookings.filter.allHosts')}</Select.Item>
						{#each members as m}
							<Select.Item value={m.id} label={m.name}>{m.name}</Select.Item>
						{/each}
					</Select.Content>
				</Select.Root>

				{#if teams.length > 0}
					<Select.Root type="single" bind:value={fTeam} onValueChange={reload}>
						<Select.Trigger class="h-9 w-[140px]" aria-label={$t('bookings.filter.teamAria')}>
							{teams.find((tm) => tm.id === fTeam)?.name ?? $t('bookings.filter.allTeams')}
						</Select.Trigger>
						<Select.Content>
							<Select.Item value="" label={$t('bookings.filter.allTeams')}>{$t('bookings.filter.allTeams')}</Select.Item>
							{#each teams as tm}
								<Select.Item value={tm.id} label={tm.name}>{tm.name}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				{/if}
			{/if}

			<Select.Root type="single" bind:value={fStatus} onValueChange={reload}>
				<Select.Trigger class="h-9 w-[140px]" aria-label={$t('bookings.filter.statusAria')}>
					{statusFilterLabel}
				</Select.Trigger>
				<Select.Content>
					<Select.Item value="" label={$t('bookings.filter.anyStatus')}>{$t('bookings.filter.anyStatus')}</Select.Item>
					<Select.Item value="confirmed" label={$t('bookings.filter.status.confirmed')}>{$t('bookings.filter.status.confirmed')}</Select.Item>
					<Select.Item value="rescheduled" label={$t('bookings.filter.status.rescheduled')}>{$t('bookings.filter.status.rescheduled')}</Select.Item>
					<Select.Item value="cancelled" label={$t('bookings.filter.status.cancelled')}>{$t('bookings.filter.status.cancelled')}</Select.Item>
				</Select.Content>
			</Select.Root>

			{#if hasFilters}
				<Button variant="ghost" size="sm" onclick={clearFilters}>{$t('common.clear')}</Button>
			{/if}
		</div>
	</div>

	{#if loading}
		<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
	{:else if viewMode === 'calendar'}
		<div class="rounded-lg border bg-card overflow-hidden">
			<!-- Calendar toolbar -->
			<div class="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-3">
				<div class="flex items-center gap-2">
					<Button variant="outline" size="sm" onclick={goCalToday}>Today</Button>
					<div class="inline-flex rounded-md border">
						<button type="button" class="px-2 py-1 text-muted-foreground hover:bg-muted hover:text-foreground" onclick={() => shiftCal(-1)} aria-label="Previous period">
							<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>
						</button>
						<button type="button" class="border-l px-2 py-1 text-muted-foreground hover:bg-muted hover:text-foreground" onclick={() => shiftCal(1)} aria-label="Next period">
							<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>
						</button>
					</div>
					<span class="text-sm font-semibold">{calHeaderLabel}</span>
				</div>
				<div class="inline-flex rounded-md border p-0.5 text-xs">
					<button type="button" class="rounded px-2.5 py-1 font-medium transition-colors {calScale === 'week' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (calScale = 'week')}>Week</button>
					<button type="button" class="rounded px-2.5 py-1 font-medium transition-colors {calScale === 'month' ? 'bg-secondary text-secondary-foreground' : 'text-muted-foreground hover:text-foreground'}" onclick={() => (calScale = 'month')}>Month</button>
				</div>
			</div>

			{#if calScale === 'week'}
				<div class="grid grid-cols-1 divide-y sm:grid-cols-7 sm:divide-x sm:divide-y-0">
					{#each weekDays as wd}
						{@const dayBookings = bookingsByDate.get(wd.iso) ?? []}
						{@const isToday = wd.iso === localISO(new Date())}
						<div class="min-h-[120px] sm:min-h-[320px] p-2.5 flex flex-col gap-1.5">
							<div class="flex items-center justify-between border-b pb-1.5 text-xs">
								<span class="font-medium text-muted-foreground">{wd.short}</span>
								<span class="inline-flex h-6 w-6 items-center justify-center rounded-full font-semibold {isToday ? 'bg-primary text-primary-foreground' : 'text-foreground'}">{wd.day}</span>
							</div>
							{#if dayBookings.length === 0}
								<p class="py-2 text-[11px] text-muted-foreground/60 sm:text-center">No bookings</p>
							{:else}
								{#each dayBookings as b}
									<button
										type="button"
										onclick={() => (detailBooking = b)}
										class="w-full rounded-md border px-2 py-1.5 text-left text-xs transition-colors hover:bg-muted/60 {b.status === 'cancelled' ? 'opacity-50 line-through' : 'bg-primary/5 border-primary/20'}"
									>
										<div class="font-semibold truncate">{fmtShortTime(b.start_at)} · {b.attendees?.[0]?.name ?? 'Guest'}</div>
										<div class="truncate text-[11px] text-muted-foreground font-mono">{b.event_type_slug}</div>
									</button>
								{/each}
							{/if}
						</div>
					{/each}
				</div>
			{:else}
				<div class="grid grid-cols-7 border-b bg-muted/20 text-center text-[11px] font-medium text-muted-foreground">
					{#each ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'] as wd}
						<div class="py-2">{wd}</div>
					{/each}
				</div>
				<div class="grid grid-cols-7 divide-x divide-y">
					{#each monthCells as cell}
						{#if cell.day === null}
							<div class="min-h-[64px] sm:min-h-[104px] bg-muted/10"></div>
						{:else}
							{@const dayBookings = bookingsByDate.get(cell.iso) ?? []}
							{@const isToday = cell.iso === localISO(new Date())}
							{@const isSelected = cell.iso === selectedCalDay}
							<div
								role="button"
								tabindex="0"
								onclick={() => (selectedCalDay = cell.iso)}
								onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') selectedCalDay = cell.iso; }}
								class="min-h-[64px] sm:min-h-[104px] p-1.5 text-left transition-colors hover:bg-muted/30 cursor-pointer {isSelected ? 'bg-muted/40' : ''}"
							>
								<div class="mb-1 flex items-center justify-between">
									<span class="inline-flex h-6 w-6 items-center justify-center rounded-full text-xs font-medium {isToday ? 'bg-primary text-primary-foreground' : 'text-foreground'}">{cell.day}</span>
									{#if dayBookings.length > 0}
										<span class="sm:hidden h-1.5 w-1.5 rounded-full bg-primary"></span>
									{/if}
								</div>
								<div class="hidden sm:flex flex-col gap-1">
									{#each dayBookings.slice(0, 3) as b}
										<button
											type="button"
											onclick={(e) => { e.stopPropagation(); detailBooking = b; }}
											class="w-full truncate rounded px-1.5 py-0.5 text-left text-[11px] font-medium transition-colors {b.status === 'cancelled' ? 'bg-muted text-muted-foreground line-through' : 'bg-primary/10 text-primary hover:bg-primary/20'}"
										>
											{fmtShortTime(b.start_at)} {b.attendees?.[0]?.name ?? 'Guest'}
										</button>
									{/each}
									{#if dayBookings.length > 3}
										<span class="px-1 text-[10px] text-muted-foreground">+{dayBookings.length - 3} more</span>
									{/if}
								</div>
							</div>
						{/if}
					{/each}
				</div>
				<!-- Mobile selected-day list -->
				<div class="border-t p-3 sm:hidden">
					<p class="mb-2 text-xs font-semibold text-muted-foreground">Bookings on {selectedCalDay}</p>
					{#if (bookingsByDate.get(selectedCalDay) ?? []).length === 0}
						<p class="text-xs text-muted-foreground">No bookings on this date.</p>
					{:else}
						<div class="space-y-1.5">
							{#each bookingsByDate.get(selectedCalDay) ?? [] as b}
								<button
									type="button"
									onclick={() => (detailBooking = b)}
									class="w-full rounded-md border p-2 text-left text-xs hover:bg-muted/50"
								>
									<div class="font-medium">{fmtShortTime(b.start_at)} · {b.attendees?.[0]?.name ?? 'Guest'}</div>
									<div class="text-[11px] text-muted-foreground">{b.event_type_slug} · {bookingStatusLabel(b.status)}</div>
								</button>
							{/each}
						</div>
					{/if}
				</div>
			{/if}
		</div>
	{:else if items.length === 0}
		<div class="rounded-lg border border-dashed bg-card p-12 text-center">
			<p class="text-sm text-muted-foreground">
				{hasFilters ? $t('bookings.filter.noMatch') : timeFilter === 'upcoming' ? $t('bookings.empty.noUpcoming') : $t('bookings.empty.noPast')}
			</p>
			{#if hasFilters}
				<Button variant="outline" size="sm" class="mt-3" onclick={clearFilters}>{$t('common.clearFilters')}</Button>
			{/if}
		</div>
	{:else}
	<!-- Mobile card list (< md) -->
	<div class="divide-y rounded-lg border bg-card md:hidden">
		{#each sortedItems as b}
			<div class="p-4 space-y-2.5">
				<div class="flex items-start justify-between gap-2">
					<div class="min-w-0">
						{#if b.attendees && b.attendees.length > 0}
							<div class="font-medium truncate">{b.attendees[0].name}</div>
							<div class="text-xs text-muted-foreground truncate">{b.attendees[0].email}</div>
						{:else}
							<span class="text-muted-foreground">{$t('common.emDash')}</span>
						{/if}
					</div>
					{#if b.status === 'confirmed'}
						<Badge class="shrink-0 bg-green-50 text-green-700 border-green-200">{bookingStatusLabel(b.status)}</Badge>
					{:else if b.status === 'cancelled'}
						<Badge variant="destructive" class="shrink-0 bg-destructive/10 text-destructive border-transparent">{bookingStatusLabel(b.status)}</Badge>
					{:else}
						<Badge variant="secondary" class="shrink-0">{bookingStatusLabel(b.status)}</Badge>
					{/if}
				</div>
				<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
					<span>{fmt(b.start_at)}</span>
					<span class="font-mono">{b.event_type_slug ?? $t('common.emDash')}</span>
					{#if scope === 'all' && b.host_name}<span>Host: {b.host_name}</span>{/if}
				</div>
				<div class="flex flex-wrap items-center gap-2 pt-1">
					<Button variant="outline" size="sm" class="h-8 text-xs" onclick={() => toggleExpand(b.id)}>
						{expandedId === b.id ? 'Hide details' : 'Details'}
					</Button>
					{#if b.status === 'confirmed'}
						{#if reschedulingId === b.id}
							<Button variant="outline" size="sm" class="h-8 text-xs" onclick={cancelReschedule}>{$t('bookings.cancelReschedule')}</Button>
						{:else}
							<Button variant="outline" size="sm" class="h-8 text-xs" onclick={() => startReschedule(b)}>Reschedule</Button>
							<Button variant="ghost" size="sm" class="h-8 text-xs text-destructive hover:text-destructive" onclick={() => requestCancel(b.id)}>Cancel</Button>
						{/if}
					{/if}
				</div>
				{#if expandedId === b.id}
					<div class="rounded-md border bg-muted/20 p-3 text-xs space-y-1.5">
						<div><span class="font-medium">{$t('bookings.details.bookedOn')}:</span> {fmt(b.created_at)}</div>
						{#if b.location_value}
							<div class="break-all"><span class="font-medium">{$t('bookings.details.location')}:</span> {b.location_value}</div>
						{/if}
						{#if answersLoading[b.id]}
							<p class="text-muted-foreground">{$t('bookings.intake.loading')}</p>
						{:else if answersCache[b.id]?.length}
							{#each answersCache[b.id] as a}
								<div><span class="font-medium">{a.label}:</span> {a.value || $t('common.emDash')}</div>
							{/each}
						{/if}
					</div>
				{/if}
				{#if reschedulingId === b.id}
					<div class="rounded-md border bg-muted/30 p-3 space-y-2.5">
						<p class="text-xs font-medium">{$t('bookings.reschedule.title', { name: b.attendees?.[0]?.name ?? $t('common.attendeeFallback') })}</p>
						<DatePicker bind:value={rescheduleDate} placeholder={$t('bookings.reschedule.pickDate')} minToday class="w-full" />
						{#if slotsLoading}<p class="text-xs text-muted-foreground">{$t('bookings.reschedule.loadingSlots')}</p>
						{:else if slotsError}<p class="text-xs text-destructive">{slotsError}</p>
						{:else if slots.length > 0}
							<div class="flex flex-wrap gap-1.5">
								{#each slots as slot}
									<button onclick={() => (selectedSlot = slot.start)} class="rounded-md px-2.5 py-1 text-xs font-medium {selectedSlot === slot.start ? 'bg-primary text-primary-foreground' : 'border bg-background'}">{fmtSlotTime(slot.start)}</button>
								{/each}
							</div>
						{/if}
						{#if selectedSlot}
							<div class="flex gap-2">
								<Button size="sm" onclick={confirmReschedule} disabled={rescheduling}>{rescheduling ? $t('bookings.reschedule.inProgress') : $t('bookings.reschedule.confirm', { time: fmtSlotTime(selectedSlot) })}</Button>
								<Button size="sm" variant="outline" onclick={cancelReschedule}>{$t('common.cancel')}</Button>
							</div>
						{/if}
					</div>
				{/if}
			</div>
		{/each}
	</div>

	<!-- Desktop table (>= md) -->
	<div class="hidden md:block rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">
						<button type="button" class="inline-flex items-center gap-1 hover:text-foreground" onclick={() => toggleSort('attendee')}>
							{$t('bookings.table.attendee')}
							{#if sortField === 'attendee'}<span>{sortDir === 'asc' ? '↑' : '↓'}</span>{/if}
						</button>
					</th>
					{#if scope === 'all'}<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('bookings.table.host')}</th>{/if}
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">
						<button type="button" class="inline-flex items-center gap-1 hover:text-foreground" onclick={() => toggleSort('event')}>
							{$t('bookings.table.event')}
							{#if sortField === 'event'}<span>{sortDir === 'asc' ? '↑' : '↓'}</span>{/if}
						</button>
					</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">
						<button type="button" class="inline-flex items-center gap-1 hover:text-foreground" onclick={() => toggleSort('start_at')}>
							{$t('bookings.table.startTime')}
							{#if sortField === 'start_at'}<span>{sortDir === 'asc' ? '↑' : '↓'}</span>{/if}
						</button>
					</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('bookings.table.status')}</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				{#each sortedItems as b}
					<tr class="transition-colors hover:bg-muted/30">
						<td class="px-4 py-3">
							{#if b.attendees && b.attendees.length > 0}
								<div class="font-medium">{b.attendees[0].name}</div>
								<div class="text-xs text-muted-foreground">{b.attendees[0].email}</div>
							{:else}
								<span class="text-muted-foreground">{$t('common.emDash')}</span>
							{/if}
						</td>
						{#if scope === 'all'}<td class="px-4 py-3 text-muted-foreground">{b.host_name || $t('common.emDash')}</td>{/if}
						<td class="px-4 py-3 font-mono text-xs text-muted-foreground">{b.event_type_slug ?? $t('common.emDash')}</td>
						<td class="px-4 py-3 text-muted-foreground">{fmt(b.start_at)}</td>
						<td class="px-4 py-3">
							<div class="flex flex-wrap items-center gap-1.5">
								{#if b.status === 'confirmed'}
									<Badge class="bg-green-50 text-green-700 border-green-200">{bookingStatusLabel(b.status)}</Badge>
								{:else if b.status === 'cancelled'}
									<Badge variant="destructive" class="bg-destructive/10 text-destructive border-transparent">{bookingStatusLabel(b.status)}</Badge>
								{:else}
									<Badge variant="secondary">{bookingStatusLabel(b.status)}</Badge>
								{/if}
								{#if b.payment_status === 'paid'}
									<Badge class="border-emerald-200 bg-emerald-50 text-emerald-700">{fmtMoney(b.amount_paid_cents, b.amount_paid_currency)}</Badge>
								{:else if b.payment_status === 'refunded'}
									<Badge variant="secondary" class="text-muted-foreground">{$t('bookings.badge.refunded')}</Badge>
								{:else if b.payment_status === 'pending'}
									<Badge class="border-amber-200 bg-amber-50 text-amber-700">{$t('bookings.badge.unpaid')}</Badge>
								{/if}
								{#if b.status === 'confirmed' && b.rsvp_status === 'accepted'}
									<Badge class="border-green-200 bg-green-50 text-green-700">{$t('bookings.badge.guestAccepted')}</Badge>
								{:else if b.status === 'confirmed' && b.rsvp_status === 'tentative'}
									<Badge class="border-amber-200 bg-amber-50 text-amber-700">{$t('bookings.badge.guestMaybe')}</Badge>
								{:else if b.status === 'confirmed' && b.rsvp_status === 'declined'}
									<Badge class="border-transparent bg-destructive/10 text-destructive">{$t('bookings.badge.guestDeclined')}</Badge>
								{/if}
							</div>
						</td>
						<td class="px-4 py-3">
							<Tooltip.Provider>
								<div class="flex items-center justify-end gap-1">
									<Tooltip.Root>
										<Tooltip.Trigger
											class={buttonVariants({ variant: 'ghost', size: 'icon' })}
											onclick={() => toggleExpand(b.id)}
											aria-expanded={expandedId === b.id}
										>
											<svg
												xmlns="http://www.w3.org/2000/svg" width="16" height="16"
												viewBox="0 0 24 24" fill="none" stroke="currentColor"
												stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
												style="transition:transform .15s;transform:rotate({expandedId === b.id ? 180 : 0}deg)"
											><polyline points="6 9 12 15 18 9"/></svg>
										</Tooltip.Trigger>
										<Tooltip.Content>{expandedId === b.id ? $t('bookings.tooltip.hideResponses') : $t('bookings.tooltip.showResponses')}</Tooltip.Content>
									</Tooltip.Root>

									{#if b.status === 'confirmed'}
										{#if reschedulingId === b.id}
											<Button variant="outline" size="sm" onclick={cancelReschedule}>
												{$t('bookings.cancelReschedule')}
											</Button>
										{:else}
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => startReschedule(b)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>{$t('bookings.tooltip.reschedule')}</Tooltip.Content>
											</Tooltip.Root>

											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => requestCancel(b.id)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>{$t('bookings.tooltip.cancel')}</Tooltip.Content>
											</Tooltip.Root>
										{/if}
									{/if}
								</div>
							</Tooltip.Provider>
						</td>
					</tr>

					{#if expandedId === b.id}
						<tr>
							<td colspan={scope === 'all' ? 6 : 5} class="p-0">
								<div class="border-t bg-muted/20 px-4 py-3">
									<p class="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('bookings.details.title')}</p>
									<dl class="mb-3 space-y-1.5 text-sm">
										<div class="flex gap-4">
											<dt class="w-48 shrink-0 font-medium text-foreground">{$t('bookings.details.bookedOn')}</dt>
											<dd class="text-muted-foreground">{fmt(b.created_at)}</dd>
										</div>
										{#if b.payment_status}
											<div class="flex gap-4">
												<dt class="w-48 shrink-0 font-medium text-foreground">{$t('bookings.details.payment')}</dt>
												<dd class="text-muted-foreground">
													{payLabel[b.payment_status] ?? b.payment_status}{#if b.amount_paid_cents} · {fmtMoney(b.amount_paid_cents, b.amount_paid_currency)}{/if}
												</dd>
											</div>
										{/if}
										{#if b.location_value}
											<div class="flex gap-4">
												<dt class="w-48 shrink-0 font-medium text-foreground">{$t('bookings.details.location')}</dt>
												<dd class="break-all text-muted-foreground">
													{#if /^https?:/.test(b.location_value)}
														<a href={b.location_value} target="_blank" rel="noopener noreferrer" class="text-primary underline">{b.location_value}</a>
													{:else}{b.location_value}{/if}
												</dd>
											</div>
										{/if}
									</dl>
									<p class="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">{$t('bookings.intake.title')}</p>
									{#if answersLoading[b.id]}
										<p class="text-sm text-muted-foreground">{$t('bookings.intake.loading')}</p>
									{:else if !answersCache[b.id] || answersCache[b.id].length === 0}
										<p class="text-sm text-muted-foreground">{$t('bookings.intake.empty')}</p>
									{:else}
										<dl class="space-y-2">
											{#each answersCache[b.id] as a}
												<div class="flex gap-4 text-sm">
													<dt class="w-48 shrink-0 font-medium text-foreground">{a.label}</dt>
													<dd class="text-muted-foreground {a.type !== 'checkbox' ? 'whitespace-pre-wrap' : ''}">
														{#if a.type === 'checkbox'}
															<!-- Liberal comparison on purpose. Checkbox answers are canonicalised to
															     "yes"/"no" on the way in now, but rows created before that landed hold
															     whatever the surface sent - the embed widget sent "Yes". A strict
															     === 'yes' renders those as "No", i.e. the opposite of what the guest
															     ticked, which matters when the question is a consent checkbox. -->
															{['yes', 'true', '1', 'on', 'checked'].includes(String(a.value).trim().toLowerCase()) ? $t('common.yes') : $t('common.no')}
														{:else}
															{a.value || $t('common.emDash')}
														{/if}
													</dd>
												</div>
											{/each}
										</dl>
									{/if}
								</div>
							</td>
						</tr>
					{/if}

					{#if reschedulingId === b.id}
						<tr>
							<td colspan={scope === 'all' ? 6 : 5} class="p-0">
								<div class="border-t bg-muted/30 px-4 py-4">
									<p class="mb-3 text-sm font-medium">{$t('bookings.reschedule.title', { name: b.attendees?.[0]?.name ?? $t('common.attendeeFallback') })}</p>

									<div class="flex flex-wrap items-end gap-3">
										<div class="space-y-1.5">
											<p class="text-sm font-medium">{$t('bookings.reschedule.newDate')}</p>
											<DatePicker
												bind:value={rescheduleDate}
												placeholder={$t('bookings.reschedule.pickDate')}
												minToday
												class="w-[180px]"
											/>
										</div>

										{#if slotsLoading}
											<p class="pb-1 text-sm text-muted-foreground">{$t('bookings.reschedule.loadingSlots')}</p>
										{:else if slotsError}
											<p class="rounded-md bg-destructive/10 px-3 py-1.5 text-sm text-destructive">{slotsError}</p>
										{:else if rescheduleDate && slots.length === 0}
											<p class="pb-1 text-sm text-muted-foreground">{$t('bookings.reschedule.noSlots')}</p>
										{/if}
									</div>

									{#if slots.length > 0}
										<div class="mt-3 flex flex-wrap gap-2">
											{#each slots as slot}
												<button
													onclick={() => (selectedSlot = slot.start)}
													class="inline-flex items-center justify-center rounded-md px-3 py-1.5 text-xs font-medium transition-colors {selectedSlot === slot.start ? 'bg-primary text-primary-foreground hover:bg-primary/90' : 'border bg-background hover:bg-accent hover:text-accent-foreground'}"
												>
													{fmtSlotTime(slot.start)}
												</button>
											{/each}
										</div>
									{/if}

									{#if rescheduleError}
										<p class="mt-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{rescheduleError}</p>
									{/if}

									{#if selectedSlot}
										<div class="mt-4 flex gap-2">
											<Button onclick={confirmReschedule} disabled={rescheduling}>
												{rescheduling ? $t('bookings.reschedule.inProgress') : $t('bookings.reschedule.confirm', { time: fmtSlotTime(selectedSlot) })}
											</Button>
											<Button variant="outline" onclick={cancelReschedule}>
												{$t('common.cancel')}
											</Button>
										</div>
									{/if}
								</div>
							</td>
						</tr>
					{/if}
				{/each}
			</tbody>
		</table>
	</div>
	{#if total > PAGE_SIZE}
		<div class="mt-4 flex items-center justify-between gap-4">
			<p class="text-sm text-muted-foreground">{$t('bookings.pagination', { start: pageStart, end: pageEnd, total })}</p>
			<div class="flex items-center gap-2">
				<Button variant="outline" size="sm" disabled={offset === 0} onclick={() => goTo(offset - PAGE_SIZE)}>
					{$t('common.previous')}
				</Button>
				<Button variant="outline" size="sm" disabled={pageEnd >= total} onclick={() => goTo(offset + PAGE_SIZE)}>
					{$t('common.next')}
				</Button>
			</div>
		</div>
	{/if}
	{/if}
{/if}

<Dialog.Root open={!!detailBooking} onOpenChange={(open) => { if (!open) detailBooking = null; }}>
	<Dialog.Content class="w-[calc(100vw-2rem)] max-w-md sm:max-w-md">
		{#if detailBooking}
			<Dialog.Header>
				<Dialog.Title>{detailBooking.attendees?.[0]?.name ?? 'Booking details'}</Dialog.Title>
				<Dialog.Description>{detailBooking.attendees?.[0]?.email ?? ''}</Dialog.Description>
			</Dialog.Header>
			<div class="space-y-3 py-2 text-sm">
				<div class="flex justify-between gap-2 border-b pb-2">
					<span class="text-muted-foreground">Event</span>
					<span class="font-mono text-xs">{detailBooking.event_type_slug}</span>
				</div>
				<div class="flex justify-between gap-2 border-b pb-2">
					<span class="text-muted-foreground">Time</span>
					<span>{fmt(detailBooking.start_at)}</span>
				</div>
				<div class="flex justify-between gap-2 border-b pb-2">
					<span class="text-muted-foreground">Status</span>
					<span>{bookingStatusLabel(detailBooking.status)}</span>
				</div>
				{#if detailBooking.location_value}
					<div class="flex justify-between gap-2">
						<span class="text-muted-foreground">Location</span>
						<span class="truncate text-right">{detailBooking.location_value}</span>
					</div>
				{/if}
			</div>
			<Dialog.Footer class="flex-wrap gap-2">
				{#if detailBooking.status === 'confirmed'}
					<Button
						variant="outline"
						onclick={() => {
							const b = detailBooking!;
							detailBooking = null;
							viewMode = 'list';
							startReschedule(b);
						}}
					>
						Reschedule
					</Button>
					<Button
						variant="destructive"
						onclick={() => {
							const id = detailBooking!.id;
							detailBooking = null;
							requestCancel(id);
						}}
					>
						Cancel booking
					</Button>
				{/if}
				<Button variant="ghost" onclick={() => (detailBooking = null)}>Close</Button>
			</Dialog.Footer>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<ConfirmDialog
	bind:open={confirmOpen}
	title={$t('bookings.confirmCancel.title')}
	description={$t('bookings.confirmCancel.description')}
	confirmText={$t('bookings.confirmCancel.confirm')}
	cancelText={$t('bookings.confirmCancel.cancel')}
	destructive
	onConfirm={cancel}
/>
