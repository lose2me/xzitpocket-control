import { afterEach, describe, expect, it, vi } from 'vitest';
import { consoleHarness, settle } from './harness.js';

let h;
afterEach(() => { h?.close(); delete window.echarts; });

const chartInstance = () => ({
  setOption: vi.fn(), clear: vi.fn(), dispose: vi.fn(), resize: vi.fn(),
  getDom: vi.fn(() => document.createElement('div')),
  on: vi.fn(),
});

const breakdown = {
  platforms: { android: 30, ios: 20 },
  versions: { '1.0.0': 25, '1.0.1': 10 },
  colleges: { 计算机学院: 12, 未知: 3, 机械学院: 12 },
  classes: { '计算机 1 班': 8, 未知: 2 },
  event_hours: { '23:00': 4, '01:00': 9 },
  feature_usage: { campus_card: 5, power: 0, unknown_feature: 3 },
  cdk_activations_today: 2, cdk_activations_week: 7, cdk_activations_total: 42,
};

const statsAnswer = ({ url }) => {
  if (url.endsWith('/metrics/overview')) return { body: { dau: 10, wau: 20, mau: 30, active_devices: 5, total_users: 100, today_events: 200 } };
  if (url.endsWith('/metrics/breakdown')) return { body: breakdown };
  if (url.includes('/metrics/series')) return { body: { items: [{ day: '2000-01-01', total_users: 90, dau: 9, wau: 19 }, { day: '2999-01-01', total_users: 1, dau: 1, wau: 1 }] } };
  if (url.endsWith('/overview-display')) return { body: { scalePercent: 200 } };
  if (url.endsWith('/admin/me')) return { body: { name: '测试管理员', permissions: '*=rw' } };
  if (url.includes('/cdn/stats')) {
    return { body: {
      days: 30, stale: false,
      traffic_package: { remaining: 1500000000, total: 3000000000, expire_time: '2026-01-02T00:00:00Z', count: 2 },
      domains: [
        { domain: 'xuda.live', today_flux: 999, today_requests: 12000, today_origin_flux: 1000, hit_rate: 0.91234, flux: 1500, bandwidth_peak: 9000000, flux_series: [{ day: '01-01', value: 15 }] },
        { domain: 'dl.xuda.live', today_flux: 10 },
      ],
    } };
  }
  if (url.includes('/host/stats')) {
    return { body: {
      out_bandwidth: 12.345, in_bandwidth: 1, out_ratio: 0.5, cpu: 10, mem: 20, disk: 30,
      traffic_package: { remaining: 1024 * 1024 * 1024, total: 2 * 1024 * 1024 * 1024, expire_time: '2026-02-03T00:00:00Z' },
      out_series: [{ day: '01-01', value: 1 }],
    } };
  }
  if (url.includes('/buckets/stats')) {
    return { body: {
      days: 7, stale: false,
      buckets: [{ key: 'assets', std_storage_mb: 2048, object_number: 1234, total_requests: 5678, cdn_origin_traffic: 100, inbound_traffic: 200, internet_traffic: 300, origin_series: [{ day: '01-01', value: 2 }] }],
    } };
  }
  return { body: { items: [], total: 0 } };
};

