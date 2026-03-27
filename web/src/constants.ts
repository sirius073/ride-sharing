const DEFAULT_API_URL = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8081';
const DEFAULT_WEBSOCKET_URL = process.env.NEXT_PUBLIC_WEBSOCKET_URL ?? 'ws://localhost:8081';

function toBrowserReachableURL(rawUrl: string): string {
	if (typeof window === 'undefined') {
		return rawUrl;
	}

	try {
		const parsed = new URL(rawUrl);
		const browserHost = window.location.hostname || 'localhost';

		if (parsed.hostname === 'api-gateway') {
			parsed.hostname = browserHost;
			if (!parsed.port) {
				parsed.port = '8081';
			}
			return parsed.toString().replace(/\/$/, '');
		}

		return rawUrl.replace(/\/$/, '');
	} catch {
		return rawUrl.replace(/\/$/, '');
	}
}

export const API_URL = toBrowserReachableURL(DEFAULT_API_URL);
export const WEBSOCKET_URL = toBrowserReachableURL(DEFAULT_WEBSOCKET_URL);
