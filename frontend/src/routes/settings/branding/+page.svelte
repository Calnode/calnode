<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';

	type Branding = { business_name: string; logo_url: string };

	let loading = $state(true);
	let saving = $state(false);
	let businessName = $state('');
	let logoUrl = $state('');

	const logoValid = $derived(logoUrl === '' || logoUrl.startsWith('https://'));

	onMount(async () => {
		try {
			const b = await api.get<Branding>('/v1/settings/branding');
			businessName = b.business_name ?? '';
			logoUrl = b.logo_url ?? '';
		} catch (e: any) {
			toast.error(e.message || 'Could not load branding settings');
		} finally {
			loading = false;
		}
	});

	async function save() {
		if (!logoValid) {
			toast.error('Logo URL must be an absolute https:// URL');
			return;
		}
		saving = true;
		try {
			const b = await api.patch<Branding>('/v1/settings/branding', {
				business_name: businessName,
				logo_url: logoUrl
			});
			businessName = b.business_name ?? '';
			logoUrl = b.logo_url ?? '';
			toast.success('Branding saved');
		} catch (e: any) {
			toast.error(e.message || 'Could not save branding settings');
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
	<div class="max-w-2xl space-y-6">
		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Branding</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Your business name and logo appear in confirmation/reschedule/cancellation emails and at the top of
				your public booking and manage pages.
			</p>

			<div class="mt-4 space-y-1.5">
				<Label for="business-name">Business name</Label>
				<Input id="business-name" bind:value={businessName} placeholder="Orchestratr" maxlength={200} />
				<p class="text-xs text-muted-foreground">Shown as the wordmark when no logo is set. Falls back to “Calnode” if left blank.</p>
			</div>

			<div class="mt-4 space-y-1.5">
				<Label for="logo-url">Logo URL <span class="font-normal text-muted-foreground">(optional)</span></Label>
				<Input id="logo-url" bind:value={logoUrl} placeholder="https://cdn.example.com/logo.png" />
				{#if !logoValid}
					<p class="text-xs text-destructive">Must be an absolute <code class="rounded bg-muted px-1">https://</code> URL.</p>
				{:else}
					<p class="text-xs text-muted-foreground">
						Must be a hosted <code class="rounded bg-muted px-1">https://</code> image (email clients can’t load local files). Displayed ~30px tall.
					</p>
				{/if}
			</div>
		</div>

		<!-- Live preview of the email/page header wordmark -->
		<div class="rounded-lg border bg-card p-6">
			<p class="text-xs font-medium text-muted-foreground">Header preview</p>
			<div class="mt-3 flex items-center rounded-md border bg-white p-4">
				{#if logoUrl && logoValid}
					<img src={logoUrl} alt={businessName || 'logo'} class="h-[30px] max-h-[30px]" />
				{:else}
					<span class="text-[15px] font-semibold text-zinc-900">{businessName || 'Calnode'}</span>
				{/if}
			</div>
		</div>

		<Button onclick={save} disabled={saving || !logoValid}>{saving ? 'Saving…' : 'Save'}</Button>
	</div>
{/if}
