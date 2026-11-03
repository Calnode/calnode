<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type GoogleSettings } from '$lib/api';
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

	let googleSettings = $state<GoogleSettings | null>(null);
	let clientID = $state('');
	let clientSecret = $state('');

	// Host the server builds its OAuth redirect URIs from. Prefer the server's
	// configured base_url so the displayed URIs match exactly what we send to
	// Google; fall back to the current origin if it's somehow blank.
	const redirectBase = $derived(
		googleSettings?.base_url || (typeof window !== 'undefined' ? window.location.origin : '')
	);
	const isLocal = $derived(redirectBase.includes('localhost') || redirectBase.includes('127.0.0.1'));

	// Catches the "moved to a custom domain but never updated BASE_URL" trap: the server
	// still computes redirect URIs from its own configured base_url, which can silently
	// drift from whatever domain an admin is actually browsing this page at (e.g. after
	// pointing a custom domain at a host whose BASE_URL secret still says the old default).
	const browserOrigin = $derived(typeof window !== 'undefined' ? window.location.origin : '');
	const originMismatch = $derived(
		!!googleSettings?.base_url && !!browserOrigin &&
		googleSettings.base_url.replace(/\/+$/, '') !== browserOrigin
	);

	onMount(() => loadingFlag.run(async () => {
		googleSettings = await api.get<GoogleSettings>('/v1/settings/google');
		clientID = googleSettings.client_id;
	}, tr('settings.google.error.load')));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = { client_id: clientID };
			if (clientSecret) body.client_secret = clientSecret;
			googleSettings = await api.patch<GoogleSettings>('/v1/settings/google', body);
			clientSecret = '';
			toast.success(tr('settings.google.toast.saved'));
		}, tr('settings.google.error.save'));
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

		{#if originMismatch}
			<div class="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900">
				<p class="font-medium">{$t('settings.google.originMismatch.title')}</p>
				<p class="mt-1 text-amber-800">
					{$t('settings.google.originMismatch.body', { browserOrigin, baseUrl: googleSettings?.base_url ?? '' })}
				</p>
			</div>
		{/if}

		{#if !googleSettings?.configured}
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">{$t('settings.google.setup.title')}</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>{$t('settings.google.setup.step1')}</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>{$t('settings.google.setup.step2')}</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						{$t('settings.google.setup.step3')}
						<p class="mt-1.5 text-xs text-muted-foreground">{$t('settings.google.setup.step3Testing')}</p>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">4</span>
					<div>{$t('settings.google.setup.step4')}</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">5</span>
					<div>
						{$t('settings.google.setup.step5')}
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/calendar/callback</code>
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/auth/callback</code>
						{#if !isLocal}
							<p class="mt-1.5 text-xs text-muted-foreground">{$t('settings.google.setup.step5Localhost')}</p>
						{/if}
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">6</span>
					<div>{$t('settings.google.setup.step6')}</div>
				</li>
			</ol>
		</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">{$t('settings.google.oauth.title')}</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.google.oauth.subtitle')}</p>
				</div>
				{#if googleSettings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {googleSettings.configured ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {googleSettings.configured ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{googleSettings.configured ? $t('common.configured') : $t('common.notConfigured')}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="g-client-id">{$t('settings.google.clientId')}</Label>
					<Input id="g-client-id" type="text" placeholder={$t('settings.google.clientIdPlaceholder')} bind:value={clientID} />
				</div>
				<div class="space-y-1.5">
					<Label for="g-client-secret">{$t('settings.google.clientSecret')}</Label>
					<Input id="g-client-secret" type="password"
						placeholder={googleSettings?.client_secret_set ? $t('common.storedSecretPlaceholder') : $t('settings.google.clientSecretPlaceholder')}
						bind:value={clientSecret} />
					{#if googleSettings?.client_secret_set && !clientSecret}
						<p class="text-xs text-muted-foreground">{$t('common.leaveBlankKeep')}</p>
					{/if}
				</div>
			</div>

			{#if googleSettings?.configured}
				<div class="mt-5 border-t pt-4">
					<p class="text-xs font-medium text-muted-foreground">{$t('settings.google.redirectUris.title')}</p>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.google.redirectUris.hint')}</p>
					<code class="mt-2 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/calendar/callback</code>
					<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{redirectBase}/v1/auth/callback</code>
					{#if !isLocal}
						<p class="mt-1.5 text-xs text-muted-foreground">{$t('settings.google.redirectUris.localhostHint')}</p>
					{/if}
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
