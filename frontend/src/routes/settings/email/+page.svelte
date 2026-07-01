<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type User, type EmailSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	const testingFlag = createAsyncFlag();

	let emailSettings = $state<EmailSettings | null>(null);
	let smtpHost = $state('');
	let smtpPort = $state('587');
	let smtpUser = $state('');
	let smtpPass = $state('');
	let smtpTLS = $state(false);
	let smtpStartTLS = $state(true);
	let emailFrom = $state('');
	let emailFromName = $state('Calnode');

	let userEmail = $state('');

	onMount(() => loadingFlag.run(async () => {
		const [me, email] = await Promise.all([
			api.get<User>('/v1/users/me'),
			api.get<EmailSettings>('/v1/settings/email'),
		]);
		userEmail = me.email;
		emailSettings = email;
		smtpHost = email.smtp_host;
		smtpPort = email.smtp_port || '587';
		smtpUser = email.smtp_user;
		smtpTLS = email.smtp_tls;
		smtpStartTLS = email.smtp_starttls;
		emailFrom = email.email_from;
		emailFromName = email.email_from_name || 'Calnode';
	}, 'Could not load email settings'));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = {
				smtp_host: smtpHost, smtp_port: smtpPort, smtp_user: smtpUser,
				smtp_tls: smtpTLS, smtp_starttls: smtpStartTLS,
				email_from: emailFrom, email_from_name: emailFromName,
			};
			if (smtpPass) body.smtp_pass = smtpPass;
			emailSettings = await api.patch<EmailSettings>('/v1/settings/email', body);
			smtpPass = '';
			toast.success('Email settings saved');
		}, 'Could not save email settings');
	}

	async function test() {
		await testingFlag.run(async () => {
			try {
				await api.post('/v1/settings/email/test');
			} catch (e: any) {
				if (e.message === 'Email is not configured — save SMTP settings first') {
					throw new Error('Save your settings first, then try again.');
				}
				throw e;
			}
			toast.success(`Test email sent to ${userEmail}`);
		}, 'Could not send test email');
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Admin access required.</p>
{:else}

{#if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<div class="max-w-lg">
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
							<p class="text-xs text-muted-foreground">Stored — leave blank to keep it.</p>
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
				<Button onclick={save} disabled={savingFlag.active}>
					{savingFlag.active ? 'Saving…' : 'Save'}
				</Button>
				<Button variant="outline" onclick={test} disabled={testingFlag.active || !emailSettings?.enabled}>
					{testingFlag.active ? 'Sending…' : 'Send test email'}
				</Button>
			</div>
		</div>
	</div>
{/if}

{/if}
