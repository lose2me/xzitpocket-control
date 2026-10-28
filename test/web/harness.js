import * as Vue from 'vue';
import { vi } from 'vitest';

export async function consoleHarness({ path = '/overview', permissions = '*=rw', authenticated = true, desktop = true, storage, respond } = {}) {
  vi.useFakeTimers();
  vi.resetModules();
  localStorage.clear();
  document.cookie = 'control_csrf=; Max-Age=0; path=/';
  Object.entries(storage || {}).forEach(([key, value]) => localStorage.setItem(key, value));
  if (authenticated) {
    localStorage.setItem('control_admin', 'test-token');
    localStorage.setItem('control_admin_name', 'test-admin');
    localStorage.setItem('control_admin_perms', permissions);
  }
  window.history.replaceState({}, '', path);
  document.body.innerHTML = '<div id="app"></div>';
  const requests = [];
  let handler = respond;
  const fetch = vi.fn(async (url, options = {}) => {
    const request = { url: String(url), ...options };
    requests.push(request);
    const response = await handler?.(request);
    if (response instanceof Error) throw response;
    const { status = 200, body = {} } = response || {};
    return {
      ok: status >= 200 && status < 300,
      status,
      json: async () => {
        if (body === 'invalid-json') throw new SyntaxError('invalid response');
        return body;
      },
      blob: async () => new Blob(['test document'], { type: 'application/pdf' }),
    };
  });
  vi.stubGlobal('fetch', fetch);
  vi.stubGlobal('confirm', vi.fn(() => true));
  vi.stubGlobal('alert', vi.fn());
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn().mockResolvedValue() } });
  vi.stubGlobal('Vuetify', { createVuetify: () => ({ install() {} }), useDisplay: () => ({ mdAndUp: Vue.ref(desktop) }) });
  let app;
  let vm;
  let footer;
  vi.stubGlobal('Vue', {
    ...Vue,
    createApp(component) {
      app = Vue.createApp({ ...component, template: undefined, render: () => null });
      const originalComponent = app.component.bind(app);
      app.component = (name, value) => { footer = value; originalComponent(name, value); return app; };
      const originalMount = app.mount.bind(app);
      app.mount = (...args) => { vm = originalMount(...args); return vm; };
      return app;
    },
  });
  await import('../../web/app.js');
  await settle();
  return {
    vm, requests, fetch, footer,
    respond: (value) => { handler = value; },
    clearRequests: () => { requests.length = 0; },
    close: () => { app.unmount(); vi.clearAllTimers(); vi.useRealTimers(); vi.unstubAllGlobals(); },
  };
}

export async function settle() {
  for (let i = 0; i < 50; i++) await Promise.resolve();
  await Vue.nextTick();
}
