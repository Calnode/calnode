<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';
	import { createAsyncFlag } from '$lib/async-action.svelte';

	type Tracking = {
		head_html: string;
		csp_allow: string;
		datalayer_enabled: boolean;
		datalayer_fields: string[];
		available_fields: string[];
		gtm_container_id: string;
		ga4_measurement_id: string;
	};

	const fieldLabels: Record<string, string> = {
		booking_id: 'Booking reference', event_type_slug: 'Event type slug', event_type_name: 'Event type name',
		start_at: 'Start time', end_at: 'End time', status: 'Status', location: 'Location',
		host_name: 'Host name', attendee_name: 'Attendee name', attendee_email: 'Attendee email',
		attendee_timezone: 'Attendee timezone', answers: 'Intake answers',
		value: 'Revenue / amount', currency: 'Currency', is_paid: 'Paid flag', transaction_id: 'Transaction ID'
	};
	const piiFields = new Set(['attendee_name', 'attendee_email', 'attendee_timezone', 'answers']);

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	let headHtml = $state('');
	let cspAllow = $state('');
	let dlEnabled = $state(false);
	let dlFields = $state<string[]>([]);
	let availableFields = $state<string[]>([]);
	let gtmId = $state('');
	let ga4Id = $state('');

	onMount(() => loadingFlag.run(async () => {
		const t = await api.get<Tracking>('/v1/settings/tracking');
		headHtml = t.head_html ?? '';
		cspAllow = t.csp_allow ?? '';
		dlEnabled = t.datalayer_enabled;
		dlFields = t.datalayer_fields ?? [];
		availableFields = t.available_fields ?? [];
		gtmId = t.gtm_container_id ?? '';
		ga4Id = t.ga4_measurement_id ?? '';
	}, 'Could not load tracking settings'));

	function toggleField(key: string) {
		dlFields = dlFields.includes(key) ? dlFields.filter((f) => f !== key) : [...dlFields, key];
	}

	async function save() {
		await savingFlag.run(async () => {
			const t = await api.patch<Tracking>('/v1/settings/tracking', {
				head_html: headHtml,
				csp_allow: cspAllow,
				datalayer_enabled: dlEnabled,
				datalayer_fields: dlFields,
				gtm_container_id: gtmId.trim(),
				ga4_measurement_id: ga4Id.trim()
			});
			dlFields = t.datalayer_fields ?? [];
			gtmId = t.gtm_container_id ?? '';
			ga4Id = t.ga4_measurement_id ?? '';
			toast.success('Tracking settings saved');
		}, 'Could not save tracking settings');
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Admin access required.</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<div class="max-w-2xl space-y-6">
		<!-- Native GA4 / GTM -->
		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Google Analytics &amp; Tag Manager</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Enter an ID and Calnode loads the official tag on your booking page automatically — no snippet to
				paste, and the page's CSP is handled for you. Leave a field blank to turn that tag off.
			</p>
			<div class="mt-4 grid gap-4 sm:grid-cols-2">
				<div class="space-y-1.5">
					<Label for="gtm-id">GTM Container ID</Label>
					<Input id="gtm-id" bind:value={gtmId} placeholder="GTM-XXXXXXX" class="font-mono" />
					<p class="text-xs text-muted-foreground">
						Recommended — manages GA4 + Ads tags. Trigger them on the
						<code class="rounded bg-muted px-1">calnode_booking_confirmed</code> dataLayer event below.
					</p>
				</div>
				<div class="space-y-1.5">
					<Label for="ga4-id">GA4 Measurement ID</Label>
					<Input id="ga4-id" bind:value={ga4Id} placeholder="G-XXXXXXXXXX" class="font-mono" />
					<p class="text-xs text-muted-foreground">
						Loads GA4 directly. Bookings fire a <code class="rounded bg-muted px-1">purchase</code> /
						<code class="rounded bg-muted px-1">generate_lead</code> event with revenue.
					</p>
				</div>
			</div>
		</div>

		<!-- Code injection -->
		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Code injection (head)</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Raw HTML/JS injected into the &lt;head&gt; of your public booking and manage pages — for any tag
				<em>not</em> covered above (Meta Pixel, Plausible, custom). It runs on visitors' browsers; only admins can set it.
			</p>
			<div class="mt-4 space-y-1.5">
				<Label for="head-html">&lt;head&gt; HTML</Label>
				<Textarea id="head-html" bind:value={headHtml} rows={8} class="font-mono text-xs"
					placeholder="<!-- Paste your GTM / GA4 / Meta Pixel snippet here -->" />
			</div>
			<div class="mt-4 space-y-1.5">
				<Label for="csp-allow">Allowed origins <span class="font-normal text-muted-foreground">(optional)</span></Label>
				<Input id="csp-allow" bind:value={cspAllow}
					placeholder="https://www.googletagmanager.com https://*.google-analytics.com" />
				<p class="text-xs text-muted-foreground">
					Leave blank to allow any <code class="rounded bg-muted px-1">https:</code> origin while injection is
					active — simplest, and GTM-managed tags just work. Fill in to lock the page's CSP to only these
					origins (space-separated); tags from other domains will then be blocked.
				</p>
			</div>
		</div>

		<!-- dataLayer events -->
		<div class="rounded-lg border bg-card p-6">
			<div class="flex items-start justify-between gap-4">
				<div>
					<h2 class="text-sm font-semibold">dataLayer events</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Push <code class="rounded bg-muted px-1">calnode_booking_confirmed</code> /
						<code class="rounded bg-muted px-1">_cancelled</code> /
						<code class="rounded bg-muted px-1">_rescheduled</code> into
						<code class="rounded bg-muted px-1">window.dataLayer</code> so GTM can trigger on them.
					</p>
				</div>
				<Switch bind:checked={dlEnabled} />
			</div>
			{#if dlEnabled}
				<div class="mt-4 space-y-2">
					<p class="text-xs font-medium text-muted-foreground">
						Fields to include — untick anything you don't want exposed to the browser / GTM.
					</p>
					<div class="grid grid-cols-2 gap-x-4 gap-y-1">
						{#each availableFields as key}
							<label class="flex cursor-pointer items-center gap-2 font-mono text-sm">
								<Checkbox checked={dlFields.includes(key)} onCheckedChange={() => toggleField(key)} />
								<span>{fieldLabels[key] ?? key}{#if piiFields.has(key)}<span class="ml-1 text-[10px] font-medium uppercase text-amber-600">PII</span>{/if}</span>
							</label>
						{/each}
					</div>
				</div>
			{/if}
		</div>

		<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? 'Saving…' : 'Save'}</Button>
	</div>
{/if}
