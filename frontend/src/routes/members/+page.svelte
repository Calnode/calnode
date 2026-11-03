<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type TeamMember, type Invite, type UpcomingBooking } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import * as Dialog from '$lib/components/ui/dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Badge } from '$lib/components/ui/badge';
	import * as Select from '$lib/components/ui/select';
	import { toast } from 'svelte-sonner';
	import { t, tr } from '$lib/i18n';

	let members: TeamMember[] = $state([]);
	let invites: Invite[] = $state([]);
	let loading = $state(true);
	let error = $state('');
	let showArchived = $state(false);

	// Invite form
	let showInvite = $state(false);
	let inviteEmail = $state('');
	let inviting = $state(false);
	let inviteError = $state('');
	let inviteResult = $state<{ invite_url: string; email: string; email_sent: boolean; note: string } | null>(null);
	let copied = $state(false);

	// Confirm dialog
	let confirmOpen = $state(false);
	let confirmTitle = $state('');
	let confirmDescription = $state('');
	let confirmActionText = $state('');
	let pendingAction: (() => void) | null = null;

	function openConfirm(opts: { title: string; description: string; confirmText: string; action: () => void }) {
		confirmTitle = opts.title;
		confirmDescription = opts.description;
		confirmActionText = opts.confirmText;
		pendingAction = opts.action;
		confirmOpen = true;
	}

	// Password reset — keyed by user id
	let resetTarget = $state<string | null>(null);
	let resetPassword = $state('');
	let resetting = $state(false);
	let resetError = $state('');
	let resetOk = $state(false);

	// Resolve-meetings (archive) dialog
	let resolveOpen = $state(false);
	let resolveMember = $state<TeamMember | null>(null);
	let resolveBookings = $state<UpcomingBooking[]>([]);
	let resolveChoice = $state<Record<string, string>>({});
	let resolveBusy = $state(false);
	let resolveError = $state('');

	let reassignTargets = $derived(members.filter((m) => !m.archived && m.id !== resolveMember?.id));

	async function load() {
		try {
			const [membersRes, invitesRes] = await Promise.all([
				api.get<TeamMember[]>(showArchived ? '/v1/users?include_archived=true' : '/v1/users'),
				api.get<Invite[]>('/v1/invites')
			]);
			members = membersRes;
			invites = invitesRes;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	async function toggleArchived() {
		showArchived = !showArchived;
		await load();
	}

	async function sendInvite() {
		inviteError = '';
		inviteResult = null;
		if (!inviteEmail.trim()) { inviteError = tr('members.invite.validation.email'); return; }
		inviting = true;
		try {
			const res = await api.post<{
				id: string; email: string; invite_url: string;
				expires_at: string; email_sent: boolean; note: string;
			}>('/v1/invites', { email: inviteEmail.trim().toLowerCase() });
			inviteResult = res;
			inviteEmail = '';
			await load();
		} catch (e: any) {
			inviteError = e.message;
		} finally {
			inviting = false;
		}
	}

	function revokeInvite(id: string) {
		openConfirm({
			title: tr('members.confirm.revoke.title'),
			description: tr('members.confirm.revoke.description'),
			confirmText: tr('members.confirm.revoke.confirm'),
			action: async () => {
				try { await api.del(`/v1/invites/${id}`); await load(); }
				catch (e: any) { error = e.message; }
			}
		});
	}

	// Re-issue a pending invite: mints a fresh link + new 7-day expiry and re-emails
	// it (the old link stops working). The original token can't be recovered.
	async function resendInvite(id: string) {
		try {
			const res = await api.post<{ email: string; invite_url: string; email_sent: boolean }>(
				`/v1/invites/${id}/resend`, {}
			);
			await load();
			if (res.email_sent) {
				toast.success(tr('members.toast.inviteResent', { email: res.email }));
			} else {
				// No SMTP configured — surface the fresh link so the admin can send it manually.
				showInvite = true;
				inviteResult = {
					invite_url: res.invite_url, email: res.email, email_sent: false,
					note: tr('members.invite.noteManual')
				};
				toast.success(tr('members.toast.newLink', { email: res.email }));
			}
		} catch (e: any) {
			toast.error(e.message || tr('members.toast.errors.resend'));
		}
	}

	// --- Role management (owner only) ---
	async function setRole(m: TeamMember, role: 'admin' | 'member') {
		try {
			await api.patch(`/v1/users/${m.id}/role`, { role });
			toast.success(role === 'admin' ? tr('members.toast.roleAdmin', { name: m.name }) : tr('members.toast.roleMember', { name: m.name }));
			await load();
		} catch (e: any) { toast.error(e.message || tr('members.toast.errors.role')); }
	}

	function confirmTransfer(m: TeamMember) {
		openConfirm({
			title: tr('members.confirm.transfer.title', { name: m.name }),
			description: tr('members.confirm.transfer.description'),
			confirmText: tr('members.confirm.transfer.confirm'),
			action: async () => {
				try { await api.post(`/v1/users/${m.id}/transfer-ownership`); toast.success(tr('members.toast.newOwner', { name: m.name })); await load(); }
				catch (e: any) { toast.error(e.message || tr('members.toast.errors.transfer')); }
			}
		});
	}

	// --- Archive / restore ---
	async function startArchive(m: TeamMember) {
		error = '';
		try {
			const res = await api.get<{ items: UpcomingBooking[] }>(`/v1/users/${m.id}/upcoming-bookings`);
			if (res.items.length > 0) {
				resolveMember = m;
				resolveBookings = res.items;
				resolveChoice = {};
				resolveError = '';
				resolveOpen = true;
			} else {
				openConfirm({
					title: tr('members.confirm.archive.title', { name: m.name }),
					description: tr('members.confirm.archive.description'),
					confirmText: tr('members.confirm.archive.confirm'),
					action: () => doArchive(m.id)
				});
			}
		} catch (e: any) { error = e.message; }
	}

	async function doArchive(id: string) {
		try { await api.post(`/v1/users/${id}/archive`); toast.success(tr('members.toast.archived')); await load(); }
		catch (e: any) { toast.error(e.message || tr('members.toast.errors.archive')); }
	}

	async function restoreMember(m: TeamMember) {
		try { await api.post(`/v1/users/${m.id}/restore`); toast.success(tr('members.toast.restored', { name: m.name })); await load(); }
		catch (e: any) { toast.error(e.message || tr('members.toast.errors.restore')); }
	}

	// --- Resolve-meetings dialog actions ---
	async function reassignOne(bookingId: string) {
		const hostId = resolveChoice[bookingId];
		if (!hostId) return;
		resolveBusy = true; resolveError = '';
		try {
			await api.post(`/v1/bookings/${bookingId}/reassign`, { host_id: hostId });
			resolveBookings = resolveBookings.filter((b) => b.id !== bookingId);
			await finishResolveIfDone();
		} catch (e: any) { resolveError = e.message; }
		finally { resolveBusy = false; }
	}

	async function cancelOne(bookingId: string) {
		resolveBusy = true; resolveError = '';
		try {
			await api.post(`/v1/bookings/${bookingId}/cancel`, { reason: 'Host is being archived' });
			resolveBookings = resolveBookings.filter((b) => b.id !== bookingId);
			await finishResolveIfDone();
		} catch (e: any) { resolveError = e.message; }
		finally { resolveBusy = false; }
	}

	async function cancelAllRemaining() {
		resolveBusy = true; resolveError = '';
		try {
			for (const b of [...resolveBookings]) {
				await api.post(`/v1/bookings/${b.id}/cancel`, { reason: 'Host is being archived' });
			}
			resolveBookings = [];
			await finishResolveIfDone();
		} catch (e: any) { resolveError = e.message; }
		finally { resolveBusy = false; }
	}

	async function finishResolveIfDone() {
		if (resolveBookings.length === 0 && resolveMember) {
			const id = resolveMember.id;
			resolveOpen = false;
			resolveMember = null;
			await doArchive(id);
		}
	}

	// --- Password reset ---
	function startReset(id: string) { resetTarget = id; resetPassword = ''; resetError = ''; resetOk = false; }
	function cancelReset() { resetTarget = null; resetPassword = ''; resetError = ''; resetOk = false; }

	async function submitReset(userId: string) {
		resetError = ''; resetOk = false;
		if (!resetPassword) { resetError = tr('members.validation.password'); return; }
		resetting = true;
		try {
			await api.post(`/v1/users/${userId}/password`, { password: resetPassword });
			resetOk = true; resetPassword = '';
			setTimeout(() => { resetTarget = null; resetOk = false; }, 2000);
		} catch (e: any) { resetError = e.message; }
		finally { resetting = false; }
	}

	async function copyInviteUrl(url: string) {
		await navigator.clipboard.writeText(url);
		copied = true;
		setTimeout(() => { copied = false; }, 2000);
	}

	const roleLabels = $derived({
		owner: $t('members.role.owner'),
		admin: $t('members.role.admin'),
		member: $t('members.role.member')
	});
	function roleLabel(m: TeamMember) {
		return roleLabels[m.role as keyof typeof roleLabels] ?? roleLabels.member;
	}
	function roleVariant(m: TeamMember): 'default' | 'secondary' | 'outline' {
		return m.role === 'owner' ? 'default' : m.role === 'admin' ? 'secondary' : 'outline';
	}

	const authLabels = $derived({
		google: $t('members.badge.google'),
		microsoft: $t('members.badge.microsoft'),
		email: $t('members.badge.email')
	});
	function authBadge(m: TeamMember): string[] {
		const badges: string[] = [];
		if (m.provider === 'google') badges.push(authLabels.google);
		else if (m.provider === 'microsoft') badges.push(authLabels.microsoft);
		if (m.email_login) badges.push(authLabels.email);
		return badges;
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}
	function fmtDateTime(iso: string) {
		return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
	}
	function daysLeft(iso: string) {
		const diff = new Date(iso).getTime() - Date.now();
		return Math.max(0, Math.ceil(diff / 86_400_000));
	}
</script>

<ConfirmDialog
	bind:open={confirmOpen}
	title={confirmTitle}
	description={confirmDescription}
	confirmText={confirmActionText}
	destructive
	onConfirm={() => pendingAction?.()}
/>

<!-- Resolve-meetings dialog (shown when archiving a member with upcoming bookings) -->
<Dialog.Root bind:open={resolveOpen}>
	<Dialog.Content class="max-w-2xl">
		<Dialog.Header>
			<Dialog.Title>{$t('members.resolve.title', { name: resolveMember?.name ?? '' })}</Dialog.Title>
			<Dialog.Description>
				{$t('members.resolve.description', { count: resolveBookings.length })}
			</Dialog.Description>
		</Dialog.Header>

		{#if resolveError}
			<p class="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{resolveError}</p>
		{/if}

		<div class="max-h-[50vh] space-y-2 overflow-y-auto">
			{#each resolveBookings as b (b.id)}
				<div class="rounded-lg border p-3">
					<div class="mb-2">
						<p class="text-sm font-medium">{b.event_type_name}</p>
						<p class="text-xs text-muted-foreground">
							{fmtDateTime(b.start_at)} · {b.attendee_name || b.attendee_email || $t('common.attendeeFallback')}
						</p>
					</div>
					<div class="flex flex-wrap items-center gap-2">
						<Select.Root
							type="single"
							value={resolveChoice[b.id] ?? ''}
							onValueChange={(v) => { resolveChoice = { ...resolveChoice, [b.id]: v ?? '' }; }}
							disabled={resolveBusy || reassignTargets.length === 0}
						>
							<Select.Trigger class="w-fit min-w-40">
								{reassignTargets.find((t) => t.id === resolveChoice[b.id])?.name ?? $t('members.resolve.reassignTo')}
							</Select.Trigger>
							<Select.Content>
								{#each reassignTargets as t}
									<Select.Item value={t.id} label={t.name}>{t.name}</Select.Item>
								{/each}
							</Select.Content>
						</Select.Root>
						<Button size="sm" variant="outline" class="h-8" disabled={resolveBusy || !resolveChoice[b.id]} onclick={() => reassignOne(b.id)}>
							{$t('members.resolve.reassign')}
						</Button>
						<Button size="sm" variant="ghost" class="h-8 text-destructive hover:text-destructive" disabled={resolveBusy} onclick={() => cancelOne(b.id)}>
							{$t('members.resolve.cancelMeeting')}
						</Button>
					</div>
				</div>
			{/each}
			{#if reassignTargets.length === 0}
				<p class="text-xs text-muted-foreground">{$t('members.resolve.noTargets')}</p>
			{/if}
		</div>

		<Dialog.Footer class="mt-2 gap-2 sm:justify-between">
			<Button variant="ghost" class="text-destructive hover:text-destructive" disabled={resolveBusy} onclick={cancelAllRemaining}>
				{$t('members.resolve.cancelAll')}
			</Button>
			<Button variant="outline" disabled={resolveBusy} onclick={() => { resolveOpen = false; resolveMember = null; }}>
				{$t('members.resolve.doneLater')}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<svelte:head><title>{$t('members.headTitle')}</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">{$t('members.title')}</h1>
		<p class="mt-1 text-sm text-muted-foreground">{$t('members.subtitle')}</p>
	</div>
	{#if $currentUser?.is_admin}
		<Button onclick={() => { showInvite = !showInvite; inviteError = ''; inviteResult = null; }}>
			{showInvite ? $t('members.invite.toggleCancel') : $t('members.invite.toggleOpen')}
		</Button>
	{/if}
</div>

{#if showInvite}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">{$t('members.invite.formTitle')}</h2>

		{#if inviteResult}
			<div class="mb-4 rounded-lg border border-amber-200 bg-amber-50 p-4">
				<p class="mb-1 text-sm font-semibold text-amber-900">{$t('members.invite.linkGenerated')}</p>
				<p class="mb-3 text-xs text-amber-800">{inviteResult.note}</p>
				{#if inviteResult.email_sent}
					<p class="mb-3 text-xs text-amber-700">{$t('members.invite.emailSent', { email: inviteResult.email })}</p>
				{:else}
					<p class="mb-3 text-xs text-amber-700">{$t('members.invite.smtpManual', { email: inviteResult.email })}</p>
				{/if}
				<div class="flex items-center gap-2">
					<code class="flex-1 overflow-x-auto rounded border bg-white px-2 py-1.5 text-xs font-mono text-gray-800">{inviteResult.invite_url}</code>
					<Button variant="outline" size="sm" onclick={() => copyInviteUrl(inviteResult!.invite_url)}>
						{copied ? $t('common.copied') : $t('common.copy')}
					</Button>
				</div>
			</div>
		{/if}

		{#if inviteError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{inviteError}</p>{/if}

		<div class="mb-4 space-y-1.5">
			<Label for="inv-email">{$t('members.invite.emailLabel')}</Label>
			<Input id="inv-email" type="email" bind:value={inviteEmail} placeholder={$t('members.invite.emailPlaceholder')}
				onkeydown={(e) => e.key === 'Enter' && sendInvite()} />
		</div>

		<Button onclick={sendInvite} disabled={inviting}>
			{inviting ? $t('members.invite.generating') : $t('members.invite.generate')}
		</Button>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else}
	<div class="mb-8">
		<div class="mb-3 flex items-center justify-between">
			<h2 class="text-sm font-semibold text-muted-foreground uppercase tracking-wide">{$t('members.section.members')}</h2>
			{#if $currentUser?.is_admin}
				<button class="text-xs text-muted-foreground hover:text-foreground" onclick={toggleArchived}>
					{showArchived ? $t('members.hideArchived') : $t('members.showArchived')}
				</button>
			{/if}
		</div>
		{#if members.length === 0}
			<div class="rounded-lg border border-dashed bg-card p-8 text-center">
				<p class="text-sm text-muted-foreground">{$t('members.empty')}</p>
			</div>
		{:else}
			<div class="rounded-lg border bg-card overflow-hidden">
				<table class="w-full text-sm">
					<thead>
						<tr class="border-b">
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('members.table.name')}</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('members.table.role')}</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('members.table.teams')}</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('members.table.page')}</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('members.table.auth')}</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('members.table.joined')}</th>
							{#if $currentUser?.is_admin}<th class="px-4 pb-3 pt-3"></th>{/if}
						</tr>
					</thead>
					<tbody class="divide-y">
						{#each members as m (m.id)}
							<tr class="transition-colors hover:bg-muted/30 {m.archived ? 'opacity-60' : ''}">
								<td class="px-4 py-3">
									<div class="flex items-center gap-2.5">
										{#if m.avatar_url}
											<img src={m.avatar_url} alt={m.name} class="h-7 w-7 shrink-0 rounded-full object-cover" />
										{:else}
											<div class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold text-muted-foreground">
												{m.name.slice(0, 2).toUpperCase()}
											</div>
										{/if}
										<div class="min-w-0">
											<p class="font-medium">
												{m.name}
												{#if m.id === $currentUser?.id}<span class="text-xs text-muted-foreground">{$t('members.you')}</span>{/if}
											</p>
											<p class="text-xs text-muted-foreground">{m.email}</p>
										</div>
									</div>
								</td>
								<td class="px-4 py-3">
									<div class="flex items-center gap-1.5">
										<Badge variant={roleVariant(m)}>{roleLabel(m)}</Badge>
										{#if m.archived}<Badge variant="outline" class="text-xs text-muted-foreground">{$t('members.badge.archived')}</Badge>{/if}
									</div>
								</td>
								<td class="px-4 py-3">
									<div class="flex gap-1.5 flex-wrap">
										{#each m.teams as tm}
											<Badge variant="secondary" class="text-xs">{tm.name}</Badge>
										{:else}
											<span class="text-xs text-muted-foreground/50">{$t('common.emDash')}</span>
										{/each}
									</div>
								</td>
								<td class="px-4 py-3">
									{#if m.handle}
										<a href="/u/{m.handle}" target="_blank" rel="noopener noreferrer" class="text-xs text-primary hover:underline font-mono">/u/{m.handle}</a>
									{:else}
										<span class="text-xs text-muted-foreground/50" title={$t('members.noPublicPageTitle')}>{$t('common.emDash')}</span>
									{/if}
								</td>
								<td class="px-4 py-3">
									<div class="flex gap-1.5 flex-wrap">
										{#each authBadge(m) as b}
											<Badge variant="outline" class="text-xs">{b}</Badge>
										{/each}
									</div>
								</td>
								<td class="px-4 py-3 text-sm text-muted-foreground">{fmtDate(m.created_at)}</td>
								{#if $currentUser?.is_admin}
									<td class="px-4 py-3">
										<div class="flex flex-wrap items-center justify-end gap-1">
											{#if m.archived}
												{#if m.archived_by_name}
													<span class="text-xs text-muted-foreground">{$t('members.archivedBy', { name: m.archived_by_name })}</span>
												{/if}
												<!-- Owner can restore anyone; an admin only members they archived. -->
												{#if $currentUser.is_owner || m.archived_by === $currentUser.id}
													<Button size="sm" variant="outline" class="h-7 text-xs" onclick={() => restoreMember(m)}>{$t('members.restore')}</Button>
												{/if}
											{:else if m.id !== $currentUser.id}
												{#if resetTarget === m.id}
													<div class="flex items-center gap-1.5">
														{#if resetOk}
															<span class="text-xs text-green-600 font-medium">{$t('members.passwordUpdated')}</span>
														{:else}
															<Input type="password" bind:value={resetPassword} placeholder={$t('members.passwordPlaceholder')}
																class="h-7 w-36 text-xs" onkeydown={(e) => e.key === 'Enter' && submitReset(m.id)} />
															{#if resetError}<span class="text-xs text-destructive">{resetError}</span>{/if}
															<Button size="sm" variant="outline" class="h-7 text-xs" onclick={() => submitReset(m.id)} disabled={resetting}>
																{resetting ? '…' : $t('members.setPassword')}
															</Button>
															<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={cancelReset}>{$t('common.cancel')}</Button>
														{/if}
													</div>
												{:else}
													{#if $currentUser.is_owner && !m.is_owner}
														{#if m.is_admin}
															<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => setRole(m, 'member')}>{$t('members.makeMember')}</Button>
														{:else}
															<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => setRole(m, 'admin')}>{$t('members.makeAdmin')}</Button>
														{/if}
														<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => confirmTransfer(m)}>{$t('members.transferOwnership')}</Button>
													{/if}
													<!-- Reset password + Archive only on members this viewer may manage:
													     never the owner; another admin only if the viewer is the owner. -->
													{#if !m.is_owner && (!m.is_admin || $currentUser.is_owner)}
														<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => startReset(m.id)}>{$t('members.resetPassword')}</Button>
														<Button size="sm" variant="ghost" class="h-7 text-xs text-destructive hover:text-destructive" onclick={() => startArchive(m)}>{$t('members.archive')}</Button>
													{/if}
												{/if}
											{/if}
										</div>
									</td>
								{/if}
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</div>

	<!-- Pending invites -->
	{#if $currentUser?.is_admin && invites.length > 0}
		<div>
			<h2 class="mb-3 text-sm font-semibold text-muted-foreground uppercase tracking-wide">{$t('members.section.pendingInvites')}</h2>
			<div class="rounded-lg border bg-card overflow-hidden">
				<table class="w-full text-sm">
					<thead>
						<tr class="border-b">
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('members.table.email')}</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">{$t('members.table.expires')}</th>
							<th class="px-4 pb-3 pt-3"></th>
						</tr>
					</thead>
					<tbody class="divide-y">
						{#each invites as inv}
							<tr class="transition-colors hover:bg-muted/30">
								<td class="px-4 py-3">{inv.email}</td>
								<td class="px-4 py-3 text-muted-foreground">
									{fmtDate(inv.expires_at)}
									<span class="ml-1 text-xs">{$t('members.expiresDaysLeft', { days: daysLeft(inv.expires_at) })}</span>
								</td>
								<td class="px-4 py-3 text-right whitespace-nowrap">
									<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => resendInvite(inv.id)}>
										{$t('members.resend')}
									</Button>
									<Button size="sm" variant="ghost" class="h-7 text-xs text-destructive hover:text-destructive" onclick={() => revokeInvite(inv.id)}>
										{$t('members.revoke')}
									</Button>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<p class="mt-2 text-xs text-muted-foreground">
				{$t('members.resendHint')}
			</p>
		</div>
	{/if}
{/if}
