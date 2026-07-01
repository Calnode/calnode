import { toast } from 'svelte-sonner';

// createAsyncFlag tracks whether an async action is in flight and standardizes the
// try/toast-on-error/finally shape every settings page's load() and save() repeated by
// hand. Business logic (what to fetch, what body to send, how to populate local fields)
// stays in the caller's fn — this only shares the boilerplate around it.
export function createAsyncFlag(initial = false) {
	let active = $state(initial);

	async function run<T>(fn: () => Promise<T>, fallbackMessage: string): Promise<T | undefined> {
		active = true;
		try {
			return await fn();
		} catch (e: any) {
			toast.error(e.message || fallbackMessage);
			return undefined;
		} finally {
			active = false;
		}
	}

	return {
		get active() {
			return active;
		},
		run,
	};
}
