export type ThemeMode = 'light' | 'dark' | 'system';

const STORAGE_KEY = 'fp-theme';
const DARK_QUERY = '(prefers-color-scheme: dark)';

function readStoredMode(): ThemeMode {
	if (typeof window === 'undefined') return 'system';
	try {
		const raw = window.localStorage.getItem(STORAGE_KEY);
		if (raw === 'light' || raw === 'dark' || raw === 'system') return raw;
	} catch {
		// Ignore storage access errors.
	}
	return 'system';
}

function readSystemDark(): boolean {
	if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
	return window.matchMedia(DARK_QUERY).matches;
}

class ThemeStore {
	mode = $state<ThemeMode>(readStoredMode());
	systemDark = $state<boolean>(readSystemDark());
	resolved = $derived<'light' | 'dark'>(
		this.mode === 'system' ? (this.systemDark ? 'dark' : 'light') : this.mode
	);

	private initialized = false;
	private mediaCleanup: (() => void) | null = null;

	init() {
		if (typeof window === 'undefined') return;
		this.mode = readStoredMode();
		this.systemDark = readSystemDark();
		this.apply();

		if (this.initialized || typeof window.matchMedia !== 'function') return;
		this.initialized = true;

		const mq = window.matchMedia(DARK_QUERY);
		const onChange = (e: MediaQueryListEvent) => {
			this.systemDark = e.matches;
			this.apply();
		};
		mq.addEventListener('change', onChange);

		const onStorage = (e: StorageEvent) => {
			if (e.key === STORAGE_KEY) {
				this.mode = readStoredMode();
				this.apply();
			}
		};
		window.addEventListener('storage', onStorage);

		this.mediaCleanup = () => {
			mq.removeEventListener('change', onChange);
			window.removeEventListener('storage', onStorage);
		};
	}

	destroy() {
		this.mediaCleanup?.();
		this.mediaCleanup = null;
		this.initialized = false;
	}

	set(next: ThemeMode) {
		this.mode = next;
		if (typeof window !== 'undefined') {
			try {
				window.localStorage.setItem(STORAGE_KEY, next);
			} catch {
				// Ignore storage access errors.
			}
		}
		this.apply();
	}

	apply() {
		if (typeof document === 'undefined') return;
		const isDark = this.resolved === 'dark';
		const root = document.documentElement;
		root.classList.toggle('dark', isDark);
		root.style.colorScheme = isDark ? 'dark' : 'light';
	}
}

export const theme = new ThemeStore();
