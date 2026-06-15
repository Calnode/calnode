<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type User } from '$lib/api';
	import { prefs, prefsFromUser, TIMEZONES, WEEK_DAYS } from '$lib/prefs';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';

	let user: User | null = $state(null);
	let loading = $state(true);
	let saving = $state(false);
	let saved = $state(false);
	let error = $state('');
	let uploading = $state(false);
	let avatarUrl = $state('');
	let fileInput: HTMLInputElement | undefined = $state(undefined);

	let timezone = $state('UTC');
	let time_format = $state<'12h' | '24h'>('12h');
	let week_start = $state(1);
	let date_format = $state<'dmy' | 'mdy' | 'ymd'>('dmy');

	onMount(async () => {
		try {
			user = await api.get<User>('/v1/users/me');
			timezone = user.timezone;
			time_format = user.time_format ?? '12h';
			week_start = user.week_start ?? 1;
			date_format = user.date_format ?? 'dmy';
			avatarUrl = user.avatar_url ?? '';
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	});

	async function uploadAvatar() {
		if (!fileInput?.files?.[0]) return;
		const data = new FormData();
		data.append('avatar', fileInput.files[0]);
		uploading = true;
		error = '';
		try {
			const res = await api.postForm<{ avatar_url: string }>('/v1/users/me/avatar', data);
			avatarUrl = res.avatar_url;
			const updated = await api.get<User>('/v1/users/me');
			currentUser.set(updated);
		} catch (e: any) {
			error = e.message;
		} finally {
			uploading = false;
			if (fileInput) fileInput.value = '';
		}
	}

	async function removeAvatar() {
		error = '';
		try {
			await api.del('/v1/users/me/avatar');
			avatarUrl = '';
			const updated = await api.get<User>('/v1/users/me');
			currentUser.set(updated);
		} catch (e: any) {
			error = e.message;
		}
	}

	async function save() {
		saving = true;
		saved = false;
		error = '';
		try {
			const updated = await api.patch<User>('/v1/users/me', {
				timezone, time_format, week_start, date_format,
			});
			currentUser.set(updated);
			prefs.set(prefsFromUser(updated));
			user = updated;
			saved = true;
			setTimeout(() => (saved = false), 3000);
		} catch (e: any) {
			error = e.message;
		} finally {
			saving = false;
		}
	}

	function initials(name: string) {
		return name.split(' ').map((p) => p[0]).join('').toUpperCase().slice(0, 2);
	}

	const selectCls = 'flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring';
</script>

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}
{#if saved}<p class="mb-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">Settings saved.</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<form onsubmit={(e) => { e.preventDefault(); save(); }} class="max-w-lg space-y-4">
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Profile</h2>
			<div class="space-y-4">
				<div class="flex items-center gap-4">
					{#if avatarUrl}
						<img src={avatarUrl} alt={user?.name} class="h-16 w-16 rounded-full object-cover ring-2 ring-border" />
					{:else}
						<div class="flex h-16 w-16 shrink-0 items-center justify-center rounded-full bg-muted text-xl font-semibold text-muted-foreground">
							{initials(user?.name ?? 'U')}
						</div>
					{/if}
					<div class="flex flex-col gap-1.5">
						<p class="text-sm font-medium">Profile photo</p>
						<div class="flex gap-2">
							<input bind:this={fileInput} type="file" accept="image/jpeg,image/png,image/gif,image/webp" class="hidden" onchange={uploadAvatar} />
							<Button type="button" variant="outline" size="sm" onclick={() => fileInput?.click()} disabled={uploading}>
								{uploading ? 'Uploading…' : 'Upload photo'}
							</Button>
							{#if avatarUrl}
								<Button type="button" variant="ghost" size="sm" onclick={removeAvatar} class="text-destructive hover:text-destructive">Remove</Button>
							{/if}
						</div>
						<p class="text-xs text-muted-foreground">JPEG, PNG, GIF or WebP · max 5 MB</p>
					</div>
				</div>

				<div class="space-y-1.5">
					<Label class="text-muted-foreground">Name</Label>
					<Input type="text" disabled value={user?.name ?? ''} class="opacity-60" />
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
					<select id="timezone" bind:value={timezone} class={selectCls}>
						{#each TIMEZONES as tz}
							<option value={tz}>{tz}</option>
						{/each}
					</select>
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
					<select id="week-start" bind:value={week_start} class={selectCls}>
						{#each [0, 1] as d}
							<option value={d}>{WEEK_DAYS[d]}</option>
						{/each}
					</select>
				</div>

				<div class="space-y-1.5">
					<Label for="date-format">Date format</Label>
					<select id="date-format" bind:value={date_format} class={selectCls}>
						<option value="dmy">DD/MM/YYYY</option>
						<option value="mdy">MM/DD/YYYY</option>
						<option value="ymd">YYYY-MM-DD</option>
					</select>
				</div>
			</div>
		</div>

		<Button type="submit" disabled={saving}>
			{saving ? 'Saving…' : 'Save'}
		</Button>
	</form>
{/if}
