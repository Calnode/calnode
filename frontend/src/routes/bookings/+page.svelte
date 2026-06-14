<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Booking } from '$lib/api';

	let items: Booking[] = [];
	let loading = true;
	let error = '';

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

	function fmt(iso: string) {
		return new Date(iso).toLocaleString(undefined, {
			dateStyle: 'medium',
			timeStyle: 'short'
		});
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
						<td style="text-align:right;">
							{#if b.status === 'confirmed'}
								<button class="btn-secondary btn-sm" on:click={() => cancel(b.id)}>Cancel</button>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
