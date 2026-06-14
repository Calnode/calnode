<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type EventType } from '$lib/api';

	let items: EventType[] = [];
	let loading = true;
	let error = '';
	let showCreate = false;

	let form = { slug: '', name: '', description: '', duration_minutes: 30 };
	let creating = false;
	let createError = '';

	async function load() {
		try {
			const res = await api.get<{ items: EventType[] }>('/v1/event-types');
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
		if (!form.slug || !form.name || !form.duration_minutes) {
			createError = 'Slug, name, and duration are required.';
			return;
		}
		creating = true;
		try {
			await api.post('/v1/event-types', {
				slug: form.slug,
				name: form.name,
				description: form.description || undefined,
				duration_minutes: Number(form.duration_minutes)
			});
			form = { slug: '', name: '', description: '', duration_minutes: 30 };
			showCreate = false;
			await load();
		} catch (e: any) {
			createError = e.message;
		} finally {
			creating = false;
		}
	}

	async function toggleActive(et: EventType) {
		try {
			await api.patch(`/v1/event-types/${et.slug}`, { is_active: !et.is_active });
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	async function del(slug: string) {
		if (!confirm('Delete this event type?')) return;
		try {
			await api.del(`/v1/event-types/${slug}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function bookLink(slug: string) {
		return `${window.location.origin}/book/${slug}`;
	}
</script>

<svelte:head><title>Event Types — Calnode</title></svelte:head>

<div class="page-header">
	<h1>Event Types</h1>
	<button class="btn-primary" on:click={() => (showCreate = !showCreate)}>
		{showCreate ? 'Cancel' : '+ New event type'}
	</button>
</div>

{#if showCreate}
	<div class="card" style="margin-bottom:20px;">
		<h2 style="margin:0 0 16px;font-size:15px;">Create event type</h2>
		{#if createError}<div class="error-msg">{createError}</div>{/if}
		<div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;">
			<div class="field">
				<label for="et-name">Name</label>
				<input id="et-name" bind:value={form.name} placeholder="30-Minute Call" />
			</div>
			<div class="field">
				<label for="et-slug">Slug (URL)</label>
				<input id="et-slug" bind:value={form.slug} placeholder="30-min-call" />
			</div>
			<div class="field">
				<label for="et-dur">Duration (minutes)</label>
				<input id="et-dur" type="number" min="5" bind:value={form.duration_minutes} />
			</div>
			<div class="field">
				<label for="et-desc">Description (optional)</label>
				<input id="et-desc" bind:value={form.description} placeholder="Brief description…" />
			</div>
		</div>
		<button class="btn-primary" on:click={create} disabled={creating}>
			{creating ? 'Creating…' : 'Create'}
		</button>
	</div>
{/if}

{#if error}<div class="error-msg">{error}</div>{/if}

{#if loading}
	<div style="color:var(--text-muted);padding:24px 0;">Loading…</div>
{:else if items.length === 0}
	<div class="card empty-state">
		<strong>No event types yet</strong>
		<p>Create your first event type to start accepting bookings.</p>
	</div>
{:else}
	<div class="card" style="padding:0;overflow:hidden;">
		<table>
			<thead>
				<tr>
					<th>Name</th>
					<th>Duration</th>
					<th>Booking link</th>
					<th>Status</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				{#each items as et}
					<tr>
						<td>
							<div style="font-weight:500;">{et.name}</div>
							<div style="color:var(--text-muted);font-size:12px;">{et.slug}</div>
						</td>
						<td>{et.duration_minutes} min</td>
						<td>
							<a href={bookLink(et.slug)} target="_blank" class="mono" style="font-size:11px;">
								{bookLink(et.slug)}
							</a>
						</td>
						<td>
							<span class="badge" class:badge-green={et.is_active} class:badge-gray={!et.is_active}>
								{et.is_active ? 'Active' : 'Inactive'}
							</span>
						</td>
						<td style="white-space:nowrap;text-align:right;">
							<a href="/admin/event-types/{et.slug}" class="btn-secondary btn-sm" style="margin-right:6px;">Settings</a>
							<button class="btn-secondary btn-sm" on:click={() => toggleActive(et)} style="margin-right:6px;">
								{et.is_active ? 'Deactivate' : 'Activate'}
							</button>
							<button class="btn-danger btn-sm" on:click={() => del(et.slug)}>Delete</button>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
