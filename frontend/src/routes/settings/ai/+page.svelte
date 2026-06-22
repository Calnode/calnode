<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type LLMSettings } from '$lib/api';
	import { currentUser } from '$lib/stores';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Label } from '$lib/components/ui/label';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import { toast } from 'svelte-sonner';
	import { saveOnCmdS } from '$lib/save-shortcut';

	let loading = $state(true);
	let saving = $state(false);
	let testing = $state(false);

	let settings: LLMSettings | null = $state(null);
	let enabled = $state(false);
	let endpoint = $state('');
	let model = $state('');
	let apiKey = $state('');
	let extraInstructions = $state('');

	onMount(async () => {
		try {
			settings = await api.get<LLMSettings>('/v1/settings/llm');
			enabled = settings.enabled;
			endpoint = settings.endpoint;
			model = settings.model;
			extraInstructions = settings.extra_instructions;
		} catch (e: any) {
			toast.error(e.message || 'Could not load AI settings');
		} finally {
			loading = false;
		}
	});

	async function save() {
		saving = true;
		try {
			const body: Record<string, unknown> = {
				enabled, endpoint: endpoint.trim(), model: model.trim(),
				extra_instructions: extraInstructions
			};
			if (apiKey) body.api_key = apiKey;
			settings = await api.patch<LLMSettings>('/v1/settings/llm', body);
			enabled = settings.enabled;
			extraInstructions = settings.extra_instructions;
			apiKey = '';
			toast.success(settings.active ? 'Saved — AI is on' : 'Saved');
		} catch (e: any) {
			toast.error(e.message || 'Could not save AI settings');
		} finally {
			saving = false;
		}
	}

	async function testConnection() {
		if (!endpoint.trim() || !model.trim()) {
			toast.error('Enter an endpoint and model first');
			return;
		}
		testing = true;
		try {
			const res = await api.post<{ ok: boolean; latency_ms?: number; error?: string }>(
				'/v1/settings/llm/test',
				{ endpoint: endpoint.trim(), model: model.trim(), api_key: apiKey || undefined }
			);
			if (res.ok) toast.success(`Connection OK (${res.latency_ms} ms)`);
			else toast.error(`Test failed: ${res.error}`);
		} catch (e: any) {
			toast.error(e.message || 'Test request failed');
		} finally {
			testing = false;
		}
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !saving)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">Admin access required.</p>
{:else if loading}
	<p class="py-8 text-sm text-muted-foreground">Loading…</p>
{:else}
	<div class="max-w-lg space-y-4">
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">AI assistant</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">
						Powers conversational booking on your booking pages. Bring your own
						model — any OpenAI-compatible endpoint (a hosted model, or one you run
						yourself). Off by default; if it's unavailable, bookers just use the calendar.
					</p>
				</div>
				{#if settings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {settings.active ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {settings.active ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{settings.active ? 'Active' : settings.configured ? 'Configured (off)' : 'Not configured'}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="ai-endpoint">API endpoint</Label>
					<Input id="ai-endpoint" type="text" placeholder="https://api.openai.com/v1" bind:value={endpoint} />
					<p class="text-xs text-muted-foreground">OpenAI-compatible base URL; the client appends <code class="rounded bg-muted px-1">/chat/completions</code>.</p>
				</div>
				<div class="space-y-1.5">
					<Label for="ai-model">Model</Label>
					<Input id="ai-model" type="text" placeholder="gpt-4o-mini / claude-haiku-4-5 / gemini-flash …" bind:value={model} />
				</div>
				<div class="space-y-1.5">
					<Label for="ai-key">API key</Label>
					<Input id="ai-key" type="password"
						placeholder={settings?.api_key_set ? '•••••••• (stored)' : 'Enter API key (optional for local endpoints)'}
						bind:value={apiKey} />
					{#if settings?.api_key_set && !apiKey}
						<p class="text-xs text-muted-foreground">Stored — leave blank to keep it.</p>
					{/if}
				</div>

				<div class="flex items-center justify-between rounded-md border bg-muted/30 px-3 py-2">
					<div>
						<Label for="ai-enabled" class="text-sm">Enable AI features</Label>
						<p class="text-xs text-muted-foreground">Turn conversational booking on for your booking pages.</p>
					</div>
					<Switch id="ai-enabled" bind:checked={enabled} />
				</div>
			</div>

			<div class="mt-5 flex gap-2">
				<Button onclick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
				<Button variant="outline" onclick={testConnection} disabled={testing}>
					{testing ? 'Testing…' : 'Test connection'}
				</Button>
			</div>
		</div>

		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">Assistant instructions</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">
				Extra guidance — tone, business context, do's and don'ts. Appended to the built-in
				instructions (which handle the booking flow + safety and can't be edited).
			</p>
			<div class="mt-3 space-y-1.5">
				<Label for="ai-extra">Additional instructions</Label>
				<Textarea id="ai-extra" rows={4} bind:value={extraInstructions}
					placeholder="e.g. Keep a warm, professional tone. We're a law firm — mention that consultations are confidential." />
			</div>
			{#if settings?.base_prompt}
				<details class="mt-4">
					<summary class="cursor-pointer text-xs font-medium text-muted-foreground">View built-in base instructions (read-only)</summary>
					<pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">{settings.base_prompt}</pre>
				</details>
			{/if}
			<div class="mt-5">
				<Button onclick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
			</div>
		</div>

		<p class="text-xs text-muted-foreground">
			Privacy: the assistant only ever receives computed availability windows and public
			event details — never your calendar event titles, attendees, or other private data.
			For strict data-residency needs, point the endpoint at inference you control.
		</p>
	</div>
{/if}
