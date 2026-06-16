const el = {
	title: document.getElementById('admin-title'),
	status: document.getElementById('admin-status'),
	error: document.getElementById('admin-error'),
	users: document.getElementById('users-list'),
	settingsError: document.getElementById('settings-error'),
	settingsStatus: document.getElementById('settings-status'),
	ollamaForm: document.getElementById('ollama-settings-form'),
	saveSettingsBtn: document.getElementById('save-settings-btn'),
	certForm: document.getElementById('cert-upload-form'),
	uploadCertBtn: document.getElementById('upload-cert-btn'),
	removeCertBtn: document.getElementById('remove-cert-btn'),
	reloadConfigBtn: document.getElementById('reload-config'),
	mtlsStatus: document.getElementById('mtls-status'),
};

function showError(message) {
	el.error.textContent = message;
	el.error.classList.remove('hidden');
}

function showSettingsError(message) {
	el.settingsStatus.textContent = '';
	el.settingsStatus.classList.add('hidden');
	el.settingsError.textContent = message;
	el.settingsError.classList.remove('hidden');
}

function showSettingsStatus(message) {
	el.settingsError.textContent = '';
	el.settingsError.classList.add('hidden');
	el.settingsStatus.textContent = message;
	el.settingsStatus.classList.remove('hidden');
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
		const verified = user.emailVerified ? '' : ' (unverified)';
		status.textContent = user.isAdmin ? 'admin' : (user.approved ? `approved${verified}` : `pending approval${verified}`);
		const visit = document.createElement('span');
		visit.textContent = user.lastSeenAt
			? `${user.active ? 'active now' : 'last visit'}: ${formatDate(user.lastSeenAt)}`
			: 'last visit: never';
		meta.append(name, status, visit);

		const actions = document.createElement('div');
		actions.className = 'user-actions';

		const approveBtn = document.createElement('button');
		approveBtn.type = 'button';
		approveBtn.className = 'secondary-button';
		approveBtn.textContent = user.approved ? 'Revoke' : 'Approve';
		approveBtn.disabled = user.isAdmin;
		approveBtn.addEventListener('click', async () => {
			const endpoint = user.approved ? 'revoke' : 'approve';
			await fetchJSON(`/api/admin/users/${encodeURIComponent(user.username)}/${endpoint}`, { method: 'POST' });
			await loadUsers();
		});

		const clearBtn = document.createElement('button');
		clearBtn.type = 'button';
		clearBtn.className = 'secondary-button';
		clearBtn.textContent = 'Clear data';
		clearBtn.disabled = user.isAdmin;
		clearBtn.title = 'Delete all chat history for this user';
		clearBtn.addEventListener('click', async () => {
			if (!confirm(`Delete all chat data for "${user.username}"?`)) return;
			await fetchJSON(`/api/admin/users/${encodeURIComponent(user.username)}/delete-data`, { method: 'POST' });
			await loadUsers();
		});

		const deleteBtn = document.createElement('button');
		deleteBtn.type = 'button';
		deleteBtn.className = 'secondary-button';
		deleteBtn.textContent = 'Delete user';
		deleteBtn.disabled = user.isAdmin;
		deleteBtn.title = 'Permanently delete this user and all their data';
		deleteBtn.addEventListener('click', async () => {
			if (!confirm(`Permanently delete user "${user.username}" and all their data?`)) return;
			await fetchJSON(`/api/admin/users/${encodeURIComponent(user.username)}/delete`, { method: 'POST' });
			await loadUsers();
		});

		actions.append(approveBtn, clearBtn, deleteBtn);
		row.append(meta, actions);
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

async function loadUsers() {
	const data = await fetchJSON('/api/admin/users');
	const users = data.users || [];
	const active = users.filter((user) => user.active).length;
	el.status.textContent = `${users.length} users, ${active} active`;
	renderUsers(users);
}

async function loadSettings() {
	const data = await fetchJSON('/api/admin/settings');
	const s = data.settings || {};
	document.getElementById('ollama-url').value = s.ollamaURL || '';
	document.getElementById('ollama-timeout').value = s.ollamaTimeout || '';
	document.getElementById('default-model').value = s.defaultModel || '';
	el.mtlsStatus.textContent = s.mtlsEnabled ? '✓ mTLS certificate is active' : 'No mTLS certificate uploaded';
	el.removeCertBtn.disabled = !s.mtlsEnabled;
}

el.ollamaForm.addEventListener('submit', async (e) => {
	e.preventDefault();
	el.settingsError.classList.add('hidden');
	el.settingsStatus.classList.add('hidden');
	el.saveSettingsBtn.disabled = true;
	const fd = new FormData(el.ollamaForm);
	const body = {};
	for (const [k, v] of fd.entries()) { if (v) body[k] = v; }
	try {
		const data = await fetchJSON('/api/admin/settings', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify(body),
		});
		await loadSettings();
		const warnings = data.warnings && data.warnings.length ? ` (${data.warnings.join(', ')})` : '';
		showSettingsStatus(`Settings saved${warnings}`);
	} catch (err) {
		showSettingsError(err.message);
	} finally {
		el.saveSettingsBtn.disabled = false;
	}
});

el.certForm.addEventListener('submit', async (e) => {
	e.preventDefault();
	el.settingsError.classList.add('hidden');
	el.settingsStatus.classList.add('hidden');
	el.uploadCertBtn.disabled = true;
	el.removeCertBtn.disabled = true;
	const fd = new FormData(el.certForm);
	try {
		await fetchJSON('/api/admin/settings/ollama-cert', { method: 'POST', body: fd });
		await loadSettings();
		el.certForm.reset();
		showSettingsStatus('Certificate uploaded');
	} catch (err) {
		showSettingsError(err.message);
	} finally {
		el.uploadCertBtn.disabled = false;
	}
});

el.removeCertBtn.addEventListener('click', async () => {
	if (!confirm('Remove the mTLS certificate?')) return;
	el.settingsError.classList.add('hidden');
	el.settingsStatus.classList.add('hidden');
	el.removeCertBtn.disabled = true;
	try {
		await fetchJSON('/api/admin/settings/ollama-cert', { method: 'DELETE' });
		await loadSettings();
		showSettingsStatus('Certificate removed');
	} catch (err) {
		showSettingsError(err.message);
	} finally {
		el.removeCertBtn.disabled = false;
	}
});

el.reloadConfigBtn.addEventListener('click', async () => {
	el.settingsError.classList.add('hidden');
	el.settingsStatus.classList.add('hidden');
	el.reloadConfigBtn.disabled = true;
	try {
		const data = await fetchJSON('/api/config/reload', { method: 'POST' });
		await Promise.all([loadUsers(), loadSettings()]);
		const warnings = data.warnings && data.warnings.length ? ` (${data.warnings.join(', ')})` : '';
		showSettingsStatus(`Config reloaded${warnings}`);
	} catch (err) {
		showSettingsError(`Could not reload config: ${err.message}`);
	} finally {
		el.reloadConfigBtn.disabled = false;
	}
});

async function load() {
	const cfg = await fetchJSON('/api/config');
	document.title = `${cfg.appName} Admin`;
	el.title.textContent = `${cfg.appName} Admin`;
	if (!cfg.isAdmin) {
		showError('admin access required');
		return;
	}
	await Promise.all([loadUsers(), loadSettings()]);
}

load().catch((err) => showError(err.message));
