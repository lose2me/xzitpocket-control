import { afterEach, describe, expect, it, vi } from 'vitest';
import { consoleHarness, settle } from './harness.js';

let h;
afterEach(() => {
  h?.close();
  delete window.MathJax;
  document.head.querySelectorAll('script').forEach(script => script.remove());
});

const answer = ({ url, method = 'GET' }) => {
  if (url.endsWith('/admin/me')) return { body: { permissions: '*=rw' } };
  if (url.includes('/ai-analyses/') && method === 'GET' && !url.includes('?')) {
    return { body: { prompts: JSON.stringify([
      { kind: 'precheck', messages: [
        { role: 'system', content: '系统' },
        { role: 'user', content: '请判断它是否是可用于作答的试卷，只输出 JSON' },
        { role: 'assistant', content: '```json\n{"solvable":true,"reason":"ok"}\n```' },
      ] },
      { kind: 'analyze', messages: [
        { role: 'user', content: '请判断它是否是可用于作答的试卷，只输出 JSON' },
        { role: 'assistant', content: '{"solvable":false,"reason":"no"}' },
        { role: 'assistant', content: '{"solvable":"yes","reason":1}' },
        { role: 'assistant', content: 'not json' },
        { role: 'assistant', content: '{}' },
        { role: 'user', content: '正式解析请求' },
      ] },
    ]) } };
  }
  if (method === 'POST' && url.endsWith('/ai-analyses')) return { body: { items: [{ id: 't9' }], failed: [{ reason: '文件缺失' }] } };
  return { body: { items: [], total: 0, deleted: 1 } };
};

