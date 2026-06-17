const localStorageKey = 'ollama-chat-tone.chats.v1';

const el = {
	appName: document.getElementById('app-name'),
	appStatus: document.getElementById('app-status'),
	appVersion: document.getElementById('app-version'),
	currentUser: document.getElementById('current-user'),
	sidebar: document.querySelector('.sidebar'),
	sidebarToggle: document.getElementById('sidebar-toggle'),
	menuBackdrop: document.getElementById('menu-backdrop'),
	chatList: document.getElementById('chat-list'),
	chatTitle: document.getElementById('chat-title'),
	chatMeta: document.getElementById('chat-meta'),
	messages: document.getElementById('messages'),
	model: document.getElementById('model'),
	thinkToggle: document.getElementById('think-toggle'),
	refreshModels: document.getElementById('refresh-models'),
	adminLink: document.getElementById('admin-link'),
	error: document.getElementById('error'),
	prompt: document.getElementById('prompt'),
	send: document.getElementById('send'),
	composer: document.getElementById('composer'),
	newChat: document.getElementById('new-chat'),
	deleteAccount: document.getElementById('delete-account'),
	deleteAccountModal: document.getElementById('delete-account-modal'),
	cancelDeleteAccount: document.getElementById('cancel-delete-account'),
	confirmDeleteAccount: document.getElementById('confirm-delete-account'),
};

const state = {
	appName: 'Ollama Chat Tone',
	defaultModel: 'llama3.2',
	authMode: 'none',
	storageMode: 'local',
	currentUser: 'anonymous',
	isAdmin: false,
	activeChatId: '',
	chats: [],
	streamController: null,
	activeJob: null,
	jobEventSource: null,
};

function showError(message) {
	el.error.textContent = message;
	el.error.classList.remove('hidden');
}

function clearError() {
	el.error.textContent = '';
	el.error.classList.add('hidden');
}

function selectedModel() {
	return el.model.value || state.defaultModel;
}

function titleFrom(content) {
	return content.replace(/\s+/g, ' ').trim().slice(0, 52) || 'New chat';
}