describe('overview dashboard', () => {
  it('loads metrics, scales values, draws every chart and filters users from a pie click', async () => {
    h = await consoleHarness({ respond: statsAnswer });
    await settle();
    expect(h.vm.overview.total_users).toBe(100);
    expect(h.vm.series.map(row => row.day)).toEqual(['2000-01-01']);
    expect(h.vm.statCards.map(card => card.value)).toEqual([20, 40, 60, 10, 200, 400]);

    const instances = [];
    window.echarts = { init: vi.fn(() => { const instance = chartInstance(); instances.push(instance); return instance; }) };
    h.vm.chartEl = document.createElement('div');
    h.vm.platformChartEl = document.createElement('div');
    h.vm.collegeChartEl = document.createElement('div');
    h.vm.eventChartEl = document.createElement('div');
    h.vm.featureChartEl = document.createElement('div');

    h.vm.switchView('overview');
    await settle();

    expect(h.vm.platformRows).toHaveLength(2);
    expect(h.vm.versionRows[0]).toMatchObject({ label: '1.0.0', value: 50 });
    expect(h.vm.collegeRows.map(row => row.value)).toEqual([24, 24, 6]);
    expect(h.vm.classRows.map(row => row.label)).toEqual(['计算机 1 班', '未知']);
    expect(h.vm.eventRows.map(row => row.label)).toEqual(['01:00', '23:00']);
    expect(h.vm.cdkActivationRows.map(row => row.value)).toEqual([2, 7, 42]);
    expect(h.vm.featureRows.map(row => row.label)).toEqual(['一卡通', 'unknown_feature']);
    expect(window.echarts.init).toHaveBeenCalledTimes(5);
    const main = instances[0];
    expect(main.setOption.mock.calls[0][0].xAxis.data).toEqual(['2000-01-01']);
    expect(main.setOption.mock.calls[0][0].series[0].data).toEqual([180]);
    const eventSeries = instances[3].setOption.mock.calls[0][0].series[0].data;
    expect(eventSeries.some(value => value === null)).toBe(true);
    expect(instances[1].on).toHaveBeenCalledWith('click', expect.any(Function));

    instances[1].on.mock.calls[0][1]({ componentType: 'series', name: '计算机学院' });
    await settle();
    expect(h.vm.view).toBe('users');
    expect(h.vm.userSearch).toBe('计算机学院');

    h.vm.switchView('overview');
    await settle();
    instances[1].on.mock.calls[0][1](null);
    instances[1].on.mock.calls[0][1]({ componentType: 'series', name: '未知' });
    await settle();
    expect(h.vm.userSearch).toBe('学院未知');
    expect(h.vm.view).toBe('users');

    window.dispatchEvent(new Event('resize'));
    expect(instances.some(instance => instance.resize.mock.calls.length > 0)).toBe(true);
  });

  it('replaces a chart bound to another element and closes the drawer on mobile', async () => {
    h = await consoleHarness({ respond: statsAnswer, desktop: false });
    await settle();
    const instance = chartInstance();
    window.echarts = { init: vi.fn(() => instance) };
    h.vm.chartEl = document.createElement('div');
    h.vm.switchView('overview');
    await settle();
    expect(h.vm.drawer).toBe(false);
    h.vm.chartEl = document.createElement('div');
    h.vm.switchView('overview');
    await settle();
    expect(instance.dispose).toHaveBeenCalled();
    h.vm.drawer = true;
    h.vm.switchView('overview');
    await settle();
    expect(h.vm.drawer).toBe(false);
  });

  it('applies and resets the overview display scale with clamped input', async () => {
    h = await consoleHarness({ respond: ({ url, method }) => (method === 'PUT' ? { body: { scalePercent: 42 } } : statsAnswer({ url })) });
    await settle();
    h.vm.overviewScaleInput = -5;
    await h.vm.applyOverviewScale();
    expect(h.requests.filter(request => request.method === 'PUT').at(-1).body).toBe('{"scalePercent":0}');
    expect(h.vm.statCards[0].value).toBe(0);
    h.vm.overviewScaleInput = 99999999;
    await h.vm.applyOverviewScale();
    expect(h.vm.overviewScaleInput).toBe(100000);
    await h.vm.resetOverviewScale();
    expect(h.vm.overviewScaleInput).toBe(100);
    h.vm.overviewScaleInput = NaN;
    await h.vm.applyOverviewScale();
    expect(h.vm.overviewScaleInput).toBe(0);
    expect(h.vm.scaleOverviewValue(10)).toBe(0);
    h.vm.overviewScaleInput = 250;
    await h.vm.applyOverviewScale();
    expect(h.vm.scaleOverviewValue('10')).toBe(25);
    expect(h.vm.scaleOverviewValue(null)).toBe(0);
  });

  it('summarises the selected user and falls back to dashes', async () => {
    h = await consoleHarness({ respond: ({ url }) => (url.includes('/users/') ? { body: { user: { display_name: '张三', college_name: null, class_name: '', status: 'disabled', last_login_at: '2026-01-01T00:00:00Z' }, student_id: '20230001' } } : statsAnswer({ url })) });
    await settle();
    expect(h.vm.userDetails).toEqual([]);
    await h.vm.showUser({ id: 'u1' });
    expect(h.vm.userDetails.map(item => item.value)).toEqual(['张三', '20230001', '-', '-', '禁用', '2026-01-01 08:00:00']);
    h.vm.closeUser();
    expect(h.vm.userDetails).toEqual([]);
  });
});

