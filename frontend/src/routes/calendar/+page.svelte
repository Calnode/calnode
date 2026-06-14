<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { api, type CalendarStatus } from '$lib/api';

	let status: CalendarStatus | null = null;
	let loading = true;
	let error = '';
	let disconnecting = false;
	let justConnected = false;

	async function load() {
		try {
			status = await api.get<CalendarStatus>('/v1/calendar/status');
		} catch (e: any) {
			if (e.message?.includes('not configured')) {
				status = { connected: false };
			} else {
				error = e.message;
			}
		} finally {
			loading = false;
		}
	}

	onMount(() => {
		justConnected = $page.url.searchParams.get('connected') === 'true';
		load();
	});

	async function disconnect() {
		if (!confirm('Disconnect Google Calendar? Calnode will stop checking for conflicts.')) return;
		disconnecting = true;
		try {
			await api.del('/v1/calendar');
			await load();
		} catch (e: any) {
			error = e.message;
		} finally {
			disconnecting = false;
		}
	}
</script>

<svelte:head><title>Calendar — Calnode</title></svelte:head>

<div class="page-header">
	<h1>Calendar</h1>
</div>

{#if error}<div class="error-msg">{error}</div>{/if}
{#if justConnected}<div class="success-msg">Google Calendar connected successfully.</div>{/if}

{#if loading}
	<div style="color:var(--text-muted);padding:24px 0;">Loading…</div>
{:else}
	<div class="card" style="max-width:480px;">
		<div style="display:flex;align-items:center;gap:12px;margin-bottom:20px;">
			<div style="font-size:32px;">📆</div>
			<div>
				<div style="font-weight:600;font-size:16px;">Google Calendar</div>
				<div style="color:var(--text-muted);font-size:13px;">
					Check availability and write confirmed bookings to your calendar.
				</div>
			</div>
		</div>

		{#if status?.connected}
			<div style="display:flex;align-items:center;gap:8px;padding:10px 14px;background:#f0fdf4;border:1px solid #bbf7d0;border-radius:6px;margin-bottom:16px;">
				<span style="color:#16a34a;font-size:18px;">✓</span>
				<div>
					<div style="font-weight:500;color:#15803d;">Connected</div>
					{#if status.calendar_id}
						<div style="font-size:12px;color:#166534;" class="mono">{status.calendar_id}</div>
					{/if}
				</div>
			</div>
			<button class="btn-secondary" on:click={disconnect} disabled={disconnecting}>
				{disconnecting ? 'Disconnecting…' : 'Disconnect calendar'}
			</button>
		{:else}
			<div style="display:flex;align-items:center;gap:8px;padding:10px 14px;background:#f8fafc;border:1px solid var(--border);border-radius:6px;margin-bottom:16px;">
				<span style="color:var(--text-muted);font-size:18px;">○</span>
				<div style="font-weight:500;color:var(--text-muted);">Not connected</div>
			</div>
			<button class="btn-primary" on:click={() => window.location.href = '/v1/calendar/connect'}>
				Connect Google Calendar
			</button>
		{/if}
	</div>
{/if}
