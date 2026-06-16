<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Team, type TeamMember } from '$lib/api';
	import { Button } from '$lib/components/ui/button';
	import { ConfirmDialog } from '$lib/components/ui/confirm-dialog';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Badge } from '$lib/components/ui/badge';
	import * as Select from '$lib/components/ui/select';
	import { toast } from 'svelte-sonner';

	let teams = $state<Team[]>([]);
	let users = $state<TeamMember[]>([]);
	let loading = $state(true);
	let error = $state('');

	// Create form
	let showCreate = $state(false);
	let newTeamName = $state('');
	let creating = $state(false);

	// Expanded team detail (teamId → full team with members)
	let expanded = $state<Record<string, Team>>({});
	// Add-member selection per team (teamId → userId)
	let addChoice = $state<Record<string, string>>({});
	// Rename state
	let renamingId = $state<string | null>(null);
	let renameValue = $state('');

	// Confirm dialog
	let confirmOpen = $state(false);
	let confirmTitle = $state('');
	let confirmDescription = $state('');
	let pendingAction: (() => void) | null = null;
	function openConfirm(o: { title: string; description: string; action: () => void }) {
		confirmTitle = o.title; confirmDescription = o.description; pendingAction = o.action; confirmOpen = true;
	}

	async function load() {
		try {
			const [teamsRes, usersRes] = await Promise.all([
				api.get<{ items: Team[] }>('/v1/teams'),
				api.get<TeamMember[]>('/v1/users')
			]);
			teams = teamsRes.items;
			users = usersRes;
		} catch (e: any) {
			error = e.message;
		} finally {
			loading = false;
		}
	}
	onMount(load);

	async function createTeam() {
		if (!newTeamName.trim()) { toast.error('Team name is required'); return; }
		creating = true;
		try {
			await api.post('/v1/teams', { name: newTeamName.trim() });
			newTeamName = '';
			showCreate = false;
			await load();
		} catch (e: any) {
			toast.error(e.message || 'Could not create team');
		} finally {
			creating = false;
		}
	}

	async function toggleExpand(team: Team) {
		if (expanded[team.id]) {
			const { [team.id]: _, ...rest } = expanded;
			expanded = rest;
			return;
		}
		try {
			const detail = await api.get<Team>(`/v1/teams/${team.id}`);
			expanded = { ...expanded, [team.id]: detail };
		} catch (e: any) {
			toast.error(e.message || 'Could not load team');
		}
	}

	async function refreshTeam(teamId: string) {
		const detail = await api.get<Team>(`/v1/teams/${teamId}`);
		expanded = { ...expanded, [teamId]: detail };
		// Keep the member counts in the list fresh too.
		teams = teams.map((t) => (t.id === teamId ? { ...t, member_count: detail.member_count } : t));
	}

	function membersNotIn(team: Team): TeamMember[] {
		const inTeam = new Set((expanded[team.id]?.members ?? []).map((m) => m.id));
		return users.filter((u) => !u.archived && !inTeam.has(u.id));
	}

	async function addMember(teamId: string) {
		const userId = addChoice[teamId];
		if (!userId) return;
		try {
			await api.post(`/v1/teams/${teamId}/members`, { user_id: userId });
			addChoice = { ...addChoice, [teamId]: '' };
			await refreshTeam(teamId);
		} catch (e: any) {
			toast.error(e.message || 'Could not add member');
		}
	}

	async function removeMember(teamId: string, userId: string) {
		try {
			await api.del(`/v1/teams/${teamId}/members/${userId}`);
			await refreshTeam(teamId);
		} catch (e: any) {
			toast.error(e.message || 'Could not remove member');
		}
	}

	async function savePriority(teamId: string, userId: string, value: number) {
		try {
			await api.patch(`/v1/teams/${teamId}/members/${userId}`, { routing_priority: Number(value) });
		} catch (e: any) {
			toast.error(e.message || 'Could not update priority');
			await refreshTeam(teamId);
		}
	}

	function startRename(team: Team) { renamingId = team.id; renameValue = team.name; }
	function cancelRename() { renamingId = null; renameValue = ''; }
	async function saveRename(team: Team) {
		if (!renameValue.trim()) { toast.error('Name cannot be empty'); return; }
		try {
			await api.patch(`/v1/teams/${team.id}`, { name: renameValue.trim() });
			renamingId = null;
			await load();
		} catch (e: any) {
			toast.error(e.message || 'Could not rename team');
		}
	}

	function deleteTeam(team: Team) {
		openConfirm({
			title: `Delete "${team.name}"?`,
			description: 'The team is removed and its members are unassigned. Members themselves and their bookings are not affected. Event types using this team for routing fall back to no team.',
			action: async () => {
				try {
					await api.del(`/v1/teams/${team.id}`);
					const { [team.id]: _, ...rest } = expanded;
					expanded = rest;
					await load();
				} catch (e: any) {
					toast.error(e.message || 'Could not delete team');
				}
			}
		});
	}
</script>

<ConfirmDialog bind:open={confirmOpen} title={confirmTitle} description={confirmDescription} confirmText="Delete" destructive onConfirm={() => pendingAction?.()} />

<svelte:head><title>Teams — Calnode</title></svelte:head>

<div class="mb-8 flex items-center justify-between">
	<div>
		<h1 class="text-2xl font-semibold tracking-tight">Teams</h1>
		<p class="mt-1 text-sm text-muted-foreground">Group members for round-robin and group event types.</p>
	</div>
	<Button onclick={() => { showCreate = !showCreate; }}>{showCreate ? 'Cancel' : 'New team'}</Button>
