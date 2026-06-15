<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type TeamMember, type Invite } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Badge } from '$lib/components/ui/badge';

	let members: TeamMember[] = $state([]);
	let invites: Invite[] = $state([]);
	let loading = $state(true);
	let error = $state('');

	// Invite form
	let showInvite = $state(false);
	let inviteEmail = $state('');
	let inviting = $state(false);
	let inviteError = $state('');
	let inviteResult = $state<{ invite_url: string; email: string; email_sent: boolean; note: string } | null>(null);
	let copied = $state(false);

	// Password reset — keyed by user id
	let resetTarget = $state<string | null>(null);
	let resetPassword = $state('');
	let resetting = $state(false);
	let resetError = $state('');
	let resetOk = $state(false);

	async function load() {
		try {
			const [membersRes, invitesRes] = await Promise.all([
				api.get<TeamMember[]>('/v1/users'),
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

	async function sendInvite() {
		inviteError = '';
		inviteResult = null;
		if (!inviteEmail.trim()) { inviteError = 'Email is required.'; return; }
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

	async function revokeInvite(id: string) {
		if (!confirm('Revoke this invite? The link will stop working immediately.')) return;
		try {
			await api.del(`/v1/invites/${id}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	async function deleteUser(id: string, name: string) {
		if (!confirm(`Remove ${name} from the team? This cannot be undone.`)) return;
		try {
			await api.del(`/v1/users/${id}`);
			await load();
		} catch (e: any) {
			error = e.message;
		}
	}

	function startReset(id: string) {
		resetTarget = id;
		resetPassword = '';
		resetError = '';
		resetOk = false;
	}

	function cancelReset() {
		resetTarget = null;
		resetPassword = '';
		resetError = '';
		resetOk = false;
	}

	async function submitReset(userId: string) {
		resetError = '';
		resetOk = false;
		if (!resetPassword) { resetError = 'Password is required.'; return; }
		resetting = true;
		try {
			await api.post(`/v1/users/${userId}/password`, { password: resetPassword });
			resetOk = true;
			resetPassword = '';
			setTimeout(() => { resetTarget = null; resetOk = false; }, 2000);
		} catch (e: any) {
			resetError = e.message;
		} finally {
			resetting = false;
		}
	}

	async function copyInviteUrl(url: string) {
		await navigator.clipboard.writeText(url);
		copied = true;
		setTimeout(() => { copied = false; }, 2000);
	}

	function roleBadge(m: TeamMember) {
		if (m.is_admin) return 'Admin';
		return 'Member';
	}

	function authBadge(m: TeamMember): string[] {
		const badges: string[] = [];
		if (m.provider === 'google') badges.push('Google');
		else if (m.provider === 'microsoft') badges.push('Microsoft');
		if (m.email_login) badges.push('Email');
		return badges;
	}

	function fmtDate(iso: string) {
		return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' });
	}

	function daysLeft(iso: string) {
		const diff = new Date(iso).getTime() - Date.now();
		return Math.max(0, Math.ceil(diff / 86_400_000));
	}
</script>

<svelte:head><title>Team — Calnode</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Team</h1>
		<p class="mt-1 text-sm text-muted-foreground">Manage team members and send invites.</p>
	</div>
	{#if $currentUser?.is_admin}
		<Button onclick={() => { showInvite = !showInvite; inviteError = ''; inviteResult = null; }}>
			{showInvite ? 'Cancel' : 'Invite member'}
		</Button>
	{/if}
</div>

{#if showInvite}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">Invite a team member</h2>

		{#if inviteResult}
			<div class="mb-4 rounded-lg border border-amber-200 bg-amber-50 p-4">
				<p class="mb-1 text-sm font-semibold text-amber-900">Invite link generated</p>
				<p class="mb-3 text-xs text-amber-800">{inviteResult.note}</p>
				{#if inviteResult.email_sent}
					<p class="mb-3 text-xs text-amber-700">An invite email has been sent to {inviteResult.email}.</p>
				{:else}
					<p class="mb-3 text-xs text-amber-700">SMTP is not configured — share this link directly with {inviteResult.email}.</p>
				{/if}
				<div class="flex items-center gap-2">
					<code class="flex-1 overflow-x-auto rounded border bg-white px-2 py-1.5 text-xs font-mono text-gray-800">{inviteResult.invite_url}</code>
					<Button variant="outline" size="sm" onclick={() => copyInviteUrl(inviteResult!.invite_url)}>
						{copied ? 'Copied!' : 'Copy'}
					</Button>
				</div>
			</div>
		{/if}

		{#if inviteError}<p class="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{inviteError}</p>{/if}

		<div class="mb-4 space-y-1.5">
			<Label for="inv-email">Email address</Label>
			<Input
				id="inv-email"
				type="email"
				bind:value={inviteEmail}
				placeholder="teammate@example.com"
				onkeydown={(e) => e.key === 'Enter' && sendInvite()}
			/>
		</div>

		<Button onclick={sendInvite} disabled={inviting}>
			{inviting ? 'Generating…' : 'Generate invite link'}
		</Button>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<!-- Members -->
	<div class="mb-8">
		<h2 class="mb-3 text-sm font-semibold text-muted-foreground uppercase tracking-wide">Members</h2>
		{#if members.length === 0}
			<div class="rounded-lg border border-dashed bg-card p-8 text-center">
				<p class="text-sm text-muted-foreground">No members yet.</p>
			</div>
		{:else}
			<div class="rounded-lg border bg-card overflow-hidden">
				<table class="w-full text-sm">
					<thead>
						<tr class="border-b">
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Name</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Role</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Auth</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Joined</th>
							{#if $currentUser?.is_admin}<th class="px-4 pb-3 pt-3"></th>{/if}
						</tr>
					</thead>
					<tbody class="divide-y">
						{#each members as m}
							<tr class="transition-colors hover:bg-muted/30">
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
											<p class="font-medium">{m.name}</p>
											<p class="text-xs text-muted-foreground">{m.email}</p>
										</div>
									</div>
								</td>
								<td class="px-4 py-3">
									<Badge variant={m.is_admin ? 'default' : 'secondary'}>{roleBadge(m)}</Badge>
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
										<div class="flex items-center justify-end gap-1">
											{#if m.id !== $currentUser.id}
												{#if resetTarget === m.id}
													<div class="flex items-center gap-1.5">
														{#if resetOk}
															<span class="text-xs text-green-600 font-medium">Password updated</span>
														{:else}
															<Input
																type="password"
																bind:value={resetPassword}
																placeholder="New password"
																class="h-7 w-36 text-xs"
																onkeydown={(e) => e.key === 'Enter' && submitReset(m.id)}
															/>
															{#if resetError}<span class="text-xs text-destructive">{resetError}</span>{/if}
															<Button size="sm" variant="outline" class="h-7 text-xs" onclick={() => submitReset(m.id)} disabled={resetting}>
																{resetting ? '…' : 'Set'}
															</Button>
															<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={cancelReset}>Cancel</Button>
														{/if}
													</div>
												{:else}
													<Button size="sm" variant="ghost" class="h-7 text-xs" onclick={() => startReset(m.id)}>
														Reset password
													</Button>
													<Button size="sm" variant="ghost" class="h-7 text-xs text-destructive hover:text-destructive" onclick={() => deleteUser(m.id, m.name)}>
														Remove
													</Button>
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
			<h2 class="mb-3 text-sm font-semibold text-muted-foreground uppercase tracking-wide">Pending Invites</h2>
			<div class="rounded-lg border bg-card overflow-hidden">
				<table class="w-full text-sm">
					<thead>
						<tr class="border-b">
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Email</th>
							<th class="px-4 pb-3 pt-3 text-left text-xs font-medium text-muted-foreground">Expires</th>
							<th class="px-4 pb-3 pt-3"></th>
						</tr>
					</thead>
					<tbody class="divide-y">
						{#each invites as inv}
							<tr class="transition-colors hover:bg-muted/30">
								<td class="px-4 py-3">{inv.email}</td>
								<td class="px-4 py-3 text-muted-foreground">
									{fmtDate(inv.expires_at)}
									<span class="ml-1 text-xs">({daysLeft(inv.expires_at)}d left)</span>
								</td>
								<td class="px-4 py-3 text-right">
									<Button size="sm" variant="ghost" class="h-7 text-xs text-destructive hover:text-destructive" onclick={() => revokeInvite(inv.id)}>
										Revoke
									</Button>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<p class="mt-2 text-xs text-muted-foreground">
				To resend an invite, generate a new one for the same email — the existing link will be replaced.
			</p>
		</div>
	{/if}
{/if}
