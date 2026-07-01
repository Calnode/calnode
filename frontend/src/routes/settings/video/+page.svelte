<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';

	type LiveKitSettings = {
		url: string;
		api_key: string;
		api_secret_set: boolean;
		configured: boolean;
	};
	type NotetakerSettings = { enabled: boolean; stt_api_key_set: boolean };

	let loading = $state(true);
	let saving = $state(false);

	let settings = $state<LiveKitSettings | null>(null);
	let url = $state('');
	let apiKey = $state('');
	let apiSecret = $state('');
	let webhookUrl = $state('');

	let notetaker = $state<NotetakerSettings | null>(null);
	let notetakerEnabled = $state(false);
	let deepgramKey = $state('');

	onMount(async () => {
		webhookUrl = `${window.location.origin}/v1/livekit/webhook`;
		try {
			settings = await api.get<LiveKitSettings>('/v1/settings/livekit');
			url = settings.url;
			apiKey = settings.api_key;
			notetaker = await api.get<NotetakerSettings>('/v1/settings/notetaker');
			notetakerEnabled = notetaker.enabled;
		} catch (e: any) {
			toast.error(e.message || 'Could not load video settings');
		} finally {
			loading = false;
		}
	});

	async function saveNotetaker() {
		saving = true;
		try {
			const body: Record<string, unknown> = { enabled: notetakerEnabled };
			if (deepgramKey.trim()) body.stt_api_key = deepgramKey.trim();
			notetaker = await api.patch<NotetakerSettings>('/v1/settings/notetaker', body);
			notetakerEnabled = notetaker.enabled;
			deepgramKey = '';
			toast.success('Notetaker settings saved');
		} catch (e: any) {
			toast.error(e.message || 'Could not save notetaker settings');
		} finally {
			saving = false;
		}
	}

	async function save() {
		saving = true;
		try {
			const body: Record<string, unknown> = { url: url.trim(), api_key: apiKey.trim() };
			if (apiSecret) body.api_secret = apiSecret;
			settings = await api.patch<LiveKitSettings>('/v1/settings/livekit', body);
			apiSecret = '';
			toast.success('Saved — "Calnode Video (LiveKit)" is now selectable as an event location');
		} catch (e: any) {
			toast.error(e.message || 'Could not save video settings');
		} finally {
			saving = false;
		}
	}

	async function copyWebhook() {
		try {
			await navigator.clipboard.writeText(webhookUrl);
			toast.success('Webhook URL copied');
		} catch {
			toast.error('Could not copy — select and copy manually');
		}
	}

	async function disconnect() {
		saving = true;
		try {
			settings = await api.patch<LiveKitSettings>('/v1/settings/livekit', { url: '' });
			url = ''; apiKey = ''; apiSecret = '';
			toast.success('LiveKit disconnected');
		} catch (e: any) {
			toast.error(e.message || 'Could not disconnect');
		} finally {
			saving = false;
		}
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !saving)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Admin access required.</p>
{:else if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<div class="max-w-lg space-y-4">
		{#if !settings?.configured}
			<div class="rounded-lg border bg-card p-6">
				<h2 class="mb-4 text-sm font-semibold">Setup instructions</h2>
				<ol class="space-y-4 text-sm">
					<li class="flex gap-3">
						<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
						<div>
							Create a free project at <a href="https://cloud.livekit.io" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">cloud.livekit.io</a>
							(or run your own <a href="https://docs.livekit.io/home/self-hosting/local/" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">self-hosted server</a> — same fields).
						</div>
					</li>
					<li class="flex gap-3">
						<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
						<div>
							Copy the project's <span class="font-medium">WebSocket URL</span> (looks like
							<code class="rounded bg-muted px-1 text-xs">wss://yourproject.livekit.cloud</code>) and an
							API <span class="font-medium">key</span> + <span class="font-medium">secret</span>.
						</div>
					</li>
					<li class="flex gap-3">
						<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
						<div>Paste them below and save. Bookings using "Calnode Video (LiveKit)" then get a built-in room — no per-host connection needed.</div>
					</li>
				</ol>
			</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Calnode Video (LiveKit)</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Built-in video meetings hosted on your LiveKit server. Each booking gets a room link;
						guests join in the browser — no account or app required.
					</p>
				</div>
				{#if settings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {settings.configured ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {settings.configured ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{settings.configured ? 'Configured' : 'Not configured'}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="lk-url">Server URL</Label>
					<Input id="lk-url" type="text" placeholder="wss://yourproject.livekit.cloud" bind:value={url} />
				</div>
				<div class="space-y-1.5">
					<Label for="lk-key">API Key</Label>
					<Input id="lk-key" type="text" placeholder="APIxxxxxxxx" bind:value={apiKey} />
				</div>
				<div class="space-y-1.5">
					<Label for="lk-secret">API Secret</Label>
					<Input id="lk-secret" type="password"
						placeholder={settings?.api_secret_set ? '•••••••• (stored)' : 'Enter API secret'}
						bind:value={apiSecret} />
					{#if settings?.api_secret_set && !apiSecret}
						<p class="text-xs text-muted-foreground">Stored — leave blank to keep it.</p>
					{/if}
				</div>
			</div>

			<div class="mt-5 flex items-center gap-3">
				<Button onclick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
				{#if settings?.configured}
					<Button variant="ghost" onclick={disconnect} disabled={saving}>Disconnect</Button>
				{/if}
			</div>
		</div>

		{#if settings?.configured}
			<div class="rounded-lg border bg-card p-6">
				<h2 class="text-sm font-semibold">Webhook (recommended)</h2>
				<p class="mt-0.5 text-xs text-muted-foreground">
					Register this URL in LiveKit so recordings finalize with accurate duration and end cleanly
					when a meeting closes. Recordings still work without it — this just makes them reliable.
				</p>
				<div class="mt-3 flex items-center gap-2">
					<code class="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1.5 text-xs">{webhookUrl}</code>
					<Button variant="outline" size="sm" onclick={copyWebhook}>Copy</Button>
				</div>
				<ol class="mt-4 space-y-2 text-xs text-muted-foreground">
					<li>
						1. In LiveKit Cloud, open <span class="font-medium">Project → Settings → Webhooks</span>
						(<a href="https://docs.livekit.io/home/server/webhooks/" target="_blank" rel="noopener noreferrer" class="text-primary underline">docs</a>).
					</li>
					<li>2. Add a webhook with the URL above, and attach the <span class="font-medium">same API key</span> you entered here — it signs the events so Calnode can verify them.</li>
					<li>3. Save. LiveKit sends all project events to this one URL; Calnode verifies each signature and uses the recording-related ones.</li>
				</ol>
			</div>

			<div class="rounded-lg border bg-card p-6">
				<div class="mb-3 flex items-start justify-between gap-3">
					<div>
						<h2 class="text-sm font-semibold">AI meeting notes (notetaker)</h2>
						<p class="mt-0.5 text-xs text-muted-foreground">
							After a meeting is recorded, transcribe it and summarize it into notes on the booking
							(using your configured LLM). Needs recording on, an LLM configured, and a Deepgram key.
						</p>
					</div>
					<Switch bind:checked={notetakerEnabled} />
				</div>
				<div class="space-y-1.5">
					<Label for="dg-key">Deepgram API key</Label>
					<Input id="dg-key" type="password"
						placeholder={notetaker?.stt_api_key_set ? '•••••••• (stored)' : 'Enter Deepgram API key'}
						bind:value={deepgramKey} />
					<p class="text-xs text-muted-foreground">
						Get one at <a href="https://console.deepgram.com" target="_blank" rel="noopener noreferrer" class="text-primary underline">console.deepgram.com</a>.{#if notetaker?.stt_api_key_set} Stored — leave blank to keep it.{/if}
					</p>
				</div>
				<div class="mt-5">
					<Button onclick={saveNotetaker} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
				</div>
			</div>
		{/if}
	</div>
{/if}
