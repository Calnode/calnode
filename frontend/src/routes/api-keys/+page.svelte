<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type APIKey } from '$lib/api';

	let items: APIKey[] = [];
	let loading = true;
	let error = '';
	let showCreate = false;
	let newName = '';
	let creating = false;
	let createError = '';
	let newKey = '';

	async function load() {
		try {
			const res = await api.get<{ items: APIKey[] }>('/v1/api-keys');
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
		if (!newName.trim()) { createError = 'Name is required.'; return; }
		creating = true;
		try {
			const res = await api.post<{ key: string }>('/v1/api-keys', { name: newName.trim() });
			newKey = res.key;
			newName = '';
			showCreate = false;
			await load();
		} catch (e: any) {
			createError = e.message;
		} finally {
			creating = false;
		}
	}

	async function revoke(id: string, name: string) {
		if (!confirm(`Revoke key "${name}"? Any integrations using it will stop working.`)) return;
		try {
			await api.del(`/v1/api-keys/${id}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}

	function copyKey() {
		navigator.clipboard.writeText(newKey).catch(() => {});
	}
</script>

<svelte:head><title>API Keys — Calnode</title></svelte:head>

<div class="page-header">
	<h1>API Keys</h1>
	<button class="btn-primary" on:click={() => { showCreate = !showCreate; createError = ''; newKey = ''; }}>
		{showCreate ? 'Cancel' : '+ New key'}
	</button>
</div>

{#if newKey}
	<div class="card" style="margin-bottom:20px;border-color:var(--success);">
		<div style="display:flex;align-items:center;gap:8px;margin-bottom:10px;">
			<span style="font-size:18px;">✅</span>
			<strong>Key created — copy it now. It won't be shown again.</strong>
		</div>
		<div class="key-reveal">{newKey}</div>
		<button class="btn-secondary btn-sm" on:click={copyKey} style="margin-top:10px;">Copy to clipboard</button>
	</div>
{/if}

{#if showCreate}
	<div class="card" style="margin-bottom:20px;">
		<h2 style="margin:0 0 14px;font-size:15px;">New API key</h2>
		{#if createError}<div class="error-msg">{createError}</div>{/if}
		<div class="field">
			<label for="key-name">Key name</label>
			<input id="key-name" bind:value={newName} placeholder="e.g. CI/CD pipeline" />
		</div>
		<button class="btn-primary" on:click={create} disabled={creating}>
			{creating ? 'Creating…' : 'Create key'}
		</button>
	</div>
{/if}

{#if error}<div class="error-msg">{error}</div>{/if}

{#if loading}
	<div style="color:var(--text-muted);padding:24px 0;">Loading…</div>
{:else if items.length === 0}
	<div class="card empty-state">
		<strong>No API keys</strong>
		<p>Create a key to authenticate CLI tools and integrations.</p>
	</div>
{:else}
	<div class="card" style="padding:0;overflow:hidden;">
		<table>
			<thead>
				<tr>
					<th>Name</th>
					<th>Created</th>
					<th>Last used</th>
					<th></th>
				</tr>
			</thead>
			<tbody>
				{#each items as k}
					<tr>
						<td style="font-weight:500;">{k.name}</td>
						<td>{fmtDate(k.created_at)}</td>
						<td>
							{#if k.last_used_at}
								{fmtDate(k.last_used_at)}
							{:else}
								<span style="color:var(--text-muted);">Never</span>
							{/if}
						</td>
						<td style="text-align:right;">
							<button class="btn-danger btn-sm" on:click={() => revoke(k.id, k.name)}>Revoke</button>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
