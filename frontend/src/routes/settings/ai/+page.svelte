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
	import { createAsyncFlag } from '$lib/async-action.svelte';
	import { t, tr } from '$lib/i18n';

	const loadingFlag = createAsyncFlag(true);
	const savingFlag = createAsyncFlag();
	const testingFlag = createAsyncFlag();

	let settings = $state<LLMSettings | null>(null);
	let enabled = $state(false);
	let endpoint = $state('');
	let model = $state('');
	let apiKey = $state('');
	let extraInstructions = $state('');

	onMount(() => loadingFlag.run(async () => {
		settings = await api.get<LLMSettings>('/v1/settings/llm');
		enabled = settings.enabled;
		endpoint = settings.endpoint;
		model = settings.model;
		extraInstructions = settings.extra_instructions;
	}, tr('settings.ai.error.load')));

	async function save() {
		await savingFlag.run(async () => {
			const body: Record<string, unknown> = {
				enabled, endpoint: endpoint.trim(), model: model.trim(),
				extra_instructions: extraInstructions
			};
			if (apiKey) body.api_key = apiKey;
			settings = await api.patch<LLMSettings>('/v1/settings/llm', body);
			enabled = settings.enabled;
			extraInstructions = settings.extra_instructions;
			apiKey = '';
			toast.success(settings.active ? tr('settings.ai.toast.savedOn') : tr('settings.ai.toast.saved'));
		}, tr('settings.ai.error.save'));
	}

	async function testConnection() {
		if (!endpoint.trim() || !model.trim()) {
			toast.error(tr('settings.ai.toast.needEndpointModel'));
			return;
		}
		await testingFlag.run(async () => {
			const res = await api.post<{ ok: boolean; latency_ms?: number; error?: string }>(
				'/v1/settings/llm/test',
				{ endpoint: endpoint.trim(), model: model.trim(), api_key: apiKey || undefined }
			);
			if (res.ok) toast.success(tr('settings.ai.toast.testOk', { ms: res.latency_ms ?? 0 }));
			else toast.error(tr('settings.ai.toast.testFailed', { error: res.error ?? '' }));
		}, tr('settings.ai.error.test'));
	}
</script>

<svelte:window onkeydown={saveOnCmdS(save, () => !savingFlag.active)} />

{#if !$currentUser?.is_admin}
	<p class="text-sm text-muted-foreground">{$t('common.adminRequired')}</p>
{:else if loadingFlag.active}
	<p class="py-8 text-sm text-muted-foreground">{$t('common.loading')}</p>
{:else}
	<div class="max-w-lg space-y-4">
		<div class="rounded-lg border bg-card p-6">
			<div class="mb-4 flex items-start justify-between gap-2">
				<div>
					<h2 class="text-sm font-semibold">{$t('settings.ai.assistant.title')}</h2>
					<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.ai.assistant.subtitle')}</p>
				</div>
				{#if settings !== null}
					<span class="flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium {settings.active ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'}">
						<span class="h-1.5 w-1.5 rounded-full {settings.active ? 'bg-green-500' : 'bg-amber-400'}"></span>
						{settings.active ? $t('settings.ai.status.active') : settings.configured ? $t('settings.ai.status.configuredOff') : $t('common.notConfigured')}
					</span>
				{/if}
			</div>

			<div class="space-y-3">
				<div class="space-y-1.5">
					<Label for="ai-endpoint">{$t('settings.ai.endpoint.label')}</Label>
					<Input id="ai-endpoint" type="text" placeholder={$t('settings.ai.endpoint.placeholder')} bind:value={endpoint} />
					<p class="text-xs text-muted-foreground">{$t('settings.ai.endpoint.hint')}</p>
				</div>
				<div class="space-y-1.5">
					<Label for="ai-model">{$t('settings.ai.model.label')}</Label>
					<Input id="ai-model" type="text" placeholder={$t('settings.ai.model.placeholder')} bind:value={model} />
				</div>
				<div class="space-y-1.5">
					<Label for="ai-key">{$t('settings.ai.apiKey.label')}</Label>
					<Input id="ai-key" type="password"
						placeholder={settings?.api_key_set ? $t('common.storedSecretPlaceholder') : $t('settings.ai.apiKey.placeholder')}
						bind:value={apiKey} />
					{#if settings?.api_key_set && !apiKey}
						<p class="text-xs text-muted-foreground">{$t('common.leaveBlankKeep')}</p>
					{/if}
				</div>

				<div class="flex items-center justify-between rounded-md border bg-muted/30 px-3 py-2">
					<div>
						<Label for="ai-enabled" class="text-sm">{$t('settings.ai.enable.label')}</Label>
						<p class="text-xs text-muted-foreground">{$t('settings.ai.enable.hint')}</p>
					</div>
					<Switch id="ai-enabled" bind:checked={enabled} />
				</div>
			</div>

			<div class="mt-5 flex gap-2">
				<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? $t('common.saving') : $t('common.save')}</Button>
				<Button variant="outline" onclick={testConnection} disabled={testingFlag.active}>
					{testingFlag.active ? $t('common.testing') : $t('settings.ai.testConnection')}
				</Button>
			</div>
		</div>

		<div class="rounded-lg border bg-card p-6">
			<h2 class="text-sm font-semibold">{$t('settings.ai.instructions.title')}</h2>
			<p class="mt-0.5 text-xs text-muted-foreground">{$t('settings.ai.instructions.subtitle')}</p>
			<div class="mt-3 space-y-1.5">
				<Label for="ai-extra">{$t('settings.ai.instructions.label')}</Label>
				<Textarea id="ai-extra" rows={4} bind:value={extraInstructions}
					placeholder={$t('settings.ai.instructions.placeholder')} />
				<p class="text-xs text-muted-foreground">{$t('settings.ai.instructions.languageHint')}</p>
			</div>
			{#if settings?.base_prompt}
				<details class="mt-4">
					<summary class="cursor-pointer text-xs font-medium text-muted-foreground">{$t('settings.ai.basePrompt.summary')}</summary>
					<pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">{settings.base_prompt}</pre>
				</details>
			{/if}
			<div class="mt-5">
				<Button onclick={save} disabled={savingFlag.active}>{savingFlag.active ? $t('common.saving') : $t('common.save')}</Button>
			</div>
		</div>

		<p class="text-xs text-muted-foreground">{$t('settings.ai.privacy')}</p>
	</div>
{/if}
