import {invokePWA, initializePWA} from "./pwa_backend";

interface RPCResponse<T = unknown> {
	id: number;
	ok: boolean;
	result?: T;
	error?: string;
}

interface PendingRPC {
	resolve: (value: unknown) => void;
	reject: (reason: Error) => void;
}

declare global {
	interface External {
		invoke: (message: string) => void;
	}

	interface Window {
		taskNotifierBridge: {
			receive: (response: RPCResponse) => void;
		};
	}
}

const pendingRPC = new Map<number, PendingRPC>();
let nextRequestID = 1;

export type RuntimeMode = "native" | "pwa";

export function runtimeMode(): RuntimeMode {
	return window.external !== undefined && typeof window.external.invoke === "function" ? "native" : "pwa";
}

export function isNativeRuntime(): boolean {
	return runtimeMode() === "native";
}

export function sendFrontendLog(stage: string, detail = ""): void {
	if (!isNativeRuntime()) {
		console.debug(`[TaskNotifier/PWA] ${stage}`, detail);
		return;
	}
	try {
		window.external.invoke(JSON.stringify({id: 0, method: "frontend_log", params: {stage, detail}}));
	} catch {
		// 診断ログ自体の失敗で管理画面を停止させない。
	}
}

export async function initializeRuntime(): Promise<void> {
	if (!isNativeRuntime()) {
		await initializePWA();
	}
}

export function invoke<T>(method: string, params: unknown = {}): Promise<T> {
	if (!isNativeRuntime()) {
		return invokePWA<T>(method, params);
	}
	const id = nextRequestID++;
	return new Promise<T>((resolve, reject) => {
		pendingRPC.set(id, {
			resolve: resolve as (value: unknown) => void,
			reject,
		});
		window.external.invoke(JSON.stringify({id, method, params}));
	});
}

window.taskNotifierBridge = {
	receive(response: RPCResponse): void {
		const pending = pendingRPC.get(response.id);
		if (pending === undefined) {
			return;
		}
		pendingRPC.delete(response.id);
		if (response.ok) {
			pending.resolve(response.result);
			return;
		}
		pending.reject(new Error(response.error ?? "処理に失敗しました。"));
	},
};
