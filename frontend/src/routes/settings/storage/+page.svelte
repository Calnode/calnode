<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Switch } from '$lib/components/ui/switch';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	type StorageSettings = {
		backups_configured: boolean;
		backups_bucket: string;
		backups_endpoint: string;
		recordings_enabled: boolean;
		recordings_storage_ready: boolean;
		recordings_prefix: string;
	};

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	let settings = $state<StorageSettings | null>(null);
	let recordingsEnabled = $state(false);

	onMount(() => loadingFlag.run(async () => {
		settings = await api.get<StorageSettings>('/v1/settings/storage');
		recordingsEnabled = settings.recordings_enabled;
	}, 'Could not load storage settings'));

	async function save() {
		await savingFlag.run(async () => {
			settings = await api.patch<StorageSettings>('/v1/settings/storage', { recordings_enabled: recordingsEnabled });
			toast.success('Saved');
		}, 'Could not save');
	}

	function badge(ok: boolean, on = 'Configured', off = 'Not configured') {
		return { text: ok ? on : off, cls: ok ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700', dot: ok ? 'bg-green-500' : 'bg-amber-400' };
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Admin access required.</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<div class="max-w-lg space-y-4">
		<p class="text-sm text-muted-foreground">Object storage is used in two places — database backups and meeting recordings. They share one bucket.</p>

		{#if !settings?.backups_configured}
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Setup instructions</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>
						Create a bucket with any S3-compatible object storage provider — for example
						<a href="https://developers.cloudflare.com/r2/" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">Cloudflare R2</a>,
						<a href="https://www.backblaze.com/cloud-storage" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">Backblaze B2</a>,
						or <a href="https://aws.amazon.com/s3/" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">AWS S3</a>.
						This is the same bucket both database backups and meeting recordings write to.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>Generate an access key ID and secret access key for that bucket.</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						There's no form for this — set these as environment variables/secrets with your hosting
						provider, then redeploy:
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">LITESTREAM_REPLICA_URL=s3://your-bucket-name/calnode</code>
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">LITESTREAM_ACCESS_KEY_ID=…</code>
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">LITESTREAM_SECRET_ACCESS_KEY=…</code>
						<p class="mt-1.5 text-xs text-muted-foreground">
							For any provider other than AWS S3 (R2, B2, MinIO, etc.), also set
							<code class="rounded bg-muted px-1">LITESTREAM_ENDPOINT</code> to that provider's S3-compatible
							endpoint URL. <code class="rounded bg-muted px-1">LITESTREAM_REGION</code> is optional — only
							some providers need it.
						</p>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">4</span>
					<div>
						Reload this page — once it shows <span class="font-medium">Configured</span> below, meeting
						recordings can be turned on beneath it.
					</div>
				</li>
			</ol>
		</div>
		{/if}

		<!-- Backups (read-only; configured via environment) -->
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-3 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Database backups (Litestream)</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">Continuous SQLite replication to your bucket. Configured via environment at deploy.</p>
				</div>
				{#if settings}
					{@const b = badge(settings.backups_configured)}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {b.cls}">
						<span class="h-1.5 w-1.5 rounded-full {b.dot}"></span>{b.text}
					</span>
				{/if}
			</div>
			{#if settings?.backups_configured}
				<dl class="mt-2 space-y-1 text-xs">
					<div class="flex gap-2"><dt class="w-20 text-muted-foreground">Bucket</dt><dd class="font-mono">{settings.backups_bucket || '—'}</dd></div>
					{#if settings.backups_endpoint}<div class="flex gap-2"><dt class="w-20 text-muted-foreground">Endpoint</dt><dd class="font-mono break-all">{settings.backups_endpoint}</dd></div>{/if}
				</dl>
			{:else}
				<p class="text-xs text-muted-foreground">Set <code class="rounded bg-muted px-1">LITESTREAM_REPLICA_URL</code> and credentials to enable backups — see setup instructions above.</p>
			{/if}
		</div>

		<!-- Recordings -->
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-3 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Meeting recordings</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">LiveKit video recordings upload to the backups bucket under a <code class="rounded bg-muted px-1">{settings?.recordings_prefix || 'recordings/'}</code> prefix.</p>
				</div>
				{#if settings}
					{@const b = badge(settings.recordings_storage_ready, 'Storage ready', 'No bucket')}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {b.cls}">
						<span class="h-1.5 w-1.5 rounded-full {b.dot}"></span>{b.text}
					</span>
				{/if}
			</div>
			<label class="flex cursor-pointer items-start justify-between gap-3">
				<span>
					<span class="text-sm font-medium">Allow hosts to record meetings</span>
					<span class="mt-0.5 block text-xs text-muted-foreground">
						The host gets a Record button in the call; everyone sees a “Recording” banner.
						{#if !settings?.recordings_storage_ready}<span class="text-amber-700"> Configure backups first (see setup instructions above) — recordings need a bucket.</span>{/if}
					</span>
				</span>
				<Switch class="mt-0.5 shrink-0" bind:checked={recordingsEnabled} disabled={!settings?.recordings_storage_ready} />
			</label>
			<div class="mt-5"><Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? 'Saving…' : 'Save'}</Button></div>
		</div>
	</div>
{/if}
