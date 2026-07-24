<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { api, type CalendarStatus, type ZoomStatus, type CalendarPick, type CalendarConnection } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { authStatus } from '$lib/stores';

	// Display names for known calendar providers.
	const PROVIDER_LABELS: Record<string, string> = {
		google: 'Google Calendar',
		microsoft: 'Microsoft 365 (Outlook)',
		caldav: 'CalDAV (Apple iCloud, Fastmail, Nextcloud)'
	};

	// CalDAV is credential-based (no OAuth redirect): an inline form collects a server + an
	// app-specific password and POSTs to the dedicated connect endpoint.
	let caldavOpen = $state(false);
	let caldavPreset = $state('icloud');
	let caldavServer = $state('');
	let caldavUser = $state('');
	let caldavPass = $state('');
	let caldavBusy = $state(false);
	let caldavErr = $state('');

	const appPwHelp: Record<string, { label: string; href: string }> = {
		icloud: { label: 'Create an app-specific password at appleid.apple.com', href: 'https://support.apple.com/102654' },
		fastmail: { label: 'Create an app password in Fastmail settings', href: 'https://www.fastmail.help/hc/en-us/articles/360058752854' }
	};

	async function connectCaldav() {
		caldavErr = '';
		if (caldavPreset === 'custom' && !caldavServer.trim()) {
			caldavErr = 'Enter your CalDAV server URL.';
			return;
		}
		if (!caldavUser.trim() || !caldavPass) {
			caldavErr = 'Username and app password are both required.';
			return;
		}
		caldavBusy = true;
		try {
			await api.post('/v1/calendar/caldav/connect', {
				preset: caldavPreset === 'custom' ? '' : caldavPreset,
				server_url: caldavPreset === 'custom' ? caldavServer.trim() : '',
				username: caldavUser.trim(),
				app_password: caldavPass
			});
			caldavOpen = false;
			caldavPass = '';
			caldavUser = '';
			justConnected = true;
			await load();
		} catch (e: any) {
			caldavErr = e.message || 'Could not connect. Check the server, username and app password.';
		} finally {
			caldavBusy = false;
		}
	}
	const label = (p?: string) => (p ? PROVIDER_LABELS[p] ?? p : 'calendar');

	let status = $state<CalendarStatus | null>(null);
	let loading = $state(true);
	let error = $state('');
	let busy = $state(false);
	let justConnected = $state(false);
	let disconnectOpen = $state(false);
	let pendingDisconnectId = $state<string | null>(null);

	const providers = $derived(status?.providers ?? []);
	const connections = $derived(status?.connections ?? []);

	// Zoom is a separate, per-host meeting-link connection (not a calendar).
	let zoom = $state<ZoomStatus | null>(null);
	let zoomJustConnected = $state(false);
	let zoomDisconnecting = $state(false);
	let zoomDisconnectOpen = $state(false);

	async function load() {
		try {
			status = await api.get<CalendarStatus>('/v1/calendar/status');
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	async function loadZoom() {
		try {
			zoom = await api.get<ZoomStatus>('/v1/zoom/status');
		} catch {
			zoom = null;
		}
	}

	onMount(() => {
		justConnected = $page.url.searchParams.get('connected') === 'true';
		zoomJustConnected = $page.url.searchParams.get('zoom') === 'connected';
		load();
		loadZoom();
	});

	// Per-account calendar picker: which of an account's calendars are checked for conflicts.
	// Loaded lazily when a connection's "Manage calendars" section is expanded.
	let pickerConnId = $state<string | null>(null);
	let pickerCals = $state<CalendarPick[]>([]);
	let pickerLoading = $state(false);
	let pickerSaving = $state(false);
	let pickerErr = $state('');

	// The picker is keyed on account identity (provider + account_email), not the connection id —
	// listing calendars can refresh the OAuth token, which recreates the connection row under a
	// new id and would strand a stale id on save.
	async function togglePicker(c: CalendarConnection) {
		if (pickerConnId === c.id) {
			pickerConnId = null;
			return;
		}
		pickerConnId = c.id;
		pickerCals = [];
		pickerErr = '';
		pickerLoading = true;
		try {
			const q = new URLSearchParams({ provider: c.provider, account: c.account_email ?? '' });
			const res = await api.get<{ calendars: CalendarPick[] }>(
				`/v1/calendar/connections/${c.id}/calendars?${q}`
			);
			pickerCals = res.calendars ?? [];
		} catch (e: any) {
			pickerErr = e.message || 'Could not load calendars for this account.';
		} finally {
			pickerLoading = false;
		}
	}

	async function savePicker(c: CalendarConnection) {
		pickerSaving = true;
		pickerErr = '';
		try {
			await api.put(`/v1/calendar/connections/${c.id}/calendars`, {
				provider: c.provider,
				account_email: c.account_email ?? '',
				calendars: pickerCals
			});
			pickerConnId = null;
			await load();
		} catch (e: any) {
			pickerErr = e.message || 'Could not save calendar selection.';
		} finally {
			pickerSaving = false;
		}
	}

	async function setDestination(id: string) {
		busy = true;
		error = '';
		try {
			await api.post(`/v1/calendar/connections/${id}/destination`, {});
			await load();
		} catch (e: any) {
			error = e.message;
		} finally {
			busy = false;
		}
	}

	function askDisconnect(id: string) {
		pendingDisconnectId = id;
		disconnectOpen = true;
	}

	async function doDisconnect() {
		if (!pendingDisconnectId) return;
		busy = true;
		try {
			await api.del(`/v1/calendar/connections/${pendingDisconnectId}`);
			await load();
		} catch (e: any) {
			error = e.message;
		} finally {
			busy = false;
			pendingDisconnectId = null;
		}
	}

	async function doZoomDisconnect() {
		zoomDisconnecting = true;
		try {
			await api.del('/v1/zoom');
			await loadZoom();
		} catch (e: any) {
			error = e.message;
		} finally {
			zoomDisconnecting = false;
		}
	}
</script>

<ConfirmDialog
	bind:open={disconnectOpen}
	title="Disconnect this calendar?"
	description="Calnode will stop checking it for conflicts. If it was your booking calendar, another connected calendar is promoted automatically."
	confirmText="Disconnect"
	destructive
	onConfirm={doDisconnect}
/>

<ConfirmDialog
	bind:open={zoomDisconnectOpen}
	title="Disconnect Zoom?"
	description="New Zoom-located bookings assigned to you won't get an auto-generated meeting link."
	confirmText="Disconnect"
	destructive
	onConfirm={doZoomDisconnect}
/>

<svelte:head><title>Calendar — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Calendar</h1>
	<p class="mt-1 text-sm text-muted-foreground">Connect one or more calendars — all are checked for conflicts, and bookings are written to the one you choose.</p>
</div>

{#if error}
	<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>
{/if}
{#if justConnected && status?.connected}
	<p class="mb-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">Calendar connected successfully.</p>
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
{:else}
	<div class="max-w-xl space-y-6">
		{#if connections.length > 0}
			<!-- Connected calendars: all checked for conflicts; exactly one is the write destination. -->
			<div class="divide-y rounded-lg border bg-card">
				{#each connections as c (c.id)}
					<div class="p-4">
						<div class="flex items-center justify-between gap-3">
						<div class="flex min-w-0 items-center gap-3">
							<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground">
								<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>
							</svg>
							<div class="min-w-0">
								<p class="truncate font-medium">{c.account_email || label(c.provider)}</p>
								<p class="text-xs text-muted-foreground">{label(c.provider)} · checked for conflicts</p>
							</div>
						</div>
						<div class="flex shrink-0 items-center gap-4">
							<label class="flex cursor-pointer items-center gap-1.5 text-sm text-muted-foreground">
								<input type="radio" name="destination" checked={c.is_destination} disabled={busy} onchange={() => setDestination(c.id)} />
								Add bookings here
							</label>
							<Button variant="ghost" size="sm" onclick={() => askDisconnect(c.id)} disabled={busy}>Disconnect</Button>
						</div>
						</div>
						<div class="mt-2 pl-8">
							<button type="button" class="text-xs font-medium text-muted-foreground underline-offset-2 hover:underline" onclick={() => togglePicker(c)}>
								{pickerConnId === c.id ? 'Hide calendars' : 'Manage calendars'}
							</button>
						</div>
						{#if pickerConnId === c.id}
							<div class="mt-3 space-y-3 rounded-md border bg-muted/30 p-3">
								{#if pickerLoading}
									<p class="text-xs text-muted-foreground">Loading calendars…</p>
								{:else if pickerErr}
									<p class="text-xs text-destructive">{pickerErr}</p>
								{:else if pickerCals.length === 0}
									<p class="text-xs text-muted-foreground">No calendars found for this account.</p>
								{:else}
									<p class="text-xs text-muted-foreground">Choose which calendars in this account are checked for conflicts.</p>
									<div class="space-y-1.5">
										{#each pickerCals as cal (cal.id)}
											<label class="flex cursor-pointer items-center gap-2 text-sm">
												<input type="checkbox" bind:checked={cal.check_conflicts} disabled={pickerSaving} />
												<span class="truncate">{cal.name}{#if cal.primary}<span class="ml-1 text-xs text-muted-foreground">(primary)</span>{/if}</span>
											</label>
										{/each}
									</div>
									<div class="flex items-center gap-2 pt-1">
										<Button size="sm" onclick={() => savePicker(c)} disabled={pickerSaving}>
											{pickerSaving ? 'Saving…' : 'Save'}
										</Button>
										<Button variant="ghost" size="sm" onclick={() => (pickerConnId = null)} disabled={pickerSaving}>Cancel</Button>
									</div>
								{/if}
							</div>
						{/if}
					</div>
				{/each}
			</div>
			<p class="text-xs text-muted-foreground">
				Use <span class="font-medium">Manage calendars</span> on each account to choose which of its
				calendars are checked for conflicts. Confirmed bookings (and any auto-generated meeting links)
				are written to the account marked <span class="font-medium">“Add bookings here”</span>.
			</p>
		{/if}

		<!-- Connect (another) calendar -->
		{#if $authStatus.demo_mode}
			<p class="text-sm text-muted-foreground">Calendar connect is disabled in the demo.</p>
		{:else}
		<div class="space-y-2">
			<p class="text-sm font-medium">{connections.length > 0 ? 'Connect another calendar' : 'Connect a calendar'}</p>
			{#each providers as p}
				{#if p === 'caldav'}
					<div class="rounded-lg border bg-card p-4">
						<div class="flex items-center justify-between">
							<div class="flex items-center gap-3">
								<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground">
									<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>
								</svg>
								<p class="font-medium">{label('caldav')}</p>
							</div>
							<Button variant={connections.length > 0 ? 'outline' : 'default'} onclick={() => (caldavOpen = !caldavOpen)}>
								{caldavOpen ? 'Cancel' : 'Connect'}
							</Button>
						</div>
						{#if caldavOpen}
							<form class="mt-4 space-y-3 border-t pt-4" onsubmit={(e) => { e.preventDefault(); connectCaldav(); }}>
								<div class="space-y-1.5">
									<Label for="caldav-preset">Provider</Label>
									<select id="caldav-preset" bind:value={caldavPreset}
										class="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm">
										<option value="icloud">Apple iCloud</option>
										<option value="fastmail">Fastmail</option>
										<option value="custom">Nextcloud / other (custom URL)</option>
									</select>
								</div>
								{#if caldavPreset === 'custom'}
									<div class="space-y-1.5">
										<Label for="caldav-server">Server URL</Label>
										<Input id="caldav-server" type="url" placeholder="https://cloud.example.com/remote.php/dav" bind:value={caldavServer} />
									</div>
								{/if}
								<div class="space-y-1.5">
									<Label for="caldav-user">Username / email</Label>
									<Input id="caldav-user" type="text" autocomplete="username" placeholder="you@icloud.com" bind:value={caldavUser} />
								</div>
								<div class="space-y-1.5">
									<Label for="caldav-pass">App-specific password</Label>
									<Input id="caldav-pass" type="password" autocomplete="off" bind:value={caldavPass} />
									{#if appPwHelp[caldavPreset]}
										<p class="text-xs text-muted-foreground">
											<a class="underline" href={appPwHelp[caldavPreset].href} target="_blank" rel="noopener noreferrer">{appPwHelp[caldavPreset].label}</a> — your normal password won't work.
										</p>
									{:else}
										<p class="text-xs text-muted-foreground">Use an app password from your provider, not your login password.</p>
									{/if}
								</div>
								{#if caldavErr}
									<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{caldavErr}</p>
								{/if}
								<Button type="submit" disabled={caldavBusy}>{caldavBusy ? 'Connecting…' : 'Connect calendar'}</Button>
							</form>
						{/if}
					</div>
				{:else}
					<div class="flex items-center justify-between rounded-lg border bg-card p-4">
						<div class="flex items-center gap-3">
							<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground">
								<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>
							</svg>
							<p class="font-medium">{label(p)}</p>
						</div>
						<Button variant={connections.length > 0 ? 'outline' : 'default'} onclick={() => (window.location.href = `/v1/calendar/connect?provider=${p}`)}>Connect</Button>
					</div>
				{/if}
			{/each}
			{#if connections.length > 0}
				<p class="text-xs text-muted-foreground">Connect a personal + work calendar (or both providers) so nothing double-books.</p>
			{/if}
		</div>
		{/if}
	</div>
{/if}

<!-- Zoom — per-host meeting links (independent of the calendar) -->
{#if zoom?.configured && !$authStatus.demo_mode}
	<div class="mt-10">
		<h2 class="text-lg font-semibold tracking-tight">Zoom</h2>
		<p class="mt-1 text-sm text-muted-foreground">
			Connect your Zoom account so bookings with a Zoom location get a real meeting link minted under your account.
		</p>
		{#if zoomJustConnected && zoom.connected}
			<p class="mt-3 max-w-md rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">Zoom connected successfully.</p>
		{/if}
		<div class="mt-3 max-w-md">
			{#if zoom.connected}
				<div class="flex items-center justify-between rounded-lg border bg-card p-5">
					<div class="flex items-center gap-3">
						<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground"><path d="m22 8-6 4 6 4V8Z"/><rect x="2" y="6" width="14" height="12" rx="2"/></svg>
						<div>
							<p class="font-medium">Zoom</p>
							<p class="mt-0.5 inline-flex items-center gap-1.5 text-sm text-green-700">
								<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
								Connected
							</p>
						</div>
					</div>
					<Button variant="outline" onclick={() => (zoomDisconnectOpen = true)} disabled={zoomDisconnecting}>
						{zoomDisconnecting ? 'Disconnecting…' : 'Disconnect'}
					</Button>
				</div>
			{:else}
				<div class="flex items-center justify-between rounded-lg border bg-card p-5">
					<div class="flex items-center gap-3">
						<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground"><path d="m22 8-6 4 6 4V8Z"/><rect x="2" y="6" width="14" height="12" rx="2"/></svg>
						<p class="font-medium">Zoom</p>
					</div>
					<Button onclick={() => (window.location.href = '/v1/zoom/connect')}>Connect</Button>
				</div>
			{/if}
		</div>
	</div>
{/if}