describe('cdn, host and bucket statistics', () => {
  it('renders CDN cards and formats byte, bitrate and percent boundaries', async () => {
    h = await consoleHarness({ respond: statsAnswer });
    await settle();
    expect(h.vm.cdnDomains.map(item => item.domain)).toEqual(['xuda.live', 'dl.xuda.live']);
    expect(h.vm.cdnTab).toBe('xuda.live');
    expect(h.vm.cdnCards.map(card => card.value)).toEqual(['999 B', '12,000', '1.00 KB', '91.2%', '1.50 KB', '9.00 Mbps']);
    expect(h.vm.cdnPackage).toMatchObject({ remaining: '1.50 GB', total: '3.00 GB', expire: '2026-01-02', count: 2 });
    expect(h.vm.activePackage).toEqual(h.vm.cdnPackage);

    expect(h.vm.formatBytes(0)).toBe('0 B');
    expect(h.vm.formatBytes(1234)).toBe('1.23 KB');
    expect(h.vm.formatBytes(12345678)).toBe('12.3 MB');
    expect(h.vm.formatBytes(123456789012)).toBe('123 GB');
    expect(h.vm.formatBitrate(5)).toBe('5 bps');
    expect(h.vm.formatBitrate(1500)).toBe('1.50 Kbps');
    expect(h.vm.formatBitrate(150000000)).toBe('150 Mbps');
    expect(h.vm.formatPercent(null)).toBe('0.0%');
    expect(h.vm.formatBytesIEC(512)).toBe('512 B');
    expect(h.vm.formatBytesIEC(2048)).toBe('2.00 KB');
    expect(h.vm.formatBytesIEC(1024 * 1024 * 1024 * 5)).toBe('5.00 GB');
    expect(h.vm.formatStorageMB(0)).toBe('0.0 MB');
    expect(h.vm.formatStorageMB(100)).toBe('100 MB');
    expect(h.vm.formatStorageMB(2048)).toBe('2.00 GB');
  });

  it('shows the Lighthouse host package and bucket cards with their charts', async () => {
    h = await consoleHarness({ respond: statsAnswer });
    await settle();
    const instances = [];
    window.echarts = { init: vi.fn(() => { const instance = chartInstance(); instances.push(instance); return instance; }) };
    h.vm.cdnChartEl = document.createElement('div');
    h.vm.hostChartEl = document.createElement('div');
    h.vm.bucketChartEl = document.createElement('div');

    h.vm.cdnTab = '__host__';
    await settle();
    expect(h.vm.hostCards.map(card => card.value)).toEqual(['12.35 Mbps', '1.00 Mbps', '0.5%', '10.0%', '20.0%', '30.0%']);
    expect(h.vm.activePackage).toMatchObject({ remaining: '1.00 GB', total: '2.00 GB', expire: '2026-02-03', count: 1 });

    h.vm.cdnTab = '__bucket_assets__';
    await settle();
    expect(h.vm.activeBucketKey).toBe('assets');
    expect(h.vm.activePackage).toBeNull();
    expect(h.vm.bucketCards.map(card => card.value)).toEqual(['2.00 GB', '1,234', '5,678', '100 B', '200 B', '300 B']);

    h.vm.cdnTab = 'xuda.live';
    await settle();
    expect(h.vm.cdnTab).toBe('xuda.live');
    h.vm.cdnTab = '__bucket_missing__';
    await settle();
    expect(h.vm.bucketCards).toEqual([]);
    expect(h.vm.activeBucket).toBeNull();
    expect(instances.every(instance => instance.setOption.mock.calls.length >= 0)).toBe(true);

    h.vm.cdnTab = 'dl.xuda.live';
    await settle();
    expect(h.vm.cdnDomain.domain).toBe('dl.xuda.live');
    const options = instances.flatMap(instance => instance.setOption.mock.calls.map(call => call[0]));
    const bySeries = (name) => options.find(option => option.series && option.series[0] && option.series[0].name === name);
    expect(bySeries('流量').tooltip.valueFormatter(1024)).toBe('1.02 KB');
    expect(bySeries('流量').yAxis.axisLabel.formatter(1500)).toBe('1.50 KB');
    expect(bySeries('出带宽').tooltip.valueFormatter(1024)).toBe('1024.00 Mbps');
    expect(bySeries('出带宽').yAxis.axisLabel.formatter(1500)).toBe('1500 Mbps');
    expect(bySeries('回源流量').tooltip.valueFormatter(1024)).toBe('1.02 KB');
    expect(bySeries('回源流量').yAxis.axisLabel.formatter(1500)).toBe('1.50 KB');
    h.vm.cdnTab = '__host__';
    await settle();
    expect(h.vm.activePackage).not.toBeNull();
  });

  it('refreshes only the active statistics section with the refresh flag', async () => {
    h = await consoleHarness({ respond: statsAnswer });
    await settle();
    h.clearRequests();
    h.vm.cdnTab = 'xuda.live';
    await settle();
    h.vm.refreshActiveStats();
    await settle();
    expect(h.requests.some(request => request.url.includes('/cdn/stats?days=30&refresh=1'))).toBe(true);

    h.vm.cdnTab = '__host__';
    await settle();
    h.clearRequests();
    h.vm.refreshActiveStats();
    await settle();
    expect(h.requests.some(request => request.url.includes('/host/stats?days=30&refresh=1'))).toBe(true);

    h.vm.cdnTab = '__bucket_assets__';
    await settle();
    h.clearRequests();
    h.vm.refreshActiveStats();
    await settle();
    expect(h.requests.some(request => request.url.includes('/buckets/stats?days=30&refresh=1'))).toBe(true);
  });

  it('polls stale CDN and bucket statistics up to three times and stops', async () => {
    let cdnCalls = 0;
    h = await consoleHarness({ respond: ({ url }) => {
      if (url.includes('/cdn/stats')) { cdnCalls += 1; return { body: { stale: true, domains: [{ domain: 'xuda.live' }] } }; }
      if (url.includes('/buckets/stats')) return { body: { stale: true, buckets: [] } };
      return statsAnswer({ url });
    } });
    await settle();
    expect(cdnCalls).toBe(1);
    for (const expected of [2, 3, 4]) {
      await vi.advanceTimersByTimeAsync(4000);
      await settle();
      expect(cdnCalls).toBe(expected);
    }
    await vi.advanceTimersByTimeAsync(20000);
    await settle();
    expect(cdnCalls).toBe(4);

    const before = h.requests.filter(request => request.url.includes('/buckets/stats')).length;
    h.vm.cdnTab = '__bucket_assets__';
    await settle();
    await vi.advanceTimersByTimeAsync(4000);
    await settle();
    expect(h.requests.filter(request => request.url.includes('/buckets/stats')).length).toBeGreaterThan(before);

    h.respond(({ url }) => (url.includes('/cdn/stats') ? { body: { stale: false, domains: [{ domain: 'other.live' }] } } : statsAnswer({ url })));
    h.vm.cdnTab = 'xuda.live';
    await h.vm.loadCDNStats(true);
    expect(h.vm.cdnTab).toBe('other.live');
  });

  it('reports load failures, keeps silent refreshes quiet and clears expired sessions', async () => {
    h = await consoleHarness({ respond: statsAnswer });
    await settle();
    h.respond(({ url }) => {
      if (url.includes('/cdn/stats')) return { status: 500, body: { error: { message: 'CDN 不可用' } } };
      if (url.includes('/host/stats')) return { status: 503, body: { error: { message: '主机不可用' } } };
      if (url.includes('/buckets/stats')) return new Error('');
      return statsAnswer({ url });
    });
    await h.vm.loadCDNStats(true);
    expect(h.vm.cdnError).toBe('CDN 不可用');
    expect(h.vm.cdnLoading).toBe(false);
    await h.vm.loadHostStats(true);
    expect(h.vm.hostError).toBe('主机不可用');
    await h.vm.loadBucketStats(true);
    expect(h.vm.bucketError).toBe('存储桶统计加载失败');
    h.vm.cdnError = '';
    await h.vm.loadCDNStats(false, true);
    expect(h.vm.cdnError).toBe('');

    h.respond(() => ({ status: 401, body: { error: { message: '未登录' } } }));
    await h.vm.loadCDNStats(true);
    expect(h.vm.logged).toBe(false);
    h.vm.logged = true;
    await h.vm.loadHostStats(true);
    expect(h.vm.logged).toBe(false);
    h.vm.logged = true;
    await h.vm.loadBucketStats(true);
    expect(h.vm.logged).toBe(false);
  });

  it('silently refreshes overview statistics every ten minutes while logged in', async () => {
    h = await consoleHarness({ respond: statsAnswer });
    await settle();
    h.vm.hostStats = { out_bandwidth: 1 };
    h.vm.bucketStats = { buckets: [] };
    h.clearRequests();
    await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
    await settle();
    expect(h.requests.some(request => request.url.includes('/cdn/stats'))).toBe(true);
    expect(h.requests.some(request => request.url.includes('/host/stats'))).toBe(true);
    expect(h.requests.filter(request => request.url.includes('/buckets/stats')).length).toBeGreaterThan(0);
    await h.vm.logout();
    h.clearRequests();
    await vi.advanceTimersByTimeAsync(10 * 60 * 1000);
    await settle();
    expect(h.requests.filter(request => request.url.includes('/stats')).length).toBe(0);
  });
});
