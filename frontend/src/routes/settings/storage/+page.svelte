<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Switch } from '$lib/components/ui/switch';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';
	import { t, tr } from '$lib/i18n';
	import LinkedText from '$lib/i18n/linked-text.svelte';

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
	}, tr('settings.storage.error.load')));

	async function save() {
		await savingFlag.run(async () => {
			settings = await api.patch<StorageSettings>('/v1/settings/storage', { recordings_enabled: recordingsEnabled });
			toast.success(tr('settings.storage.toast.saved'));
		}, tr('settings.storage.error.save'));
	}

	function badge(ok: boolean, on?: string, off?: string) {
		return {
			text: ok ? (on ?? tr('common.configured')) : (off ?? tr('common.notConfigured')),
			cls: ok ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700',
			dot: ok ? 'bg-green-500' : 'bg-amber-400'
		};
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">{$t('common.adminRequired')}</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else}
	<div class="max-w-lg space-y-4">
		<p class="text-sm text-muted-foreground">{$t('settings.storage.intro')}</p>

		{#if !settings?.backups_configured}
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">{$t('settings.storage.setup.title')}</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>
						<LinkedText
							text={$t('settings.storage.setup.step1')}
							links={{
								r2: {
									href: 'https://developers.cloudflare.com/r2/',
									text: $t('settings.storage.setup.r2'),
									target: '_blank',
									rel: 'noopener noreferrer',
									class: 'font-medium text-primary underline'
								},
								b2: {
									href: 'https://www.backblaze.com/cloud-storage',
									text: $t('settings.storage.setup.b2'),
									target: '_blank',
									rel: 'noopener noreferrer',
									class: 'font-medium text-primary underline'
								},
								s3: {
									href: 'https://aws.amazon.com/s3/',
									text: $t('settings.storage.setup.s3'),
									target: '_blank',
									rel: 'noopener noreferrer',
									class: 'font-medium text-primary underline'
								}
							}}
						/>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>{$t('settings.storage.setup.step2')}</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						{$t('settings.storage.setup.step3')}
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">LITESTREAM_REPLICA_URL=s3://your-bucket-name/calnode</code>
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">LITESTREAM_ACCESS_KEY_ID=…</code>
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">LITESTREAM_SECRET_ACCESS_KEY=…</code>
						<p class="mt-1.5 text-xs text-muted-foreground">{$t('settings.storage.setup.step3EndpointHint')}</p>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">4</span>
					<div>{$t('settings.storage.setup.step4')}</div>
				</li>
			</ol>
		</div>
		{/if}

		<!-- Backups (read-only; configured via environment) -->
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-3 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">{$t('settings.storage.backups.title')}</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.storage.backups.subtitle')}</p>
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
					<div class="flex gap-2"><dt class="w-20 text-muted-foreground">{$t('settings.storage.backups.bucket')}</dt><dd class="font-mono">{settings.backups_bucket || $t('common.emDash')}</dd></div>
					{#if settings.backups_endpoint}<div class="flex gap-2"><dt class="w-20 text-muted-foreground">{$t('settings.storage.backups.endpoint')}</dt><dd class="font-mono break-all">{settings.backups_endpoint}</dd></div>{/if}
				</dl>
			{:else}
				<p class="text-xs text-muted-foreground">{$t('settings.storage.backups.notConfiguredHint')}</p>
			{/if}
		</div>

		<!-- Recordings -->
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-3 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">{$t('settings.storage.recordings.title')}</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.storage.recordings.subtitle', { prefix: settings?.recordings_prefix || 'recordings/' })}</p>
				</div>
				{#if settings}
					{@const b = badge(settings.recordings_storage_ready, $t('settings.storage.recordings.badge.ready'), $t('settings.storage.recordings.badge.noBucket'))}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {b.cls}">
						<span class="h-1.5 w-1.5 rounded-full {b.dot}"></span>{b.text}
					</span>
				{/if}
			</div>
			<label class="flex cursor-pointer items-start justify-between gap-3">
				<span>
					<span class="text-sm font-medium">{$t('settings.storage.recordings.allow')}</span>
					<span class="mt-0.5 block text-xs text-muted-foreground">
						{$t('settings.storage.recordings.allowHint')}
						{#if !settings?.recordings_storage_ready}<span class="text-amber-700"> {$t('settings.storage.recordings.needBucket')}</span>{/if}
					</span>
				</span>
				<Switch class="mt-0.5 shrink-0" bind:checked={recordingsEnabled} disabled={!settings?.recordings_storage_ready} />
			</label>
			<div class="mt-5"><Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? $t('common.saving') : $t('common.save')}</Button></div>
		</div>
	</div>
{/if}
