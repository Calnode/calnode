<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { api, type CalendarStatus } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';

	let status: CalendarStatus | null = $state(null);
	let loading = $state(true);
	let error = $state('');
	let disconnecting = $state(false);
	let justConnected = $state(false);

	async function load() {
		try {
			status = await api.get<CalendarStatus>('/v1/calendar/status');
		} catch (e: any) {
			error = e.message;
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

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Calendar</h1>
	<p class="mt-1 text-sm text-muted-foreground">Connect your Google Calendar to check availability and log bookings.</p>
</div>

{#if error}
	<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>
{/if}
{#if justConnected}
	<p class="mb-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">Google Calendar connected successfully.</p>
{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<div class="max-w-md rounded-lg border bg-card p-6">
		<div class="mb-5 flex items-start gap-3">
			<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mt-0.5 shrink-0 text-muted-foreground">
				<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>
			</svg>
			<div>
				<p class="font-medium">Google Calendar</p>
				<p class="mt-0.5 text-sm text-muted-foreground">Check availability and write confirmed bookings to your calendar.</p>
			</div>
		</div>

		{#if status?.configured === false}
			<div class="rounded-md bg-amber-50 px-3 py-2.5 text-sm text-amber-800 ring-1 ring-inset ring-amber-200">
				<p class="font-medium">Google Calendar not configured</p>
				<p class="mt-1 text-amber-700">Set <code class="font-mono text-xs">GOOGLE_CLIENT_ID</code> and <code class="font-mono text-xs">GOOGLE_CLIENT_SECRET</code> in your <code class="font-mono text-xs">.env</code> file and restart the server.</p>
			</div>
		{:else if status?.connected}
			<div class="mb-4 flex items-center gap-2 rounded-md bg-green-50 px-3 py-2.5 text-sm text-green-700 ring-1 ring-inset ring-green-600/20">
				<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
				<div>
					<span class="font-medium">Connected</span>
					{#if status.calendar_id}
						<span class="ml-2 font-mono text-xs opacity-75">{status.calendar_id}</span>
					{/if}
				</div>
			</div>
			<Button variant="outline" onclick={disconnect} disabled={disconnecting}>
				{disconnecting ? 'Disconnecting…' : 'Disconnect calendar'}
			</Button>
		{:else}
			<div class="mb-4 flex items-center gap-2 rounded-md bg-muted px-3 py-2.5 text-sm text-muted-foreground">
				<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
				Not connected
			</div>
			<Button onclick={() => window.location.href = '/v1/calendar/connect'}>
				Connect Google Calendar
			</Button>
		{/if}
	</div>
{/if}