</div>

{#if showCreate}
	<div class="mb-6 rounded-lg border bg-card p-6">
		<h2 class="mb-4 text-sm font-semibold">New team</h2>
		<div class="flex items-end gap-3">
			<div class="flex-1 space-y-1.5">
				<Label for="team-name">Team name</Label>
				<Input id="team-name" bind:value={newTeamName} placeholder="e.g. Sales" onkeydown={(e) => e.key === 'Enter' && createTeam()} />
			</div>
			<Button onclick={createTeam} disabled={creating}>{creating ? 'Creating…' : 'Create team'}</Button>
		</div>
	</div>
{/if}

{#if error}<p class="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>{/if}

{#if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else if teams.length === 0}
	<div class="rounded-lg border border-dashed bg-card p-12 text-center">
		<p class="text-sm font-medium">No teams yet</p>
		<p class="mt-1 text-sm text-muted-foreground">Create a team to group members for round-robin or group scheduling.</p>
	</div>
{:else}
	<div class="space-y-3">
		{#each teams as team (team.id)}
			<div class="rounded-lg border bg-card">
				<div class="flex items-center justify-between gap-3 p-4">
					<div class="min-w-0">
						{#if renamingId === team.id}
							<div class="flex items-center gap-2">
								<Input bind:value={renameValue} class="h-8 w-56" onkeydown={(e) => e.key === 'Enter' && saveRename(team)} />
								<Button size="sm" class="h-8" onclick={() => saveRename(team)}>Save</Button>
								<Button size="sm" variant="ghost" class="h-8" onclick={cancelRename}>Cancel</Button>
							</div>
						{:else}
							<div class="flex items-center gap-2">
								<p class="font-medium">{team.name}</p>
								<Badge variant="outline" class="font-mono text-xs">{team.slug}</Badge>
							</div>
							<p class="mt-0.5 text-xs text-muted-foreground">{team.member_count} member{team.member_count === 1 ? '' : 's'}</p>
						{/if}
					</div>
					<div class="flex shrink-0 items-center gap-1">
						<Button size="sm" variant="outline" class="h-8 text-xs" onclick={() => toggleExpand(team)}>
							{expanded[team.id] ? 'Close' : 'Manage'}
						</Button>
						<Button size="sm" variant="ghost" class="h-8 text-xs" onclick={() => startRename(team)}>Rename</Button>
						<Button size="sm" variant="ghost" class="h-8 text-xs text-destructive hover:text-destructive" onclick={() => deleteTeam(team)}>Delete</Button>
					</div>
				</div>

				{#if expanded[team.id]}
					<div class="border-t px-4 py-4">
						<h3 class="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Members</h3>
						{#if (expanded[team.id].members ?? []).length === 0}
							<p class="mb-3 text-sm text-muted-foreground">No members in this team yet.</p>
						{:else}
							<table class="mb-3 w-full text-sm">
								<thead>
									<tr class="border-b">
										<th class="pb-2 text-left text-xs font-medium text-muted-foreground">Member</th>
										<th class="pb-2 text-left text-xs font-medium text-muted-foreground">Routing priority</th>
										<th class="pb-2"></th>
									</tr>
								</thead>
								<tbody class="divide-y">
									{#each expanded[team.id].members ?? [] as m (m.id)}
										<tr>
											<td class="py-2">
												<div class="flex items-center gap-2">
													<span class="font-medium">{m.name}</span>
													<span class="text-xs text-muted-foreground">{m.email}</span>
													{#if m.archived}<Badge variant="outline" class="text-xs text-muted-foreground">Archived</Badge>{/if}
												</div>
											</td>
											<td class="py-2">
												<Input
													type="number"
													value={m.routing_priority}
													class="h-7 w-20 text-xs"
													onchange={(e) => savePriority(team.id, m.id, +(e.currentTarget as HTMLInputElement).value)}
												/>
											</td>
											<td class="py-2 text-right">
												<Button size="sm" variant="ghost" class="h-7 text-xs text-destructive hover:text-destructive" onclick={() => removeMember(team.id, m.id)}>Remove</Button>
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						{/if}

						<!-- Add a member -->
						<div class="flex items-center gap-2">
							<Select.Root
								type="single"
								value={addChoice[team.id] ?? ''}
								onValueChange={(v) => { addChoice = { ...addChoice, [team.id]: v ?? '' }; }}
								disabled={membersNotIn(team).length === 0}
							>
								<Select.Trigger class="w-fit min-w-48">
									{#if addChoice[team.id]}
										{@const u = users.find((x) => x.id === addChoice[team.id])}
										{u ? `${u.name} (${u.email})` : 'Add a member…'}
									{:else}
										Add a member…
									{/if}
								</Select.Trigger>
								<Select.Content>
									{#each membersNotIn(team) as u}
										<Select.Item value={u.id} label={`${u.name} (${u.email})`}>{u.name} ({u.email})</Select.Item>
									{/each}
								</Select.Content>
							</Select.Root>
							<Button size="sm" variant="outline" class="h-8" disabled={!addChoice[team.id]} onclick={() => addMember(team.id)}>Add</Button>
							{#if membersNotIn(team).length === 0}
								<span class="text-xs text-muted-foreground">All active members are already in this team.</span>
							{/if}
						</div>
						<p class="mt-2 text-xs text-muted-foreground">Lower routing priority = preferred earlier (used by Priority routing; ties broken by load for round-robin).</p>
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/if}
