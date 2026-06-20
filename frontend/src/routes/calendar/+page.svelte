<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { api, type CalendarStatus } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';

	// Display names for known calendar providers.
	const PROVIDER_LABELS: Record<string, string> = {
		google: 'Google Calendar',
		microsoft: 'Microsoft 365 (Outlook)'
	};
	const label = (p?: string) => (p ? PROVIDER_LABELS[p] ?? p : 'calendar');

	let status: CalendarStatus | null = $state(null);
	let loading = $state(true);
	let error = $state('');
	let disconnecting = $state(false);
	let justConnected = $state(false);
	let disconnectOpen = $state(false);

	const providers = $derived(status?.providers ?? []);

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

	async function doDisconnect() {
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

<ConfirmDialog
	bind:open={disconnectOpen}
	title="Disconnect calendar?"
	description="Calnode will stop checking for conflicts and new bookings won't be added to your calendar."
	confirmText="Disconnect"
	destructive
	onConfirm={doDisconnect}
/>

<svelte:head><title>Calendar — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Calendar</h1>
	<p class="mt-1 text-sm text-muted-foreground">Connect a calendar to check availability and write confirmed bookings.</p>
</div>

{#if error}
	<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>
{/if}
{#if justConnected && status?.connected}
	<p class="mb-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">{label(status.provider)} connected successfully.</p>
{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if status?.configured === false}
	<div class="max-w-md rounded-md bg-amber-50 px-3 py-2.5 text-sm text-amber-800 ring-1 ring-inset ring-amber-200">
		<p class="font-medium">No calendar provider configured</p>
		<p class="mt-1 text-amber-700">
			Add Google OAuth credentials in <a href="/admin/settings/google" class="font-medium underline">Settings → Google OAuth</a>,
			or set the Microsoft (Outlook) credentials via environment variables, then restart the server.
		</p>
	</div>
{:else if status?.connected}
	<!-- Connected: show which provider, allow disconnect -->
	<div class="max-w-md rounded-lg border bg-card p-6">
		<div class="mb-5 flex items-start gap-3">
			<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mt-0.5 shrink-0 text-muted-foreground">
				<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>
			</svg>
			<div>
				<p class="font-medium">{label(status.provider)}</p>
				<p class="mt-0.5 text-sm text-muted-foreground">Check availability and write confirmed bookings to your calendar.</p>
			</div>
		</div>
		<div class="mb-4 flex items-center gap-2 rounded-md bg-green-50 px-3 py-2.5 text-sm text-green-700 ring-1 ring-inset ring-green-600/20">
			<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
			<span class="font-medium">Connected</span>
		</div>
		<Button variant="outline" onclick={() => (disconnectOpen = true)} disabled={disconnecting}>
			{disconnecting ? 'Disconnecting…' : 'Disconnect calendar'}
		</Button>
	</div>
{:else}
	<!-- Not connected: one card per available provider -->
	<div class="max-w-md space-y-3">
		{#each providers as p}
			<div class="flex items-center justify-between rounded-lg border bg-card p-5">
				<div class="flex items-center gap-3">
					<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground">
						<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>
					</svg>
					<p class="font-medium">{label(p)}</p>
				</div>
				<Button onclick={() => (window.location.href = `/v1/calendar/connect?provider=${p}`)}>Connect</Button>
			</div>
		{/each}
		<p class="text-xs text-muted-foreground">Connecting one calendar replaces any previously connected one.</p>
	</div>
{/if}