describe('ai analysis helpers', () => {
  it('labels task state, progress, solvability and elapsed time', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    expect(h.vm.aiDoneIcon(true)).toBe('mdi-check-circle');
    expect(h.vm.aiDoneIcon(false)).toBe('mdi-minus-circle-outline');
    expect(h.vm.aiDoneColor(true)).toBe('success');
    expect(h.vm.aiDoneColor(0)).toBe('grey');
    expect(h.vm.aiSolvableLabel(1)).toBe('可解析');
    expect(h.vm.aiSolvableLabel(0)).toBe('不可解析');
    expect(h.vm.aiSolvableLabel(null)).toBe('未检测');
    expect(h.vm.aiSolvableColor(1)).toBe('success');
    expect(h.vm.aiSolvableColor(0)).toBe('error');
    expect(h.vm.aiSolvableColor(2)).toBe('grey');
    expect(h.vm.aiStatusLabel('paused')).toBe('已暂停');
    expect(h.vm.aiStatusLabel('unknown')).toBe('unknown');
    expect(h.vm.aiElapsedLabel({ elapsed_seconds: -3 })).toBe('0s');
    expect(h.vm.aiElapsedLabel({})).toBe('—');
    expect(h.vm.analysisProgressLabel({ raw: { analyzed_count: 2, analyzable_count: undefined } })).toBe('2/0');
    expect(h.vm.analysisProgressColor({ analyzable_count: 0 })).toBe('grey');
    expect(h.vm.analysisProgressColor({ analyzable_count: 4, all_analyzed: true })).toBe('success');
    expect(h.vm.analysisProgressColor({ analyzable_count: 4, all_analyzed: false })).toBe('warning');
  });

  it('picks the newest task per resource and derives the batch action label', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    h.vm.aiTasks = [
      { id: 1, resource_id: 'r1', status: 'failed', created_at: '2026-01-01T00:00:00Z' },
      { id: 2, resource_id: 'r1', status: 'failed', created_at: '2026-01-02T00:00:00Z' },
      { id: 3, resource_id: 'r2', status: 'succeeded', created_at: 'invalid' },
      { id: 4, resource_id: 'r2', status: 'succeeded', created_at: 'invalid' },
    ];
    expect(h.vm.aiIsLatestTask({ resource_id: 'r1', id: 2 })).toBe(true);
    expect(h.vm.aiIsLatestTask({ resource_id: 'r1', id: 1 })).toBe(false);
    expect(h.vm.aiIsLatestTask({ resource_id: 'missing', id: 1 })).toBe(false);
    expect(h.vm.aiIsLatestTask({ resource_id: 'r2', id: 4 })).toBe(true);
    expect(h.vm.aiIsLatestOutput({ resource_id: 'r1', id: 2 })).toBe(false);
    expect(h.vm.aiIsLatestOutput({ resource_id: 'r1', id: 2, has_output: true })).toBe(true);
    expect(h.vm.aiIsLatestOutput({ resource_id: 'r1', id: 1, has_output: true })).toBe(false);
    expect(h.vm.aiIsLatestOutput({ resource_id: 'r9', id: 1, has_more_output: true })).toBe(false);

    expect(h.vm.aiActionLabel('none')).toBe('AI解析');
    const label = status => { h.vm.aiTasks = [{ id: 1, resource_id: 'r', status }]; return h.vm.aiActionLabel('r'); };
    expect(label('running')).toBe('解析中');
    expect(label('queued')).toBe('排队中');
    expect(label('paused')).toBe('继续解析');
    expect(label('succeeded')).toBe('重解');
    expect(label('failed')).toBe('重试');
    expect(label('cancelled')).toBe('AI解析');
  });

  it('queues, retries, cancels and downloads tasks, reporting partial failures', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    await h.vm.enqueueAIItem({ id: 'r1' });
    expect(h.requests.find(r => r.method === 'POST' && r.url.endsWith('/ai-analyses')).body).toBe('{"resource_ids":["r1"]}');
    expect(h.vm.snackbar).toMatchObject({ show: true, color: 'warning' });
    expect(h.vm.snackbar.text).toContain('失败 1 个');

    h.vm.aiTasks = [{ id: 'paused-1', resource_id: 'r2', status: 'paused' }];
    await h.vm.enqueueAIItem({ id: 'r2' });
    expect(h.requests.some(r => r.url.endsWith('/ai-analyses/paused-1/retry'))).toBe(true);

    h.respond(() => ({ body: { items: [] } }));
    await h.vm.enqueueAIItem({ id: 'r3' });
    expect(h.vm.snackbar.text).toBe('已加入 0 个 AI 解析任务');

    window.confirm.mockReturnValueOnce(false);
    await h.vm.reanalyzeAITask({ resource_id: 'r3', resource_name: '期中' });
    expect(h.requests.filter(r => r.method === 'POST' && r.url.endsWith('/ai-analyses')).length).toBe(2);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.reanalyzeAITask({ resource_id: 'r3', resource_name: '期中' });
    expect(h.requests.filter(r => r.method === 'POST' && r.url.endsWith('/ai-analyses')).length).toBe(3);

    await h.vm.retryAITask({ id: 't1' });
    expect(h.requests.some(r => r.url.endsWith('/ai-analyses/t1/retry'))).toBe(true);

    window.confirm.mockReturnValueOnce(false);
    await h.vm.cancelAITask({ id: 't2' });
    expect(h.requests.some(r => r.url.endsWith('/t2/cancel'))).toBe(false);
    window.confirm.mockReturnValueOnce(true);
    await h.vm.cancelAITask({ id: 't2' });
    expect(h.requests.some(r => r.url.endsWith('/t2/cancel'))).toBe(true);

    await h.vm.downloadAITask({ id: 't3', resource_name: '期末' }, 'more');
    expect(h.requests.some(r => r.url.endsWith('/t3/download?mode=more'))).toBe(true);
    await h.vm.downloadAITask({ id: 't4' });
    expect(h.requests.some(r => r.url.endsWith('/t4/download?mode=basic'))).toBe(true);
  });

  it('polls the AI task list only on the resources page while tasks are active', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    h.vm.aiTasks = [{ id: 1, resource_id: 'r1', status: 'succeeded' }];
    h.clearRequests();
    await vi.advanceTimersByTimeAsync(3000);
    expect(h.requests.filter(r => r.url.includes('/ai-analyses?limit')).length).toBe(0);

    h.vm.switchView('resources');
    await settle();
    h.vm.aiTasks = [{ id: 1, resource_id: 'r1', status: 'running' }];
    h.clearRequests();
    await vi.advanceTimersByTimeAsync(1000);
    await settle();
    expect(h.requests.some(r => r.url.includes('/ai-analyses?limit'))).toBe(true);

    h.respond(() => ({ status: 401, body: { error: { message: '过期' } } }));
    h.vm.aiTasks = [{ id: 1, resource_id: 'r1', status: 'running' }];
    await vi.advanceTimersByTimeAsync(1000);
    await settle();
    expect(h.vm.logged).toBe(false);

    h.respond(() => ({ status: 500, body: { error: { message: '坏了' } } }));
    h.vm.logged = true;
    h.vm.aiTasks = [{ id: 1, resource_id: 'r1', status: 'queued' }];
    await vi.advanceTimersByTimeAsync(1000);
    await settle();
    expect(h.vm.logged).toBe(true);
  });
});

