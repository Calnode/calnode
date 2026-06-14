<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type User } from '$lib/api';
	import { prefs, prefsFromUser, TIMEZONES, WEEK_DAYS } from '$lib/prefs';
	import { currentUser } from '$lib/stores';

	let user: User | null = null;
	let loading = true;
	let saving = false;
	let saved = false;
	let error = '';

	let timezone = 'UTC';
	let time_format: '12h' | '24h' = '12h';
	let week_start = 1;

	onMount(async () => {
		try {
			user = await api.get<User>('/v1/users/me');
			timezone = user.timezone;
			time_format = user.time_format ?? '12h';
			week_start = user.week_start ?? 1;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	});

	async function save() {
		saving = true;
		saved = false;
		error = '';
		try {
			const updated = await api.patch<User>('/v1/users/me', { timezone, time_format, week_start });
			currentUser.set(updated);
			prefs.set(prefsFromUser(updated));
			saved = true;
			setTimeout(() => (saved = false), 3000);
		} catch (e: any) {
			error = e.message;
		} finally {
			saving = false;
		}
	}
</script>

<svelte:head><title>Settings — Calnode</title></svelte:head>

<div class="page-header">
	<h1>Settings</h1>
</div>

{#if error}<div class="error-msg">{error}</div>{/if}
{#if saved}<div class="success-msg">Settings saved.</div>{/if}

{#if loading}
	<div style="color:var(--text-muted);padding:24px 0;">Loading…</div>
{:else}
	<form on:submit|preventDefault={save} style="max-width:480px;">
		<div class="card">
			<h2 style="margin:0 0 20px;font-size:15px;font-weight:600;">Profile</h2>

			<div class="field">
				<label>Name</label>
				<input type="text" value={user?.name ?? ''} disabled style="opacity:0.6;" />
			</div>
			<div class="field">
				<label>Email</label>
				<input type="email" value={user?.email ?? ''} disabled style="opacity:0.6;" />
			</div>
		</div>

		<div class="card" style="margin-top:16px;">
			<h2 style="margin:0 0 20px;font-size:15px;font-weight:600;">Preferences</h2>

			<div class="field">
				<label for="timezone">Timezone</label>
				<select id="timezone" bind:value={timezone}>
					{#each TIMEZONES as tz}
						<option value={tz}>{tz}</option>
					{/each}
				</select>
				<p class="field-hint">Used when computing available slots for your booking pages.</p>
			</div>

			<div class="field">
				<label>Time format</label>
				<div style="display:flex;gap:8px;margin-top:6px;">
					<label class="radio-option" class:selected={time_format === '12h'}>
						<input type="radio" bind:group={time_format} value="12h" />
						12-hour <span style="color:var(--text-muted);">(1:30 PM)</span>
					</label>
					<label class="radio-option" class:selected={time_format === '24h'}>
						<input type="radio" bind:group={time_format} value="24h" />
						24-hour <span style="color:var(--text-muted);">(13:30)</span>
					</label>
				</div>
			</div>

			<div class="field">
				<label for="week-start">First day of week</label>
				<select id="week-start" bind:value={week_start}>
					{#each [0, 1] as d}
						<option value={d}>{WEEK_DAYS[d]}</option>
					{/each}
				</select>
			</div>
		</div>

		<div style="margin-top:16px;">
			<button type="submit" class="btn-primary" disabled={saving}>
				{saving ? 'Saving…' : 'Save settings'}
			</button>
		</div>
	</form>
{/if}

<style>
	.radio-option {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 7px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		cursor: pointer;
		font-size: 13px;
		background: var(--surface);
		transition: border-color 0.15s, background 0.15s;
	}
	.radio-option.selected {
		border-color: var(--accent);
		background: color-mix(in srgb, var(--accent) 8%, var(--surface));
	}
	.radio-option input { display: none; }

	.field-hint {
		margin: 4px 0 0;
		font-size: 12px;
		color: var(--text-muted);
	}
</style>
