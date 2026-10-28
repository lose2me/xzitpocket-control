import { afterEach, describe, expect, it, vi } from 'vitest';
import { consoleHarness, settle } from './harness.js';

let h;
afterEach(() => h?.close());
const row = { id: 'record/1', name: '测试记录', code: 'C1', status: 'active' };
const answer = ({ url, method = 'GET' }) => {
  if (url.endsWith('/admin/me')) return { body: { name: '测试管理员', permissions: '*=rw' } };
  if (url.endsWith('/session')) return { body: { access_token: 'new-token', name: '测试管理员', permissions: '*=rw' } };
  if (url.includes('/users/') && !url.endsWith('/status')) return { body: { user: row, devices: [] } };
  if (url.includes('/question-banks/') && method === 'GET') return { body: { questionBank: { ...row, courseCodes: ['C1'], questions: [] } } };
  if (url.includes('/share-codes/') && method === 'GET') return { body: { code: '123456', data: { courses: [{ weeks: [1, 2], sessions: [1, 2] }], nested: [[{ a: null }]] } } };
  return { body: { items: [row], total: 1, deleted: 1 } };
};

describe('console authentication and permissions', () => {
  it('logs in, persists identity, signs out and removes stored credentials', async () => {
    h = await consoleHarness({ authenticated: false, respond: answer });
    expect(h.vm.logged).toBe(false);
    h.vm.loginForm.key = 'admin-key';
    await h.vm.login();
    expect(h.vm.logged).toBe(true);
    expect(localStorage.getItem('control_admin')).toBe('new-token');
    expect(h.vm.adminName).toBe('测试管理员');
    await h.vm.logout();
    expect(h.vm.logged).toBe(false);
    expect(localStorage.getItem('control_admin')).toBeNull();
  });

  it.each([401, 403, 500])('reports HTTP %i and clears only expired sessions', async status => {
    h = await consoleHarness({ respond: answer });
    h.respond(() => ({ status, body: { error: { code: 'denied', message: '访问失败' } } }));
    await expect(h.vm.loadCourses()).rejects.toMatchObject({ status, code: 'denied', message: '访问失败' });
    await h.vm.sortCourses([]);
    expect(h.vm.logged).toBe(status !== 401);
    expect(h.vm.error).toBe(status === 401 ? '' : '访问失败');
    expect(h.vm.loading).toBe(false);
  });

  it('handles invalid JSON, failed login and network failures without sticking loading', async () => {
    h = await consoleHarness({ authenticated: false, respond: () => ({ status: 500, body: 'invalid-json' }) });
    await h.vm.login();
    expect(h.vm.error).toBe('请求失败');
    expect(h.vm.loading).toBe(false);
    h.respond(() => new Error('网络中断'));
    await h.vm.login();
    expect(h.vm.error).toBe('网络中断');
    expect(h.vm.logged).toBe(false);
  });

  it('limits navigation and setting writes to independent granted modules', async () => {
    h = await consoleHarness({ permissions: 'school-calendar=r,settings-ai=rw', respond: ({url}) => ({ body: url.endsWith('/me') ? { permissions: 'school-calendar=r,settings-ai=rw' } : {} }) });
    expect(h.vm.canRead('school-calendar')).toBe(true);
    expect(h.vm.canWrite('school-calendar')).toBe(false);
    expect(h.vm.canRead('admins')).toBe(false);
    expect(h.vm.visibleNav.map(n => n.key)).toEqual(['app-config', 'web-config']);
    h.vm.switchView('admins');
    await settle();
    expect(h.vm.view).toBe('app-config');
    expect(h.vm.appConfigTab).toBe('calendar');
    expect(h.vm.adminPermSummary(null)).toBe('-');
    expect(h.vm.adminPermSummary({ super: true })).toContain('全部');
    expect(h.vm.adminPermSummary({ permissions: '' })).toBe('无权限');
    expect(h.vm.adminPermSummary({ permissions: 'school-calendar=r,settings-ai=rw' })).toContain('只读');
  });
});

