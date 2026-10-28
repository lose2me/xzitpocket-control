import * as Vue from 'vue';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { consoleHarness, settle } from './harness.js';

let h;
afterEach(() => { h?.close(); });

const row = { id: 'rec/1', name: '测试记录', status: 'active', code: 'C1' };
const answer = ({ url, method = 'GET' }) => {
  if (url.endsWith('/admin/me')) return { body: { name: '测试管理员', permissions: '*=rw' } };
  if (url.endsWith('/school-calendar')) return { body: { days: [{ date: '2026-01-01' }], colleges: [], collegeOptions: ['计算机学院'], slots: [] } };
  if (url.endsWith('/admin/settings')) return { body: { groups: [{ key: 'general', label: '常规', fields: [] }] } };
  if (url.endsWith('/admins')) return { body: { items: [{ id: 'a1', key: 'secret-key', permissions: 'users=r' }] } };
  if (url.includes('/share-codes/') && method === 'GET') {
    return { body: { shareCode: { code: '1234561' }, data: { empty: [], weeks: [1, 2], sessions: [1], nested: {}, mixed: [{ a: 1 }] } } };
  }
  if (url.includes('/users/') && method === 'GET') return { body: { user: { ...row, status: 'active' }, devices: [] } };
  if (url.includes('/risk-events')) return { body: { items: [{ id: 'k1', type: 'login_attempt_burst', device_id: 'd1', acknowledged_at: null }], deleted: 0 } };
  if (url.includes('/audit')) return { body: { items: [{ id: 'l1', action: 'admin_login', target_type: 'user', target_id: 'u1' }, { action: 'unknown_action' }] } };
  return { body: { items: [row], total: 1 } };
};

