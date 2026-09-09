import './styles.css';
import { createConsole } from './app';

// Capture the fragment once. The token remains in the request closure, never in storage or URLs.
const token = new URLSearchParams(location.hash.slice(1)).get('token');
history.replaceState(null, '', location.pathname + location.search);
const app = createConsole(document.querySelector<HTMLElement>('#app')!, token);
window.addEventListener('pagehide', () => app.stop(), { once: true });
