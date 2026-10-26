// Deterministic regressions for the real WebMCP browser script. This runs without a server or third-party packages.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';
import { test } from 'node:test';

const sourcePath = fileURLToPath(new URL('../../web/jelly/webmcp.js', import.meta.url));
const source = readFileSync(sourcePath, 'utf8');
const origin = 'https://jelly.test';

function toolList(tools = []) {
  return { ok: true, headers: { get: () => 'application/json' }, json: async () => ({ tools }) };
}

function jsonResponse(value, contentType = 'application/json') {
  return { ok: true, headers: { get: () => contentType }, json: async () => value };
}

async function executeActualScript({ documentContext, navigatorContext, tools = [], apiResponse, documentGetterThrows = false, discoveryResponse, flush = async () => {} } = {}) {
  const registrations = [];
  const requests = [];
  const consoleProblems = [];
  const listeners = new Map();
  const document = {};
  const navigator = {};
  if (documentGetterThrows) {
    Object.defineProperty(document, 'modelContext', { configurable: true, get() { throw new Error('document modelContext getter'); } });
  } else if (documentContext !== undefined) {
    Object.defineProperty(document, 'modelContext', { configurable: true, get: () => documentContext });
  }
  if (navigatorContext !== undefined) {
    Object.defineProperty(navigator, 'modelContext', { configurable: true, get: () => navigatorContext });
  }

  const fetch = async (input, options = {}) => {
    const url = String(input);
    requests.push({ url, options });
    if (options.signal?.aborted) throw new DOMException('The operation was aborted.', 'AbortError');
    if (url === `${origin}/_jelly/webmcp-tools.json`) return discoveryResponse ? discoveryResponse(options, requests.length) : toolList(tools);
    if (apiResponse) return apiResponse;
    return jsonResponse({ ok: true });
  };
  const globals = {
    AbortController,
    URL,
    addEventListener: (type, callback, options) => {
      const entries = listeners.get(type) || [];
      entries.push({ callback, options });
      listeners.set(type, entries);
    },
    console: {
      error: (...args) => consoleProblems.push(['error', ...args]),
      warn: (...args) => consoleProblems.push(['warn', ...args]),
    },
    document,
    fetch,
    location: { origin },
    navigator,
  };
  vm.runInNewContext(source, globals, { filename: sourcePath });
  // Let the script's tool-list fetch and registration Promise chain finish.
  const drain = async () => {
    await new Promise((resolve) => setImmediate(resolve));
    await new Promise((resolve) => setImmediate(resolve));
    await flush();
  };
  const dispatch = (type, event = {}) => {
    const entries = [...(listeners.get(type) || [])];
    for (const entry of entries) {
      if (entry.options?.once) {
        const current = listeners.get(type) || [];
        listeners.set(type, current.filter((candidate) => candidate !== entry));
      }
      entry.callback(event);
    }
  };
  await drain();
  return { registrations, requests, consoleProblems, listeners, dispatch, drain, fetch, toolList: tools };
}

function makeHost(registrations, registerTool = (tool, options) => {
  registrations.push({ tool, options });
  return Promise.resolve();
}) {
  return { registerTool };
}

test('no host does not fetch the tool list', async () => {
  const result = await executeActualScript();
  assert.equal(result.requests.length, 0);
  assert.deepEqual(result.consoleProblems, []);
});

test('an invalid document host falls back to navigator.modelContext', async () => {
  const registrations = [];
  const result = await executeActualScript({
    documentContext: {},
    navigatorContext: makeHost(registrations),
    tools: [{ name: 'navigator_tool', title: 'Navigator tool', description: 'A fallback tool.', input_schema: { type: 'object' }, endpoint: '/public-events/{id}' }],
  });
  assert.equal(registrations.length, 1, 'the navigator host should receive the tool');
  assert.equal(registrations[0].tool.name, 'navigator_tool');
  assert.deepEqual(result.consoleProblems, []);
});