describe('ai prompt viewer', () => {
  it('parses stored calls and hides precheck messages from the active analyze call', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    const el = document.createElement('pre');
    h.vm.promptViewEl = el;
    window.MathJax = { typesetPromise: vi.fn(async () => {}) };
    await h.vm.openAIPrompts({ id: 't1', resource_name: '期中试卷' });
    await settle();
    expect(h.vm.aiPromptDialog).toBe(true);
    expect(h.vm.aiPromptTitle).toBe('期中试卷');
    expect(h.vm.aiPromptTab).toBe('0');
    expect(h.vm.aiPromptCalls).toHaveLength(2);
    expect(h.vm.aiPromptActiveCall.messages.map(message => message.content)).toEqual(['系统', '请判断它是否是可用于作答的试卷，只输出 JSON', '```json\n{"solvable":true,"reason":"ok"}\n```']);
    expect(window.MathJax.typesetPromise).toHaveBeenCalledWith([el]);
    h.vm.aiPromptTab = '1';
    expect(h.vm.aiPromptActiveCall.messages.map(message => message.content)).toEqual([
      '{"solvable":"yes","reason":1}',
      'not json',
      '{}',
      '正式解析请求',
    ]);
    h.vm.aiPromptTab = '99';
    expect(h.vm.aiPromptActiveCall).toBeNull();
    expect(window.MathJax.typesetPromise).toHaveBeenCalledTimes(1);
    h.vm.aiPromptDialog = false;
    await settle();
    h.vm.aiPromptTab = 'json';
    await h.vm.renderAIPrompts();
    expect(window.MathJax.typesetPromise).toHaveBeenCalledTimes(1);
    h.vm.promptViewEl = null;
    h.vm.aiPromptTab = '0';
    await h.vm.renderAIPrompts();
  });

  it('keeps raw prompt text when the stored value is not a call array', async () => {
    h = await consoleHarness({ respond: ({ url, method = 'GET' }) => (url.includes('/ai-analyses/') && method === 'GET' ? { body: { prompts: 'not-json' } } : answer({ url, method })) });
    await settle();
    await h.vm.openAIPrompts({ id: 't1' });
    expect(h.vm.aiPromptCalls).toEqual([]);
    expect(h.vm.aiPromptText).toBe('not-json');
    expect(h.vm.aiPromptTab).toBe('json');
    expect(h.vm.aiPromptTitle).toBe('Prompt');
  });

  it('falls back to the JSON tab when no prompts are stored', async () => {
    h = await consoleHarness({ respond: ({ url, method = 'GET' }) => (url.includes('/ai-analyses/') && method === 'GET' ? { body: {} } : answer({ url, method })) });
    await settle();
    await h.vm.openAIPrompts({ id: 't1' });
    expect(h.vm.aiPromptText).toBe('');
    expect(h.vm.aiPromptTab).toBe('json');
  });

  it('loads MathJax once, resolving when the script fails or has no startup promise', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    h.vm.promptViewEl = document.createElement('pre');
    await h.vm.openAIPrompts({ id: 't1' });
    await settle();
    const script = document.head.querySelector('script[src="/vendor/js/mathjax-tex-svg.js"]');
    expect(script).not.toBeNull();
    script.onload();
    await settle();
    expect(window.MathJax.options.enableMenu).toBe(false);

    h.vm.aiPromptTab = 'json';
    h.vm.aiPromptTab = '0';
    await h.vm.renderAIPrompts();
    expect(document.head.querySelectorAll('script[src="/vendor/js/mathjax-tex-svg.js"]')).toHaveLength(1);

    window.MathJax.startup = { promise: Promise.reject(new Error('typeset failed')) };
    script.onload();
    await settle();
  });

  it('resolves the MathJax load through the startup promise and survives typeset failures', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    h.vm.promptViewEl = document.createElement('pre');
    const opening = h.vm.openAIPrompts({ id: 't1' });
    await settle();
    const script = document.head.querySelector('script[src="/vendor/js/mathjax-tex-svg.js"]');
    window.MathJax.startup.promise = Promise.resolve();
    window.MathJax.typesetPromise = vi.fn(async () => { throw new Error('渲染失败'); });
    script.onload();
    await opening;
    await settle();
    expect(window.MathJax.typesetPromise).toHaveBeenCalled();
  });

  it('resolves the MathJax load when the script cannot be fetched', async () => {
    h = await consoleHarness({ respond: answer });
    await settle();
    h.vm.promptViewEl = document.createElement('pre');
    const opening = h.vm.openAIPrompts({ id: 't1' });
    await settle();
    const script = document.head.querySelector('script[src="/vendor/js/mathjax-tex-svg.js"]');
    script.onerror();
    await opening;
    await settle();
    expect(window.MathJax.typesetPromise).toBeUndefined();
  });
});
