const themeKey = 'ollama-chat-client.theme';

function preferredTheme() {
	const saved = localStorage.getItem(themeKey);
	if (saved === 'light' || saved === 'dark') {
		return saved;
	}
	return 'dark';
}

function setTheme(theme) {
	document.documentElement.dataset.theme = theme;
	localStorage.setItem(themeKey, theme);
	for (const button of document.querySelectorAll('.theme-toggle')) {
		button.textContent = theme === 'dark' ? 'Light' : 'Dark';
		button.setAttribute('aria-pressed', String(theme === 'dark'));
	}
}

setTheme(preferredTheme());

window.addEventListener('DOMContentLoaded', () => {
	for (const button of document.querySelectorAll('.theme-toggle')) {
		button.addEventListener('click', () => {
			setTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark');
		});
	}
});
