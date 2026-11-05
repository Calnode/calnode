<script lang="ts">
	import { page } from '$app/stores';
	import { Button } from '$lib/components/ui/button';
	import { toast } from 'svelte-sonner';
	import { t, tr } from '$lib/i18n';

	let { slug }: { slug: string } = $props();

	// The widget derives its API base from the script's own origin, so the
	// instance origin is all the snippet needs.
	const embedOrigin = $derived($page.url.origin);
	const inlineSnippet = $derived(
		`<script src="${embedOrigin}/embed.js" async><\/script>\n<calnode-booking slug="${slug}"></calnode-booking>`
	);
	const popupSnippet = $derived(
		`<script src="${embedOrigin}/embed.js" async><\/script>\n<button data-calnode-popup="${slug}">${$t('eventTypeEditor.embed.buttonText')}</button>`
	);
	let copied = $state('');
	function copyEmbed(kind: string, text: string) {
		navigator.clipboard.writeText(text).then(() => {
			copied = kind;
			setTimeout(() => { if (copied === kind) copied = ''; }, 1500);
		}).catch(() => toast.error(tr('common.copyClipboardError')));
	}

	const snippets = $derived([
		{ key: 'inline', label: $t('eventTypeEditor.embed.inline.label'), hint: $t('eventTypeEditor.embed.inline.hint'), code: inlineSnippet },
		{ key: 'popup', label: $t('eventTypeEditor.embed.popup.label'), hint: $t('eventTypeEditor.embed.popup.hint'), code: popupSnippet },
	]);
</script>

<!-- Embed -->
<div class="mt-8">
	<h2 class="mb-1 text-sm font-semibold uppercase tracking-wider text-muted-foreground">{$t('eventTypeEditor.embed.title')}</h2>
	<p class="mb-3 text-sm text-muted-foreground">{$t('eventTypeEditor.embed.subtitle')}</p>
	<div class="space-y-5 rounded-lg border bg-card p-6">
		{#each snippets as snip}
			<div>
				<div class="mb-1.5 flex items-center justify-between">
					<div>
						<span class="text-sm font-medium">{snip.label}</span>
						<span class="ml-2 text-xs text-muted-foreground">{snip.hint}</span>
					</div>
					<Button variant="outline" size="sm" onclick={() => copyEmbed(snip.key, snip.code)}>
						{copied === snip.key ? $t('common.copied') : $t('common.copy')}
					</Button>
				</div>
				<pre class="overflow-x-auto rounded-md bg-muted px-3 py-2.5 text-xs leading-relaxed"><code>{snip.code}</code></pre>
			</div>
		{/each}
		<p class="text-xs text-muted-foreground">{$t('eventTypeEditor.embed.footer')}</p>
	</div>
</div>