describe('routing, tabs and permissions', () => {
  it('maps unknown and legacy paths, exposes titles and permission helpers', async () => {
    h = await consoleHarness({ path: '/config', respond: answer });
    await settle();
    expect(h.vm.view).toBe('app-config');
    expect(window.location.pathname).toBe('/app-config');
    expect(h.vm.title).toBe('APP 配置');
    expect(h.vm.canEditView('web-config')).toBe(true);
    expect(h.vm.canEditView('textbooks')).toBe(true);
    expect(h.vm.settingPermission('CONTROL_OVERVIEW_SCALE_PERCENT', 'general')).toBe('overview');
    expect(h.vm.settingPermission('CONTROL_ADMIN_KEY', 'general')).toBe('settings-general');
    expect(h.vm.settingGroupIcon('cdn')).toBe('mdi-web');
    expect(h.vm.settingGroupIcon('nope')).toBe('mdi-cog-outline');
    expect(h.vm.canWriteSettingGroup('general')).toBe(false);
    h.vm.settingGroups = [{ key: 'general', fields: [{ key: 'CONTROL_ADMIN_KEY' }] }];
    expect(h.vm.canWriteSettingGroup('general')).toBe(true);
    expect(h.vm.settingTabLabel).toBeUndefined();
    h.vm.settingGroups = [];
    expect(h.vm.settingTabLabel).toBe('运行配置');
    h.vm.settingGroups = [{ key: 'general', label: '常规' }];
    h.vm.settingTab = 'general';
    expect(h.vm.settingTabLabel).toBe('常规');
  });

  it('restores remembered tabs and ignores storage failures', async () => {
    h = await consoleHarness({ storage: { control_tab_library: 'review', control_tab_app_config: 'calendar' }, respond: answer });
    await settle();
    expect(h.vm.libraryTab).toBe('review');
    expect(h.vm.appConfigTab).toBe('calendar');
    h.vm.libraryTab = 'publish';
    await settle();
    expect(localStorage.getItem('control_tab_library')).toBe('publish');

    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation((key) => {
      if (String(key).startsWith('control_tab')) throw new Error('blocked');
      return undefined;
    });
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation((key) => {
      if (String(key).startsWith('control_tab')) throw new Error('blocked');
    });
    const inner = await consoleHarness({ respond: answer });
    await settle();
    expect(inner.vm.settingTab).toBe('general');
    inner.vm.settingTab = 'risk';
    await settle();
    inner.close();
    getItem.mockRestore();
    setItem.mockRestore();
  });

  it('falls back to the first visible page and ignores navigation without any access', async () => {
    h = await consoleHarness({ permissions: 'school-calendar=r', respond: ({ url }) => (url.endsWith('/admin/me') ? { body: { permissions: 'school-calendar=r' } } : answer({ url })) });
    await settle();
    expect(h.vm.visibleNav.map(item => item.key)).toEqual(['app-config']);
    h.vm.switchView('textbooks');
    await settle();
    expect(h.vm.view).toBe('app-config');
    expect(h.vm.appConfigTab).toBe('calendar');

    const restricted = await consoleHarness({ permissions: 'nothing=r', respond: ({ url }) => (url.endsWith('/admin/me') ? { body: { permissions: 'nothing=r' } } : answer({ url })) });
    await settle();
    expect(restricted.vm.visibleNav).toEqual([]);
    restricted.vm.switchView('users');
    await settle();
    expect(restricted.vm.view).toBe('overview');
    restricted.close();
  });

  it('loads every page through the router and reacts to browser history', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    for (const view of ['users', 'admins', 'risk', 'error-reports', 'library', 'textbooks', 'resources', 'courses', 'share-codes', 'app-config', 'web-config', 'audit']) {
      h.vm.switchView(view);
      await settle();
      expect(h.vm.view).toBe(view);
      expect(h.vm.error).toBe('');
    }
    expect(h.vm.audit[0].target_text).toBe('user u1');
    expect(h.vm.audit[1].target_text).toBe('');
    expect(window.location.pathname).toBe('/audit');

    window.history.pushState({}, '', '/users');
    window.dispatchEvent(new PopStateEvent('popstate'));
    await settle();
    expect(h.vm.view).toBe('users');

    window.history.pushState({}, '', '/config');
    window.dispatchEvent(new PopStateEvent('popstate'));
    await settle();
    expect(h.vm.view).toBe('app-config');
  });

  it('does not load while signed out and selects the allowed tab for each page', async () => {
    h = await consoleHarness({ permissions: 'textbook-submissions=r,resource-submissions=r', respond: ({ url }) => (url.endsWith('/admin/me') ? { body: { permissions: 'textbook-submissions=r,resource-submissions=r' } } : answer({ url })) });
    await settle();
    h.vm.switchView('textbooks');
    await settle();
    expect(h.vm.textbookTab).toBe('review');
    h.vm.switchView('resources');
    await settle();
    expect(h.vm.resourceTab).toBe('review');
    h.vm.switchView('library');
    await settle();
    expect(h.vm.libraryTab).toBe('publish');
    await h.vm.logout();
    h.clearRequests();
    await h.vm.load();
    expect(h.requests).toEqual([]);
  });

  it('permits the library and app release tabs when the module is writable', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    h.vm.switchView('textbooks');
    await settle();
    expect(h.vm.textbookTab).toBe('publish');
    h.vm.switchView('resources');
    await settle();
    expect(h.vm.resourceTab).toBe('publish');
    h.vm.switchView('app-config');
    await settle();
    expect(h.vm.appConfigTab).toBe('release');
  });

  it('renders the shared table footer with localized totals', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    expect(h.footer).toBeTruthy();
    const host = document.createElement('div');
    document.body.appendChild(host);
    const app = Vue.createApp({ render: () => Vue.h(h.footer, { total: 12345, length: 3, page: 2, unit: '条' }) });
    app.mount(host);
    expect(host.textContent).toContain('12,345');
    expect(host.textContent).toContain('条');
    app.unmount();
    host.remove();
    const single = document.createElement('div');
    document.body.appendChild(single);
    const second = Vue.createApp({ render: () => Vue.h(h.footer, { total: 0, length: 1 }) });
    second.mount(single);
    expect(single.textContent).toContain('0');
    second.unmount();
    single.remove();
  });
});