test('a throwing document registerTool getter falls back to navigator without console errors', async () => {
  const registrations = [];
  const invalidDocumentHost = Object.defineProperty({}, 'registerTool', { get() { throw new Error('registerTool getter'); } });
  const result = await executeActualScript({
    documentContext: invalidDocumentHost,
    navigatorContext: makeHost(registrations),
    tools: [{ name: 'navigator_tool', title: 'Navigator tool', description: 'A fallback tool.', input_schema: { type: 'object' }, endpoint: '/public-events/{id}' }],
  });
  assert.equal(registrations.length, 1, 'the navigator host should receive the tool');
  assert.deepEqual(result.consoleProblems, []);
});

test('a throwing document modelContext getter falls back to navigator.modelContext', async () => {
  const registrations = [];
  const result = await executeActualScript({
    documentGetterThrows: true,
    navigatorContext: makeHost(registrations),
    tools: [{ name: 'navigator_tool', title: 'Navigator tool', description: 'A fallback tool.', input_schema: { type: 'object' }, endpoint: '/public-events/{id}' }],
  });
  assert.equal(registrations.length, 1);
  assert.deepEqual(result.consoleProblems, []);
});

test('execute accepts same-origin non-API paths, encodes placeholders, sends Accept JSON, and returns JSON', async () => {
  const registrations = [];
  const tools = [{
    name: 'event_detail', title: 'Event detail', description: 'Read an event.',
    input_schema: { type: 'object' }, endpoint: '/public-events/{id}',
  }];
  const answer = { event: '雪 day', count: 3 };
  const result = await executeActualScript({
    documentContext: makeHost(registrations), tools, apiResponse: jsonResponse(answer),
  });
  assert.equal(registrations.length, 1);
  const value = await registrations[0].tool.execute({ id: '雪 day', view: 'wide screen' }, { signal: new AbortController().signal });
  assert.deepEqual(value, answer);
  const apiRequest = result.requests.find((request) => request.url.startsWith(`${origin}/public-events/`));
  assert.ok(apiRequest, 'execute should fetch the declared same-origin endpoint');
  assert.equal(apiRequest.url, `${origin}/public-events/%E9%9B%AA%20day?view=wide+screen`);
  assert.equal(apiRequest.options.method, 'GET');
  assert.equal(apiRequest.options.headers?.Accept, 'application/json');
});

test('execute sends the JSON Accept header for an API endpoint', async () => {
  const registrations = [];
  const tools = [{ name: 'event_detail', title: 'Event detail', description: 'Read an event.', input_schema: { type: 'object' }, endpoint: '/api/public-events/{id}' }];
  const result = await executeActualScript({ documentContext: makeHost(registrations), tools, apiResponse: jsonResponse({ ok: true }) });
  await registrations[0].tool.execute({ id: '42', language: 'zh-TW' }, { signal: new AbortController().signal });
  const request = result.requests.find((entry) => entry.url.startsWith(`${origin}/api/public-events/`));
  assert.ok(request);
  assert.equal(request.options.headers?.Accept, 'application/json');
});

test('tool discovery bypasses the browser cache', async () => {
  const result = await executeActualScript({
    documentContext: makeHost([]),
    tools: [{ name: 'cache_probe', title: 'Cache probe', description: 'Check discovery cache mode.', input_schema: { type: 'object' }, endpoint: '/events' }],
  });
  const discovery = result.requests.find((request) => request.url.includes('/_jelly/webmcp-tools.json'));
  assert.ok(discovery);
  assert.equal(discovery.options.cache, 'no-store');
});

test('pagehide aborts the registration signal', async () => {
  const registrations = [];
  const result = await executeActualScript({
    documentContext: makeHost(registrations),
    tools: [{ name: 'pagehide_tool', title: 'Pagehide', description: 'Abort on navigation.', input_schema: { type: 'object' }, endpoint: '/public-events/{id}' }],
  });
  assert.equal(registrations.length, 1);
  assert.equal(result.listeners.get('pagehide')?.[0]?.options?.once, true);
  result.dispatch('pagehide', { persisted: true });
  assert.equal(registrations[0].options.signal.aborted, true);
});

