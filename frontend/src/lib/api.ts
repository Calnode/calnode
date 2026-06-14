export type User = {
	id: string;
	email: string;
	name: string;
	timezone: string;
	time_format: '12h' | '24h';
	week_start: number; // 0=Sunday, 1=Monday
	date_format: 'dmy' | 'mdy' | 'ymd';
	is_admin: boolean;
};

export type EventType = {
	id: string;
	slug: string;
	name: string;
	description?: string;
	duration_minutes: number;
	is_active: boolean;
	is_public: boolean;
	location_type: string;
	location_value?: string;
	buffer_before_minutes: number;
	buffer_after_minutes: number;
	min_notice_minutes: number;
	max_future_days: number;
	created_at: string;
};

export type Question = {
	id: string;
	event_type_id: string;
	label: string;
	type: 'text' | 'select' | 'checkbox';
	options?: string[];
	required: boolean;
	position: number;
};

export type Booking = {
	id: string;
	event_type_slug: string;
	start_at: string;
	end_at: string;
	status: 'confirmed' | 'cancelled';
	attendees: { name: string; email: string }[];
	created_at: string;
};

export type APIKey = {
	id: string;
	name: string;
	created_at: string;
	last_used_at?: string;
};

export type Webhook = {
	id: string;
	url: string;
	events: string[];
	is_active: boolean;
	created_at: string;
};

export type CalendarStatus = {
	connected: boolean;
	calendar_id?: string;
	provider?: string;
};

export type AvailabilityRule = {
	id: string;
	event_type_id: string | null;
	day_of_week: number;
	start_time: string;
	end_time: string;
};

export type AvailabilityOverride = {
	id: string;
	date: string;
	is_available: boolean;
	start_time: string | null;
	end_time: string | null;
};

async function apiFetch<T>(path: string, opts: RequestInit = {}): Promise<T> {
	const res = await fetch(path, {
		credentials: 'same-origin',
		headers: {
			...(opts.body && typeof opts.body === 'string'
				? { 'Content-Type': 'application/json' }
				: {}),
			...((opts.headers as Record<string, string>) ?? {})
		},
		...opts
	});

	if (res.status === 401) {
		window.location.href = '/admin/login';
		throw new Error('unauthenticated');
	}

	if (res.status === 204) return null as T;

	const data = await res.json().catch(() => ({ error: res.statusText }));
	if (!res.ok) throw new Error(data.error ?? `HTTP ${res.status}`);
	return data as T;
}

export const api = {
	get: <T>(path: string) => apiFetch<T>(path),

	post: <T>(path: string, body?: unknown) =>
		apiFetch<T>(path, { method: 'POST', body: body ? JSON.stringify(body) : undefined }),

	patch: <T>(path: string, body?: unknown) =>
		apiFetch<T>(path, { method: 'PATCH', body: body ? JSON.stringify(body) : undefined }),

	del: <T = null>(path: string) => apiFetch<T>(path, { method: 'DELETE' })
};
