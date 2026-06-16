export type User = {
	id: string;
	email: string;
	name: string;
	timezone: string;
	time_format: '12h' | '24h';
	week_start: number; // 0=Sunday, 1=Monday
	date_format: 'dmy' | 'mdy' | 'ymd';
	avatar_url?: string;
	is_admin: boolean;
	is_owner: boolean;
	role: 'owner' | 'admin' | 'member';
	notify_confirmation: boolean;
	notify_cancellation: boolean;
	notify_reschedule: boolean;
	notify_reminder: boolean;
	notify_host_booking: boolean;
	notify_host_cancel: boolean;
	notify_host_reschedule: boolean;
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
	max_active_bookings: number;
	created_at: string;
	msg_confirmation?: string;
	msg_cancellation?: string;
	msg_reschedule?: string;
	msg_reminder?: string;
	reminders: number[]; // hours_before values
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
	configured?: boolean;
	calendar_id?: string;
	provider?: string;
};

export type EmailSettings = {
	smtp_host: string;
	smtp_port: string;
	smtp_user: string;
	smtp_pass_set: boolean; // true when a password is stored; never returned directly
	smtp_tls: boolean;
	smtp_starttls: boolean;
	email_from: string;
	email_from_name: string;
	enabled: boolean;
};

export type GoogleSettings = {
	client_id: string;
	client_secret_set: boolean;
	configured: boolean;
};

export type TeamMember = {
	id: string;
	email: string;
	name: string;
	timezone: string;
	is_admin: boolean;
	is_owner: boolean;
	role: 'owner' | 'admin' | 'member';
	email_login: boolean;
	provider?: string;
	avatar_url?: string;
	created_at: string;
	archived: boolean;
	archived_at?: string;
};

export type UpcomingBooking = {
	id: string;
	start_at: string;
	end_at: string;
	event_type_name: string;
	event_type_slug: string;
	attendee_name: string;
	attendee_email: string;
};

export type Invite = {
	id: string;
	email: string;
	expires_at: string;
	created_by: string;
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
	reason: 'day_off' | 'out_of_office' | 'custom_hours';
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

	postForm: <T>(path: string, data: FormData) =>
		apiFetch<T>(path, { method: 'POST', body: data }),

	patch: <T>(path: string, body?: unknown) =>
		apiFetch<T>(path, { method: 'PATCH', body: body ? JSON.stringify(body) : undefined }),

	del: <T = null>(path: string) => apiFetch<T>(path, { method: 'DELETE' })
};
