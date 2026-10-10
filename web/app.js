import { template } from './template.js';

(function () {
  const { createApp, ref, computed, nextTick, watch, onMounted, onBeforeUnmount } = Vue;
  const { createVuetify, useDisplay } = Vuetify;

      const vuetify = createVuetify({
    theme: {
      defaultTheme: 'control',
      themes: {
        control: {
          dark: false,
          colors: {
            primary: '#176b4d',
            secondary: '#287ca8',
            info: '#287ca8',
            success: '#2f855a',
            warning: '#b7791f',
            error: '#b23f4a',
            background: '#f4f7f8',
            surface: '#ffffff',
            'surface-variant': '#e8eef1',
            sidebar: '#173842'
          }
        }
      }
    },
    icons: { defaultSet: 'mdi' },
    locale: {
      locale: 'zhHans',
      fallback: 'en',
      messages: {
        zhHans: {
          dataIterator: { noResultsText: '没有符合条件的结果', loadingText: '加载中……' },
          dataTable: {
            itemsPerPageText: '每页条数：',
            sortBy: '排序',
            ariaLabel: {
              sortDescending: '：降序排列',
              sortAscending: '：升序排列',
              sortNone: '：未排序',
              activateNone: '点击取消排序',
              activateDescending: '点击按降序排列',
              activateAscending: '点击按升序排列',
              selectRow: '选择行',
              selectAll: '选择全部',
              selectGroup: '选择分组'
            }
          },
          dataFooter: {
            itemsPerPageText: '每页条数：',
            itemsPerPageAll: '全部',
            nextPage: '下一页',
            prevPage: '上一页',
            firstPage: '第一页',
            lastPage: '最后一页',
            pageText: '{0}-{1} 共 {2}'
          },
          dataTableFooter: {
            itemsPerPageText: '每页条数：',
            itemsPerPageAll: '全部',
            nextPage: '下一页',
            prevPage: '上一页',
            firstPage: '第一页',
            lastPage: '最后一页',
            pageText: '{0}-{1} 共 {2}'
          },
          noDataText: '暂无数据',
          pagination: {
            ariaLabel: {
              root: '分页导航',
              next: '下一页',
              previous: '上一页',
              page: '前往第 {0} 页',
              currentPage: '当前第 {0} 页',
              first: '第一页',
              last: '最后一页'
            }
          }
        }
      }
    }
  });

  async function api(path, options) {
    options = options || {};
    const csrf = document.cookie.match(/(?:^|; )control_csrf=([^;]+)/);
    const headers = Object.assign({ 'Content-Type': 'application/json' }, options.headers || {});
    const access = localStorage.getItem('control_admin');
    if (access && !headers.Authorization) headers.Authorization = 'Bearer ' + access;
    if (csrf && options.method && options.method !== 'GET') headers['X-CSRF-Token'] = decodeURIComponent(csrf[1]);
    const response = await fetch(path, Object.assign({ credentials: 'same-origin' }, options, { headers }));
    let body = {};
    try { body = await response.json(); } catch (_) {}
    if (!response.ok) {
      const error = new Error((body.error && body.error.message) || '请求失败');
      error.status = response.status;
      error.code = body.error && body.error.code;
      throw error;
    }
    return body;
  }

  const padDatePart = (value) => String(value).padStart(2, '0');
  const formatDateTime = (value) => {
    if (value === undefined || value === null || value === '') return '-';
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return String(value);
    const chinaTime = new Date(parsed.getTime() + 8 * 60 * 60 * 1000);
    return chinaTime.getUTCFullYear() + '-' + padDatePart(chinaTime.getUTCMonth() + 1) + '-' + padDatePart(chinaTime.getUTCDate())
      + ' ' + padDatePart(chinaTime.getUTCHours()) + ':' + padDatePart(chinaTime.getUTCMinutes()) + ':' + padDatePart(chinaTime.getUTCSeconds());
  };

  const emptyBankTemplate = () => ({
    questionBank: {
      new: true,
      name: '',
      requiresCDK: false,
      questions: [
        { questionNumber: 1, type: '单选题', title: '第1题', questionText: '', options: [{ label: 'A', text: '' }, { label: 'B', text: '' }], correctAnswer: 'A' }
      ]
    }
  });

  // Pretty-print shared configuration JSON. Unlike JSON.stringify, arrays of
  // plain values stay on one line, and the weeks/sessions course fields are
  // always kept to a single line whatever shape they carry.
  const shareInlineKeys = ['weeks', 'sessions'];
  const formatShareJSON = (value) => {
    const isInlineArray = (item) => item.every((entry) => entry === null || typeof entry !== 'object');
    const render = (node, depth) => {
      const pad = '  '.repeat(depth);
      const padInner = '  '.repeat(depth + 1);
      if (Array.isArray(node)) {
        if (!node.length) return '[]';
        if (isInlineArray(node)) return '[' + node.map((entry) => JSON.stringify(entry)).join(', ') + ']';
        return '[\n' + node.map((entry) => padInner + render(entry, depth + 1)).join(',\n') + '\n' + pad + ']';
      }
      if (node && typeof node === 'object') {
        const keys = Object.keys(node);
        if (!keys.length) return '{}';
        const body = keys.map((key) => {
          const value = node[key];
          const inline = shareInlineKeys.includes(key)
            ? (Array.isArray(value) && isInlineArray(value) ? '[' + value.map((entry) => JSON.stringify(entry)).join(', ') + ']' : JSON.stringify(value))
            : render(value, depth + 1);
          return padInner + JSON.stringify(key) + ': ' + inline;
        });
        return '{\n' + body.join(',\n') + '\n' + pad + '}';
      }
      return JSON.stringify(node);
    };
    return render(value, 0);
  };

  createApp({
    setup() {
      const { mdAndUp } = useDisplay();
      const logged = ref(!!localStorage.getItem('control_admin'));
      const drawer = ref(false);
      const VIEW_ROUTES = ['overview', 'users', 'devices', 'risk', 'error-reports', 'library', 'share-codes', 'config', 'audit'];
      const viewForPath = (pathname) => {
        const name = String(pathname || '').replace(/^\/+|\/+$/g, '');
        return VIEW_ROUTES.includes(name) ? name : 'overview';
      };
      const view = ref(viewForPath(window.location.pathname));
      const loading = ref(false);
      const error = ref('');
      const overview = ref({});
      const breakdown = ref({ platforms: {}, versions: {} });
      const series = ref([]);
      const users = ref([]);
      const devices = ref([]);
      const risks = ref([]);
      const audit = ref([]);
      const errorReports = ref([]);
      const errorReportsTotal = ref(0);
      const errorReportsPage = ref(1);
      const errorReportsPerPage = ref(25);
      const banks = ref([]);
      const bankTotal = ref(0);
      const bankPage = ref(1);
      const bankItemsPerPage = ref(25);
      const cdks = ref([]);
      const cdkTotal = ref(0);
      const cdkPage = ref(1);
      const cdkItemsPerPage = ref(25);
      const cdkDialog = ref(false);
      const cdkRevealDialog = ref(false);
      const cdkForm = ref({ count: 1 });
      const createdCDKs = ref([]);
      const cdkSearch = ref('');
      const shareCodes = ref([]);
      const shareCodesTotal = ref(0);
      const shareCodePage = ref(1);
      const shareCodeItemsPerPage = ref(25);
      const shareCodeDialog = ref(false);
      const shareCodeJSON = ref('{}');
      const shareCodeKind = ref('personalization');
      const createdShareCode = ref(null);
      const shareCodeDetail = ref({});
      const shareCodeDetailDialog = ref(false);
      const shareCodeHeaders = [
        { title: '分享码', key: 'code' },
        { title: '类型', key: 'type' },
        { title: '创建时间', key: 'created_at' },
        { title: '过期时间', key: 'expires_at' },
        { title: '学号', key: 'student_id' },
        { title: '操作', key: 'actions', sortable: false },
      ];
      const userSearch = ref('');
      const userPage = ref(1);
      const userItemsPerPage = ref(25);
      const userTotal = ref(0);
      const userSortBy = ref([{ key: 'last_login_at', order: 'desc' }]);
      const releaseConfig = ref({ latestVersion: '', downloadUrl: '', updatedAt: '' });
      const releaseForm = ref({ latestVersion: '', downloadUrl: '' });
      const schoolCalendarConfig = ref({ days: [], updatedAt: '' });
      const schoolCalendarDays = ref([]);
      const schoolCalendarRange = ref('');
      const schoolCalendarDayDialog = ref(false);
      const schoolCalendarRebuildDialog = ref(false);
      const schoolCalendarTableKey = ref(0);
      const schoolCalendarEditingDate = ref('');
      const schoolCalendarEditingAdjustment = ref('');
      const schoolCalendarEditingName = ref('');
      const schoolCalendarEditingNew = ref(false);
      const bankDialog = ref(false);
      const bankEditing = ref(false);
      const bankJSON = ref('');
      const bankNewOptions = [{ title: '是', value: true }, { title: '否', value: false }];
      const bankCDKOptions = [{ title: '需要', value: true }, { title: '不需要', value: false }];
      const selectedUser = ref(null);
      const userDialog = ref(false);
      const loginForm = ref({ key: '' });
      const userStatus = ref('');
      const snackbar = ref({ show: false, text: '', color: 'success' });
      const chartEl = ref(null);
      const collegeChartEl = ref(null);
      const platformChartEl = ref(null);
      const eventChartEl = ref(null);
      const featureChartEl = ref(null);
      let chart = null;
      let collegeChart = null;
      let platformChart = null;
      let eventChart = null;
      let featureChart = null;

      const nav = [
        { key: 'overview', label: '总览', icon: 'mdi-chart-line' },
        { key: 'users', label: '用户', icon: 'mdi-account-group-outline' },
        { key: 'risk', label: '风控', icon: 'mdi-shield-alert-outline' },
        { key: 'error-reports', label: '错误', icon: 'mdi-bug-outline' },
        { key: 'library', label: '文库', icon: 'mdi-book-open-page-variant' },
        { key: 'share-codes', label: '分享码', icon: 'mdi-share-variant-outline' },
        { key: 'config', label: '配置', icon: 'mdi-cog-outline' },
        { key: 'audit', label: '审计', icon: 'mdi-history' }
      ];
      const title = computed(() => (nav.find(item => item.key === view.value) || nav[0]).label);
      watch(mdAndUp, (desktop) => { if (!desktop) drawer.value = false; }, { immediate: true });

      const statCards = computed(() => [
        { label: 'DAU', value: overview.value.dau || 0, icon: 'mdi-account-check-outline', color: 'primary' },
        { label: 'WAU', value: overview.value.wau || 0, icon: 'mdi-account-clock-outline', color: 'secondary' },
        { label: 'MAU', value: overview.value.mau || 0, icon: 'mdi-account-multiple-outline', color: 'info' },
        { label: '活跃设备', value: overview.value.active_devices || 0, icon: 'mdi-devices', color: 'success' },
        { label: '用户总数', value: overview.value.total_users || 0, icon: 'mdi-account-multiple', color: 'warning' },
        { label: '今日事件', value: overview.value.today_events || 0, icon: 'mdi-pulse', color: 'error' }
      ]);
      const platformRows = computed(() => Object.entries(breakdown.value.platforms || {}).map(([label, value]) => ({ label, value })));
      const versionRows = computed(() => Object.entries(breakdown.value.versions || {}).map(([label, value]) => ({ label, value })));
      const distributionRows = (value) => Object.entries(value || {})
        .map(([label, value]) => ({ label, value: Number(value) || 0 }))
        .sort((a, b) => b.value - a.value || a.label.localeCompare(b.label));
      const collegeRows = computed(() => distributionRows(breakdown.value.colleges));
      const classRows = computed(() => distributionRows(breakdown.value.classes));
      const eventRows = computed(() => Object.entries(breakdown.value.event_hours || {})
        .map(([label, value]) => ({ label, value: Number(value) || 0 }))
        .sort((a, b) => a.label.localeCompare(b.label)));
      const cdkActivationRows = computed(() => [
        { label: '今日激活数', value: breakdown.value.cdk_activations_today || 0 },
        { label: '本周激活数', value: breakdown.value.cdk_activations_week || 0 },
        { label: '总激活数', value: breakdown.value.cdk_activations_total || 0 }
      ]);
      const featureLabels = { campus_card: '一卡通', power: '电费', exams: '考试安排', academic: '学业情况', network: '网络管理', repair: '极速报修', learning_center: '题库中心', school_calendar: '学校校历', book_list: '教材查询', teacher_evaluation: '教师评价', share_code: '分享码' };
      // 功能使用分布：仅统计用户主动使用的服务与分享码，不含启动/登录/退出等生命周期事件。
      const featureRows = computed(() => Object.entries(breakdown.value.feature_usage || {})
        .map(([key, value]) => ({ label: featureLabels[key] || valueOrDash(key), value: Number(value) || 0 }))
        .filter(row => row.value > 0)
        .sort((a, b) => b.value - a.value || a.label.localeCompare(b.label)));
      const userDetails = computed(() => {
        if (!selectedUser.value) return [];
        const user = selectedUser.value.user || {};
        return [
          { label: '显示名', value: valueOrDash(user.display_name) },
          { label: '学号', value: valueOrDash(selectedUser.value.student_id) },
          { label: '伪名', value: valueOrDash(user.student_alias) },
          { label: '学院', value: valueOrDash(user.college_name) },
          { label: '班级', value: valueOrDash(user.class_name) },
          { label: '状态', value: statusLabel(user.status) },
          { label: '最近连接', value: formatDateTime(user.last_login_at) }
        ];
      });
      const userHeaders = [
        { title: '序号', key: 'seq', width: '8%' }, { title: '学院', key: 'college_name', width: '19%' }, { title: '班级', key: 'class_name', width: '18%' },
        { title: '显示名', key: 'display_name', width: '17%' }, { title: '版本号', key: 'app_version', width: '11%' }, { title: '状态', key: 'status', width: '11%' },
        { title: '设备数', key: 'device_count', align: 'center', width: '10%' }, { title: '最近连接', key: 'last_login_at', width: '15%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '15%' }
      ];
      const deviceHeaders = [
        { title: '设备码', key: 'device_serial', sortable: false, width: '20%' }, { title: '平台', key: 'platform', width: '14%' },
        { title: '版本', key: 'app_version', width: '14%' }, { title: '安装标识', key: 'installation_id', sortable: false, width: '24%' },
        { title: '最近活动', key: 'last_seen_at', width: '18%' }, { title: '状态', key: 'status', sortable: false, width: '10%' }
      ];
      const riskHeaders = [
        { title: '类型', key: 'type', width: '16%' }, { title: '班级', key: 'class_name', sortable: false, width: '18%' },
        { title: '显示名', key: 'display_name', sortable: false, width: '16%' }, { title: '版本号', key: 'app_version', sortable: false, width: '12%' },
        { title: '次数', key: 'observed_count', align: 'center', width: '10%' }, { title: '时间', key: 'created_at', width: '14%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '10%' }
      ];
      const auditHeaders = [
        { title: '时间', key: 'created_at', width: '18%' }, { title: '动作', key: 'action', width: '18%' },
        { title: '操作者', key: 'actor_id', sortable: false, width: '16%' }, { title: '目标', key: 'target', sortable: false, width: '22%' },
        { title: '详情', key: 'detail', sortable: false, width: '26%' }
      ];
      const errorReportHeaders = [
        { title: '时间', key: 'occurred_at', width: '15%' },
        { title: '学号', key: 'student_id', sortable: false, width: '13%' },
        { title: '标题', key: 'title', width: '17%' },
        { title: '错误摘要', key: 'message', sortable: false, width: '22%' },
        { title: '版本', key: 'app_version', width: '9%' },
        { title: '平台', key: 'platform', width: '9%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '15%' },
      ];
      const userDeviceHeaders = [
        { title: '设备码', key: 'device_serial', sortable: false }, { title: '平台', key: 'platform' },
        { title: '最近活动', key: 'last_seen_at' }
      ];
      const bankHeaders = [
        { title: '顺序 ID', key: 'orderId', sortable: false, align: 'center', width: '9%' }, { title: '题库 ID', key: 'id', sortable: false, width: '13%' }, { title: '名称', key: 'name', width: '17%' },
        { title: '题目数', key: 'question_count', align: 'center', width: '9%' }, { title: '状态', key: 'status', width: '9%' },
        { title: '新题库', key: 'new', sortable: false, align: 'center', width: '7%' }, { title: 'CDK 解锁', key: 'requiresCDK', sortable: false, align: 'center', width: '10%' }, { title: '更新时间', key: 'updated_at', width: '9%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '17%' }
      ];
      const cdkHeaders = [
        { title: 'CDK ID', key: 'id', sortable: false, width: '20%' },
        { title: '题库', key: 'question_bank_id', sortable: false, width: '14%' },
        { title: '状态', key: 'status', width: '13%' }, { title: '绑定学号', key: 'bound_student_id', sortable: false, width: '16%' },
        { title: '创建时间', key: 'created_at', width: '13%' }, { title: '使用时间', key: 'used_at', width: '13%' }, { title: '操作', key: 'actions', sortable: false, align: 'center', width: '11%' }
      ];
      const riskOptions = [{ title: '全部', value: '' }, { title: '未查看', value: 'false' }, { title: '已查看', value: 'true' }];
      const bankStatusOptions = [{ title: '启用', value: 'active' }, { title: '草稿', value: 'draft' }, { title: '停用', value: 'disabled' }];
      const questionTypeOptions = ['单选题', '多选题', '判断题', '填空题'];
      const bankPageCount = computed(() => Math.max(1, Math.ceil(bankTotal.value / bankItemsPerPage.value)));
      const schoolCalendarEditingDateLabel = computed(() =>
        schoolCalendarEditingDate.value || '添加例外日期',
      );
      const schoolCalendarHeaders = [
        { title: '日期', key: 'date', width: '22%' },
        { title: '名称', key: 'name', width: '31%' },
        { title: '课程调整', key: 'adjustment', width: '31%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '16%' },
      ];
      const schoolCalendarExceptions = computed(() => schoolCalendarDays.value.filter(day => day.name || day.adjustment));

      const notify = (text, color) => { snackbar.value = { show: true, text, color: color || 'success' }; };
      const closeBankEditor = () => {
        bankDialog.value = false;
        bankEditing.value = false;
        bankJSON.value = '';
      };
      const setBankDialog = (visible) => { if (visible) bankDialog.value = true; else closeBankEditor(); };
      const clearAdminSession = () => {
        localStorage.removeItem('control_admin');
        logged.value = false;
        drawer.value = false;
        selectedUser.value = null;
        userDialog.value = false;
        closeBankEditor();
        cdkDialog.value = false;
        cdkRevealDialog.value = false;
        createdCDKs.value = [];
      };
      const call = async (fn) => {
        loading.value = true; error.value = '';
        try {
          return await fn();
        } catch (e) {
          if (e && e.status === 401 && logged.value) {
            clearAdminSession();
            return;
          }
          error.value = e && e.message ? e.message : '请求失败';
        } finally { loading.value = false; }
      };
      const login = () => call(async () => {
        const out = await api('/api/v1/admin/session', { method: 'POST', body: JSON.stringify(loginForm.value) });
        localStorage.setItem('control_admin', out.access_token); logged.value = true; notify('登录成功'); await load();
      });
      const logout = () => call(async () => {
        await api('/api/v1/admin/session', { method: 'DELETE' });
        localStorage.removeItem('control_admin'); logged.value = false; drawer.value = false; snackbar.value.show = false;
      });
      const loadOverview = async () => {
        overview.value = await api('/api/v1/admin/metrics/overview');
        breakdown.value = await api('/api/v1/admin/metrics/breakdown');
        // 趋势按天统计，当天数据尚未固定，只展示到昨天。
        const today = new Date(Date.now() + 8 * 3600 * 1000).toISOString().slice(0, 10);
        series.value = ((await api('/api/v1/admin/metrics/series?days=30')).items || []).filter(row => row.day < today);
        await nextTick(); drawChart();
      };
      const loadBanks = async () => {
        const offset = (bankPage.value - 1) * bankItemsPerPage.value;
        const out = await api('/api/v1/admin/question-banks?limit=' + bankItemsPerPage.value + '&offset=' + offset);
        banks.value = out.items || []; bankTotal.value = out.total || 0;
      };
      const loadCDKs = async () => {
        const offset = (cdkPage.value - 1) * cdkItemsPerPage.value;
        const search = cdkSearch.value.trim();
        const query = search ? '&q=' + encodeURIComponent(search) : '';
        const out = await api('/api/v1/admin/library-cdks?limit=' + cdkItemsPerPage.value + '&offset=' + offset + query);
        cdks.value = out.items || []; cdkTotal.value = out.total || 0;
      };
      const loadRelease = async () => {
        const out = await api('/api/v1/admin/app/release');
        releaseConfig.value = Object.assign({}, out || {}, { updatedAt: formatDateTime(out && out.updatedAt) });
        releaseForm.value = { latestVersion: out.latestVersion || '', downloadUrl: out.downloadUrl || '' };
      };
      const loadSchoolCalendar = async () => {
        const out = await api('/api/v1/admin/school-calendar');
        schoolCalendarConfig.value = Object.assign({}, out || {}, { updatedAt: out && out.updatedAt ? out.updatedAt : '' });
        schoolCalendarDays.value = (out && out.days || []).map(day => Object.assign({}, day, { name: day.name || '', adjustment: day.adjustment || '' }));
        schoolCalendarRange.value = schoolCalendarDays.value.length
          ? schoolCalendarDays.value[0].date.replaceAll('-', '') + '-' + schoolCalendarDays.value[schoolCalendarDays.value.length - 1].date.replaceAll('-', '')
          : '';
      };
      const normalizeCompactDate = (value) => {
        if (!/^\d{8}$/.test(value)) return '';
        const date = new Date(value.slice(0, 4) + '-' + value.slice(4, 6) + '-' + value.slice(6, 8) + 'T00:00:00Z');
        if (Number.isNaN(date.getTime()) || date.toISOString().slice(0, 10).replaceAll('-', '') !== value) return '';
        return value.slice(0, 4) + '-' + value.slice(4, 6) + '-' + value.slice(6, 8);
      };
      const rebuildSchoolCalendar = () => {
        try {
          const match = /^(\d{8})-(\d{8})$/.exec((schoolCalendarRange.value || '').trim());
          if (!match) throw new Error('请输入 YYYYMMDD-YYYYMMDD 格式的日期范围');
          const parseDate = (value) => {
            const date = new Date(value.slice(0, 4) + '-' + value.slice(4, 6) + '-' + value.slice(6, 8) + 'T00:00:00Z');
            if (Number.isNaN(date.getTime()) || date.toISOString().slice(0, 10).replaceAll('-', '') !== value) throw new Error('日期范围无效');
            return date;
          };
          const start = parseDate(match[1]);
          const end = parseDate(match[2]);
          if (start > end) {
            throw new Error('开始日期不能晚于结束日期');
          }
          const days = [];
          for (let cursor = new Date(start); cursor <= end; cursor.setUTCDate(cursor.getUTCDate() + 1)) {
            const date = cursor.toISOString().slice(0, 10);
            days.push({ date, name: '', adjustment: '' });
            if (days.length > 1000) throw new Error('校历范围不能超过 1000 天');
          }
          schoolCalendarDays.value = days;
          schoolCalendarTableKey.value++;
          return true;
        } catch (e) {
          error.value = e && e.message ? e.message : '校历范围无效';
          return false;
        }
      };
      const loadErrorReports = async () => {
        const offset = (errorReportsPage.value - 1) * errorReportsPerPage.value;
        const out = await api('/api/v1/admin/error-reports?limit=' + errorReportsPerPage.value + '&offset=' + offset);
        errorReports.value = out.items || [];
        errorReportsTotal.value = out.total || 0;
      };
      const searchCDKs = () => call(async () => { cdkPage.value = 1; await loadCDKs(); });
      const loadLibrary = async () => {
        const results = await Promise.all([loadBanks(), loadCDKs(), api('/api/v1/admin/metrics/breakdown')]);
        breakdown.value = results[2];
      };
      const loadShareCodes = async () => {
        const offset = (shareCodePage.value - 1) * shareCodeItemsPerPage.value;
        const out = await api('/api/v1/admin/share-codes?limit=' + shareCodeItemsPerPage.value + '&offset=' + offset);
        shareCodes.value = (out.items || []).map(item => Object.assign({}, item, { type: String(item.code || '').slice(-1) === '1' ? 'schedule' : 'personalization' }));
        shareCodesTotal.value = out.total || 0;
      };
      const shareCodeDetailJSON = computed(() => formatShareJSON(shareCodeDetail.value.data || {}));
      const openShareCodeDetail = (row) => call(async () => {
        const out = await api('/api/v1/admin/share-codes/' + encodeURIComponent(rawItem(row).id));
        shareCodeDetail.value = Object.assign({}, out.shareCode || {}, { type: String(out.shareCode && out.shareCode.code || '').slice(-1) === '1' ? 'schedule' : 'personalization', data: out.data || {} });
        shareCodeDetailDialog.value = true;
      });
      const openShareCodeDialog = () => { shareCodeJSON.value = '{}'; shareCodeKind.value = 'personalization'; createdShareCode.value = null; shareCodeDialog.value = true; };
      const createShareCode = () => call(async () => {
        let data;
        try { data = JSON.parse(shareCodeJSON.value || '{}'); } catch (_) { throw new Error('分享数据 JSON 格式无效'); }
        if (!data || typeof data !== 'object' || Array.isArray(data)) throw new Error('分享数据必须是 JSON 对象');
        createdShareCode.value = await api('/api/v1/admin/share-codes', { method: 'POST', body: JSON.stringify({ suffix: shareCodeKind.value === 'schedule' ? '1' : '2', data }) });
        notify('分享码已生成');
        await loadShareCodes();
      });
      const deleteShareCode = (row) => call(async () => {
        const item = rawItem(row);
        if (!window.confirm('确定删除这个分享码吗？')) return;
        await api('/api/v1/admin/share-codes/' + encodeURIComponent(item.id), { method: 'DELETE' });
        notify('分享码已删除');
        await loadShareCodes();
      });
      const loadUsers = async () => {
        const search = userSearch.value.trim();
        const sort = userSortBy.value[0] || { key: 'last_login_at', order: 'desc' };
        const offset = (userPage.value - 1) * userItemsPerPage.value;
        const query = '?limit=' + userItemsPerPage.value + '&offset=' + offset
          + '&sort=' + encodeURIComponent(sort.key || 'last_login_at') + '&order=' + encodeURIComponent(sort.order || 'desc')
          + (search ? '&q=' + encodeURIComponent(search) : '');
        const out = await api('/api/v1/admin/users' + query);
        users.value = out.items || [];
        userTotal.value = out.total || 0;
      };
      const searchUsers = () => call(async () => { userPage.value = 1; await loadUsers(); });
      const sortUsers = (sortBy) => call(async () => {
        userSortBy.value = Array.isArray(sortBy) && sortBy.length ? sortBy : [{ key: 'last_login_at', order: 'desc' }];
        userPage.value = 1;
        await loadUsers();
      });
      const load = async () => {
        if (!logged.value) return;
        await call(async () => {
          if (view.value === 'overview') await loadOverview();
          else if (view.value === 'users') await loadUsers();
          else if (view.value === 'risk') risks.value = (await api('/api/v1/admin/risk-events?limit=100')).items || [];
          else if (view.value === 'error-reports') await loadErrorReports();
          else if (view.value === 'library') await loadLibrary();
          else if (view.value === 'share-codes') await loadShareCodes();
          else if (view.value === 'config') await Promise.all([loadRelease(), loadSchoolCalendar()]);
          else if (view.value === 'audit') audit.value = (await api('/api/v1/admin/audit?limit=100')).items || [];
        });
      };
      const switchView = (name) => {
        view.value = name;
        if (window.location.pathname !== '/' + name) window.history.pushState({ view: name }, '', '/' + name);
        if (!mdAndUp.value) drawer.value = false;
        load();
      };
      const handlePopState = () => { view.value = viewForPath(window.location.pathname); load(); };
      const drawChart = () => {
        if (!window.echarts || view.value !== 'overview') return;
        if (chart) {
          let dom = null; try { dom = chart.getDom(); } catch (_) {}
          if (dom !== chartEl.value) { chart.dispose(); chart = null; }
        }
        if (chartEl.value) {
          if (!chart) chart = window.echarts.init(chartEl.value);
          chart.clear();
          chart.setOption({
            animationDuration: 350, tooltip: { trigger: 'axis' },
            legend: { data: ['总用户', 'DAU', 'WAU'], top: 0, textStyle: { color: '#5e6d76' } },
            grid: { left: 42, right: 18, top: 34, bottom: 30 },
            xAxis: { type: 'category', data: series.value.map(x => x.day), axisLabel: { color: '#697984' }, axisLine: { lineStyle: { color: '#d8e0e4' } } },
            yAxis: { type: 'value', axisLabel: { color: '#697984' }, splitLine: { lineStyle: { color: '#edf1f3' } } },
            series: [
              { name: '总用户', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => x.total_users), itemStyle: { color: '#176b4d' }, lineStyle: { width: 3 } },
              { name: 'DAU', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => x.dau), itemStyle: { color: '#287ca8' }, lineStyle: { width: 3 } },
              { name: 'WAU', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => x.wau), itemStyle: { color: '#805ad5' }, lineStyle: { width: 3 } }
            ]
          });
        }
        if (collegeChart) collegeChart.dispose();
        if (collegeChartEl.value && collegeRows.value.length) {
          collegeChart = window.echarts.init(collegeChartEl.value);
          collegeChart.setOption({
            animationDuration: 350,
            tooltip: { trigger: 'item' },
            legend: { type: 'scroll', bottom: 0, textStyle: { color: '#5e6d76' } },
            series: [{ type: 'pie', radius: ['36%', '68%'], center: ['50%', '43%'], data: collegeRows.value.map(row => ({ name: row.label, value: row.value })), label: { formatter: '{b}: {c}' } }]
          });
        }
        if (platformChart) platformChart.dispose();
        if (platformChartEl.value && platformRows.value.length) {
          platformChart = window.echarts.init(platformChartEl.value);
          platformChart.setOption({
            animationDuration: 350,
            tooltip: { trigger: 'item' },
            legend: { type: 'scroll', bottom: 0, textStyle: { color: '#5e6d76' } },
            series: [{ type: 'pie', radius: ['36%', '68%'], center: ['50%', '43%'], data: platformRows.value.map(row => ({ name: row.label, value: row.value })), label: { formatter: '{b}: {c}' } }]
          });
        }
        // 刻度保留全部 24 个时段，未固定的小时（含当前小时）数据置空，折线只画到上一个完整小时。
        const eventHourLimit = new Date(Date.now() + 8 * 3600 * 1000).getUTCHours();
        if (eventChart) eventChart.dispose();
        if (eventChartEl.value && eventRows.value.length) {
          eventChart = window.echarts.init(eventChartEl.value);
          eventChart.setOption({
            animationDuration: 350,
            tooltip: { trigger: 'axis' },
            grid: { left: 42, right: 18, top: 18, bottom: 42 },
            xAxis: { type: 'category', data: eventRows.value.map(row => row.label), axisLabel: { color: '#697984', rotate: 24 }, axisLine: { lineStyle: { color: '#d8e0e4' } } },
            yAxis: { type: 'value', minInterval: 1, axisLabel: { color: '#697984' }, splitLine: { lineStyle: { color: '#edf1f3' } } },
            series: [{ name: '事件数', type: 'line', smooth: true, showSymbol: false, data: eventRows.value.map(row => Number(row.label.slice(0, 2)) < eventHourLimit ? row.value : null), itemStyle: { color: '#287ca8' }, lineStyle: { width: 3 } }]
          });
        }
        if (featureChart) featureChart.dispose();
        if (featureChartEl.value && featureRows.value.length) {
          featureChart = window.echarts.init(featureChartEl.value);
          featureChart.setOption({
            animationDuration: 350,
            tooltip: { trigger: 'item' },
            legend: { type: 'scroll', bottom: 0, textStyle: { color: '#5e6d76' } },
            series: [{ type: 'pie', radius: ['36%', '68%'], center: ['50%', '43%'], data: featureRows.value.map(row => ({ name: row.label, value: row.value })), label: { formatter: '{b}: {c}' } }]
          });
        }
      };
      const openNewBank = () => {
        bankEditing.value = false;
        bankJSON.value = JSON.stringify(emptyBankTemplate(), null, 2);
        bankDialog.value = true;
      };
      const editBank = (row) => call(async () => {
        const out = await api('/api/v1/admin/question-banks/' + encodeURIComponent(rawItem(row).id));
        bankEditing.value = true;
        bankJSON.value = JSON.stringify({
          questionBank: {
            id: out.questionBank.id,
            orderId: out.questionBank.orderId,
            new: !!out.questionBank.new,
            name: out.questionBank.name,
            status: out.status || 'active',
            requiresCDK: !!out.questionBank.requiresCDK,
            questions: out.questionBank.questions || []
          }
        }, null, 2);
        bankDialog.value = true;
      });
      const saveBank = () => call(async () => {
        let parsed;
        try { parsed = JSON.parse(bankJSON.value || ''); } catch (_) { throw new Error('题库 JSON 格式无效'); }
        const bank = parsed && typeof parsed === 'object' && !Array.isArray(parsed) && parsed.questionBank ? parsed.questionBank : parsed;
        if (!bank || typeof bank !== 'object' || Array.isArray(bank)) throw new Error('题库 JSON 格式无效');
        if (!bank.name || !String(bank.name).trim()) throw new Error('请填写题库名称');
        const bankID = (bank.id || '').trim();
        if (bankEditing.value && !bankID) throw new Error('题库 ID 无效');
        const payload = { questionBank: bank };
        const path = '/api/v1/admin/question-banks' + (bankEditing.value ? '/' + encodeURIComponent(bankID) : '');
        const editing = bankEditing.value;
        await api(path, { method: editing ? 'PUT' : 'POST', body: JSON.stringify(payload) });
        closeBankEditor(); notify(editing ? '题库已更新' : '题库已创建'); await loadBanks();
      });
      const saveRelease = () => call(async () => {
        const latestVersion = (releaseForm.value.latestVersion || '').trim();
        const downloadUrl = (releaseForm.value.downloadUrl || '').trim();
        if (!latestVersion) throw new Error('请填写最新版版本号');
        if (!downloadUrl) throw new Error('请填写下载 URL');
        const out = await api('/api/v1/admin/app/release', { method: 'PUT', body: JSON.stringify({ latestVersion, downloadUrl }) });
        releaseConfig.value = Object.assign({}, out || {}, { updatedAt: formatDateTime(out && out.updatedAt) });
        releaseForm.value = { latestVersion: out.latestVersion || latestVersion, downloadUrl: out.downloadUrl || downloadUrl };
        notify('APP 发布配置已保存');
      });
      const saveSchoolCalendar = () => call(async () => {
        if (!schoolCalendarDays.value.length) throw new Error('请至少配置一天校历');
        const out = await api('/api/v1/admin/school-calendar', { method: 'PUT', body: JSON.stringify({ days: schoolCalendarDays.value }) });
        schoolCalendarConfig.value = Object.assign({}, out || {}, { updatedAt: out && out.updatedAt ? out.updatedAt : '' });
        schoolCalendarDays.value = (out.days || schoolCalendarDays.value).map(day => Object.assign({}, day, { name: day.name || '', adjustment: day.adjustment || '' }));
        notify('学校校历已保存');
      });
      const openSchoolCalendarRebuild = () => {
        error.value = '';
        schoolCalendarRebuildDialog.value = true;
      };
      const confirmSchoolCalendarRebuild = () => {
        schoolCalendarRebuildDialog.value = false;
        if (rebuildSchoolCalendar()) saveSchoolCalendar();
      };
      const openNewSchoolCalendarDay = () => {
        schoolCalendarEditingNew.value = true;
        schoolCalendarEditingDate.value = '';
        schoolCalendarEditingAdjustment.value = '';
        schoolCalendarEditingName.value = '';
        schoolCalendarDayDialog.value = true;
      };
      const openSchoolCalendarDay = (day) => {
        schoolCalendarEditingNew.value = false;
        schoolCalendarEditingDate.value = day.date.replaceAll('-', '');
        schoolCalendarEditingAdjustment.value = day.adjustment || '';
        schoolCalendarEditingName.value = day.name || '';
        schoolCalendarDayDialog.value = true;
      };
      const deleteSchoolCalendarException = (day) => {
        const item = rawItem(day);
        const target = schoolCalendarDays.value.find(entry => entry.date === item.date);
        if (!target) return;
        target.name = '';
        target.adjustment = '';
        schoolCalendarTableKey.value++;
      };
      const saveSchoolCalendarDay = () => {
        const compactDate = (schoolCalendarEditingDate.value || '').trim();
        const date = normalizeCompactDate(compactDate);
        if (!date) {
          error.value = '日期必须是有效的 YYYYMMDD 格式';
          return;
        }
        const day = schoolCalendarDays.value.find(item => item.date === date);
        if (!day && !schoolCalendarEditingNew.value) return;
        const value = (schoolCalendarEditingAdjustment.value || '').trim();
        if (value !== '' && value !== '/' && !/^\d{8}$/.test(value)) {
          error.value = '调整参数必须是 YYYYMMDD 或 /';
          return;
        }
        if (!day) {
          const parsed = new Date(date + 'T00:00:00Z');
          const first = schoolCalendarDays.value[0];
          const last = schoolCalendarDays.value[schoolCalendarDays.value.length - 1];
          if (Number.isNaN(parsed.getTime()) || !first || !last || date < first.date || date > last.date) {
            error.value = '例外日期必须位于当前校历范围内';
            return;
          }
          schoolCalendarDays.value.push({ date, name: '', adjustment: '' });
          schoolCalendarDays.value.sort((a, b) => a.date.localeCompare(b.date));
        }
        const target = schoolCalendarDays.value.find(item => item.date === date);
        target.name = (schoolCalendarEditingName.value || '').trim();
        target.adjustment = value;
        schoolCalendarDayDialog.value = false;
        schoolCalendarEditingNew.value = false;
        schoolCalendarTableKey.value++;
      };
      const openNewCDK = () => {
        call(async () => {
          cdkForm.value = { count: 1 };
          createdCDKs.value = []; cdkDialog.value = true;
        });
      };
      const createCDK = () => call(async () => {
        const count = Math.max(1, Math.min(500, Number(cdkForm.value.count) || 1));
        const out = await api('/api/v1/admin/library-cdks', { method: 'POST', body: JSON.stringify({ count }) });
        createdCDKs.value = out.items || [out];
        cdkDialog.value = false; cdkRevealDialog.value = true; notify(count > 1 ? ('已生成 ' + count + ' 个 CDK') : 'CDK 已创建'); await loadCDKs();
      });
      const copyCDK = async () => {
        if (!createdCDKs.value.length) return;
        const text = createdCDKs.value.map(item => item.code).filter(Boolean).join('\n');
        try { await navigator.clipboard.writeText(text); notify('CDK 已复制'); }
        catch (_) { notify('复制失败，请手动复制', 'warning'); }
      };
      const setCDKStatus = (row) => call(async () => {
        const item = rawItem(row);
        const next = item.status === 'disabled' ? 'active' : 'disabled';
        if (!window.confirm(next === 'disabled' ? '确定禁用这个 CDK 吗？' : '确定启用这个 CDK 吗？')) return;
        await api('/api/v1/admin/library-cdks/' + encodeURIComponent(item.id), { method: 'PATCH', body: JSON.stringify({ status: next }) });
        notify(next === 'disabled' ? 'CDK 已禁用' : 'CDK 已启用'); await loadCDKs();
      });
      const setBankStatus = (row) => call(async () => {
        const item = rawItem(row);
        const next = item.status === 'disabled' ? 'active' : 'disabled';
        if (!window.confirm((next === 'disabled' ? '确定停用' : '确定启用') + '题库“' + item.name + '”吗？')) return;
        await api('/api/v1/admin/question-banks/' + encodeURIComponent(item.id) + '/status', { method: 'PATCH', body: JSON.stringify({ status: next }) });
        notify(next === 'disabled' ? '题库已停用' : '题库已启用'); await loadBanks();
      });
      const removeBank = (row) => call(async () => {
        const item = rawItem(row);
        if (!window.confirm('确定删除题库“' + item.name + '”吗？该题库的题目与已绑定的 CDK 会一并删除，且不可恢复。')) return;
        await api('/api/v1/admin/question-banks/' + encodeURIComponent(item.id), { method: 'DELETE' });
        notify('题库已删除'); await loadBanks();
      });
      const showUser = (user) => call(async () => {
        const row = rawItem(user); selectedUser.value = await api('/api/v1/admin/users/' + encodeURIComponent(row.id));
        userStatus.value = selectedUser.value.user.status || ''; userDialog.value = true;
      });
      const closeUser = () => { userDialog.value = false; selectedUser.value = null; userStatus.value = ''; };
      const setStatus = (user) => call(async () => {
        const row = rawItem(user);
        await api('/api/v1/admin/users/' + encodeURIComponent(row.id) + '/status', { method: 'PATCH', body: JSON.stringify({ status: userStatus.value || row.status }) });
        notify('用户状态已更新'); closeUser(); await load();
      });
      const disableUser = (user) => call(async () => {
        const row = rawItem(user);
        if (!row) return;
        const next = row.status === 'disabled' ? 'active' : 'disabled';
        if (!window.confirm('确定' + (next === 'disabled' ? '停用' : '启用') + '这个用户吗？')) return;
        await api('/api/v1/admin/users/' + encodeURIComponent(row.id) + '/status', { method: 'PATCH', body: JSON.stringify({ status: next }) });
        notify(next === 'disabled' ? '用户已停用' : '用户已启用'); await load();
      });
      const setRiskDeviceStatus = (risk) => call(async () => {
        const item = rawItem(risk);
        if (!item.device_id) return;
        const revoked = !!item.device_revoked_at;
        const next = revoked ? 'active' : 'revoked';
        if (!window.confirm(revoked ? '确定恢复这个设备吗？' : '确定封禁这个设备吗？')) return;
        await api('/api/v1/admin/devices/' + encodeURIComponent(item.device_id) + '/status', { method: 'PATCH', body: JSON.stringify({ status: next }) });
        notify(revoked ? '设备已恢复' : '设备已封禁'); await load();
      });
      const clearRiskEvents = () => call(async () => {
        if (!window.confirm('确定清空全部风控记录吗？此操作不可恢复。')) return;
        const out = await api('/api/v1/admin/risk-events', { method: 'DELETE' });
        notify('已清空 ' + (out.deleted || 0) + ' 条风控记录'); await load();
      });
      const acknowledge = (risk) => call(async () => {
        const row = rawItem(risk);
        await api('/api/v1/admin/risk-events/' + encodeURIComponent(row.id), { method: 'PATCH', body: '{}' });
        notify('风控记录已标记'); await load();
      });
      const setErrorReportStudentIgnored = (row) => call(async () => {
        const item = rawItem(row);
        const ignored = !item.ignored;
        const studentID = valueOrDash(item.student_id);
        const prompt = ignored
          ? '确定忽略学号“' + studentID + '”后续的错误上报吗？'
          : '确定允许学号“' + studentID + '”继续上报错误吗？';
        if (!window.confirm(prompt)) return;
        await api('/api/v1/admin/error-reports/' + encodeURIComponent(item.id), { method: 'PATCH', body: JSON.stringify({ ignored }) });
        notify(ignored ? '已忽略该学号后续的错误上报' : '已允许该学号继续上报错误');
        await loadErrorReports();
      });
      const clearErrorReports = () => call(async () => {
        if (!errorReportsTotal.value) {
          notify('暂无错误记录', 'info');
          return;
        }
        if (!window.confirm('确定清空所有错误记录吗？此操作不可撤销，并会恢复所有学号的错误上报。')) return;
        const out = await api('/api/v1/admin/error-reports', { method: 'DELETE' });
        errorReports.value = [];
        errorReportsTotal.value = 0;
        errorReportsPage.value = 1;
        notify('已清空 ' + (out.deleted || 0) + ' 条错误记录');
      });
      const rawItem = (item) => item && item.raw ? item.raw : item;
      const valueOrDash = (value) => {
        if (value === undefined || value === null || value === '') return '-';
        return typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(value) ? formatDateTime(value) : value;
      };
      const statusColor = (status) => ({ active: 'success', draft: 'info', disabled: 'warning', used: 'info' }[status] || 'secondary');
      const statusLabel = (status) => ({ active: '启用', draft: '草稿', disabled: '禁用', used: '已兑换' }[status] || valueOrDash(status));
      const riskTypeLabel = (type) => ({ login_attempt_burst: '短时间登录过多', account_device_burst: '账号设备过多' }[type] || valueOrDash(type));
      const eventTypeLabel = (type) => ({ app_start: '启动', foreground: '前台', heartbeat: '心跳', control_login_success: '登录成功', library_open: '打开文库', logout: '退出' }[type] || valueOrDash(type));
      const actionLabel = (action) => ({ admin_login: '管理员登录', admin_logout: '管理员退出', login_attempt: '登录尝试', user_status_change: '用户状态更新', error_report_student_ignore: '忽略学号错误上报', error_report_student_allow: '允许学号错误上报', error_reports_clear: '清空错误记录', question_bank_create: '创建题库', question_bank_update: '更新题库', question_bank_status: '更新题库状态', library_cdk_create: '生成通用 CDK', library_cdk_redeem: '兑换通用 CDK', library_cdk_status: '更新通用 CDK 状态', app_release_update: '更新 APP 发布配置', school_calendar_update: '更新学校校历', risk_acknowledge: '标记风控记录', risk_clear: '清空风控记录', device_revoke: '封禁设备', device_restore: '恢复设备' }[action] || valueOrDash(action));
      const riskStatusColor = (risk) => risk.acknowledged_at ? 'success' : 'warning';
      const handleResize = () => { if (chart) chart.resize(); if (platformChart) platformChart.resize(); if (collegeChart) collegeChart.resize(); if (eventChart) eventChart.resize(); if (featureChart) featureChart.resize(); };
      watch(view, async (name) => { if (name === 'overview') { await nextTick(); drawChart(); } });
      onMounted(() => {
        if (window.location.pathname !== '/' + view.value) window.history.replaceState({ view: view.value }, '', '/' + view.value);
        if (logged.value) load();
        window.addEventListener('resize', handleResize);
        window.addEventListener('popstate', handlePopState);
      });
      onBeforeUnmount(() => { window.removeEventListener('resize', handleResize); window.removeEventListener('popstate', handlePopState); if (chart) chart.dispose(); if (platformChart) platformChart.dispose(); if (collegeChart) collegeChart.dispose(); if (eventChart) eventChart.dispose(); if (featureChart) featureChart.dispose(); });

      return {
        logged, drawer, mdAndUp, view, loading, error, overview, breakdown, series, users, devices, risks, audit, errorReports, errorReportsTotal, errorReportsPage, errorReportsPerPage,
        banks, bankTotal, bankPage, bankItemsPerPage, bankDialog, bankEditing, bankJSON, releaseConfig, releaseForm, schoolCalendarConfig, schoolCalendarDays, schoolCalendarRange, schoolCalendarExceptions, schoolCalendarHeaders, schoolCalendarTableKey, schoolCalendarDayDialog, schoolCalendarRebuildDialog, schoolCalendarEditingDate, schoolCalendarEditingDateLabel, schoolCalendarEditingAdjustment, schoolCalendarEditingName,
        cdks, cdkTotal, cdkPage, cdkItemsPerPage, cdkDialog, cdkRevealDialog, cdkForm, createdCDKs, cdkSearch, shareCodes, shareCodesTotal, shareCodePage, shareCodeItemsPerPage, shareCodeDialog, shareCodeJSON, shareCodeKind, createdShareCode, shareCodeDetail, shareCodeDetailDialog, shareCodeDetailJSON, userSearch, userPage, userItemsPerPage, userTotal, userSortBy,
        shareCodeHeaders,
        selectedUser, userDialog, loginForm, userStatus, snackbar, chartEl, platformChartEl, collegeChartEl, eventChartEl, featureChartEl, nav, title, statCards,
        platformRows, versionRows, collegeRows, classRows, eventRows, cdkActivationRows, featureRows, userDetails, userHeaders, deviceHeaders, riskHeaders, auditHeaders, errorReportHeaders, userDeviceHeaders,
        bankHeaders, cdkHeaders, riskOptions, bankStatusOptions, bankNewOptions, bankCDKOptions, questionTypeOptions, bankPageCount, login, logout, switchView, load, loadBanks, loadCDKs, searchCDKs, loadShareCodes, loadRelease, loadSchoolCalendar, openSchoolCalendarRebuild, confirmSchoolCalendarRebuild, loadErrorReports, saveRelease, saveSchoolCalendar, openNewSchoolCalendarDay, openSchoolCalendarDay, deleteSchoolCalendarException, saveSchoolCalendarDay, loadUsers, searchUsers, sortUsers,
        showUser, closeUser, setStatus, disableUser, setRiskDeviceStatus, clearRiskEvents, acknowledge, openNewBank, closeBankEditor, setBankDialog, editBank, saveBank, setBankStatus, removeBank, openNewCDK, createCDK, copyCDK, setCDKStatus, openShareCodeDialog, createShareCode, deleteShareCode, openShareCodeDetail, setErrorReportStudentIgnored, clearErrorReports, rawItem, valueOrDash, formatDateTime, statusColor, statusLabel, riskTypeLabel, eventTypeLabel, actionLabel, riskStatusColor
      };
    },
    template,
  }).use(vuetify).mount('#app');
})();