test('persisted pageshow starts one fresh registration generation and the next pagehide aborts it', async () => {
  const registrations = [];
  let discoveries = 0;
  const tool = { name: 'lifecycle_tool', title: 'Lifecycle', description: 'Tracks page generations.', input_schema: { type: 'object' }, endpoint: '/events' };
  const result = await executeActualScript({
    documentContext: makeHost(registrations),
    discoveryResponse: async () => {
      discoveries++;
      return toolList([tool]);
    },
  });
  assert.equal(registrations.length, 1);
  const first = registrations[0];
  result.dispatch('pagehide', { persisted: true });
  assert.equal(first.options.signal.aborted, true);

  result.dispatch('pageshow', { persisted: true });
  await result.drain();
  assert.equal(discoveries, 2, 'restoration must fetch the current tool list again');
  assert.equal(registrations.length, 2, 'restoration must register exactly one fresh generation');
  assert.equal(result.requests.filter((request) => request.url.includes('/_jelly/webmcp-tools.json')).every((request) => request.options.cache === 'no-store'), true,
    'each discovery fetch bypasses cached lists');
  assert.notEqual(registrations[1].options.signal, first.options.signal);
  assert.equal(registrations[1].options.signal.aborted, false);

  result.dispatch('pageshow', { persisted: false });
  result.dispatch('pageshow', { persisted: true });
  await result.drain();
  assert.equal(registrations.length, 2, 'ordinary and duplicate pageshow events while active do not restart discovery');
  result.dispatch('pagehide', { persisted: true });
  assert.equal(registrations[1].options.signal.aborted, true);
  assert.equal(first.options.signal.aborted, true, 'an old generation stays aborted');
});

test('a tool list whose JSON finishes after pagehide is not registered', async () => {
  const registrations = [];
  let finishJSON;
  let discoverySignal;
  const pendingJSON = new Promise((resolve) => { finishJSON = resolve; });
  const tool = { name: 'late_tool', title: 'Late', description: 'Must not register after hide.', input_schema: { type: 'object' }, endpoint: '/events' };
  const result = await executeActualScript({
    documentContext: makeHost(registrations),
    discoveryResponse: async (options) => {
      discoverySignal = options.signal;
      // The response has arrived; this per-case deferred body models JSON becoming available after pagehide.
      // The generic fetch stub still rejects requests started with an already-aborted signal.
      return { ok: true, headers: { get: () => 'application/json' }, json: async () => {
        return pendingJSON;
      } };
    },
  });
  assert.equal(registrations.length, 0);
  result.dispatch('pagehide', { persisted: true });
  assert.equal(discoverySignal.aborted, true, 'the in-flight discovery signal is aborted by pagehide');
  finishJSON({ tools: [tool] });
  await result.drain();
  assert.equal(registrations.length, 0, 'late JSON completion after abort must not register tools');
});

test('a failed tool-list fetch on restoration leaves the active tool list empty', async () => {
  const registrations = [];
  let discoveries = 0;
  const tool = { name: 'temporary_tool', title: 'Temporary', description: 'Disappears on restoration failure.', input_schema: { type: 'object' }, endpoint: '/events' };
  const result = await executeActualScript({
    documentContext: makeHost(registrations),
    discoveryResponse: async () => {
      discoveries++;
      if (discoveries > 1) return { ok: false, headers: { get: () => 'application/json' }, json: async () => ({ tools: [] }) };
      return toolList([tool]);
    },
  });
  assert.equal(registrations.length, 1);
  result.dispatch('pagehide', { persisted: true });
  result.dispatch('pageshow', { persisted: true });
  await result.drain();
  assert.equal(discoveries, 2, 'restoration attempts discovery again');
  assert.equal(registrations.length, 1, '404 or rejected discovery cannot add tools on restoration');
});
