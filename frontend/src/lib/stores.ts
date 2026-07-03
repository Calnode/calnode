import { writable } from 'svelte/store';
import type { User } from './api';

export const currentUser = writable<User | null>(null);

export type AuthStatus = {
	demo_mode: boolean;
	next_reset_at?: string; // RFC3339; only present when demo_mode is true
};

export const authStatus = writable<AuthStatus>({ demo_mode: false });