describe('content and account operations', () => {
  it('creates, edits, validates and deletes courses', async () => {
    h = await consoleHarness({ respond: answer });
    h.vm.openNewCourse();
    await h.vm.saveCourse();
    expect(h.vm.error).toContain('编号');
    h.vm.courseForm.code = 'C1';
    await h.vm.saveCourse();
    expect(h.vm.error).toContain('名称');
    h.vm.courseForm.name = '课程';
    document.cookie = 'control_csrf=csrf%20token';
    await h.vm.saveCourse();
    const created = h.requests.find(r => r.method === 'POST' && r.url.endsWith('/courses'));
    expect(JSON.parse(created.body)).toEqual({ code: 'C1', name: '课程' });
    expect(created.headers['X-CSRF-Token']).toBe('csrf token');
    h.vm.editCourse({ raw: row });
    await h.vm.saveCourse();
    expect(h.requests.some(r => r.method === 'PUT' && r.url.endsWith('record%2F1'))).toBe(true);
    window.confirm.mockReturnValueOnce(false);
    h.clearRequests();
    await h.vm.deleteCourse(row);
    expect(h.requests).toHaveLength(0);
    await h.vm.deleteCourse(row);
    expect(h.requests[0].method).toBe('DELETE');
    expect(h.vm.booksLabel([{ name: '书', tags: '标签' }, { name: '另一本' }])).toBe('书 / 标签；另一本');
    expect(h.vm.booksLabel()).toBe('');
  });

  it('loads, searches and sorts all paginated content with encoded queries', async () => {
    h = await consoleHarness({ respond: answer });
    const scenarios = [
      ['loadBanks', 'sortBanks', 'bankPage'], ['loadCDKs', 'sortCDKs', 'cdkPage'],
      ['loadCourses', 'sortCourses', 'coursePage'], ['loadTextbooks', 'sortTextbooks', 'textbookPage'],
      ['loadShareCodes', 'sortShareCodes', 'shareCodePage'], ['loadErrorReports', 'sortErrorReports', 'errorReportsPage'],
      ['loadUsers', 'sortUsers', 'userPage'],
    ];
    for (const [load, sort, page] of scenarios) {
      h.vm[page] = 3;
      await h.vm[load]();
      await h.vm[sort]([{ key: 'name', order: 'asc' }]);
      expect(h.vm[page]).toBe(1);
      expect(h.requests.at(-1).url).toContain('sort=');
      await h.vm[sort](null);
    }
    for (const key of ['courseSearch', 'textbookSearch', 'cdkSearch', 'userSearch']) h.vm[key] = 'C 1/中文';
    for (const search of ['searchCourses', 'searchTextbooks', 'searchCDKs', 'searchUsers']) await h.vm[search]();
    expect(h.requests.some(r => r.url.includes('q=C%201%2F'))).toBe(true);
    h.vm.filterUsersBy('学院', 'college_name');
    await settle();
    expect(h.vm.userSearch).toBe('学院');
    expect(h.vm.pageCount(null)).toBe(1);
    expect(h.vm.pagedSlice([1, 2, 3], 99, 2)).toEqual([3]);
    expect(h.vm.pagedSlice(null, -1)).toEqual([]);
    expect(h.vm.pagedSlice([1], 0, 0)).toEqual([1]);
  });

  it('validates bank JSON, saves current course arrays and preserves edit IDs only in URLs', async () => {
    h = await consoleHarness({ respond: answer });
    await h.vm.openNewBank();
    expect(h.vm.bankDialog).toBe(true);
    for (const text of ['invalid', '[]', 'null', '{}']) {
      h.vm.bankJSON = text;
      await h.vm.saveBank();
      expect(h.vm.error).not.toBe('');
    }
    h.vm.bankJSON = JSON.stringify({ questionBank: { name: '题库', courseCode: 'old' } });
    h.vm.bankCourseCodes = [' C1 ', ''];
    await h.vm.saveBank();
    const post = h.requests.find(r => r.method === 'POST' && r.url.endsWith('/question-banks'));
    expect(JSON.parse(post.body).questionBank).toEqual({ name: '题库', courseCodes: ['C1'] });
    await h.vm.editBank(row);
    await h.vm.saveBank();
    expect(h.requests.some(r => r.method === 'PUT' && r.url.endsWith('record%2F1'))).toBe(true);
    h.vm.bankEditing = true;
    h.vm.bankJSON = '{"name":"题库"}';
    await h.vm.saveBank();
    expect(h.vm.error).toContain('ID');
    h.vm.setBankDialog(false);
    expect(h.vm.bankDialog).toBe(false);
  });

  it('performs status, archive, restore and delete operations with cancellation', async () => {
    h = await consoleHarness({ path: '/courses', respond: answer });
    for (const action of ['setBankStatus', 'setCDKStatus', 'disableUser']) {
      await h.vm[action](row);
      expect(JSON.parse(h.requests.findLast(r => r.method === 'PATCH').body).status).toBe('disabled');
      await h.vm[action]({ ...row, status: 'disabled' });
      expect(JSON.parse(h.requests.findLast(r => r.method === 'PATCH').body).status).toBe('active');
    }
    for (const action of ['setBankArchived', 'setTextbookArchived', 'setResourceArchived']) {
      await h.vm[action](row, true);
      expect(h.requests.some(r => r.url.endsWith('/archive'))).toBe(true);
      await h.vm[action](row, false);
      expect(h.requests.some(r => r.url.endsWith('/restore'))).toBe(true);
    }
    for (const action of ['removeBank', 'deleteTextbook', 'deleteResource', 'deleteShareCode', 'deleteAdmin']) {
      await h.vm[action](row);
      expect(h.requests.some(r => r.method === 'DELETE')).toBe(true);
      h.clearRequests();
      window.confirm.mockReturnValueOnce(false);
      await h.vm[action](row);
      expect(h.requests).toHaveLength(0);
    }
    await h.vm.disableUser(null);
    await h.vm.deleteAdmin({ super: true });
  });

  it('manages user details, risk devices, error reporting and cleanup', async () => {
    h = await consoleHarness({ path: '/courses', respond: answer });
    await h.vm.showUser(row);
    expect(h.vm.userDialog).toBe(true);
    await h.vm.setStatus(row);
    expect(h.vm.selectedUser).toBeNull();
    await h.vm.setRiskDeviceStatus({});
    for (const device_revoked_at of ['', '2026-01-01']) {
      await h.vm.setRiskDeviceStatus({ device_id: 'd1', device_revoked_at });
      expect(h.requests.some(r => r.url.includes('/devices/d1/status'))).toBe(true);
    }
    await h.vm.acknowledge(row);
    await h.vm.clearRiskEvents();
    for (const ignored of [false, true]) await h.vm.setErrorReportStudentIgnored({ ...row, student_id: '20260001', ignored });
    h.vm.errorReportsTotal = 0;
    h.clearRequests();
    await h.vm.clearErrorReports();
    expect(h.requests).toHaveLength(0);
    h.vm.errorReportsTotal = 3;
    await h.vm.clearErrorReports();
    expect(h.vm.errorReportsTotal).toBe(0);
  });

  it('creates share codes, renders configuration JSON and clears deleted rows', async () => {
    h = await consoleHarness({ respond: answer });
    h.vm.openShareCodeDialog();
    h.vm.shareCodeJSON = 'bad-json';
    await h.vm.createShareCode();
    expect(h.vm.error).not.toBe('');
    h.vm.shareCodeJSON = '{"theme":"dark"}';
    await h.vm.createShareCode();
    expect(h.requests.some(r => r.method === 'POST' && r.url.endsWith('/share-codes'))).toBe(true);
    await h.vm.openShareCodeDetail(row);
    expect(JSON.parse(h.vm.shareCodeDetailJSON)).toEqual(h.vm.shareCodeDetail.data);
    expect(h.vm.shareCodeDetailJSON).toContain('"weeks": [1, 2]');
  });

  it('generates bounded CDKs and handles clipboard denial', async () => {
    h = await consoleHarness({ respond: answer });
    h.vm.createdCDKs = [];
    await h.vm.copyCDK();
    h.vm.openNewCDK();
    await settle();
    for (const count of [0, 2, 900]) {
      h.vm.cdkForm.count = count;
      await h.vm.createCDK();
      const request = h.requests.findLast(r => r.method === 'POST' && r.url.endsWith('/library-cdks'));
      expect(JSON.parse(request.body).count).toBe(Math.max(1, Math.min(500, count)));
    }
    h.vm.createdCDKs = [{ code: '123' }, { code: '456' }, {}];
    await h.vm.copyCDK();
    expect(navigator.clipboard.writeText).toHaveBeenLastCalledWith('123\n456');
    navigator.clipboard.writeText.mockRejectedValueOnce(new Error('denied'));
    await h.vm.copyCDK();
    expect(h.vm.snackbar.color).toBe('warning');
  });
});
