<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Dialog from '$lib/components/ui/dialog';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import type CropperType from 'cropperjs';

	type Branding = { business_name: string; logo_url: string };

	let loading = $state(true);
	let saving = $state(false);
	let uploading = $state(false);
	let businessName = $state('');
	let logoUrl = $state('');
	let fileInput: HTMLInputElement | undefined = $state();

	// Crop dialog — Cropper is lazy-loaded client-side only to avoid SSR failures.
	let cropOpen = $state(false);
	let cropSrc = $state('');
	let cropperEl: HTMLImageElement | undefined = $state();
	let cropperInstance: CropperType | null = null;
	let CropperClass: typeof CropperType | null = null;

	$effect(() => {
		if (!cropperEl || !CropperClass) return;
		// Free-form crop (aspectRatio NaN) with the whole image selected by default,
		// so cropping is optional — the user can adjust or just Save the full image.
		const c = new CropperClass(cropperEl, {
			aspectRatio: NaN,
			viewMode: 1,
			autoCropArea: 1,
			movable: true,
			zoomable: true,
			rotatable: false,
			scalable: false,
			background: true
		});
		cropperInstance = c;
		return () => { c.destroy(); cropperInstance = null; };
	});

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

	async function onFileChange() {
		const file = fileInput?.files?.[0];
		if (!file) return;
		if (!CropperClass) {
			const [mod] = await Promise.all([
				import('cropperjs'),
				import('cropperjs/dist/cropper.min.css')
			]);
			CropperClass = mod.default;
		}
		const reader = new FileReader();
		reader.onload = (e) => {
			cropSrc = e.target?.result as string;
			cropOpen = true;
		};
		reader.readAsDataURL(file);
	}

	function cancelCrop() {
		cropOpen = false;
		cropSrc = '';
		if (fileInput) fileInput.value = '';
	}

	async function cropAndUpload() {
		if (!cropperInstance) return;
		uploading = true;
		try {
			const canvas = cropperInstance.getCroppedCanvas({ maxWidth: 1200, maxHeight: 1200 });
			const blob = await new Promise<Blob>((resolve, reject) =>
				canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('Canvas export failed'))), 'image/png')
			);
			const data = new FormData();
			data.append('logo', blob, 'logo.png');
			const res = await api.postForm<{ logo_url: string }>('/v1/settings/branding/logo', data);
			logoUrl = res.logo_url;
			cropOpen = false;
			cropSrc = '';
			if (fileInput) fileInput.value = '';
			toast.success('Logo uploaded');
		} catch (e: any) {
			toast.error(e.message || 'Could not upload logo');
		} finally {
			uploading = false;
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
				pages. The business name is the wordmark where there's no logo.
			</p>

			<div class="mt-4 space-y-1.5">
				<Label for="business-name">Business name</Label>
				<Input id="business-name" bind:value={businessName} placeholder="Orchestratr" maxlength={200} />
				<p class="text-xs text-muted-foreground">Used where there's no logo. Falls back to “Calnode” if left blank.</p>
			</div>

			<div class="mt-5 space-y-2">
				<Label>Logo</Label>
				<div class="flex items-center gap-4">
					<input bind:this={fileInput} type="file" accept="image/jpeg,image/png,image/gif,image/webp" class="hidden" onchange={onFileChange} />
					<button
						type="button"
						onclick={() => fileInput?.click()}
						disabled={uploading}
						title={logoUrl ? 'Replace logo' : 'Upload logo'}
						class="group relative flex h-16 w-44 cursor-pointer items-center justify-center overflow-hidden rounded-md border bg-white px-3 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-wait"
					>
						{#if logoUrl}
							<img src={logoUrl} alt="Logo" class="max-h-[40px] max-w-full" />
						{:else}
							<span class="text-xs text-muted-foreground">Click to upload</span>
						{/if}
						<div class="absolute inset-0 flex items-center justify-center bg-black/40 opacity-0 transition-opacity group-hover:opacity-100">
							<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/><circle cx="12" cy="13" r="4"/></svg>
						</div>
					</button>
					{#if logoUrl}
						<Button type="button" variant="ghost" size="sm" onclick={removeLogo} class="text-destructive hover:text-destructive">Remove</Button>
					{/if}
				</div>
				<p class="text-xs text-muted-foreground">
					Click the box to upload. PNG with a transparent background works best, on a light background. Any
					shape — you can crop it next. Max 5 MB, displayed ~30px tall.
				</p>
			</div>
		</div>

		<Button onclick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
	</div>

	<Dialog.Root bind:open={cropOpen} onOpenChange={(o) => { if (!o) cancelCrop(); }}>
		<Dialog.Content class="max-w-lg">
			<Dialog.Header>
				<Dialog.Title>Crop logo</Dialog.Title>
				<Dialog.Description>Drag to adjust, or just save to use the whole image.</Dialog.Description>
			</Dialog.Header>
			<div class="mt-2 overflow-hidden rounded-md bg-muted" style="max-height: 360px;">
				{#if cropSrc}
					<img bind:this={cropperEl} src={cropSrc} alt="Crop preview" class="block max-w-full" />
				{/if}
			</div>
			<Dialog.Footer class="mt-4">
				<Button variant="outline" onclick={cancelCrop} disabled={uploading}>Cancel</Button>
				<Button onclick={cropAndUpload} disabled={uploading}>{uploading ? 'Uploading…' : 'Save logo'}</Button>
			</Dialog.Footer>
		</Dialog.Content>
	</Dialog.Root>
{/if}
