<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { base } from '$app/paths';
	import '../app.css';
	import { api, type User } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { prefs, prefsFromUser } from '$lib/prefs';
	import { Toaster } from '$lib/components/ui/sonner';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	let checking = $state(true);

	const isLogin = $derived($page.route.id === '/login');
	const isPublicRoute = $derived(
		$page.route.id === '/login' ||
		$page.route.id === '/claim' ||
		$page.route.id === '/invite/[token]'
	);

	const navItems = [
		{
			href: `${base}/`,
			label: 'Home',
			adminOnly: false,
			exact: true,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/></svg>`
		},
		{
			href: `${base}/event-types`,
			label: 'Event Types',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/></svg>`
		},
		{
			href: `${base}/availability`,
			label: 'Availability',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>`
		},
		{
			href: `${base}/bookings`,
			label: 'Bookings',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>`
		},
		{
			href: `${base}/team`,
			label: 'Team',
			adminOnly: true,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>`
		},
		{
			href: `${base}/api-keys`,
			label: 'API Keys',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/></svg>`
		},
		{
			href: `${base}/webhooks`,
			label: 'Webhooks',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/></svg>`
		},
		{
			href: `${base}/calendar`,
			label: 'Calendar',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/><rect x="8" y="14" width="2" height="2"/><rect x="13" y="14" width="2" height="2"/></svg>`
		},
		{
			href: `${base}/settings`,
			label: 'Settings',
			adminOnly: false,
			icon: `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`
		}
	];

	onMount(async () => {
		if (isPublicRoute) {
			checking = false;
			return;
		}
		try {
			const me = await api.get<User>('/v1/users/me');
			currentUser.set(me);
			prefs.set(prefsFromUser(me));
		} catch {
			window.location.href = '/admin/login';
			return;
		}
		checking = false;
	});

	async function logout() {
		await fetch('/v1/auth/logout', { method: 'POST', credentials: 'same-origin' });
		window.location.href = '/admin/login';
	}

	function initials(name: string) {
		return name
			.split(' ')
			.map((p) => p[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}
</script>

<Toaster position="bottom-right" />

{#if isPublicRoute}
	{@render children()}
{:else if checking}
	<div class="flex h-full items-center justify-center text-sm text-muted-foreground">
		Loading…
	</div>
{:else}
	<div class="flex h-full">
		<!-- Sidebar -->
		<aside class="flex w-56 shrink-0 flex-col border-r border-sidebar-border bg-sidebar">
			<!-- User section -->
			<a href="{base}/settings/profile" class="flex items-center gap-3 border-b border-sidebar-border px-4 py-3 hover:bg-sidebar-accent/60 transition-colors">
				{#if $currentUser?.avatar_url}
					<img src={$currentUser.avatar_url} alt={$currentUser.name} class="h-7 w-7 shrink-0 rounded-full object-cover" />
				{:else}
					<div class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary text-xs font-semibold text-primary-foreground">
						{initials($currentUser?.name ?? 'U')}
					</div>
				{/if}
				<div class="min-w-0 flex-1">
					<p class="truncate text-sm font-medium text-sidebar-foreground">{$currentUser?.name ?? ''}</p>
				</div>
			</a>

			<!-- Nav -->
			<nav class="flex-1 space-y-0.5 p-2">
				{#each navItems as item}
					{#if !item.adminOnly || $currentUser?.is_admin}
						{@const active = item.exact
							? $page.url.pathname === item.href || $page.url.pathname === base
							: $page.url.pathname.startsWith(item.href)}
						<a
							href={item.href}
							class="flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-medium transition-colors
								{active
									? 'bg-sidebar-accent text-sidebar-accent-foreground'
									: 'text-sidebar-foreground/70 hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground'}"
						>
							<span class="shrink-0 {active ? 'opacity-100' : 'opacity-60'}">{@html item.icon}</span>
							{item.label}
						</a>
					{/if}
				{/each}
			</nav>

			<!-- Footer -->
			<div class="border-t border-sidebar-border p-2">
				<button
					onclick={logout}
					class="flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-medium text-sidebar-foreground/60 transition-colors hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground"
				>
					<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="shrink-0">
						<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
						<polyline points="16 17 21 12 16 7"/>
						<line x1="21" y1="12" x2="9" y2="12"/>
					</svg>
					Sign out
				</button>
			</div>
		</aside>

		<!-- Main content -->
		<main class="flex-1 overflow-y-auto bg-background">
			<div class="mx-auto max-w-4xl px-8 py-8">
				{@render children()}
			</div>
		</main>
	</div>
{/if}
