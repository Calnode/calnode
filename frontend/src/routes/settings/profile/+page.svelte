<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type User } from '$lib/api';
	import { prefs, prefsFromUser, TIMEZONES, weekDays } from '$lib/prefs';
	import { t, tr, locale, setLocale, ADMIN_LOCALES, isAdminLocale } from '$lib/i18n';
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
	let handle = $state('');
	const uploadingFlag = createAsyncFlag();
	let avatarUrl = $state('');
	let fileInput = $state<HTMLInputElement | undefined>(undefined);

	let name = $state('');
	let booking_accent = $state('#111827');
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
		handle = user.handle ?? '';
		booking_accent = user.booking_accent;
		timezone = user.timezone;
		time_format = user.time_format ?? '12h';
		week_start = user.week_start ?? 1;
		date_format = user.date_format ?? 'dmy';
		avatarUrl = user.avatar_url ?? '';
	}, tr('settings.profile.error.load')));

	// Mirrors the normalisation the API applies, so the link we show is the URL that
	// actually resolves — a handle with spaces or punctuation is rewritten server-side.
	const handleSlug = $derived(
		handle.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
	);

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
			toast.success(tr('settings.profile.toast.avatarUpdated'));
		}, tr('settings.profile.error.upload'));
	}

	async function removeAvatar() {
		try {
			await api.del('/v1/users/me/avatar');
			avatarUrl = '';
			const updated = await api.get<User>('/v1/users/me');
			currentUser.set(updated);
		} catch (e: any) {
			toast.error(e.message || tr('settings.profile.error.removeAvatar'));
		}
	}

	async function save() {
		await savingFlag.run(async () => {
			const updated = await api.patch<User>('/v1/users/me', {
				name, timezone, time_format, week_start, date_format, booking_accent, handle,
			});
			currentUser.set(updated);
			prefs.set(prefsFromUser(updated));
			user = updated;
			toast.success(tr('settings.profile.toast.settingsSaved'));
		}, tr('settings.profile.error.save'));
	}

	function initials(name: string) {
		return name.split(' ').map((p) => p[0]).join('').toUpperCase().slice(0, 2);
	}

	const DATE_FORMATS = $derived([
		{ value: 'dmy', label: $t('settings.profile.dateFormat.dmy') },
		{ value: 'mdy', label: $t('settings.profile.dateFormat.mdy') },
		{ value: 'ymd', label: $t('settings.profile.dateFormat.ymd') },
	]);
	const weekdayLabels = $derived(weekDays($t));
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else}
	<form onsubmit={(e) => { e.preventDefault(); save(); }} class="max-w-lg space-y-4">
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">{$t('settings.profile.section.profile')}</h2>
			<div class="space-y-4">
				<div class="flex items-center gap-4">
					<input bind:this={fileInput} type="file" accept="image/jpeg,image/png,image/gif,image/webp" class="hidden" onchange={onFileChange} />
					<button
						type="button"
						onclick={() => fileInput?.click()}
						disabled={uploadingFlag.active}
						title={avatarUrl ? $t('settings.profile.photo.replaceTitle') : $t('settings.profile.photo.uploadTitle')}
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
					<!-- min-w-0 + flex-1 so this text column shrinks against the fixed-size avatar
					     rather than pushing the row wide. Defensive: the settings shell itself stops
					     laying out sensibly below ~600px (the nav and content sit in one flex row with
					     no stacking breakpoint), so narrow viewports are already degraded for reasons
					     this does not fix. -->
					<div class="flex min-w-0 flex-1 flex-col gap-1.5">
						<p class="text-sm font-medium">{$t('settings.profile.photo.title')}</p>
						<p class="text-xs text-muted-foreground">{$t('settings.profile.photo.hint', { action: avatarUrl ? $t('settings.profile.photo.actionReplace') : $t('settings.profile.photo.actionUpload') })}</p>
						{#if avatarUrl}
							<Button type="button" variant="ghost" size="sm" onclick={removeAvatar} class="w-fit text-destructive hover:text-destructive">{$t('settings.profile.photo.remove')}</Button>
						{/if}
					</div>
				</div>

				<div class="space-y-2">
					<Label for="booking-accent">{$t('settings.profile.accent.label')}</Label>
					<div class="flex items-center gap-3">
						<input id="booking-accent" type="color" bind:value={booking_accent} class="h-10 w-14 cursor-pointer rounded border p-1" />
						<span class="text-sm text-muted-foreground">{booking_accent}</span>
					</div>
					<p class="text-xs text-muted-foreground">{$t('settings.profile.accent.hint')}</p>
				</div>
				<div class="space-y-1.5">
					<Label for="name">{$t('settings.profile.name.label')}</Label>
					<Input id="name" type="text" bind:value={name} placeholder={$t('settings.profile.name.placeholder')} />
					<p class="text-xs text-muted-foreground">{$t('settings.profile.name.hint')}</p>
				</div>
				<div class="space-y-1.5">
					<Label for="handle">{$t('settings.profile.handle.label')}</Label>
					<Input id="handle" type="text" bind:value={handle} placeholder={$t('settings.profile.handle.placeholder')} />
					{#if handleSlug}
						<p class="text-xs text-muted-foreground">
							{$t('settings.profile.handle.publicPage')}
							<a
								href={`/u/${handleSlug}`}
								target="_blank"
								rel="noopener noreferrer"
								class="font-mono text-primary hover:underline"
							>
								/u/{handleSlug}
							</a>
						</p>
					{:else}
						<p class="text-xs text-muted-foreground">{$t('settings.profile.handle.noPage')}</p>
					{/if}
				</div>
				<div class="space-y-1.5">
					<Label class="text-muted-foreground">{$t('settings.profile.email.label')}</Label>
					<Input type="email" disabled value={user?.email ?? ''} class="opacity-60" />
				</div>
			</div>
		</div>

		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">{$t('settings.profile.section.preferences')}</h2>
			<div class="space-y-4">
				<div class="space-y-1.5">
					<Label for="ui-locale">{$t('settings.profile.language.label')}</Label>
					<Select.Root type="single" value={$locale} onValueChange={(v) => { if (isAdminLocale(v)) setLocale(v); }}>
						<Select.Trigger id="ui-locale" class="w-full">
							{ADMIN_LOCALES.find((l) => l.code === $locale)?.name ?? $t('common.select')}
						</Select.Trigger>
						<Select.Content>
							{#each ADMIN_LOCALES as loc}
								<Select.Item value={loc.code} label={loc.name}>{loc.name}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
					<p class="text-xs text-muted-foreground">{$t('settings.profile.language.hint')}</p>
				</div>

				<div class="space-y-1.5">
					<Label for="timezone">{$t('settings.profile.timezone.label')}</Label>
					<Combobox
						items={TIMEZONES.map((tz) => ({ value: tz, label: tz }))}
						bind:value={timezone}
						placeholder={$t('settings.profile.timezone.placeholder')}
						searchPlaceholder={$t('settings.profile.timezone.searchPlaceholder')}
					/>
					<p class="text-xs text-muted-foreground">{$t('settings.profile.timezone.hint')}</p>
				</div>

				<div class="space-y-1.5">
					<p class="text-sm font-medium">{$t('settings.profile.timeFormat.label')}</p>
					<div class="flex gap-2">
						{#each [{ value: '12h', label: $t('settings.profile.timeFormat.12h'), hint: $t('settings.profile.timeFormat.12hHint') }, { value: '24h', label: $t('settings.profile.timeFormat.24h'), hint: $t('settings.profile.timeFormat.24hHint') }] as opt}
							<label class="flex flex-1 cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors {time_format === opt.value ? 'border-primary bg-primary/5' : 'bg-background hover:bg-accent/50'}">
								<input type="radio" bind:group={time_format} value={opt.value} class="sr-only" />
								{opt.label}
								<span class="text-xs text-muted-foreground">({opt.hint})</span>
							</label>
						{/each}
					</div>
				</div>

				<div class="space-y-1.5">
					<Label for="week-start">{$t('settings.profile.weekStart.label')}</Label>
					<Select.Root type="single" value={String(week_start)} onValueChange={(v) => { if (v) week_start = Number(v); }}>
						<Select.Trigger id="week-start" class="w-full">
							{weekdayLabels[week_start]}
						</Select.Trigger>
						<Select.Content>
							{#each [0, 1] as d}
								<Select.Item value={String(d)} label={weekdayLabels[d]}>{weekdayLabels[d]}</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>

				<div class="space-y-1.5">
					<Label for="date-format">{$t('settings.profile.dateFormat.label')}</Label>
					<Select.Root type="single" value={date_format} onValueChange={(v) => { if (v) date_format = v as 'dmy' | 'mdy' | 'ymd'; }}>
						<Select.Trigger id="date-format" class="w-full">
							{DATE_FORMATS.find((f) => f.value === date_format)?.label ?? $t('common.select')}
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
			{savingFlag.active ? $t('common.saving') : $t('common.save')}
		</Button>
	</form>
{/if}

<Dialog.Root bind:open={cropOpen} onOpenChange={(o) => { if (!o) cancelCrop(); }}>
	<Dialog.Content class="max-w-md">
		<Dialog.Header>
			<Dialog.Title>{hasExistingAvatar ? $t('settings.profile.crop.replaceTitle') : $t('settings.profile.crop.uploadTitle')}</Dialog.Title>
			<Dialog.Description>{$t('settings.profile.crop.description')}</Dialog.Description>
		</Dialog.Header>
		<div class="mt-2 overflow-hidden rounded-md bg-muted" style="max-height: 360px;">
			{#if cropSrc}
				<img bind:this={cropperEl} src={cropSrc} alt={$t('settings.profile.crop.previewAlt')} class="block max-w-full" />
			{/if}
		</div>
		<Dialog.Footer class="mt-4">
			<Button variant="outline" onclick={cancelCrop} disabled={uploadingFlag.active}>{$t('common.cancel')}</Button>
			<Button onclick={cropAndUpload} disabled={uploadingFlag.active}>
				{uploadingFlag.active ? $t('settings.profile.crop.uploading') : $t('settings.profile.crop.save')}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
