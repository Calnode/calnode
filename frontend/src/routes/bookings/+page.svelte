<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Booking } from '$lib/api';
	import { prefs, fmtDateTime, fmtTime } from '$lib/prefs';
	import { Button, buttonVariants } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import * as Tooltip from '$lib/components/ui/tooltip';
	import { DatePicker } from '$lib/components/ui/date-picker';

	let items: Booking[] = $state([]);
	let loading = $state(true);
	let error = $state('');

	let reschedulingId: string | null = $state(null);
	let reschedulingSlug = $state('');
	let rescheduleDate = $state('');
	let slots: { start: string; end: string }[] = $state([]);
	let slotsLoading = $state(false);
	let slotsError = $state('');
	let selectedSlot = $state('');
	let rescheduling = $state(false);
	let rescheduleError = $state('');

	type AnswerItem = { label: string; type: string; value: string };
	let expandedId: string | null = $state(null);
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

	async function load() {
		try {
			const res = await api.get<{ items: Booking[] }>('/v1/bookings');
			items = res.items;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function cancel(id: string) {
		if (!confirm('Cancel this booking?')) return;
		try {
			await api.post(`/v1/bookings/${id}/cancel`, { reason: 'cancelled by admin' });
			await load();
		} catch (e: any) {
			error = e.message;
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

	function todayISO() {
		return new Date().toISOString().slice(0, 10);
	}
</script>

<svelte:head><title>Bookings — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Bookings</h1>
	<p class="mt-1 text-sm text-muted-foreground">All scheduled and past meetings.</p>
</div>

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if items.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">No bookings yet</p>
		<p class="mt-1 text-sm text-muted-foreground">Bookings will appear here once attendees schedule time with you.</p>
	</div>
{:else}
	<div class="rounded-lg border bg-card overflow-hidden">
		<table class="w-full text-sm">
			<thead>
				<tr class="border-b">
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Attendee</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Event</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Start time</th>
					<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Status</th>
					<th class="px-4 pb-3 pt-3"></th>
				</tr>
			</thead>
			<tbody class="divide-y">
				{#each items as b}
					<tr class="transition-colors hover:bg-muted/30">
						<td class="px-4 py-3">
							{#if b.attendees && b.attendees.length > 0}
								<div class="font-medium">{b.attendees[0].name}</div>
								<div class="text-xs text-muted-foreground">{b.attendees[0].email}</div>
							{:else}
								<span class="text-muted-foreground">—</span>
							{/if}
						</td>
						<td class="px-4 py-3 font-mono text-xs text-muted-foreground">{b.event_type_slug ?? '—'}</td>
						<td class="px-4 py-3 text-muted-foreground">{fmt(b.start_at)}</td>
						<td class="px-4 py-3">
							{#if b.status === 'confirmed'}
								<Badge class="bg-green-50 text-green-700 border-green-200">{b.status}</Badge>
							{:else if b.status === 'cancelled'}
								<Badge variant="destructive" class="bg-destructive/10 text-destructive border-transparent">{b.status}</Badge>
							{:else}
								<Badge variant="secondary">{b.status}</Badge>
							{/if}
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
										<Tooltip.Content>{expandedId === b.id ? 'Hide responses' : 'Show responses'}</Tooltip.Content>
									</Tooltip.Root>

									{#if b.status === 'confirmed'}
										{#if reschedulingId === b.id}
											<Button variant="outline" size="sm" onclick={cancelReschedule}>
												Cancel reschedule
											</Button>
										{:else}
											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => startReschedule(b)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Reschedule</Tooltip.Content>
											</Tooltip.Root>

											<Tooltip.Root>
												<Tooltip.Trigger
													class={buttonVariants({ variant: 'ghost', size: 'icon' })}
													onclick={() => cancel(b.id)}
												>
													<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
												</Tooltip.Trigger>
												<Tooltip.Content>Cancel booking</Tooltip.Content>
											</Tooltip.Root>
										{/if}
									{/if}
								</div>
							</Tooltip.Provider>
						</td>
					</tr>

					{#if expandedId === b.id}
						<tr>
							<td colspan="5" class="p-0">
								<div class="border-t bg-muted/20 px-4 py-3">
									<p class="mb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Intake responses</p>
									{#if answersLoading[b.id]}
										<p class="text-sm text-muted-foreground">Loading…</p>
									{:else if !answersCache[b.id] || answersCache[b.id].length === 0}
										<p class="text-sm text-muted-foreground">No intake responses for this booking.</p>
									{:else}
										<dl class="space-y-2">
											{#each answersCache[b.id] as a}
												<div class="flex gap-4 text-sm">
													<dt class="w-48 shrink-0 font-medium text-foreground">{a.label}</dt>
													<dd class="text-muted-foreground">
														{#if a.type === 'checkbox'}
															{a.value === 'yes' ? 'Yes' : 'No'}
														{:else}
															{a.value || '—'}
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
							<td colspan="5" class="p-0">
								<div class="border-t bg-muted/30 px-4 py-4">
									<p class="mb-3 text-sm font-medium">Reschedule — {b.attendees?.[0]?.name ?? 'attendee'}</p>

									<div class="flex flex-wrap items-end gap-3">
										<div class="space-y-1.5">
											<p class="text-sm font-medium">New date</p>
											<DatePicker
												bind:value={rescheduleDate}
												placeholder="Pick a date"
												minToday
												class="w-[180px]"
											/>
										</div>

										{#if slotsLoading}
											<p class="pb-1 text-sm text-muted-foreground">Loading slots…</p>
										{:else if slotsError}
											<p class="rounded-md bg-destructive/10 px-3 py-1.5 text-sm text-destructive">{slotsError}</p>
										{:else if rescheduleDate && slots.length === 0}
											<p class="pb-1 text-sm text-muted-foreground">No available slots on this date.</p>
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
												{rescheduling ? 'Rescheduling…' : `Confirm — ${fmtSlotTime(selectedSlot)}`}
											</Button>
											<Button variant="outline" onclick={cancelReschedule}>
												Cancel
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
{/if}
