<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import { base } from '$app/paths';
	import '../app.css';
	import { api, type User } from '$lib/api';
	import { currentUser } from '$lib/stores';

	let checking = true;

	$: isLogin = $page.route.id === '/login';

	const navItems = [
		{ href: `${base}/event-types`, label: 'Event Types', icon: '📅' },
		{ href: `${base}/bookings`, label: 'Bookings', icon: '📋' },
		{ href: `${base}/api-keys`, label: 'API Keys', icon: '🔑' },
		{ href: `${base}/webhooks`, label: 'Webhooks', icon: '🔔' },
		{ href: `${base}/calendar`, label: 'Calendar', icon: '📆' }
	];

	onMount(async () => {
		if (isLogin) {
			checking = false;
			return;
		}
		try {
			const me = await api.get<User>('/v1/users/me');
			currentUser.set(me);
		} catch {
			window.location.href = '/v1/auth/login';
			return;
		}
		checking = false;
	});

	async function logout() {
		await fetch('/v1/auth/logout', { method: 'POST', credentials: 'same-origin' });
		window.location.href = '/v1/auth/login';
	}
</script>

{#if isLogin}
	<slot />
{:else if checking}
	<div style="display:flex;align-items:center;justify-content:center;height:100vh;color:#64748b;">
		Loading…
	</div>
{:else}
	<div class="app-shell">
		<nav class="sidebar">
			<div class="logo">
				<span class="logo-mark">⚡</span>
				<span class="logo-text">Calnode</span>
			</div>

			<ul class="nav-list">
				{#each navItems as item}
					<li>
						<a
							href={item.href}
							class="nav-link"
							class:active={$page.url.pathname.startsWith(item.href)}
						>
							<span class="nav-icon">{item.icon}</span>
							{item.label}
						</a>
					</li>
				{/each}
			</ul>

			<div class="sidebar-footer">
				{#if $currentUser}
					<div class="user-info">
						<div class="user-name">{$currentUser.name}</div>
						<div class="user-email">{$currentUser.email}</div>
					</div>
				{/if}
				<button class="btn-secondary btn-sm" on:click={logout} style="width:100%;margin-top:8px;">
					Sign out
				</button>
			</div>
		</nav>

		<main class="content">
			<slot />
		</main>
	</div>
{/if}

<style>
	.app-shell {
		display: flex;
		height: 100vh;
		overflow: hidden;
	}

	.sidebar {
		width: var(--sidebar-w);
		min-width: var(--sidebar-w);
		background: var(--sidebar-bg);
		display: flex;
		flex-direction: column;
		padding: 0;
		overflow-y: auto;
	}

	.logo {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 18px 16px 14px;
		border-bottom: 1px solid rgba(255,255,255,0.08);
		margin-bottom: 8px;
	}
	.logo-mark { font-size: 18px; }
	.logo-text { font-size: 16px; font-weight: 700; color: #f8fafc; letter-spacing: -0.02em; }

	.nav-list {
		list-style: none;
		margin: 0;
		padding: 0 8px;
		flex: 1;
	}
	.nav-list li { margin: 2px 0; }

	.nav-link {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 8px 10px;
		border-radius: 6px;
		color: var(--sidebar-text);
		font-size: 13px;
		font-weight: 500;
		text-decoration: none;
		transition: background 0.15s, color 0.15s;
	}
	.nav-link:hover { background: var(--sidebar-hover); color: #f8fafc; text-decoration: none; }
	.nav-link.active { background: var(--sidebar-active-bg); color: var(--sidebar-active); }

	.nav-icon { font-size: 14px; width: 18px; text-align: center; }

	.sidebar-footer {
		padding: 12px 8px 16px;
		border-top: 1px solid rgba(255,255,255,0.08);
	}
	.user-info { padding: 0 4px; }
	.user-name { font-size: 13px; font-weight: 600; color: #f8fafc; }
	.user-email { font-size: 11px; color: var(--sidebar-text); margin-top: 1px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

	.content {
		flex: 1;
		overflow-y: auto;
		padding: 28px 32px;
	}
</style>
