<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Booking } from '$lib/api';
	import { prefs, fmtDateTime, fmtTime } from '$lib/prefs';

	let items: Booking[] = [];
	let loading = true;
	let error = '';

	// Reschedule state
	let reschedulingId: string | null = null;
	let reschedulingSlug = '';
	let rescheduleDate = '';
	let slots: { start: string; end: string }[] = [];
	let slotsLoading = false;
	let slotsError = '';
	let selectedSlot = '';
	let rescheduling = false;
	let rescheduleError = '';

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

	async function loadSlots() {
		if (!rescheduleDate) return;
		slotsLoading = true;
		slotsError = '';
		slots = [];
		selectedSlot = '';
		try {
			const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
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

	// Minimum selectable date: today (can't reschedule to the past)
	function todayISO() {
		return new Date().toISOString().slice(0, 10);
	}
</script>

<svelte:head><title>Bookings — Calnode</title></svelte:head>

<div class="page-header">
	<h1>Bookings</h1>
</div>

{#if error}<div class="error-msg">{error}</div>{/if}

{#if loading}
	<div style="color:var(--text-muted);padding:24px 0;">Loading…</div>
{:else if items.length === 0}
	<div class="card empty-state">
		<strong>No bookings yet</strong>
		<p>Bookings will appear here once attendees schedule time with you.</p>
	</div>
{:else}
	<div class="card" style="padding:0;overflow:hidden;">
		<table>
			<thead>
				<tr>
					<th>Attendee</th>
					<th>Event</th>
					<th>Start time</th>
					<th>Status</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				{#each items as b}
					<tr>
						<td>
							{#if b.attendees && b.attendees.length > 0}
								<div style="font-weight:500;">{b.attendees[0].name}</div>
								<div style="color:var(--text-muted);font-size:12px;">{b.attendees[0].email}</div>
							{:else}
								<span style="color:var(--text-muted);">—</span>
							{/if}
						</td>
						<td class="mono">{b.event_type_slug ?? '—'}</td>
						<td>{fmt(b.start_at)}</td>
						<td>
							<span
								class="badge"
								class:badge-green={b.status === 'confirmed'}
								class:badge-red={b.status === 'cancelled'}
							>
								{b.status}
							</span>
						</td>
						<td style="text-align:right;white-space:nowrap;">
							{#if b.status === 'confirmed'}
								{#if reschedulingId === b.id}
									<button class="btn-secondary btn-sm" on:click={cancelReschedule}>
										Cancel reschedule
									</button>
								{:else}
									<button
										class="btn-secondary btn-sm"
										style="margin-right:6px;"
										on:click={() => startReschedule(b)}
									>
										Reschedule
									</button>
									<button class="btn-danger btn-sm" on:click={() => cancel(b.id)}>
										Cancel
									</button>
								{/if}
							{/if}
						</td>
					</tr>

					{#if reschedulingId === b.id}
						<tr>
							<td colspan="5" style="padding:0;">
								<div style="padding:16px;background:var(--surface-hover);border-top:1px solid var(--border);">
									<div style="font-weight:500;margin-bottom:12px;">
										Reschedule — {b.attendees?.[0]?.name ?? 'attendee'}
									</div>

									<div style="display:flex;gap:12px;align-items:flex-end;flex-wrap:wrap;">
										<div class="field" style="margin:0;">
											<label for="reschedule-date-{b.id}">New date</label>
											<input
												id="reschedule-date-{b.id}"
												type="date"
												min={todayISO()}
												bind:value={rescheduleDate}
												on:change={loadSlots}
											/>
										</div>

										{#if slotsLoading}
											<div style="color:var(--text-muted);padding-bottom:6px;">Loading slots…</div>
										{:else if slotsError}
											<div class="error-msg" style="margin:0;">{slotsError}</div>
										{:else if rescheduleDate && slots.length === 0}
											<div style="color:var(--text-muted);padding-bottom:6px;">No available slots on this date.</div>
										{/if}
									</div>

									{#if slots.length > 0}
										<div style="display:flex;gap:8px;flex-wrap:wrap;margin-top:12px;">
											{#each slots as slot}
												<button
													class="btn-secondary btn-sm"
													class:btn-primary={selectedSlot === slot.start}
													style={selectedSlot === slot.start ? 'border:none;' : ''}
													on:click={() => (selectedSlot = slot.start)}
												>
													{fmtSlotTime(slot.start)}
												</button>
											{/each}
										</div>
									{/if}

									{#if rescheduleError}
										<div class="error-msg" style="margin-top:12px;">{rescheduleError}</div>
									{/if}

									{#if selectedSlot}
										<div style="margin-top:16px;display:flex;gap:8px;">
											<button
												class="btn-primary"
												on:click={confirmReschedule}
												disabled={rescheduling}
											>
												{rescheduling ? 'Rescheduling…' : `Confirm — ${fmtSlotTime(selectedSlot)}`}
											</button>
											<button class="btn-secondary" on:click={cancelReschedule}>
												Cancel
											</button>
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
