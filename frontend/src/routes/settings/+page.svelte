<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type User, type EmailSettings } from '$lib/api';
	import { prefs, prefsFromUser, TIMEZONES, WEEK_DAYS } from '$lib/prefs';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';

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

	// Email (SMTP) settings — separate card/form
	let emailSettings: EmailSettings | null = $state(null);
	let smtpHost = $state('');
	let smtpPort = $state('587');
	let smtpUser = $state('');
	let smtpPass = $state(''); // blank = keep existing stored password
	let smtpTLS = $state(false);
	let smtpStartTLS = $state(true);
	let emailFrom = $state('');
	let emailFromName = $state('Calnode');
	let emailSaving = $state(false);
	let emailSaved = $state(false);
	let emailError = $state('');
	let emailTesting = $state(false);
	let emailTestSent = $state(false);
	let emailTestError = $state('');

	// Notification prefs
	let notify_confirmation = $state(true);
	let notify_cancellation = $state(true);
	let notify_reschedule = $state(true);
	let notify_reminder = $state(true);
	let notify_host_booking = $state(true);
	let notify_host_cancel = $state(true);
	let notify_host_reschedule = $state(true);

	onMount(async () => {
		try {
			const [me, email] = await Promise.all([
				api.get<User>('/v1/users/me'),
				api.get<EmailSettings>('/v1/settings/email'),
			]);
			user = me;
			timezone = user.timezone;
			time_format = user.time_format ?? '12h';
			week_start = user.week_start ?? 1;
			date_format = user.date_format ?? 'dmy';
			avatarUrl = user.avatar_url ?? '';
			notify_confirmation = user.notify_confirmation ?? true;
			notify_cancellation = user.notify_cancellation ?? true;
			notify_reschedule = user.notify_reschedule ?? true;
			notify_reminder = user.notify_reminder ?? true;
			notify_host_booking = user.notify_host_booking ?? true;
			notify_host_cancel = user.notify_host_cancel ?? true;
			notify_host_reschedule = user.notify_host_reschedule ?? true;

			emailSettings = email;
			smtpHost = email.smtp_host;
			smtpPort = email.smtp_port || '587';
			smtpUser = email.smtp_user;
			smtpTLS = email.smtp_tls;
			smtpStartTLS = email.smtp_starttls;
			emailFrom = email.email_from;
			emailFromName = email.email_from_name || 'Calnode';
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	});

	async function saveEmailSettings() {
		emailSaving = true;
		emailSaved = false;
		emailError = '';
		try {
			const body: Record<string, unknown> = {
				smtp_host: smtpHost,
				smtp_port: smtpPort,
				smtp_user: smtpUser,
				smtp_tls: smtpTLS,
				smtp_starttls: smtpStartTLS,
				email_from: emailFrom,
				email_from_name: emailFromName,
			};
			if (smtpPass) body.smtp_pass = smtpPass;
			emailSettings = await api.patch<EmailSettings>('/v1/settings/email', body);
			smtpPass = ''; // clear — password is now stored server-side
			emailSaved = true;
			setTimeout(() => (emailSaved = false), 4000);
		} catch (e: any) {
			emailError = e.message;
		} finally {
			emailSaving = false;
		}
	}

	async function testEmailConnection() {
		emailTesting = true;
		emailTestSent = false;
		emailTestError = '';
		try {
			await api.post('/v1/settings/email/test');
			emailTestSent = true;
			setTimeout(() => (emailTestSent = false), 6000);
		} catch (e: any) {
			emailTestError = e.message === 'Email is not configured — save SMTP settings first'
				? 'Save your settings first, then try again.'
				: e.message;
		} finally {
			emailTesting = false;
		}
	}

	function initials(name: string) {
		return name.split(' ').map((p) => p[0]).join('').toUpperCase().slice(0, 2);
	}

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
				notify_confirmation, notify_cancellation, notify_reschedule, notify_reminder,
				notify_host_booking, notify_host_cancel, notify_host_reschedule,
			});
			currentUser.set(updated);
			prefs.set(prefsFromUser(updated));
			saved = true;
			setTimeout(() => (saved = false), 3000);
		} catch (e: any) {
			error = e.message;
			if (user) {
				timezone = user.timezone;
				time_format = user.time_format ?? '12h';
				week_start = user.week_start ?? 1;
				date_format = user.date_format ?? 'dmy';
				notify_confirmation = user.notify_confirmation ?? true;
				notify_cancellation = user.notify_cancellation ?? true;
				notify_reschedule = user.notify_reschedule ?? true;
				notify_reminder = user.notify_reminder ?? true;
				notify_host_booking = user.notify_host_booking ?? true;
				notify_host_cancel = user.notify_host_cancel ?? true;
				notify_host_reschedule = user.notify_host_reschedule ?? true;
			}
		} finally {
			saving = false;
		}
	}

	const selectCls = 'flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring';
