const el = {
	title: document.getElementById('admin-title'),
	status: document.getElementById('admin-status'),
	error: document.getElementById('admin-error'),
	users: document.getElementById('users-list'),
};

function showError(message) {
	el.error.textContent = message;
	el.error.classList.remove('hidden');
}

async function fetchJSON(url, options = {}) {
	const response = await fetch(url, options);
	const text = await response.text();
	let data = {};
	if (text) {
		try {
			data = JSON.parse(text);
		} catch {
			data = { error: text };
		}
	}
	if (!response.ok) {
		throw new Error(data.error || `${response.status} ${response.statusText}`);
	}
	return data;
}

function renderUsers(users) {
	el.users.replaceChildren(...users.map((user) => {
		const row = document.createElement('article');
		row.className = 'user-row';
		row.classList.toggle('active-user', Boolean(user.active));

		const meta = document.createElement('div');
		meta.className = 'user-meta';
		const name = document.createElement('strong');
		name.textContent = user.username;
		const status = document.createElement('span');
		status.textContent = user.isAdmin ? 'admin' : (user.approved ? 'approved' : 'pending approval');
		const visit = document.createElement('span');
		visit.textContent = user.lastSeenAt
			? `${user.active ? 'active now' : 'last visit'}: ${formatDate(user.lastSeenAt)}`
			: 'last visit: never';
		meta.append(name, status, visit);

		const action = document.createElement('button');
		action.type = 'button';
		action.className = 'secondary-button';
		action.textContent = user.approved ? 'Revoke' : 'Approve';
		action.disabled = user.isAdmin;
		action.addEventListener('click', async () => {
			const endpoint = user.approved ? 'revoke' : 'approve';
			await fetchJSON(`/api/admin/users/${encodeURIComponent(user.username)}/${endpoint}`, { method: 'POST' });
			await load();
		});

		row.append(meta, action);
		return row;
	}));
}

function formatDate(value) {
	const normalized = value.includes('T') ? value : value.replace(' ', 'T') + 'Z';
	const date = new Date(normalized);
	if (Number.isNaN(date.getTime())) {
		return value;
	}
	return date.toLocaleString();
}

async function load() {
	const cfg = await fetchJSON('/api/config');
	document.title = `${cfg.appName} Admin`;
	el.title.textContent = `${cfg.appName} Admin`;
	if (!cfg.isAdmin) {
		showError('admin access required');
		return;
	}

	const data = await fetchJSON('/api/admin/users');
	renderUsers(data.users || []);
	const users = data.users || [];
	const active = users.filter((user) => user.active).length;
	el.status.textContent = `${users.length} users, ${active} active`;
}

load().catch((err) => showError(err.message));
