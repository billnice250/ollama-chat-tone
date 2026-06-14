const localStorageKey = 'ollama-chat-client.chats.v1';

const el = {
	appName: document.getElementById('app-name'),
	appStatus: document.getElementById('app-status'),
	currentUser: document.getElementById('current-user'),
	sidebar: document.querySelector('.sidebar'),
	sidebarToggle: document.getElementById('sidebar-toggle'),
	chatList: document.getElementById('chat-list'),
	chatTitle: document.getElementById('chat-title'),
	chatMeta: document.getElementById('chat-meta'),
	messages: document.getElementById('messages'),
	model: document.getElementById('model'),
	refreshModels: document.getElementById('refresh-models'),
	adminLink: document.getElementById('admin-link'),
	error: document.getElementById('error'),
	prompt: document.getElementById('prompt'),
	send: document.getElementById('send'),
	composer: document.getElementById('composer'),
	newChat: document.getElementById('new-chat'),
};

const state = {
	appName: 'Ollama Chat',
	defaultModel: 'llama3.2',
	authMode: 'none',
	storageMode: 'local',
	currentUser: 'anonymous',
	isAdmin: false,
	activeChatId: '',
	chats: [],
	streamController: null,
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
		const thinking = document.createElement('span');
		thinking.className = 'thinking';
		const label = document.createElement('span');
		label.className = 'thinking-label';
		label.textContent = message.pending ? 'Thinking' : 'Thinking log';
		const text = document.createElement('span');
		text.textContent = message.thinking;
		thinking.append(label, text);
		bubble.append(thinking);
	}

	const content = document.createElement('span');
	content.textContent = message.content || 'Thinking...';
	bubble.append(content);
	row.append(bubble);
	return row;
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
	try {
		await store().loadChat(id);
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
	el.sidebarToggle.setAttribute('aria-expanded', 'false');
}

function setStreaming(streaming) {
	el.send.textContent = streaming ? 'Stop' : 'Send';
	el.send.classList.toggle('stop', streaming);
	el.prompt.disabled = streaming;
}

function chatMessages(chat) {
	return (chat.messages || [])
		.filter((message) => !message.pending)
		.map((message) => ({ role: message.role, content: message.content }));
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
	state.defaultModel = cfg.defaultModel || state.defaultModel;
	state.authMode = cfg.authMode || state.authMode;
	state.storageMode = cfg.storageMode || (state.authMode === 'none' ? 'local' : 'server');
	state.currentUser = cfg.currentUser || 'anonymous';
	state.isAdmin = Boolean(cfg.isAdmin);
	document.title = state.appName;
	el.appName.textContent = state.appName;
	el.currentUser.textContent = state.currentUser;
	el.adminLink.classList.toggle('hidden', !state.isAdmin);
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

	try {
		await fetchStream('/api/chat', {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			signal: state.streamController.signal,
			body: JSON.stringify({ model: currentModel, messages: chatMessages(chat), stream: true }),
		}, (chunk) => {
			if (chunk.message?.thinking) {
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
	const open = el.sidebar.classList.toggle('open');
	el.sidebarToggle.setAttribute('aria-expanded', String(open));
});

el.refreshModels.addEventListener('click', () => {
	clearError();
	loadModels().catch((err) => showError(`Could not load models: ${err.message}`));
});

el.composer.addEventListener('submit', (event) => {
	event.preventDefault();
	if (state.streamController) {
		state.streamController.abort();
		return;
	}
	send(el.prompt.value.trim()).catch((err) => showError(err.message));
});

el.prompt.addEventListener('keydown', (event) => {
	if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
		event.preventDefault();
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
		render();
	}
}

boot().catch((err) => showError(err.message));