describe('formatters and small helpers', () => {
  it('labels statuses, risk types, events and audit actions with dash fallbacks', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    expect(h.vm.valueOrDash(undefined)).toBe('-');
    expect(h.vm.valueOrDash('')).toBe('-');
    expect(h.vm.valueOrDash('2026-01-01T00:00:00Z')).toBe('2026-01-01 08:00:00');
    expect(h.vm.valueOrDash('2026-01-01T00:00:00.500Z')).toBe('2026-01-01 08:00:00');
    expect(h.vm.valueOrDash('纯文本')).toBe('纯文本');
    expect(h.vm.formatDateTime('2026-13-99')).toBe('2026-13-99');
    expect(h.vm.formatDateTime(0)).toBe('1970-01-01 08:00:00');
    expect(h.vm.courseCodesText(['C1', '', 'C2'])).toBe('C1、C2');
    expect(h.vm.courseCodesText('C1')).toBe('-');
    expect(h.vm.statusColor('active')).toBe('success');
    expect(h.vm.statusColor('other')).toBe('secondary');
    expect(h.vm.statusLabel('used')).toBe('已兑换');
    expect(h.vm.statusLabel('other')).toBe('other');
    expect(h.vm.riskTypeLabel('account_device_burst')).toBe('账号设备过多');
    expect(h.vm.riskTypeLabel('other')).toBe('other');
    expect(h.vm.eventTypeLabel('app_start')).toBe('启动');
    expect(h.vm.eventTypeLabel('other')).toBe('other');
    expect(h.vm.actionLabel('admin_login')).toBe('管理员登录');
    expect(h.vm.actionLabel('other')).toBe('other');
    expect(h.vm.riskStatusColor({ acknowledged_at: 'x' })).toBe('success');
    expect(h.vm.riskStatusColor({})).toBe('warning');
    expect(h.vm.bankPageCount).toBe(1);
    h.vm.bankTotal = 33;
    expect(h.vm.bankPageCount).toBe(3);
    h.vm.setBankDialog(true);
    expect(h.vm.bankDialog).toBe(true);
    h.vm.setBankDialog(false);
    expect(h.vm.bankDialog).toBe(false);
    expect(h.vm.pageCount([1, 2, 3])).toBe(1);
    expect(h.vm.pagedSlice([1, 2, 3], 5)).toEqual([1, 2, 3]);
    expect(h.vm.pagedSlice([], 0)).toEqual([]);
    expect(h.vm.booksLabel([{ name: '书', tags: '标签' }, { name: '另一本' }])).toBe('书 / 标签；另一本');
    expect(h.vm.booksLabel(null)).toBe('');
  });

  it('renders share code JSON with inline arrays and empty containers', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    await h.vm.openShareCodeDetail({ id: 's1' });
    expect(h.vm.shareCodeDetail.type).toBe('schedule');
    expect(h.vm.shareCodeDetailJSON).toContain('"weeks": [1, 2]');
    expect(h.vm.shareCodeDetailJSON).toContain('"empty": []');
    expect(h.vm.shareCodeDetailJSON).toContain('"nested": {}');
    expect(h.vm.shareCodeDetailJSON).toContain('"mixed": [\n');
  });
});