</script>

<svelte:head><title>Settings — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Settings</h1>
	<p class="mt-1 text-sm text-muted-foreground">Manage your profile and preferences.</p>
</div>

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}
{#if saved}<p class="mb-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">Settings saved.</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<form onsubmit={(e) => { e.preventDefault(); save(); }} class="max-w-lg space-y-4">
		<!-- Profile -->
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-4 text-sm font-semibold">Profile</h2>
			<div class="space-y-4">
				<!-- Avatar -->
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
							<input
								bind:this={fileInput}
								type="file"
								accept="image/jpeg,image/png,image/gif,image/webp"
								class="hidden"
								onchange={uploadAvatar}
							/>
							<Button type="button" variant="outline" size="sm" onclick={() => fileInput?.click()} disabled={uploading}>
								{uploading ? 'Uploading…' : 'Upload photo'}
							</Button>
							{#if avatarUrl}
								<Button type="button" variant="ghost" size="sm" onclick={removeAvatar} class="text-destructive hover:text-destructive">
									Remove
								</Button>
							{/if}
						</div>
						<p class="text-xs text-muted-foreground">JPEG, PNG, GIF or WebP · max 5 MB</p>
					</div>
				</div>

				<div class="space-y-1.5">
					<Label for="profile-name" class="text-muted-foreground">Name</Label>
					<Input id="profile-name" type="text" disabled value={user?.name ?? ''} class="opacity-60" />
				</div>
				<div class="space-y-1.5">
					<Label for="profile-email" class="text-muted-foreground">Email</Label>
					<Input id="profile-email" type="email" disabled value={user?.email ?? ''} class="opacity-60" />
				</div>
			</div>
		</div>

		<!-- Preferences -->
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

		<!-- Messaging -->
		<div class="rounded-lg border bg-card p-6">
			<h2 class="mb-1 text-sm font-semibold">Messaging</h2>
			<p class="mb-5 text-xs text-muted-foreground">Control which emails you and your attendees receive.</p>

			<div class="space-y-5">
				<div>
					<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Emails sent to your attendees</p>
					<div class="space-y-3">
						<div class="flex items-center justify-between gap-4">
							<Label for="nc" class="cursor-pointer font-normal">Booking confirmation</Label>
							<Switch id="nc" bind:checked={notify_confirmation} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nca" class="cursor-pointer font-normal">Cancellation notice</Label>
							<Switch id="nca" bind:checked={notify_cancellation} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nr" class="cursor-pointer font-normal">Reschedule notice</Label>
							<Switch id="nr" bind:checked={notify_reschedule} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nrm" class="cursor-pointer font-normal">Reminder emails</Label>
							<Switch id="nrm" bind:checked={notify_reminder} />
						</div>
					</div>
				</div>

				<div class="border-t pt-5">
					<p class="mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">Emails sent to you</p>
					<div class="space-y-3">
						<div class="flex items-center justify-between gap-4">
							<Label for="nhb" class="cursor-pointer font-normal">New booking received</Label>
							<Switch id="nhb" bind:checked={notify_host_booking} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nhc" class="cursor-pointer font-normal">Booking cancelled</Label>
							<Switch id="nhc" bind:checked={notify_host_cancel} />
						</div>
						<div class="flex items-center justify-between gap-4">
							<Label for="nhr" class="cursor-pointer font-normal">Booking rescheduled</Label>
							<Switch id="nhr" bind:checked={notify_host_reschedule} />
						</div>
					</div>
				</div>
			</div>
		</div>

		<Button type="submit" disabled={saving}>
			{saving ? 'Saving…' : 'Save settings'}
		</Button>
	</form>

	<!-- Email settings — separate save, separate API endpoint -->
	<div class="mt-4 max-w-lg">
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">Email</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">SMTP settings for sending booking emails.</p>
				</div>
				{#if emailSettings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {emailSettings.enabled ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {emailSettings.enabled ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{emailSettings.enabled ? 'Configured' : 'Not configured'}
					</span>
				{/if}
			</div>

			{#if emailError}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{emailError}</p>{/if}
			{#if emailSaved}<p class="mb-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-700">Email settings saved.</p>{/if}

			<div class="space-y-4">
				<div class="grid grid-cols-3 gap-3">
					<div class="col-span-2 space-y-1.5">
						<Label for="smtp-host">SMTP host</Label>
						<Input id="smtp-host" type="text" placeholder="smtp.gmail.com" bind:value={smtpHost} />
					</div>
					<div class="space-y-1.5">
						<Label for="smtp-port">Port</Label>
						<Input id="smtp-port" type="text" placeholder="587" bind:value={smtpPort} />
					</div>
				</div>

				<div class="grid grid-cols-2 gap-3">
					<div class="space-y-1.5">
						<Label for="smtp-user">Username</Label>
						<Input id="smtp-user" type="text" placeholder="you@example.com" bind:value={smtpUser} />
					</div>
					<div class="space-y-1.5">
						<Label for="smtp-pass">Password</Label>
						<Input id="smtp-pass" type="password"
							placeholder={emailSettings?.smtp_pass_set ? '•••••••• (stored)' : 'Enter password'}
							bind:value={smtpPass} />
						{#if emailSettings?.smtp_pass_set && !smtpPass}
							<p class="text-xs text-muted-foreground">Password is stored — leave blank to keep it.</p>
						{/if}
					</div>
				</div>

				<div class="grid grid-cols-2 gap-3">
					<div class="space-y-1.5">
						<Label for="email-from">From address</Label>
						<Input id="email-from" type="email" placeholder="bookings@example.com" bind:value={emailFrom} />
					</div>
					<div class="space-y-1.5">
						<Label for="email-from-name">From name</Label>
						<Input id="email-from-name" type="text" placeholder="Calnode" bind:value={emailFromName} />
					</div>
				</div>

				<div class="space-y-2 rounded-md border p-3">
					<p class="text-xs font-medium text-muted-foreground">TLS / encryption</p>
					<div class="flex items-center justify-between gap-4">
						<div>
							<Label for="smtp-starttls" class="cursor-pointer font-normal">STARTTLS</Label>
							<p class="text-xs text-muted-foreground">Recommended for port 587</p>
						</div>
						<Switch id="smtp-starttls" bind:checked={smtpStartTLS} />
					</div>
					<div class="flex items-center justify-between gap-4">
						<div>
							<Label for="smtp-tls" class="cursor-pointer font-normal">Implicit TLS</Label>
							<p class="text-xs text-muted-foreground">For port 465 (SSL)</p>
						</div>
						<Switch id="smtp-tls" bind:checked={smtpTLS} />
					</div>
				</div>
			</div>

			<div class="mt-5 flex flex-wrap items-center gap-3">
				<Button onclick={saveEmailSettings} disabled={emailSaving}>
					{emailSaving ? 'Saving…' : 'Save email settings'}
				</Button>
				<Button variant="outline" onclick={testEmailConnection}
					disabled={emailTesting || !emailSettings?.enabled}>
					{emailTesting ? 'Sending…' : 'Send test email'}
				</Button>
				{#if emailTestSent}
					<span class="text-sm text-green-700">Test email sent to {user?.email}</span>
				{/if}
				{#if emailTestError}
					<span class="text-sm text-destructive">{emailTestError}</span>
				{/if}
			</div>
		</div>
	</div>
{/if}
