<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type User } from '$lib/api';
	import { prefs, prefsFromUser, TIMEZONES, WEEK_DAYS } from '$lib/prefs';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import * as Select from '$lib/components/ui/select';
	import { Combobox } from '$lib/components/ui/combobox';
	import * as Avatar from '$lib/components/ui/avatar';
	import * as Dialog from '$lib/components/ui/dialog';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';
	import type CropperType from 'cropperjs';

	let user = $state<User | null>(null);
	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	const uploadingFlag = createAsyncFlag();
	let avatarUrl = $state('');
	let fileInput = $state<HTMLInputElement | undefined>(undefined);

	let name = $state('');
	let timezone = $state('UTC');
	let time_format = $state<'12h' | '24h'>('12h');
	let week_start = $state(1);
	let date_format = $state<'dmy' | 'mdy' | 'ymd'>('dmy');

	// Crop dialog state — Cropper is lazy-loaded client-side only to avoid SSR failures
	let cropOpen = $state(false);
	let cropSrc = $state('');
	let cropperEl = $state<HTMLImageElement | undefined>(undefined);
	let hasExistingAvatar = $state(false);
	let cropperInstance: CropperType | null = null;
	let CropperClass: (typeof CropperType) | null = null;

	$effect(() => {
		if (!cropperEl || !CropperClass) return;
		const c = new CropperClass(cropperEl, {
			aspectRatio: 1,
			viewMode: 1,
			autoCropArea: 0.8,
			movable: true,
			zoomable: true,
			rotatable: false,
			scalable: false,
		});
		cropperInstance = c;
		return () => { c.destroy(); cropperInstance = null; };
	});

	async function onFileChange() {
		const file = fileInput?.files?.[0];
		if (!file) return;
		// Lazy-load Cropper only when the user actually picks a file
		if (!CropperClass) {
			const [mod] = await Promise.all([
				import('cropperjs'),
				import('cropperjs/dist/cropper.min.css'),
			]);
			CropperClass = mod.default;
		}
		hasExistingAvatar = !!avatarUrl;
		const reader = new FileReader();
		reader.onload = (e) => {
			cropSrc = e.target?.result as string;
			cropOpen = true;
		};
		reader.readAsDataURL(file);
	}

	onMount(() => loadingFlag.run(async () => {
		user = await api.get<User>('/v1/users/me');
		name = user.name ?? '';
		timezone = user.timezone;
		time_format = user.time_format ?? '12h';
		week_start = user.week_start ?? 1;
		date_format = user.date_format ?? 'dmy';
		avatarUrl = user.avatar_url ?? '';
	}, 'Could not load profile'));

	function cancelCrop() {
		cropOpen = false;
		cropSrc = '';
		if (fileInput) fileInput.value = '';
	}

	async function cropAndUpload() {
		if (!cropperInstance) return;

		await uploadingFlag.run(async () => {
			const croppedCanvas = cropperInstance!.getCroppedCanvas({ width: 400, height: 400 });
			const blob = await new Promise<Blob>((resolve, reject) =>
				croppedCanvas.toBlob(b => b ? resolve(b) : reject(new Error('Canvas export failed')), 'image/jpeg', 0.88)
			);
			const data = new FormData();
			data.append('avatar', blob, 'avatar.jpg');
			const res = await api.postForm<{ avatar_url: string }>('/v1/users/me/avatar', data);
			avatarUrl = res.avatar_url;
			const updated = await api.get<User>('/v1/users/me');
			currentUser.set(updated);
			cropOpen = false;
			cropSrc = '';
			if (fileInput) fileInput.value = '';
			toast.success('Avatar updated');
		}, 'Could not upload avatar');
	}

	async function removeAvatar() {
		try {
			await api.del('/v1/users/me/avatar');
			avatarUrl = '';
			const updated = await api.get<User>('/v1/users/me');
			currentUser.set(updated);
		} catch (e: any) {
			toast.error(e.message || 'Could not remove avatar');
		}
	}

	async function save() {
		await savingFlag.run(async () => {
			const updated = await api.patch<User>('/v1/users/me', {
				name, timezone, time_format, week_start, date_format,
			});
			currentUser.set(updated);
			prefs.set(prefsFromUser(updated));
			user = updated;
			toast.success('Settings saved');
		}, 'Could not save settings');
	}

	function initials(name: string) {
		return name.split(' ').map((p) => p[0]).join('').toUpperCase().slice(0, 2);
	}

	const DATE_FORMATS = [
		{ value: 'dmy', label: 'DD/MM/YYYY' },
		{ value: 'mdy', label: 'MM/DD/YYYY' },
		{ value: 'ymd', label: 'YYYY-MM-DD' },
	];
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<form onsubmit={(e) => { e.preventDefault(); save(); }} class="max-w-lg space-y-4">
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Profile</h2>
			<div class="space-y-4">
				<div class="flex items-center gap-4">
					<input bind:this={fileInput} type="file" accept="image/jpeg,image/png,image/gif,image/webp" class="hidden" onchange={onFileChange} />
					<button
						type="button"
						onclick={() => fileInput?.click()}
						disabled={uploadingFlag.active}
						title={avatarUrl ? 'Replace photo' : 'Upload photo'}
						class="group relative cursor-pointer rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-wait"
					>
						<Avatar.Root class="size-16 text-xl font-semibold">
							<Avatar.Image src={avatarUrl || undefined} alt={user?.name} />
							<Avatar.Fallback>{initials(user?.name ?? 'U')}</Avatar.Fallback>
						</Avatar.Root>
						<div class="absolute inset-0 flex items-center justify-center rounded-full bg-black/40 opacity-0 transition-opacity group-hover:opacity-100">
							<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 19a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4l2-3h6l2 3h4a2 2 0 0 1 2 2z"/><circle cx="12" cy="13" r="4"/></svg>
						</div>
					</button>
					<div class="flex flex-col gap-1.5">
						<p class="text-sm font-medium">Profile photo</p>
						<p class="text-xs text-muted-foreground">Click your avatar to {avatarUrl ? 'replace' : 'upload'} · JPEG, PNG, GIF or WebP · max 5 MB</p>
						{#if avatarUrl}
							<Button type="button" variant="ghost" size="sm" onclick={removeAvatar} class="w-fit text-destructive hover:text-destructive">Remove photo</Button>
						{/if}
					</div>
				</div>

				<div class="space-y-1.5">
					<Label for="name">Name</Label>
					<Input id="name" type="text" bind:value={name} placeholder="Your name" />
					<p class="text-xs text-muted-foreground">Your personal name, shown as the meeting host. Your business brand (logo, business name) is set separately in Settings → Branding.</p>
				</div>
				<div class="space-y-1.5">
					<Label class="text-muted-foreground">Email</Label>
					<Input type="email" disabled value={user?.email ?? ''} class="opacity-60" />
				</div>
			</div>
		</div>

		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Preferences</h2>
			<div class="space-y-4">
				<div class="space-y-1.5">
					<Label for="timezone">Timezone</Label>
					<Combobox
						items={TIMEZONES.map((tz) => ({ value: tz, label: tz }))}
						bind:value={timezone}
						placeholder="Select timezone…"
						searchPlaceholder="Search timezones…"
					/>
					<p class="text-xs text-muted-foreground">Used when computing available slots for your booking pages.</p>
				</div>

				<div class="space-y-1.5">
					<p class="text-sm font-medium">Time format</p>
					<div class="flex gap-2">
						{#each [{ value: '12h', label: '12-hour', hint: '1:30 PM' }, { value: '24h', label: '24-hour', hint: '13:30' }] as opt}
							<label class="flex flex-1 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors {time_format === opt.value ? 'border-primary bg-primary/5' : 'bg-background hover:bg-accent/50'}">
								<input type="radio" bind:group={time_format} value={opt.value} class="sr-only" />
								{opt.label}
								<span class="text-xs text-muted-foreground">({opt.hint})</span>
							</label>
						{/each}
					</div>
				</div>

				<div class="space-y-1.5">
					<Label for="week-start">First day of week</Label>
					<Select.Root type="single" value={String(week_start)} onValueChange={(v) => { if (v) week_start = Number(v); }}>
						<Select.Trigger id="week-start" class="w-full">
							{WEEK_DAYS[week_start]}
						</Select.Trigger>
						<Select.Content>
							{#each [0, 1] as d}
								<Select.Item value={String(d)} label={WEEK_DAYS[d]}>{WEEK_DAYS[d]}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>

				<div class="space-y-1.5">
					<Label for="date-format">Date format</Label>
					<Select.Root type="single" value={date_format} onValueChange={(v) => { if (v) date_format = v as 'dmy' | 'mdy' | 'ymd'; }}>
						<Select.Trigger id="date-format" class="w-full">
							{DATE_FORMATS.find((f) => f.value === date_format)?.label ?? 'Select…'}
						</Select.Trigger>
						<Select.Content>
							{#each DATE_FORMATS as f}
								<Select.Item value={f.value} label={f.label}>{f.label}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>
			</div>
		</div>

		<Button type="submit" disabled={savingFlag.active}>
			{savingFlag.active ? 'Saving…' : 'Save'}
		</Button>
	</form>
{/if}

<Dialog.Root bind:open={cropOpen} onOpenChange={(o) => { if (!o) cancelCrop(); }}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>{hasExistingAvatar ? 'Replace photo' : 'Upload photo'}</Dialog.Title>
			<Dialog.Description>Drag or pinch to adjust. The cropped area will be saved.</Dialog.Description>
		</Dialog.Header>
		<div class="mt-2 overflow-hidden rounded-md bg-muted" style="max-height: 360px;">
			{#if cropSrc}
				<img bind:this={cropperEl} src={cropSrc} alt="Crop preview" class="block max-w-full" />
			{/if}
		</div>
		<Dialog.Footer class="mt-4">
			<Button variant="outline" onclick={cancelCrop} disabled={uploadingFlag.active}>Cancel</Button>
			<Button onclick={cropAndUpload} disabled={uploadingFlag.active}>
				{uploadingFlag.active ? 'Uploading…' : 'Save photo'}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
