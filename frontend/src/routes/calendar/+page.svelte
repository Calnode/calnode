<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { api, type CalendarStatus, type ZoomStatus, type CalendarPick, type CalendarConnection } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { authStatus } from '$lib/stores';
	import { t, tr } from '$lib/i18n';

	// Display names for known calendar providers.
	const PROVIDER_LABELS = $derived<Record<string, string>>({
		google: $t('calendar.provider.google'),
		microsoft: $t('calendar.provider.microsoft'),
		caldav: $t('calendar.provider.caldav')
	});

	// Where to send an admin for each provider Calnode supports but this instance hasn't
	// been given credentials for. Google is self-serve (a settings page); Microsoft is
	// currently env-var-only, so it points at the docs instead of a form.
	const UNCONFIGURED_SETUP = $derived<Record<string, { text: string; href: string; external?: boolean }>>({
		google: { text: $t('calendar.setup.google'), href: '/admin/settings/google' },
		microsoft: {
			text: $t('calendar.setup.microsoft'),
			href: 'https://github.com/Calnode/calnode/blob/main/.env.example',
			external: true
		}
	});

	// CalDAV is credential-based (no OAuth redirect): an inline form collects a server + an
	// app-specific password and POSTs to the dedicated connect endpoint.
	let caldavOpen = $state(false);
	let caldavPreset = $state('icloud');
	let caldavServer = $state('');
	let caldavUser = $state('');
	let caldavPass = $state('');
	let caldavBusy = $state(false);
	let caldavErr = $state('');

	const appPwHelp = $derived<Record<string, { label: string; href: string }>>({
		icloud: { label: $t('calendar.caldav.icloudHelp'), href: 'https://support.apple.com/102654' },
		fastmail: { label: $t('calendar.caldav.fastmailHelp'), href: 'https://www.fastmail.help/hc/en-us/articles/360058752854' }
	});

	async function connectCaldav() {
		caldavErr = '';
		if (caldavPreset === 'custom' && !caldavServer.trim()) {
			caldavErr = tr('calendar.caldav.err.server');
			return;
		}
		if (!caldavUser.trim() || !caldavPass) {
			caldavErr = tr('calendar.caldav.err.credentials');
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
			caldavErr = e.message || tr('calendar.caldav.err.connect');
		} finally {
			caldavBusy = false;
		}
	}
	const label = (p?: string) => (p ? PROVIDER_LABELS[p] ?? p : $t('calendar.picker.calendar'));

	let status = $state<CalendarStatus | null>(null);
	let loading = $state(true);
	let error = $state('');
	let busy = $state(false);
	let justConnected = $state(false);
	let disconnectOpen = $state(false);
	// The connection object, not its id: the id is recreated on every OAuth token refresh
	// (which opening the calendar picker can trigger), so a stale one strands the request.
	// Both endpoints key on provider + account_email instead.
	let pendingDisconnect = $state<CalendarConnection | null>(null);

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
	// Connection ids whose provider reported a dead OAuth grant (409) — flagged for reconnect.
	let reauthNeeded = $state<Set<string>>(new Set());

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
			if (reauthNeeded.has(c.id)) {
				const s = new Set(reauthNeeded);
				s.delete(c.id);
				reauthNeeded = s; // recovered after a reconnect
			}
		} catch (e: any) {
			if (e?.status === 409) reauthNeeded = new Set(reauthNeeded).add(c.id);
			pickerErr = e.message || tr('common.failed');
		} finally {
			pickerLoading = false;
		}
	}

	// Exclusive within this account's list, but clearable: selecting the current
	// destination again removes it (plain radios can't express "none", and the
	// backend accepts a save with no destination — writes then fall back to the
	// account's bound calendar). The backend clears the flag across every other
	// account when it saves, so the UI only has to keep this list consistent.
	function pickDestination(chosen: CalendarPick) {
		pickerCals = pickerCals.map((c) => ({ ...c, is_destination: c.id === chosen.id }));
	}

	function toggleDestination(chosen: CalendarPick) {
		if (chosen.is_destination) {
			pickerCals = pickerCals.map((c) => ({ ...c, is_destination: false }));
		} else {
			pickDestination(chosen);
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
			pickerErr = e.message || tr('common.failed');
		} finally {
			pickerSaving = false;
		}
	}

	async function setDestination(c: CalendarConnection) {
		busy = true;
		error = '';
		try {
			await api.post(`/v1/calendar/connections/${c.id}/destination`, {
				provider: c.provider,
				account_email: c.account_email ?? ''
			});
			await load();
		} catch (e: any) {
			error = e.message;
		} finally {
			busy = false;
		}
	}

	function askDisconnect(c: CalendarConnection) {
		pendingDisconnect = c;
		disconnectOpen = true;
	}

	async function doDisconnect() {
		if (!pendingDisconnect) return;
		const c = pendingDisconnect;
		busy = true;
		try {
			const q = new URLSearchParams({ provider: c.provider, account: c.account_email ?? '' });
			await api.del(`/v1/calendar/connections/${c.id}?${q}`);
			await load();
		} catch (e: any) {
			error = e.message;
		} finally {
			busy = false;
			pendingDisconnect = null;
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
	title={$t('calendar.confirmDisconnect.title')}
	description={$t('calendar.confirmDisconnect.description')}
	confirmText={$t('calendar.confirmDisconnect.confirm')}
	destructive
	onConfirm={doDisconnect}
/>

<ConfirmDialog
	bind:open={zoomDisconnectOpen}
	title={$t('calendar.confirmZoomDisconnect.title')}
	description={$t('calendar.confirmZoomDisconnect.description')}
	confirmText={$t('calendar.disconnect')}
	destructive
	onConfirm={doZoomDisconnect}
/>

<svelte:head><title>{$t('calendar.headTitle')}</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">{$t('calendar.title')}</h1>
	<p class="mt-1 text-sm text-muted-foreground">{$t('calendar.subtitle')}</p>
</div>

{#if error}
	<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>
{/if}
{#if justConnected && status?.connected}
	<p class="mb-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">{$t('calendar.connectedSuccess')}</p>
{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else if status?.configured === false}
	<div class="max-w-md rounded-md bg-amber-50 px-3 py-2.5 text-sm text-amber-800 ring-1 ring-inset ring-amber-200">
		<p class="font-medium">{$t('calendar.noProvider.title')}</p>
		<p class="mt-1 text-amber-700">
			{$t('calendar.noProvider.body')}
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
								<p class="text-xs text-muted-foreground">{label(c.provider)} · {$t('calendar.checkedForConflicts')}</p>
								{#if reauthNeeded.has(c.id)}
									<p class="text-xs font-medium text-destructive">{$t('calendar.reconnectNeeded')}</p>
								{/if}
							</div>
						</div>
						<div class="flex shrink-0 items-center gap-4">
							<label class="flex cursor-pointer items-center gap-1.5 text-sm text-muted-foreground">
								<input type="radio" name="destination" checked={c.is_destination} disabled={busy} onchange={() => setDestination(c)} />
								{$t('calendar.addBookingsHere')}
							</label>
							<Button variant="ghost" size="sm" onclick={() => askDisconnect(c)} disabled={busy}>{$t('calendar.disconnect')}</Button>
						</div>
						</div>
						<div class="mt-2 pl-8">
							<button type="button" class="text-xs font-medium text-muted-foreground underline-offset-2 hover:underline" onclick={() => togglePicker(c)}>
								{pickerConnId === c.id ? $t('calendar.hideCalendars') : $t('calendar.manageCalendars')}
							</button>
						</div>
						{#if pickerConnId === c.id}
							<div class="mt-3 space-y-3 rounded-md border bg-muted/30 p-3">
								{#if pickerLoading}
									<p class="text-xs text-muted-foreground">{$t('calendar.loadingCalendars')}</p>
								{:else if pickerErr}
									<p class="text-xs text-destructive">{pickerErr}</p>
								{:else if pickerCals.length === 0}
									<p class="text-xs text-muted-foreground">{$t('calendar.noCalendars')}</p>
								{:else}
									<p class="text-xs text-muted-foreground">
										{$t('calendar.picker.instructions')}
									</p>
									<div class="space-y-1.5">
										<div class="flex items-center gap-2 pb-1 text-xs font-medium text-muted-foreground">
											<span class="w-10 shrink-0 text-center">{$t('calendar.picker.check')}</span>
											<span class="w-20 shrink-0 text-center">{$t('calendar.picker.destination')}</span>
											<span>{$t('calendar.picker.calendar')}</span>
										</div>
										{#each pickerCals as cal (cal.id)}
											<div class="flex items-center gap-2 text-sm">
												<span class="w-10 shrink-0 text-center">
													<input type="checkbox" bind:checked={cal.check_conflicts} disabled={pickerSaving}
														aria-label={$t('calendar.picker.checkAria', { name: cal.name })} />
												</span>
												<span class="w-20 shrink-0 text-center">
													<!-- One destination per account in the UI; the API enforces one per user
													     across all accounts and moves the account-level destination to match.
													     A checkbox (not a radio) so the choice can be cleared again. -->
													<input type="checkbox" disabled={pickerSaving || !cal.writable}
														checked={cal.is_destination}
														onchange={() => toggleDestination(cal)}
														aria-label={$t('calendar.picker.destinationAria', { name: cal.name })} />
												</span>
												<span class="truncate">
													{cal.name}
													{#if cal.primary}<span class="ml-1 text-xs text-muted-foreground">{$t('calendar.picker.primary')}</span>{/if}
													{#if !cal.writable}<span class="ml-1 text-xs text-muted-foreground">{$t('calendar.picker.readOnly')}</span>{/if}
												</span>
											</div>
										{/each}
									</div>
									<div class="flex items-center gap-2 pt-1">
										<Button size="sm" onclick={() => savePicker(c)} disabled={pickerSaving}>
											{pickerSaving ? $t('calendar.saving') : $t('common.save')}
										</Button>
										<Button variant="ghost" size="sm" onclick={() => (pickerConnId = null)} disabled={pickerSaving}>{$t('common.cancel')}</Button>
									</div>
								{/if}
							</div>
						{/if}
					</div>
				{/each}
			</div>
			<p class="text-xs text-muted-foreground">
				{$t('calendar.footer.manageHint')}
			</p>
		{/if}

		<!-- Connect (another) calendar -->
		{#if $authStatus.demo_mode}
			<p class="text-sm text-muted-foreground">{$t('calendar.demoDisabled')}</p>
		{:else}
		<div class="space-y-2">
			<p class="text-sm font-medium">{connections.length > 0 ? $t('calendar.connectAnother') : $t('calendar.connect')}</p>
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
								{caldavOpen ? $t('common.cancel') : $t('calendar.connectButton')}
							</Button>
						</div>
						{#if caldavOpen}
							<form class="mt-4 space-y-3 border-t pt-4" onsubmit={(e) => { e.preventDefault(); connectCaldav(); }}>
								<div class="space-y-1.5">
									<Label for="caldav-preset">{$t('calendar.caldav.provider')}</Label>
									<select id="caldav-preset" bind:value={caldavPreset}
										class="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm">
										<option value="icloud">{$t('calendar.caldav.icloud')}</option>
										<option value="fastmail">{$t('calendar.caldav.fastmail')}</option>
										<option value="custom">{$t('calendar.caldav.custom')}</option>
									</select>
								</div>
								{#if caldavPreset === 'custom'}
									<div class="space-y-1.5">
										<Label for="caldav-server">{$t('calendar.caldav.serverUrl')}</Label>
										<Input id="caldav-server" type="url" placeholder={$t('calendar.caldav.serverPlaceholder')} bind:value={caldavServer} />
									</div>
								{/if}
								<div class="space-y-1.5">
									<Label for="caldav-user">{$t('calendar.caldav.username')}</Label>
									<Input id="caldav-user" type="text" autocomplete="username" placeholder={$t('calendar.caldav.usernamePlaceholder')} bind:value={caldavUser} />
								</div>
								<div class="space-y-1.5">
									<Label for="caldav-pass">{$t('calendar.caldav.appPassword')}</Label>
									<Input id="caldav-pass" type="password" autocomplete="off" bind:value={caldavPass} />
									{#if appPwHelp[caldavPreset]}
										<p class="text-xs text-muted-foreground">
											<a class="underline" href={appPwHelp[caldavPreset].href} target="_blank" rel="noopener noreferrer">{appPwHelp[caldavPreset].label}</a> {$t('calendar.caldav.passwordSuffix')}
										</p>
									{:else}
										<p class="text-xs text-muted-foreground">{$t('calendar.caldav.passwordHint')}</p>
									{/if}
								</div>
								{#if caldavErr}
									<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{caldavErr}</p>
								{/if}
								<Button type="submit" disabled={caldavBusy}>{caldavBusy ? $t('calendar.connecting') : $t('calendar.connectCalendar')}</Button>
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
						<Button variant={connections.length > 0 ? 'outline' : 'default'} onclick={() => (window.location.href = `/v1/calendar/connect?provider=${p}`)}>{$t('calendar.connectButton')}</Button>
					</div>
				{/if}
			{/each}
			{#each status?.unconfigured_providers ?? [] as p}
				<div class="flex items-center justify-between rounded-lg border border-dashed bg-muted/20 p-4">
					<div class="flex items-center gap-3">
						<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground/60">
							<rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>
						</svg>
						<div>
							<p class="font-medium text-muted-foreground">{label(p)}</p>
							<p class="text-xs text-muted-foreground">{$t('calendar.unconfigured')}</p>
						</div>
					</div>
					{#if UNCONFIGURED_SETUP[p]}
						<a href={UNCONFIGURED_SETUP[p].href}
							class="text-sm font-medium text-primary underline-offset-2 hover:underline"
							target={UNCONFIGURED_SETUP[p].external ? '_blank' : undefined}
							rel={UNCONFIGURED_SETUP[p].external ? 'noopener noreferrer' : undefined}>
							{UNCONFIGURED_SETUP[p].text}
						</a>
					{/if}
				</div>
			{/each}
			{#if connections.length > 0}
				<p class="text-xs text-muted-foreground">{$t('calendar.hint.multiCalendar')}</p>
			{/if}
		</div>
		{/if}
	</div>
{/if}

<!-- Zoom — per-host meeting links (independent of the calendar) -->
{#if zoom?.configured && !$authStatus.demo_mode}
	<div class="mt-10">
		<h2 class="text-lg font-semibold tracking-tight">{$t('calendar.zoom.title')}</h2>
		<p class="mt-1 text-sm text-muted-foreground">
			{$t('calendar.zoom.subtitle')}
		</p>
		{#if zoomJustConnected && zoom.connected}
			<p class="mt-3 max-w-md rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">{$t('calendar.zoom.connectedSuccess')}</p>
		{/if}
		<div class="mt-3 max-w-md">
			{#if zoom.connected}
				<div class="flex items-center justify-between rounded-lg border bg-card p-5">
					<div class="flex items-center gap-3">
						<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground"><path d="m22 8-6 4 6 4V8Z"/><rect x="2" y="6" width="14" height="12" rx="2"/></svg>
						<div>
							<p class="font-medium">{$t('calendar.zoom.title')}</p>
							<p class="mt-0.5 inline-flex items-center gap-1.5 text-sm text-green-700">
								<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>
								{$t('calendar.zoom.connected')}
							</p>
						</div>
					</div>
					<Button variant="outline" onclick={() => (zoomDisconnectOpen = true)} disabled={zoomDisconnecting}>
						{zoomDisconnecting ? $t('calendar.zoom.disconnecting') : $t('calendar.disconnect')}
					</Button>
				</div>
			{:else}
				<div class="flex items-center justify-between rounded-lg border bg-card p-5">
					<div class="flex items-center gap-3">
						<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0 text-muted-foreground"><path d="m22 8-6 4 6 4V8Z"/><rect x="2" y="6" width="14" height="12" rx="2"/></svg>
						<p class="font-medium">{$t('calendar.zoom.title')}</p>
					</div>
					<Button onclick={() => (window.location.href = '/v1/zoom/connect')}>{$t('calendar.connectButton')}</Button>
				</div>
			{/if}
		</div>
	</div>
{/if}
