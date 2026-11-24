// Remembers groups opened in this browser so people can find them again.
// Storage can be unavailable (private mode), so every access is guarded.
export type Recent = { id: string; name: string };
const KEY = 'chia.recent';

export function loadRecent(): Recent[] {
	try {
		const v = JSON.parse(localStorage.getItem(KEY) ?? '[]');
		return Array.isArray(v) ? v : [];
	} catch {
		return [];
	}
}

export function rememberGroup(id: string, name: string) {
	try {
		const list = [{ id, name }, ...loadRecent().filter((r) => r.id !== id)].slice(0, 8);
		localStorage.setItem(KEY, JSON.stringify(list));
	} catch {
		/* ignore */
	}
}