describe('administrator and record maintenance', () => {
  it('creates, edits and deletes administrators with validation and cancellation', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    await h.vm.loadAdmins();
    expect(h.vm.admins).toHaveLength(1);
    h.vm.openNewAdmin();
    expect(h.vm.adminDialog).toBe(true);
    await h.vm.saveAdmin();
    expect(h.vm.error).toBe('请填写管理密钥');
    h.vm.adminForm.key = 'short';
    await h.vm.saveAdmin();
    expect(h.vm.error).toBe('管理密钥至少 10 位');
    h.vm.adminForm.key = 'long-enough-key';
    await h.vm.saveAdmin();
    expect(h.vm.error).toBe('请至少授予一个模块的读权限');
    h.vm.adminPermsForm.users = 'rw';
    await h.vm.saveAdmin();
    const created = h.requests.find(request => request.method === 'POST' && request.url.endsWith('/admins'));
    expect(JSON.parse(created.body)).toEqual({ key: 'long-enough-key', permissions: 'users=rw' });
    expect(h.vm.snackbar.text).toBe('管理员已创建');

    h.vm.editAdmin({ raw: { id: 'a1', permissions: 'users=r,school-calendar=rw' } });
    expect(h.vm.adminEditingId).toBe('a1');
    expect(h.vm.adminPermsForm['school-calendar']).toBe('rw');
    await h.vm.saveAdmin();
    expect(h.requests.some(request => request.method === 'PUT' && request.url.endsWith('/admins/a1'))).toBe(true);
    expect(h.vm.error).toBe('');

    h.vm.randomAdminKey();
    expect(h.vm.adminForm.key).toMatch(/^[0-9a-f]{32}$/);

    window.confirm.mockReturnValueOnce(false);
    await h.vm.deleteAdmin({ id: 'a1' });
    expect(h.requests.some(request => request.method === 'DELETE')).toBe(false);
    await h.vm.deleteAdmin({ super: true, id: 'root' });
    expect(h.requests.some(request => request.method === 'DELETE')).toBe(false);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.deleteAdmin({ id: 'a1' });
    expect(h.requests.some(request => request.method === 'DELETE' && request.url.endsWith('/admins/a1'))).toBe(true);
  });

  it('updates user, device, risk and error report state', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    h.vm.switchView('users');
    await settle();
    await h.vm.showUser({ id: 'u1' });
    h.vm.userStatus = 'disabled';
    await h.vm.setStatus({ id: 'u1' });
    expect(h.requests.some(request => request.method === 'PATCH' && request.url.endsWith('/users/u1/status'))).toBe(true);
    expect(h.vm.userDialog).toBe(false);

    window.confirm.mockReturnValueOnce(false);
    await h.vm.disableUser({ id: 'u1', status: 'active' });
    expect(h.requests.filter(request => request.url.endsWith('/users/u1/status')).length).toBe(1);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.disableUser({ id: 'u1', status: 'disabled' });
    expect(h.requests.filter(request => request.url.endsWith('/users/u1/status')).length).toBe(2);
    await h.vm.disableUser(null);
    expect(h.vm.error).toBe('');

    h.vm.switchView('risk');
    await settle();
    window.confirm.mockReturnValueOnce(false);
    await h.vm.setRiskDeviceStatus({ device_id: 'd1' });
    expect(h.requests.some(request => request.url.includes('/devices/d1/status'))).toBe(false);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.setRiskDeviceStatus({ device_id: 'd1', device_revoked_at: '2026-01-01' });
    expect(h.requests.some(request => request.method === 'PATCH' && request.url.includes('/devices/d1/status'))).toBe(true);
    await h.vm.setRiskDeviceStatus({});
    expect(h.vm.error).toBe('');

    window.confirm.mockReturnValueOnce(false);
    await h.vm.clearRiskEvents();
    expect(h.requests.some(request => request.method === 'DELETE' && request.url.includes('/risk-events'))).toBe(false);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.clearRiskEvents();
    expect(h.requests.some(request => request.method === 'DELETE' && request.url.includes('/risk-events'))).toBe(true);
    await h.vm.acknowledge({ id: 'k1' });
    expect(h.requests.some(request => request.method === 'PATCH' && request.url.endsWith('/risk-events/k1'))).toBe(true);

    h.vm.switchView('error-reports');
    await settle();
    window.confirm.mockReturnValueOnce(false);
    await h.vm.setErrorReportStudentIgnored({ id: 'e1', student_id: '20230001' });
    expect(h.requests.some(request => request.url.includes('/error-reports/e1'))).toBe(false);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.setErrorReportStudentIgnored({ id: 'e1', student_id: '20230001', ignored: true });
    const ignored = h.requests.find(request => request.method === 'PATCH' && request.url.endsWith('/error-reports/e1'));
    expect(ignored.body).toBe('{"ignored":false}');

    h.vm.errorReportsTotal = 0;
    await h.vm.clearErrorReports();
    expect(h.vm.snackbar).toMatchObject({ text: '暂无错误记录', color: 'info' });
    h.vm.errorReportsTotal = 4;
    window.confirm.mockReturnValueOnce(false);
    await h.vm.clearErrorReports();
    expect(h.requests.some(request => request.method === 'DELETE' && request.url.endsWith('/error-reports'))).toBe(false);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.clearErrorReports();
    expect(h.vm.errorReportsTotal).toBe(0);
    expect(h.vm.snackbar.text).toBe('已清空 0 条错误记录');
  });

  it('searches users with the unknown-field shortcuts and resets sorting', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    h.vm.switchView('users');
    await settle();
    h.vm.userSearch = '学院未知';
    await h.vm.searchUsers();
    expect(h.requests.some(request => request.url.includes('&empty=college'))).toBe(true);
    h.vm.userSearch = '班级未知';
    await h.vm.searchUsers();
    expect(h.requests.some(request => request.url.includes('&empty=class'))).toBe(true);
    h.vm.userSearch = '张三';
    await h.vm.searchUsers();
    expect(h.requests.some(request => request.url.includes('&q=%E5%BC%A0%E4%B8%89'))).toBe(true);
    await h.vm.sortUsers([]);
    expect(h.vm.userSortBy).toEqual([{ key: 'last_login_at', order: 'desc' }]);
    await h.vm.sortUsers([{ key: 'college_name', order: 'asc' }]);
    expect(h.requests.some(request => request.url.includes('sort=college_name&order=asc'))).toBe(true);
  });

  it('toggles CDKs and question banks with confirmation and archive paths', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    window.confirm.mockReturnValueOnce(false);
    await h.vm.setCDKStatus({ id: 'c1', status: 'active' });
    expect(h.requests.some(request => request.url.includes('/library-cdks/c1'))).toBe(false);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.setCDKStatus({ id: 'c1', status: 'active' });
    expect(h.requests.some(request => request.method === 'PATCH' && request.url.endsWith('/library-cdks/c1'))).toBe(true);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.setCDKStatus({ id: 'c2', status: 'disabled' });
    expect(h.requests.find(request => request.method === 'PATCH' && request.url.endsWith('/library-cdks/c2')).body).toBe('{"status":"active"}');

    window.confirm.mockReturnValueOnce(false);
    await h.vm.setBankStatus({ id: 'b1', status: 'active', name: '题库' });
    expect(h.requests.some(request => request.url.includes('/question-banks/b1/status'))).toBe(false);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.setBankStatus({ id: 'b1', status: 'active', name: '题库' });
    expect(h.requests.find(request => request.method === 'PATCH' && request.url.endsWith('/question-banks/b1/status')).body).toBe('{"status":"disabled"}');

    window.confirm.mockReturnValueOnce(false);
    await h.vm.removeBank({ id: 'b1', name: '题库' });
    expect(h.requests.some(request => request.method === 'DELETE')).toBe(false);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.removeBank({ id: 'b1', name: '题库' });
    expect(h.requests.some(request => request.method === 'DELETE' && request.url.endsWith('/question-banks/b1'))).toBe(true);

    await h.vm.setBankArchived({ id: 'b1' }, true);
    expect(h.requests.some(request => request.url.endsWith('/question-banks/b1/archive'))).toBe(true);
    await h.vm.setBankArchived({ id: 'b1' }, false);
    expect(h.requests.some(request => request.url.endsWith('/question-banks/b1/restore'))).toBe(true);
  });

  it('rejects a share payload that is not a JSON object', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    h.vm.openShareCodeDialog();
    h.vm.shareCodeJSON = '[1,2]';
    await h.vm.createShareCode();
    expect(h.vm.error).toBe('分享数据必须是 JSON 对象');
    h.vm.shareCodeJSON = '42';
    await h.vm.createShareCode();
    expect(h.vm.error).toBe('分享数据必须是 JSON 对象');
  });
});
