<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type StripeSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';
	import { t, tr } from '$lib/i18n';

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	let confirmDisconnectOpen = $state(false);

	let settings = $state<StripeSettings | null>(null);
	let secretKey = $state('');
	let publishableKey = $state('');
	let webhookSecret = $state('');

	const webhookURL = $derived(settings?.webhook_url || '');

	onMount(() => loadingFlag.run(async () => {
		settings = await api.get<StripeSettings>('/v1/settings/stripe');
		publishableKey = settings.publishable_key;
	}, tr('settings.payments.error.load')));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = { publishable_key: publishableKey };
			if (secretKey) body.secret_key = secretKey;
			if (webhookSecret) body.webhook_secret = webhookSecret;
			settings = await api.patch<StripeSettings>('/v1/settings/stripe', body);
			secretKey = '';
			webhookSecret = '';
			toast.success(tr('settings.payments.toast.saved'));
		}, tr('settings.payments.error.save'));
	}

	async function disconnect() {
		await savingFlag.run(async () => {
			settings = await api.patch<StripeSettings>('/v1/settings/stripe', { clear: true });
			secretKey = publishableKey = webhookSecret = '';
			toast.success(tr('settings.payments.toast.disconnected'));
		}, tr('settings.payments.error.disconnect'));
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
			<h2 class="mb-4 text-sm font-semibold">{$t('settings.payments.setup.title')}</h2>
			<ol class="space-y-4 text-sm">
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">1</span>
					<div>{$t('settings.payments.setup.step1')}</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>
						{$t('settings.payments.setup.step2')}
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{webhookURL}</code>
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>{$t('settings.payments.setup.step3')}</div>
				</li>
			</ol>
		</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">{$t('settings.payments.stripe.title')}</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.payments.stripe.subtitle')}</p>
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
					<Label for="s-secret">{$t('settings.payments.secretKey')}</Label>
					<Input id="s-secret" type="password"
						placeholder={settings?.secret_key_set ? $t('common.storedSecretPlaceholder') : $t('settings.payments.secretKeyPlaceholder')}
						bind:value={secretKey} />
					{#if settings?.secret_key_set && !secretKey}
						<p class="text-xs text-muted-foreground">{$t('common.leaveBlankKeep')}</p>
					{/if}
				</div>
				<div class="space-y-1.5">
					<Label for="s-pub">{$t('settings.payments.publishableKey')}</Label>
					<Input id="s-pub" type="text" placeholder={$t('settings.payments.publishableKeyPlaceholder')} bind:value={publishableKey} />
				</div>
				<div class="space-y-1.5">
					<Label for="s-wh">{$t('settings.payments.webhookSecret')}</Label>
					<Input id="s-wh" type="password"
						placeholder={settings?.webhook_secret_set ? $t('common.storedSecretPlaceholder') : $t('settings.payments.webhookSecretPlaceholder')}
						bind:value={webhookSecret} />
					{#if settings?.webhook_secret_set && !webhookSecret}
						<p class="text-xs text-muted-foreground">{$t('common.leaveBlankKeep')}</p>
					{/if}
				</div>
			</div>

			{#if settings?.configured}
				<div class="mt-5 border-t pt-4">
					<p class="text-xs font-medium text-muted-foreground">{$t('settings.payments.webhookEndpoint.title')}</p>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.payments.webhookEndpoint.hint')}</p>
					<code class="mt-2 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{webhookURL}</code>
				</div>
			{/if}

			<div class="mt-5 flex items-center gap-2">
				<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? $t('common.saving') : $t('common.save')}</Button>
				{#if settings?.configured}
					<Button variant="outline" onclick={() => (confirmDisconnectOpen = true)} disabled={savingFlag.active}>{$t('settings.payments.disconnect')}</Button>
				{/if}
			</div>
		</div>
	</div>
{/if}


<ConfirmDialog
	bind:open={confirmDisconnectOpen}
	title={$t('settings.payments.confirmDisconnect.title')}
	description={$t('settings.payments.confirmDisconnect.description')}
	confirmText={$t('settings.payments.confirmDisconnect.confirm')}
	destructive
	onConfirm={disconnect}
/>
{/if}
