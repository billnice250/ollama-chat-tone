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

		const meta = document.createElement('div');
		meta.className = 'user-meta';
		const name = document.createElement('strong');
		name.textContent = user.username;
		const status = document.createElement('span');
		status.textContent = user.isAdmin ? 'admin' : (user.approved ? 'approved' : 'pending approval');
		meta.append(name, status);

		const action = document.createElement('button');
		action.type = 'button';
		action.className = 'secondary-button';
		action.textContent = user.approved ? 'Approved' : 'Approve';
		action.disabled = user.approved;
		action.addEventListener('click', async () => {
			await fetchJSON(`/api/admin/users/${encodeURIComponent(user.username)}/approve`, { method: 'POST' });
			await load();
		});

		row.append(meta, action);
		return row;
	}));
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
	el.status.textContent = `${(data.users || []).length} users`;
}

load().catch((err) => showError(err.message));
