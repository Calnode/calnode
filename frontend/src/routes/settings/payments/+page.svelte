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
	}, 'Could not load payment settings'));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = { publishable_key: publishableKey };
			if (secretKey) body.secret_key = secretKey;
			if (webhookSecret) body.webhook_secret = webhookSecret;
			settings = await api.patch<StripeSettings>('/v1/settings/stripe', body);
			secretKey = '';
			webhookSecret = '';
			toast.success('Saved — set a price on an event type to start charging');
		}, 'Could not save payment settings');
	}

	async function disconnect() {
		await savingFlag.run(async () => {
			settings = await api.patch<StripeSettings>('/v1/settings/stripe', { clear: true });
			secretKey = publishableKey = webhookSecret = '';
			toast.success('Stripe disconnected');
		}, 'Could not disconnect');
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Admin access required.</p>
{:else}

{#if loadingFlag.active}
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
						In your <a href="https://dashboard.stripe.com/apikeys" target="_blank" rel="noopener noreferrer" class="font-medium text-primary underline">Stripe Dashboard → Developers → API keys</a>,
						copy the <span class="font-medium">Secret key</span> and <span class="font-medium">Publishable key</span>.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">2</span>
					<div>
						Go to <span class="font-medium">Developers → Webhooks → Add endpoint</span> and set the URL to:
						<code class="mt-1 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{webhookURL}</code>
						Subscribe to <code class="rounded bg-muted px-1">checkout.session.completed</code> and
						<code class="rounded bg-muted px-1">checkout.session.expired</code>.
					</div>
				</li>
				<li class="flex gap-3">
					<span class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">3</span>
					<div>
						Copy the endpoint's <span class="font-medium">Signing secret</span> (<code class="rounded bg-muted px-1">whsec_…</code>),
						paste all three values below, and save.
					</div>
				</li>
			</ol>
		</div>
		{/if}

		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Stripe</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Lets you charge for bookings. An event type with a price sends the booker to Stripe
						Checkout before the slot is confirmed.
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
					<Label for="s-secret">Secret key</Label>
					<Input id="s-secret" type="password"
						placeholder={settings?.secret_key_set ? '•••••••• (stored)' : 'sk_live_… or sk_test_…'}
						bind:value={secretKey} />
					{#if settings?.secret_key_set && !secretKey}
						<p class="text-xs text-muted-foreground">Stored — leave blank to keep it.</p>
					{/if}
				</div>
				<div class="space-y-1.5">
					<Label for="s-pub">Publishable key</Label>
					<Input id="s-pub" type="text" placeholder="pk_live_… or pk_test_…" bind:value={publishableKey} />
				</div>
				<div class="space-y-1.5">
					<Label for="s-wh">Webhook signing secret</Label>
					<Input id="s-wh" type="password"
						placeholder={settings?.webhook_secret_set ? '•••••••• (stored)' : 'whsec_…'}
						bind:value={webhookSecret} />
					{#if settings?.webhook_secret_set && !webhookSecret}
						<p class="text-xs text-muted-foreground">Stored — leave blank to keep it.</p>
					{/if}
				</div>
			</div>

			{#if settings?.configured}
				<div class="mt-5 border-t pt-4">
					<p class="text-xs font-medium text-muted-foreground">Webhook endpoint</p>
					<p class="mt-0.5 text-xs text-muted-foreground">Register this in Stripe → Developers → Webhooks.</p>
					<code class="mt-2 block rounded bg-muted px-2 py-1 text-xs font-mono break-all">{webhookURL}</code>
				</div>
			{/if}

			<div class="mt-5 flex items-center gap-2">
				<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? 'Saving…' : 'Save'}</Button>
				{#if settings?.configured}
					<Button variant="outline" onclick={() => (confirmDisconnectOpen = true)} disabled={savingFlag.active}>Disconnect</Button>
				{/if}
			</div>
		</div>
	</div>
{/if}


<ConfirmDialog
	bind:open={confirmDisconnectOpen}
	title="Remove Stripe credentials?"
	description="Paid event types will stop being bookable."
	confirmText="Remove"
	destructive
	onConfirm={disconnect}
/>
{/if}
