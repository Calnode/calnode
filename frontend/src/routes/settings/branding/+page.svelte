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
	let uploading = $state(false);
	let businessName = $state('');
	let logoUrl = $state('');
	let fileInput: HTMLInputElement | undefined = $state();

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
		saving = true;
		try {
			const b = await api.patch<Branding>('/v1/settings/branding', { business_name: businessName });
			businessName = b.business_name ?? '';
			toast.success('Branding saved');
		} catch (e: any) {
			toast.error(e.message || 'Could not save branding settings');
		} finally {
			saving = false;
		}
	}

	async function onLogoSelected(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		uploading = true;
		try {
			const data = new FormData();
			data.append('logo', file, file.name);
			const res = await api.postForm<{ logo_url: string }>('/v1/settings/branding/logo', data);
			logoUrl = res.logo_url;
			toast.success('Logo uploaded');
		} catch (err: any) {
			toast.error(err.message || 'Could not upload logo');
		} finally {
			uploading = false;
			if (fileInput) fileInput.value = '';
		}
	}

	async function removeLogo() {
		try {
			await api.del('/v1/settings/branding/logo');
			logoUrl = '';
			toast.success('Logo removed');
		} catch (e: any) {
			toast.error(e.message || 'Could not remove logo');
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
				Your logo appears at the top of booking confirmation emails and on your public booking and manage
				pages. The business name is used where there's no logo, and as the email sender name's companion.
			</p>

			<div class="mt-4 space-y-1.5">
				<Label for="business-name">Business name</Label>
				<Input id="business-name" bind:value={businessName} placeholder="Orchestratr" maxlength={200} />
				<p class="text-xs text-muted-foreground">Shown as the wordmark when no logo is set. Falls back to “Calnode” if left blank.</p>
			</div>

			<div class="mt-5 space-y-2">
				<Label>Logo</Label>
				<div class="flex items-center gap-4">
					<div class="flex h-16 w-40 items-center justify-center overflow-hidden rounded-md border bg-white px-3">
						{#if logoUrl}
							<img src={logoUrl} alt="Logo" class="max-h-[40px] max-w-full" />
						{:else}
							<span class="text-xs text-muted-foreground">No logo</span>
						{/if}
					</div>
					<div class="flex flex-col gap-2">
						<Button variant="outline" size="sm" disabled={uploading} onclick={() => fileInput?.click()}>
							{uploading ? 'Uploading…' : logoUrl ? 'Replace logo' : 'Upload logo'}
						</Button>
						{#if logoUrl}
							<Button variant="ghost" size="sm" onclick={removeLogo}>Remove</Button>
						{/if}
					</div>
				</div>
				<input bind:this={fileInput} type="file" accept="image/png,image/jpeg,image/gif,image/webp" class="hidden" onchange={onLogoSelected} />
				<p class="text-xs text-muted-foreground">
					PNG with a transparent background works best, on a light background. Any width; aim for at least ~120px tall. Max 5 MB. Displayed ~30px tall.
				</p>
			</div>
		</div>

		<Button onclick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
	</div>
{/if}
