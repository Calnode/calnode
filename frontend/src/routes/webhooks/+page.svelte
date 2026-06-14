<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Webhook } from '$lib/api';

	let items: Webhook[] = [];
	let loading = true;
	let error = '';
	let showCreate = false;

	const allEvents = [
		'booking.created',
		'booking.cancelled',
		'booking.rescheduled'
	];

	let form = { url: '', events: ['booking.created', 'booking.cancelled'] };
	let creating = false;
	let createError = '';

	async function load() {
		try {
			const res = await api.get<{ items: Webhook[] }>('/v1/webhooks');
			items = res.items;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function create() {
		createError = '';
		if (!form.url) { createError = 'URL is required.'; return; }
		if (!form.url.startsWith('https://')) { createError = 'URL must start with https://'; return; }
		if (form.events.length === 0) { createError = 'Select at least one event.'; return; }
		creating = true;
		try {
			await api.post('/v1/webhooks', { url: form.url, events: form.events });
			form = { url: '', events: ['booking.created', 'booking.cancelled'] };
			showCreate = false;
			await load();
		} catch (e: any) {
			createError = e.message;
		} finally {
			creating = false;
		}
	}

	async function del(id: string) {
		if (!confirm('Delete this webhook?')) return;
		try {
			await api.del(`/v1/webhooks/${id}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}

	function toggleEvent(ev: string) {
		if (form.events.includes(ev)) {
			form.events = form.events.filter((e) => e !== ev);
		} else {
			form.events = [...form.events, ev];
		}
	}
</script>

<svelte:head><title>Webhooks — Calnode</title></svelte:head>

<div class="page-header">
	<h1>Webhooks</h1>
	<button class="btn-primary" on:click={() => { showCreate = !showCreate; createError = ''; }}>
		{showCreate ? 'Cancel' : '+ New webhook'}
	</button>
</div>

{#if showCreate}
	<div class="card" style="margin-bottom:20px;">
		<h2 style="margin:0 0 14px;font-size:15px;">New webhook</h2>
		{#if createError}<div class="error-msg">{createError}</div>{/if}
		<div class="field">
			<label for="wh-url">Endpoint URL</label>
			<input id="wh-url" type="url" bind:value={form.url} placeholder="https://your-server.com/hooks/calnode" />
		</div>
		<div class="field">
			<label>Events to send</label>
			<div style="display:flex;flex-direction:column;gap:6px;margin-top:4px;">
				{#each allEvents as ev}
					<label style="display:flex;align-items:center;gap:8px;text-transform:none;letter-spacing:0;font-size:13px;font-weight:400;color:var(--text);">
						<input type="checkbox" checked={form.events.includes(ev)} on:change={() => toggleEvent(ev)} style="width:auto;" />
						<span class="mono">{ev}</span>
					</label>
				{/each}
			</div>
		</div>
		<button class="btn-primary" on:click={create} disabled={creating}>
			{creating ? 'Creating…' : 'Create webhook'}
		</button>
	</div>
{/if}

{#if error}<div class="error-msg">{error}</div>{/if}

{#if loading}
	<div style="color:var(--text-muted);padding:24px 0;">Loading…</div>
{:else if items.length === 0}
	<div class="card empty-state">
		<strong>No webhooks</strong>
		<p>Add a webhook to receive real-time notifications for booking events.</p>
	</div>
{:else}
	<div class="card" style="padding:0;overflow:hidden;">
		<table>
			<thead>
				<tr>
					<th>URL</th>
					<th>Events</th>
					<th>Status</th>
					<th>Created</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				{#each items as wh}
					<tr>
						<td class="mono" style="max-width:280px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">{wh.url}</td>
						<td style="font-size:11px;color:var(--text-muted);">{(wh.events ?? []).join(', ')}</td>
						<td>
							<span class="badge" class:badge-green={wh.is_active} class:badge-gray={!wh.is_active}>
								{wh.is_active ? 'Active' : 'Inactive'}
							</span>
						</td>
						<td>{fmtDate(wh.created_at)}</td>
						<td style="text-align:right;">
							<button class="btn-danger btn-sm" on:click={() => del(wh.id)}>Delete</button>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
