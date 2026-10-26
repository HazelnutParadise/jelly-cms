function modelContext() {
  let candidate;
  try {
    if (typeof document !== "undefined") candidate = document.modelContext;
    if (candidate && typeof candidate.registerTool === "function") return { host: candidate, register: candidate.registerTool };
  } catch (_) {}
  try {
    if (typeof navigator !== "undefined") candidate = navigator.modelContext;
    if (candidate && typeof candidate.registerTool === "function") return { host: candidate, register: candidate.registerTool };
  } catch (_) {}
  return null;
}

const capability = modelContext();
const host = capability && capability.host;
if (host && typeof fetch === "function" && typeof AbortController === "function") {
  const base = location.origin;
  let currentController;
  const forbidden = path => /^\/(?:admin|auth|login|install|account|_jelly|api\/(?:admin|auth|install|account))(?:\/|$)/i.test(path);
  const getURL = (tool, input) => {
    if (!tool || typeof tool.endpoint !== "string" || !tool.endpoint.startsWith("/") || tool.endpoint.startsWith("//") || /[?#\\]/.test(tool.endpoint)) throw new TypeError("Invalid tool endpoint");
    let path = tool.endpoint;
    const used = new Set();
    path = path.replace(/\{([A-Za-z][A-Za-z0-9_]*)\}/g, (_, key) => {
      used.add(key);
      const value = input && input[key];
      if (value === null || value === undefined) throw new TypeError("Missing path value");
      const part = String(value);
      if (part === "." || part === ".." || /[\\/]/.test(part) || /%2e/i.test(part)) throw new TypeError("Invalid path value");
      return encodeURIComponent(part);
    });
    if (/\{[^}]*\}/.test(path)) throw new TypeError("Invalid path placeholder");
    const url = new URL(path, base);
    if (url.origin !== base || forbidden(url.pathname) || /%(?:2f|5c)/i.test(url.pathname)) throw new TypeError("Invalid tool URL");
    if (input && typeof input === "object") for (const key of Object.keys(input)) {
      if (!used.has(key) && input[key] !== null && input[key] !== undefined) url.searchParams.set(key, String(input[key]));
    }
    return url;
  };
  const run = async (tool, input, options, generationSignal) => {
    const url = getURL(tool, input);
    const requestController = new AbortController();
    const callerSignal = options && options.signal;
    const signals = new Set([generationSignal, callerSignal].filter(Boolean));
    const listeners = new Map();
    const cancel = signal => requestController.abort(signal.reason);
    for (const signal of signals) {
      if (signal.aborted) cancel(signal);
      else {
        const listener = () => cancel(signal);
        listeners.set(signal, listener);
        signal.addEventListener("abort", listener, { once: true });
      }
    }
    try {
      const response = await fetch(url.href, { method: "GET", headers: { Accept: "application/json" }, credentials: "same-origin", redirect: "error", signal: requestController.signal });
      const type = response.headers.get("content-type") || "";
      if (!/\bjson\b/i.test(type)) throw new TypeError("Tool response is not JSON");
      return await response.json();
    } finally {
      for (const [signal, listener] of listeners) signal.removeEventListener("abort", listener);
    }
  };
  const bootstrap = () => {
    const generation = new AbortController();
    currentController = generation;
    const signal = generation.signal;
    try { addEventListener("pagehide", () => generation.abort(), { once: true }); } catch (_) {}
    Promise.resolve().then(async () => {
      if (signal.aborted) return;
      const response = await fetch(new URL("/_jelly/webmcp-tools.json", base), { credentials: "same-origin", cache: "no-store", signal });
      if (signal.aborted || !response.ok) return;
      const payload = await response.json();
      if (signal.aborted || !payload || !Array.isArray(payload.tools)) return;
      for (const tool of payload.tools) {
        if (signal.aborted) return;
        if (!tool || typeof tool.name !== "string" || typeof tool.title !== "string" || typeof tool.description !== "string" || !tool.input_schema || typeof tool.input_schema !== "object") continue;
        try {
          if (signal.aborted) return;
          const result = capability.register.call(host, { name: tool.name, title: tool.title, description: tool.description, inputSchema: tool.input_schema, annotations: tool.annotations || {}, execute: (input, options) => run(tool, input, options, signal) }, { signal });
          if (result && typeof result.then === "function") result.catch(() => {});
        } catch (_) {}
      }
    }).catch(() => {});
  };
  bootstrap();
  try {
    addEventListener("pageshow", event => {
      if (event.persisted && currentController && currentController.signal.aborted) bootstrap();
    });
  } catch (_) {}
}
