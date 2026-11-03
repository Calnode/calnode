<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type ZoomSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';
	import { t, tr } from '$lib/i18n';

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();

	let settings = $state<ZoomSettings | null>(null);
	let clientID = $state('');
	let clientSecret = $state('');

	const redirectURI = $derived(settings?.redirect_uri || '');

	onMount(() => loadingFlag.run(async () => {
		settings = await api.get<ZoomSettings>('/v1/settings/zoom');
		clientID = settings.client_id;
	}, tr('settings.zoom.error.load')));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = { client_id: clientID };
			if (clientSecret) body.client_secret = clientSecret;
			settings = await api.patch<ZoomSettings>('/v1/settings/zoom', body);
			clientSecret = '';
			toast.success(tr('settings.zoom.toast.saved'));
		}, tr('settings.zoom.error.save'));
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">{$t('common.adminRequired')}</p>
{:else}

{#if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else}
	<div class="max-w-lg space-y-4">

		{#if !settings?.configured}
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">{$t('settings.zoom.setup.title')}</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>{$t('settings.zoom.setup.step1')}</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>{$t('settings.zoom.setup.step2')}</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						{$t('settings.zoom.setup.step3')}
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectURI}</code>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">4</span>
					<div>{$t('settings.zoom.setup.step4')}</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">5</span>
					<div>{$t('settings.zoom.setup.step5')}</div>
				</li>
			</ol>
		</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">{$t('settings.zoom.title')}</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.zoom.subtitle')}</p>
				</div>
				{#if settings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {settings.configured ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {settings.configured ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{settings.configured ? $t('common.configured') : $t('common.notConfigured')}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="z-client-id">{$t('settings.zoom.clientId')}</Label>
					<Input id="z-client-id" type="text" placeholder={$t('settings.zoom.clientIdPlaceholder')} bind:value={clientID} />
				</div>
				<div class="space-y-1.5">
					<Label for="z-client-secret">{$t('settings.zoom.clientSecret')}</Label>
					<Input id="z-client-secret" type="password"
						placeholder={settings?.client_secret_set ? $t('common.storedSecretPlaceholder') : $t('settings.zoom.clientSecretPlaceholder')}
						bind:value={clientSecret} />
					{#if settings?.client_secret_set && !clientSecret}
						<p class="text-xs text-muted-foreground">{$t('common.leaveBlankKeep')}</p>
					{/if}
				</div>
			</div>

			{#if settings?.configured}
				<div class="mt-5 border-t pt-4">
					<p class="text-xs font-medium text-muted-foreground">{$t('settings.zoom.redirect.title')}</p>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.zoom.redirect.hint')}</p>
					<code class="mt-2 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectURI}</code>
				</div>
				<div class="mt-4 rounded-md bg-amber-50 px-3 py-2.5 text-xs text-amber-800 ring-1 ring-inset ring-amber-200">
					{$t('settings.zoom.multiMemberWarning')}
				</div>
			{/if}

			<div class="mt-5">
				<Button onclick={save} disabled={savingFlag.active}>
					{savingFlag.active ? $t('common.saving') : $t('common.save')}
				</Button>
			</div>
		</div>
	</div>
{/if}

{/if}