function activeChat() {
	return state.chats.find((chat) => chat.id === state.activeChatId);
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

function render() {
	if (state.chats.length === 0) {
		renderEmptyShell();
		return;
	}

	const chat = activeChat() || state.chats[0];
	state.activeChatId = chat.id;

	el.chatTitle.textContent = chat.title;
	el.chatMeta.textContent = chat.messages && chat.messages.length > 0
		? `${chat.messages.length} messages`
		: 'No messages yet';

	renderChatList();
	renderMessages(chat);
}

function renderEmptyShell() {
	el.chatTitle.textContent = 'New chat';
	el.chatMeta.textContent = 'No messages yet';
	el.chatList.replaceChildren();
	el.messages.replaceChildren(emptyState());
}

function renderChatList() {
	const items = state.chats.map((chat) => {
		const item = document.createElement('div');
		item.className = `chat-item${chat.id === state.activeChatId ? ' active' : ''}`;

		const selectButton = document.createElement('button');
		selectButton.type = 'button';
		selectButton.className = 'chat-select';
		selectButton.innerHTML = '<span class="chat-title"></span><span class="chat-count"></span>';
		selectButton.querySelector('.chat-title').textContent = chat.title;
		selectButton.querySelector('.chat-count').textContent = String(chat.messageCount || (chat.messages || []).length);
		selectButton.addEventListener('click', () => selectChat(chat.id));

		const deleteButton = document.createElement('button');
		deleteButton.type = 'button';
		deleteButton.className = 'chat-delete';
		deleteButton.textContent = 'Delete';
		deleteButton.addEventListener('click', (event) => {
			event.stopPropagation();
			deleteChat(chat.id).catch((err) => showError(err.message));
		});

		item.append(selectButton, deleteButton);
		return item;
	});
	el.chatList.replaceChildren(...items);
}

function renderMessages(chat) {
	const messages = chat.messages || [];
	el.messages.replaceChildren();
	if (messages.length === 0) {
		el.messages.append(emptyState());
		return;
	}

	for (const message of messages) {
		el.messages.append(messageNode(message));
	}
	el.messages.scrollTop = el.messages.scrollHeight;
}

function emptyState() {
	const empty = document.createElement('div');
	empty.className = 'empty';
	empty.innerHTML = '<div><strong>Start a chat</strong><span>Pick a model and send your first message.</span></div>';
	return empty;
}

function messageNode(message) {
	const row = document.createElement('article');
	row.className = `message ${message.role === 'user' ? 'user' : 'assistant'}`;
	if (message.pending) {
		row.classList.add('pending');
	}

	const bubble = document.createElement('div');
	bubble.className = 'bubble';

	const role = document.createElement('span');
	role.className = 'role';
	role.textContent = message.role === 'user' ? 'You' : (message.model || selectedModel());
	bubble.append(role);

	if (message.thinking) {
		const thinking = document.createElement('details');
		thinking.className = 'thinking';
		thinking.open = Boolean(message.thinkingOpen);
		thinking.addEventListener('toggle', () => {
			message.thinkingOpen = thinking.open;
		});
		const label = document.createElement('summary');
		label.className = 'thinking-label';
		label.textContent = message.pending ? 'thinking' : 'thinking log';
		const text = document.createElement('span');
		text.className = 'thinking-content';
		text.textContent = message.thinking;
		thinking.append(label, text);
		bubble.append(thinking);
	}

	const content = document.createElement('div');
	content.className = 'markdown-body';
	if (message.pending && !message.content) {
		const thinking = document.createElement('span');
		thinking.className = 'thinking-animation';
		thinking.textContent = 'Thinking';
		content.append(thinking);
	} else {
		content.append(...renderMarkdown(message.content || ''));
	}
	bubble.append(content);
	row.append(bubble);
	return row;
}

function renderMarkdown(markdown) {
	const lines = markdown.replace(/\r\n/g, '\n').split('\n');
	const nodes = [];
	let i = 0;

	while (i < lines.length) {
		const line = lines[i];
		const trimmed = line.trim();

		if (trimmed === '') {
			i++;
			continue;
		}

		const fence = trimmed.match(/^```(\w+)?\s*$/);
		if (fence) {
			const codeLines = [];
			i++;
			while (i < lines.length && !lines[i].trim().startsWith('```')) {
				codeLines.push(lines[i]);
				i++;
			}
			if (i < lines.length) {
				i++;
			}
			nodes.push(codeBlock(codeLines.join('\n'), fence[1] || ''));
			continue;
		}

		const heading = trimmed.match(/^(#{1,3})\s+(.+)$/);
		if (heading) {
			const h = document.createElement(`h${heading[1].length + 2}`);
			h.append(...renderInline(heading[2]));
			nodes.push(h);
			i++;
			continue;
		}

		if (/^[-*]\s+/.test(trimmed)) {
			const list = document.createElement('ul');
			while (i < lines.length && /^[-*]\s+/.test(lines[i].trim())) {
				const item = document.createElement('li');
				item.append(...renderInline(lines[i].trim().replace(/^[-*]\s+/, '')));
				list.append(item);
				i++;
			}
			nodes.push(list);
			continue;
		}

		if (/^\d+\.\s+/.test(trimmed)) {
			const list = document.createElement('ol');
			while (i < lines.length && /^\d+\.\s+/.test(lines[i].trim())) {
				const item = document.createElement('li');
				item.append(...renderInline(lines[i].trim().replace(/^\d+\.\s+/, '')));
				list.append(item);
				i++;
			}
			nodes.push(list);
			continue;
		}

		if (isTableStart(lines, i)) {
			const tableLines = [lines[i], lines[i + 1]];
			i += 2;
			while (i < lines.length && isTableRow(lines[i])) {
				tableLines.push(lines[i]);
				i++;
			}
			nodes.push(tableNode(tableLines));
			continue;
		}

		const paragraph = [];
		while (i < lines.length && lines[i].trim() !== '' && !isBlockStart(lines[i].trim())) {
			paragraph.push(lines[i].trim());
			i++;
		}
		const p = document.createElement('p');
		p.append(...renderInline(paragraph.join(' ')));
		nodes.push(p);
	}

	return nodes.length > 0 ? nodes : [document.createTextNode('')];
}

function isBlockStart(line) {
	return /^```/.test(line) || /^(#{1,3})\s+/.test(line) || /^[-*]\s+/.test(line) || /^\d+\.\s+/.test(line) || isTableRow(line);
}

function codeBlock(code, language) {
	const pre = document.createElement('pre');
	const codeEl = document.createElement('code');
	if (language) {
		codeEl.dataset.language = language;
	}
	codeEl.textContent = code;
	pre.append(codeEl);
	return pre;
}

function isTableStart(lines, index) {
	return isTableRow(lines[index]) && index + 1 < lines.length && isTableDivider(lines[index + 1]);
}

function isTableRow(line) {
	const trimmed = line.trim();
	return trimmed.includes('|') && splitTableRow(trimmed).length >= 2;
}

function isTableDivider(line) {
	const cells = splitTableRow(line);
	return cells.length >= 2 && cells.every((cell) => /^:?-{3,}:?$/.test(cell.trim()));
}

function splitTableRow(line) {
	let trimmed = line.trim();
	if (trimmed.startsWith('|')) {
		trimmed = trimmed.slice(1);
	}
	if (trimmed.endsWith('|')) {
		trimmed = trimmed.slice(0, -1);
	}
	return trimmed.split('|').map((cell) => cell.trim());
}

function tableNode(lines) {
	const table = document.createElement('table');
	const thead = document.createElement('thead');
	const tbody = document.createElement('tbody');
	const headerCells = splitTableRow(lines[0]);
	const alignments = splitTableRow(lines[1]).map((cell) => {
		if (cell.startsWith(':') && cell.endsWith(':')) {
			return 'center';
		}
		if (cell.endsWith(':')) {
			return 'right';
		}
		return 'left';
	});

	const headerRow = document.createElement('tr');
	for (let i = 0; i < headerCells.length; i++) {
		const th = document.createElement('th');
		th.style.textAlign = alignments[i] || 'left';
		th.append(...renderInline(headerCells[i]));
		headerRow.append(th);
	}
	thead.append(headerRow);

	for (const line of lines.slice(2)) {
		const tr = document.createElement('tr');
		const cells = splitTableRow(line);
		for (let i = 0; i < headerCells.length; i++) {
			const td = document.createElement('td');
			td.style.textAlign = alignments[i] || 'left';
			td.append(...renderInline(cells[i] || ''));
			tr.append(td);
		}
		tbody.append(tr);
	}

	table.append(thead, tbody);
	return table;
}

function renderInline(text) {
	const nodes = [];
	const pattern = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|\[[^\]]+\]\([^)]+\))/g;
	let lastIndex = 0;
	let match;

	while ((match = pattern.exec(text)) !== null) {
		if (match.index > lastIndex) {
			nodes.push(document.createTextNode(text.slice(lastIndex, match.index)));
		}
		nodes.push(inlineNode(match[0]));
		lastIndex = pattern.lastIndex;
	}

	if (lastIndex < text.length) {
		nodes.push(document.createTextNode(text.slice(lastIndex)));
	}
	return nodes;
}

function inlineNode(token) {
	if (token.startsWith('`') && token.endsWith('`')) {
		const code = document.createElement('code');
		code.textContent = token.slice(1, -1);
		return code;
	}
	if (token.startsWith('**') && token.endsWith('**')) {
		const strong = document.createElement('strong');
		strong.append(...renderInline(token.slice(2, -2)));
		return strong;
	}
	if (token.startsWith('*') && token.endsWith('*')) {
		const em = document.createElement('em');
		em.append(...renderInline(token.slice(1, -1)));
		return em;
	}

	const link = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/);
	if (link) {
		const a = document.createElement('a');
		a.textContent = link[1];
		a.href = safeHref(link[2]);
		a.target = '_blank';
		a.rel = 'noopener noreferrer';
		return a;
	}

	return document.createTextNode(token);
}

function safeHref(href) {
	try {
		const url = new URL(href, window.location.origin);
		if (url.protocol === 'http:' || url.protocol === 'https:' || url.protocol === 'mailto:') {
			return url.href;
		}
	} catch {}
	return '#';
}

function normalizeConversation(conversation) {
	return {
		id: conversation.id,
		title: conversation.title || 'New chat',
		createdAt: conversation.createdAt || Date.now(),
		updatedAt: conversation.updatedAt || conversation.createdAt || Date.now(),
		messageCount: conversation.messageCount || (conversation.messages || []).length,
		messages: (conversation.messages || []).map(normalizeMessage),
	};
}

function normalizeMessage(message) {
	return {
		id: message.id,
		role: message.role,
		content: message.content || '',
		thinking: message.thinking || '',
		model: message.model || '',
		createdAt: message.createdAt || Date.now(),
	};
}

const localStore = {
	async load() {
		try {
			const parsed = JSON.parse(localStorage.getItem(localStorageKey) || '[]');
			state.chats = Array.isArray(parsed) ? parsed : [];
		} catch {
			state.chats = [];
		}
		state.activeChatId = state.chats[0]?.id || '';
	},
	async createChat() {
		const chat = {
			id: crypto.randomUUID(),
			title: 'New chat',
			messages: [],
			messageCount: 0,
			createdAt: Date.now(),
			updatedAt: Date.now(),
		};
		state.chats.unshift(chat);
		state.activeChatId = chat.id;
		await this.save();
		return chat;
	},
	async loadChat(id) {
		state.activeChatId = id;
	},
	async updateTitle(chat, title) {
		chat.title = title || 'New chat';
		chat.updatedAt = Date.now();
		await this.save();
	},
	async addMessage(chat, message) {
		const saved = normalizeMessage({ ...message, id: crypto.randomUUID(), createdAt: Date.now() });
		const pendingIndex = chat.messages.findIndex((item) => item === message);
		if (pendingIndex >= 0) {
			chat.messages[pendingIndex] = saved;
		} else {
			chat.messages.push(saved);
		}
		chat.updatedAt = Date.now();
		chat.messageCount = chat.messages.length;
		await this.save();
		return saved;
	},
	async deleteChat(id) {
		state.chats = state.chats.filter((chat) => chat.id !== id);
		if (state.activeChatId === id) {
			state.activeChatId = state.chats[0]?.id || '';
		}
		await this.save();
	},
	async save() {
		localStorage.setItem(localStorageKey, JSON.stringify(state.chats.slice(0, 20)));
	},
};

const serverStore = {
	async load() {
		const data = await fetchJSON('/api/conversations');
		state.chats = (data.conversations || []).map(normalizeConversation);
		state.activeChatId = state.chats[0]?.id || '';
		if (state.activeChatId) {
			await this.loadChat(state.activeChatId);
		}
	},
	async createChat() {
		const data = await fetchJSON('/api/conversations', {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify({ title: 'New chat' }),
		});
		const chat = normalizeConversation(data.conversation);
		state.chats = [chat, ...state.chats.filter((item) => item.id !== chat.id)];
		state.activeChatId = chat.id;
		return chat;
	},
	async loadChat(id) {
		const data = await fetchJSON(`/api/conversations/${encodeURIComponent(id)}`);
		const chat = normalizeConversation(data.conversation);
		const idx = state.chats.findIndex((item) => item.id === id);
		if (idx >= 0) {
			state.chats[idx] = chat;
		} else {
			state.chats.unshift(chat);
		}
		state.activeChatId = id;
		return chat;
	},
	async updateTitle(chat, title) {
		const data = await fetchJSON(`/api/conversations/${encodeURIComponent(chat.id)}`, {
			method: 'PATCH',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify({ title }),
		});
		chat.title = data.conversation.title;
	},
	async addMessage(chat, message) {
		const data = await fetchJSON(`/api/conversations/${encodeURIComponent(chat.id)}/messages`, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify(message),
		});
		const saved = normalizeMessage(data.message);
		const pendingIndex = chat.messages.findIndex((item) => item === message);
		if (pendingIndex >= 0) {
			chat.messages[pendingIndex] = saved;
		}
		chat.messageCount = chat.messages.length;
		return saved;
	},
	async deleteChat(id) {
		await fetchJSON(`/api/conversations/${encodeURIComponent(id)}`, { method: 'DELETE' });
		state.chats = state.chats.filter((chat) => chat.id !== id);
		if (state.activeChatId === id) {
			state.activeChatId = state.chats[0]?.id || '';
			if (state.activeChatId) {
				await this.loadChat(state.activeChatId);
			}
		}
	},
	async save() {},
};

function store() {
	return state.storageMode === 'server' ? serverStore : localStore;
}

async function selectChat(id) {
	clearError();
	stopJobStream();
	state.activeJob = null;
	setStreaming(false);
	try {
		await store().loadChat(id);
		const chat = activeChat();
		await attachActiveJob(chat);
		closeMobileSidebar();
		render();
	} catch (err) {
		showError(err.message);
	}
}

async function createChat() {
	clearError();
	const chat = await store().createChat();
	state.activeChatId = chat.id;
	closeMobileSidebar();
	render();
}

async function deleteChat(id) {
	const chat = state.chats.find((item) => item.id === id);
	if (!chat) {
		return;
	}
	if (!confirm(`Delete "${chat.title}"?`)) {
		return;
	}
	await store().deleteChat(id);
	if (state.chats.length === 0) {
		await createChat();
		return;
	}
	render();
}

function closeMobileSidebar() {
	el.sidebar.classList.remove('open');
	el.menuBackdrop.classList.remove('open');
	el.sidebarToggle.setAttribute('aria-expanded', 'false');
}

function toggleMobileSidebar() {
	const open = el.sidebar.classList.toggle('open');
	el.menuBackdrop.classList.toggle('open', open);
	el.sidebarToggle.setAttribute('aria-expanded', String(open));
}

async function deleteAccount() {
	if (state.currentUser === 'anonymous') {
		return;
	}
	await fetchJSON('/api/account', { method: 'DELETE' });
	localStorage.removeItem(localStorageKey);
	window.location.replace('/auth/logout');
}

function openDeleteAccountModal() {
	if (state.currentUser === 'anonymous') {
		return;
	}
	closeMobileSidebar();
	el.deleteAccountModal.classList.remove('hidden');
	el.cancelDeleteAccount.focus();
}

function closeDeleteAccountModal() {
	el.deleteAccountModal.classList.add('hidden');
}

function setStreaming(streaming) {
	el.send.textContent = streaming ? 'Stop' : 'Send';
	el.send.classList.toggle('stop', streaming);
	el.prompt.disabled = streaming;
}

function stopJobStream() {
	if (state.jobEventSource) {
		state.jobEventSource.close();
		state.jobEventSource = null;
	}
}

function chatMessages(chat) {
	return (chat.messages || [])
		.filter((message) => !message.pending)
		.map((message) => ({ role: message.role, content: message.content }));
}

function pendingAssistant(chat, job) {
	let assistant = (chat.messages || []).find((message) => message.pending && message.jobId === job.id);
	if (!assistant) {
		assistant = {
			role: 'assistant',
			content: job.content || '',
			thinking: job.thinking || '',
			model: job.model || selectedModel(),
			pending: true,
			jobId: job.id,
		};
		chat.messages = chat.messages || [];
		chat.messages.push(assistant);
	}
	return assistant;
}

async function startServerJob(chat, model, assistant) {
	const data = await fetchJSON(`/api/conversations/${encodeURIComponent(chat.id)}/jobs`, {
		method: 'POST',
		headers: { 'content-type': 'application/json' },
		body: JSON.stringify({
			model,
			messages: chatMessages(chat),
			think: el.thinkToggle.checked,
		}),
	});
	const job = data.job;
	assistant.jobId = job.id;
	assistant.model = job.model || model;
	assistant.content = job.content || assistant.content || '';
	assistant.thinking = job.thinking || assistant.thinking || '';
	state.activeJob = { conversationId: chat.id, id: job.id };
	setStreaming(true);
	render();
	streamServerJob(chat, job.id, assistant);
}

async function attachActiveJob(chat) {
	if (state.storageMode !== 'server' || !chat?.id) {
		return;
	}
	const data = await fetchJSON(`/api/conversations/${encodeURIComponent(chat.id)}/jobs/active`);
	if (!data.job) {
		return;
	}
	const assistant = pendingAssistant(chat, data.job);
	state.activeJob = { conversationId: chat.id, id: data.job.id };
	setStreaming(true);
	render();
	streamServerJob(chat, data.job.id, assistant);
}

function streamServerJob(chat, jobId, assistant) {
	stopJobStream();
	const url = `/api/conversations/${encodeURIComponent(chat.id)}/jobs/${encodeURIComponent(jobId)}/events`;
	const es = new EventSource(url);
	state.jobEventSource = es;

	function handleJobData(job) {
		assistant.content = job.content || '';
		assistant.thinking = job.thinking || '';
		assistant.model = job.model || assistant.model;
		el.appStatus.textContent = assistant.content
			? `Streaming from ${assistant.model}`
			: `${assistant.model} is thinking...`;

		if (job.status === 'running') {
			if (state.activeChatId === chat.id) {
				render();
			}
			return;
		}

		// Terminal state — stop streaming and clean up.
		stopJobStream();
		assistant.pending = false;
		state.activeJob = null;
		setStreaming(false);
		el.appStatus.textContent = `Model: ${assistant.model}`;

		if (job.status === 'complete') {
			store().loadChat(chat.id).then(() => render()).catch((err) => showError(err.message));
		} else if (job.status === 'canceled') {
			assistant.content = assistant.content ? `${assistant.content}\n\n[stopped]` : '[stopped]';
		} else if (job.status === 'error') {
			assistant.content = `Error: ${job.error || 'generation failed'}`;
			showError(job.error || 'generation failed');
		}
		render();
	}

	es.onmessage = (event) => {
		try {
			handleJobData(JSON.parse(event.data));
		} catch (err) {
			stopJobStream();
			state.activeJob = null;
			setStreaming(false);
			assistant.pending = false;
			assistant.content = `Error: ${err.message}`;
			showError(err.message);
			render();
		}
	};

	es.onerror = () => {
		// Only treat as an error when the job is still active.  When the server
		// closes the SSE connection after a terminal event the browser fires
		// onerror; by then state.activeJob is already null so we ignore it.
		if (state.activeJob) {
			stopJobStream();
			state.activeJob = null;
			setStreaming(false);
			assistant.pending = false;
			if (!assistant.content) {
				assistant.content = 'Error: connection lost';
				showError('Connection lost while streaming response');
			}
			render();
		}
	};
}

async function cancelServerJob() {
	if (!state.activeJob) {
		return;
	}
	const job = state.activeJob;
	await fetchJSON(`/api/conversations/${encodeURIComponent(job.conversationId)}/jobs/${encodeURIComponent(job.id)}/cancel`, {
		method: 'POST',
	});
	stopJobStream();
	state.activeJob = null;
	setStreaming(false);
	const chat = activeChat();
	if (chat?.id === job.conversationId) {
		const assistant = (chat.messages || []).find((message) => message.pending && message.jobId === job.id);
		if (assistant) {
			assistant.pending = false;
			assistant.content = assistant.content ? `${assistant.content}\n\n[stopped]` : '[stopped]';
		}
		render();
	}
}

async function fetchStream(url, options, onChunk) {
	const response = await fetch(url, options);
	if (!response.ok) {
		const text = await response.text();
		let data = {};
		try {
			data = JSON.parse(text);
		} catch {
			data = { error: text };
		}
		throw new Error(data.error || `${response.status} ${response.statusText}`);
	}
	if (!response.body) {
		throw new Error('streaming is not supported by this browser');
	}

	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	let buffer = '';
	while (true) {
		const { value, done } = await reader.read();
		if (done) {
			break;
		}
		buffer += decoder.decode(value, { stream: true });
		const lines = buffer.split('\n');
		buffer = lines.pop() || '';
		for (const line of lines) {
			consumeStreamLine(line, onChunk);
		}
	}
	buffer += decoder.decode();
	consumeStreamLine(buffer, onChunk);
}

function consumeStreamLine(line, onChunk) {
	if (!line.trim()) {
		return;
	}
	const chunk = JSON.parse(line);
	if (chunk.error) {
		throw new Error(chunk.error);
	}
	onChunk(chunk);
}

async function loadConfig() {
	const cfg = await fetchJSON('/api/config');
	state.appName = cfg.appName || state.appName;
	state.version = cfg.version || state.version || 'dev';
	state.defaultModel = cfg.defaultModel || state.defaultModel;
	state.authMode = cfg.authMode || state.authMode;
	state.storageMode = cfg.storageMode || (state.authMode === 'none' ? 'local' : 'server');
	state.currentUser = cfg.currentUser || 'anonymous';
	state.isAdmin = Boolean(cfg.isAdmin);
	document.title = state.appName;
	el.appName.textContent = state.appName;
	el.appVersion.textContent = `Version ${state.version}`;
	el.currentUser.textContent = state.currentUser;
	el.adminLink.classList.toggle('hidden', !state.isAdmin);
	el.deleteAccount.classList.toggle('hidden', state.currentUser === 'anonymous' || state.authMode === 'none');
}

async function loadModels() {
	el.refreshModels.disabled = true;
	el.appStatus.textContent = 'Loading models...';
	try {
		const data = await fetchJSON('/api/models');
		el.model.replaceChildren();

		const names = [...new Set((data.models || []).map((m) => m.name).filter(Boolean))].sort();
		if (!names.includes(state.defaultModel)) {
			names.unshift(state.defaultModel);
		}

		for (const name of names) {
			const option = document.createElement('option');
			option.value = name;
			option.textContent = name;
			option.selected = name === state.defaultModel;
			el.model.append(option);
		}

		el.appStatus.textContent = names.length === 1
			? `Model: ${names[0]}`
			: `${names.length} models available`;
	} finally {
		el.refreshModels.disabled = false;
	}
}

async function send(content) {
	clearError();
	if (!content) {
		return;
	}

	let chat = activeChat();
	if (!chat) {
		chat = await store().createChat();
	}

	if ((chat.messages || []).length === 0) {
		await store().updateTitle(chat, titleFrom(content));
	}

	const currentModel = selectedModel();
	const userMessage = { role: 'user', content };
	chat.messages.push(userMessage);
	await store().addMessage(chat, userMessage);
	await store().save();
	render();

	state.streamController = new AbortController();
	setStreaming(true);
	el.prompt.value = '';

	const assistant = { role: 'assistant', content: '', thinking: '', model: currentModel, pending: true };
	chat.messages.push(assistant);
	render();

	if (state.storageMode === 'server') {
		try {
			await startServerJob(chat, currentModel, assistant);
		} catch (err) {
			assistant.pending = false;
			assistant.content = `Error: ${err.message}`;
			showError(err.message);
			render();
			setStreaming(false);
		} finally {
			state.streamController = null;
			el.prompt.focus();
		}
		return;
	}

	try {
		await fetchStream('/api/chat', {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			signal: state.streamController.signal,
			body: JSON.stringify({ model: currentModel, messages: chatMessages(chat), stream: true, think: el.thinkToggle.checked }),
		}, (chunk) => {
			if (el.thinkToggle.checked && chunk.message?.thinking) {
				assistant.thinking += chunk.message.thinking;
				el.appStatus.textContent = `${currentModel} is thinking...`;
			}
			if (chunk.message?.content) {
				assistant.content += chunk.message.content;
				el.appStatus.textContent = `Streaming from ${currentModel}`;
			}
			if (chunk.done) {
				assistant.pending = false;
			}
			render();
		});

		assistant.pending = false;
		if (!assistant.content) {
			assistant.content = assistant.thinking ? '(no final response)' : '(empty response)';
		}
		el.appStatus.textContent = `Model: ${currentModel}`;
		await store().addMessage(chat, assistant);
		await store().save();
		render();
	} catch (err) {
		if (err.name === 'AbortError') {
			assistant.pending = false;
			assistant.content = assistant.content ? `${assistant.content}\n\n[stopped]` : '[stopped]';
			el.appStatus.textContent = `Stopped ${currentModel}`;
			await store().addMessage(chat, assistant);
			await store().save();
			render();
			return;
		}

		assistant.pending = false;
		assistant.content = `Error: ${err.message}`;
		showError(err.message);
		await store().addMessage(chat, assistant);
		await store().save();
		render();
	} finally {
		state.streamController = null;
		setStreaming(false);
		el.prompt.focus();
	}
}

el.newChat.addEventListener('click', () => {
	createChat().catch((err) => showError(err.message));
});

el.sidebarToggle.addEventListener('click', () => {
	toggleMobileSidebar();
});

el.menuBackdrop.addEventListener('click', closeMobileSidebar);

el.deleteAccount.addEventListener('click', () => {
	openDeleteAccountModal();
});

el.cancelDeleteAccount.addEventListener('click', closeDeleteAccountModal);

el.deleteAccountModal.addEventListener('click', (event) => {
	if (event.target === el.deleteAccountModal) {
		closeDeleteAccountModal();
	}
});

el.confirmDeleteAccount.addEventListener('click', () => {
	el.confirmDeleteAccount.disabled = true;
	deleteAccount().catch((err) => {
		el.confirmDeleteAccount.disabled = false;
		closeDeleteAccountModal();
		showError(err.message);
	});
});

el.refreshModels.addEventListener('click', () => {
	clearError();
	loadModels().catch((err) => showError(`Could not load models: ${err.message}`));
});

el.composer.addEventListener('submit', (event) => {
	event.preventDefault();
	if (state.activeJob) {
		cancelServerJob().catch((err) => showError(err.message));
		return;
	}
	if (state.streamController) {
		state.streamController.abort();
		return;
	}
	send(el.prompt.value.trim()).catch((err) => showError(err.message));
});

el.prompt.addEventListener('keydown', (event) => {
	if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
		event.preventDefault();
		if (state.activeJob || state.streamController) {
			return;
		}
		send(el.prompt.value.trim()).catch((err) => showError(err.message));
	}
});

async function boot() {
	await loadConfig();
	await Promise.all([
		loadModels().catch((err) => showError(`Could not load models: ${err.message}`)),
		store().load(),
	]);
	if (state.chats.length === 0) {
		await createChat();
	} else {
		await attachActiveJob(activeChat());
		render();
	}
}

boot().catch((err) => showError(err.message));
