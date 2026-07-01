<script lang="ts">
	import { page } from '$app/stores';
	import { Button } from '$lib/components/ui/button';
	import { toast } from 'svelte-sonner';

	let { slug }: { slug: string } = $props();

	// The widget derives its API base from the script's own origin, so the
	// instance origin is all the snippet needs.
	const embedOrigin = $derived($page.url.origin);
	const inlineSnippet = $derived(
		`<script src="${embedOrigin}/embed.js" async><\/script>\n<calnode-booking slug="${slug}"></calnode-booking>`
	);
	const popupSnippet = $derived(
		`<script src="${embedOrigin}/embed.js" async><\/script>\n<button data-calnode-popup="${slug}">Book a call</button>`
	);
	let copied = $state('');
	function copyEmbed(kind: string, text: string) {
		navigator.clipboard.writeText(text).then(() => {
			copied = kind;
			setTimeout(() => { if (copied === kind) copied = ''; }, 1500);
		}).catch(() => toast.error('Could not copy to clipboard'));
	}
</script>

<!-- Embed -->
<div class="mt-8">
	<h2 class="mb-1 text-sm font-semibold uppercase tracking-wider text-muted-foreground">Embed</h2>
	<p class="mb-3 text-sm text-muted-foreground">Drop this booking widget on your website — it renders inline, no iframe. Paste before <code>&lt;/body&gt;</code>.</p>
	<div class="space-y-5 rounded-lg border bg-card p-6">
		{#each [{ key: 'inline', label: 'Inline', hint: 'Embeds the booking calendar directly in the page.', code: inlineSnippet }, { key: 'popup', label: 'Popup button', hint: 'A button that opens the booking widget in a modal.', code: popupSnippet }] as snip}
			<div>
				<div class="mb-1.5 flex items-center justify-between">
					<div>
						<span class="text-sm font-medium">{snip.label}</span>
						<span class="ml-2 text-xs text-muted-foreground">{snip.hint}</span>
					</div>
					<Button variant="outline" size="sm" onclick={() => copyEmbed(snip.key, snip.code)}>
						{copied === snip.key ? 'Copied!' : 'Copy'}
					</Button>
				</div>
				<pre class="overflow-x-auto rounded-md bg-muted px-3 py-2.5 text-xs leading-relaxed"><code>{snip.code}</code></pre>
			</div>
		{/each}
		<p class="text-xs text-muted-foreground">The widget calls this instance's public booking API. To restrict which sites may embed it, set <code>EMBED_ALLOWED_ORIGINS</code>.</p>
	</div>
</div>
