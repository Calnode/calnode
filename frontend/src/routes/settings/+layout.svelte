<script lang="ts">
	import { page } from '$app/stores';
	import { base } from '$app/paths';
	import { currentUser } from '$lib/stores';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	const navItems = [
		{ href: `${base}/settings/profile`, label: 'Profile' },
		{ href: `${base}/settings/notifications`, label: 'Notifications' },
		{ href: `${base}/settings/branding`, label: 'Branding', adminOnly: true },
		{ href: `${base}/settings/email`, label: 'Email', adminOnly: true },
		{ href: `${base}/settings/google`, label: 'Google OAuth', adminOnly: true },
		{ href: `${base}/settings/tracking`, label: 'Tracking', adminOnly: true },
	];
</script>

<svelte:head><title>Settings — Calnode</title></svelte:head>

<div class="mb-8">
	<h1 class="text-2xl font-semibold tracking-tight">Settings</h1>
	<p class="mt-1 text-sm text-muted-foreground">Manage your profile, preferences, and integrations.</p>
</div>

<div class="flex gap-8">
	<nav class="w-44 shrink-0">
		<ul class="space-y-0.5">
			{#each navItems as item}
				{#if !item.adminOnly || $currentUser?.is_admin}
					{@const active = $page.url.pathname.startsWith(item.href)}
					<li>
						<a
							href={item.href}
							class="block rounded-md px-3 py-2 text-sm font-medium transition-colors
								{active
									? 'bg-accent text-accent-foreground'
									: 'text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground'}"
						>
							{item.label}
						</a>
					</li>
				{/if}
			{/each}
		</ul>
	</nav>

	<div class="min-w-0 flex-1">
		{@render children()}
	</div>
</div>
