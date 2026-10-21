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
    const body = options.body;
    const isForm = typeof FormData !== 'undefined' && body instanceof FormData;
    const isBlob = typeof Blob !== 'undefined' && body instanceof Blob;
    const headers = Object.assign({}, options.headers || {});
    if (!isForm && !isBlob && !headers['Content-Type']) headers['Content-Type'] = 'application/json';
    if (isBlob && !headers['Content-Type']) headers['Content-Type'] = 'application/octet-stream';
    const access = localStorage.getItem('control_admin');
    if (access && !headers.Authorization) headers.Authorization = 'Bearer ' + access;
    if (csrf && options.method && options.method !== 'GET') headers['X-CSRF-Token'] = decodeURIComponent(csrf[1]);
    const response = await fetch(path, Object.assign({ credentials: 'same-origin' }, options, { headers }));
    let responseBody = {};
    try { responseBody = await response.json(); } catch (_) {}
    if (!response.ok) {
      const error = new Error((responseBody.error && responseBody.error.message) || '请求失败');
      error.status = response.status;
      error.code = responseBody.error && responseBody.error.code;
      throw error;
    }
    return responseBody;
  }

  // uploadWithProgress posts a FormData body with XMLHttpRequest so the console
  // can show real upload progress (fetch has no upload-progress event).
  function uploadWithProgress(path, body, onProgress) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open('POST', path);
      const access = localStorage.getItem('control_admin');
      if (access) xhr.setRequestHeader('Authorization', 'Bearer ' + access);
      const csrf = document.cookie.match(/(?:^|; )control_csrf=([^;]+)/);
      if (csrf) xhr.setRequestHeader('X-CSRF-Token', decodeURIComponent(csrf[1]));
      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable && onProgress) onProgress(event.loaded, event.total);
      };
      xhr.onload = () => {
        let responseBody = {};
        try { responseBody = JSON.parse(xhr.responseText); } catch (_) {}
        if (xhr.status >= 200 && xhr.status < 300) { resolve(responseBody); return; }
        const error = new Error((responseBody.error && responseBody.error.message) || '请求失败');
        error.status = xhr.status;
        error.code = responseBody.error && responseBody.error.code;
        reject(error);
      };
      xhr.onerror = () => reject(new Error('网络错误'));
      xhr.send(body);
    });
  }

  // Fetch an authenticated attachment and save it through a blob, so the
  // browser downloads instead of opening the PDF viewer.
  async function downloadFile(path, filename) {
    const headers = {};
    const access = localStorage.getItem('control_admin');
    if (access) headers.Authorization = 'Bearer ' + access;
    const response = await fetch(path, { credentials: 'same-origin', headers });
    if (!response.ok) {
      let body = {};
      try { body = await response.json(); } catch (_) {}
      const error = new Error((body.error && body.error.message) || '下载失败');
      error.status = response.status;
      error.code = body.error && body.error.code;
      throw error;
    }
    const blob = await response.blob();
    const objectUrl = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = objectUrl;
    anchor.download = filename || 'download.pdf';
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    setTimeout(() => URL.revokeObjectURL(objectUrl), 60000);
  }

  const pdfFileName = (name) => {
    const base = String(name || '资料').trim().replace(/[\\/:*?"<>|]+/g, '_') || '资料';
    return /\.pdf$/i.test(base) ? base : base + '.pdf';
  };

  // Exam submissions may be PDF or Word; the server stores the format.
  const documentFileName = (name, format) => {
    const ext = ['docx', 'doc'].includes(String(format || '').toLowerCase()) ? format.toLowerCase() : 'pdf';
    const base = String(name || '资料').trim().replace(/[\\/:*?"<>|]+/g, '_').replace(/\.(pdf|docx|doc)$/i, '') || '资料';
    return base + '.' + ext;
  };

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
      const parseAdminPerms = (raw) => {
        const perms = {};
        String(raw || '').split(',').forEach((part) => {
          const pieces = part.split('=');
          if (pieces.length === 2 && pieces[0].trim()) perms[pieces[0].trim()] = pieces[1].trim();
        });
        return perms;
      };
      const logged = ref(!!localStorage.getItem('control_admin'));
      const adminName = ref(localStorage.getItem('control_admin_name') || '');
      const adminPerms = ref(parseAdminPerms(localStorage.getItem('control_admin_perms')));
      const admins = ref([]);
      const adminDialog = ref(false);
      const adminEditingId = ref('');
      const adminForm = ref({ key: '' });
      const adminPermsForm = ref({});
      const drawer = ref(false);
      const VIEW_ROUTES = ['overview', 'users', 'admins', 'devices', 'risk', 'error-reports', 'library', 'textbooks', 'resources', 'courses', 'share-codes', 'config', 'audit'];
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
      const cdnStats = ref(null);
      const cdnTab = ref('');
      const cdnChartEl = ref(null);
      const cdnLoading = ref(false);
      const cdnError = ref('');
      const hostStats = ref(null);
      const hostLoading = ref(false);
      const hostError = ref('');
      const hostChartEl = ref(null);
      let hostChart = null;
      const bucketStats = ref(null);
      const bucketLoading = ref(false);
      const bucketError = ref('');
      const bucketChartEl = ref(null);
      let bucketChart = null;
      let bucketPollTimer = null;
      let cdnChart = null;
      let cdnPollTimer = null;
      let cdnPollAttempts = 0;
      let cdnTabInitialized = false;
      let statsTimer = null;
      const users = ref([]);
      const devices = ref([]);
      const risks = ref([]);
      const audit = ref([]);
      const errorReports = ref([]);
      const errorReportsTotal = ref(0);
      const errorReportsPage = ref(1);
      const errorReportsPerPage = ref(25);
      const errorReportSortBy = ref([]);
      const banks = ref([]);
      const bankTotal = ref(0);
      const bankPage = ref(1);
      const bankItemsPerPage = ref(25);
      const bankSortBy = ref([]);
      const cdks = ref([]);
      const cdkTotal = ref(0);
      const cdkPage = ref(1);
      const cdkItemsPerPage = ref(25);
      const cdkSortBy = ref([]);
      const courses = ref([]);
      const courseTotal = ref(0);
      const coursePage = ref(1);
      const courseItemsPerPage = ref(25);
      const courseSearch = ref('');
      const courseDialog = ref(false);
      const courseEditingId = ref('');
      const courseForm = ref({ code: '', name: '' });
      const courseSortBy = ref([]);
      const textbooks = ref([]);
      const textbookTotal = ref(0);
      const textbookPage = ref(1);
      const textbookItemsPerPage = ref(25);
      const textbookSearch = ref('');
      const textbookSortBy = ref([]);
      const textbookDialog = ref(false);
      const textbookEditingId = ref('');
      const textbookForm = ref({ name: '', tags: '', courseCodes: [] });
      const textbookSubmissions = ref([]);
      const textbookSubmissionTotal = ref(0);
      const archivedTextbooks = ref([]);
      const archivedTextbookTotal = ref(0);
      const resourceSubmissions = ref([]);
      const resourceSubmissionTotal = ref(0);
      const archivedResources = ref([]);
      const archivedResourceTotal = ref(0);
      const archivedBanks = ref([]);
      const archivedBankTotal = ref(0);
      const resourceDialog = ref(false);
      const resourceCourse = ref({ code: '', name: '' });
      const courseResources = ref([]);
      const contributorDialog = ref(false);
      const contributorKind = ref('');
      const contributorId = ref('');
      const contributorName = ref('');
      const contributorStudentID = ref('');
      const resourceFiles = ref([]);
      const uploading = ref(false);
      const uploadProgress = ref(0);
      const courseOptions = ref([]);
      const pdfCanvasEl = ref(null);
      const pdfFile = ref(null);
      const pdfPage = ref(1);
      const pdfPageCount = ref(0);
      const coverBlob = ref(null);
      const coverPreviewUrl = ref('');
      const existingCoverUrl = ref('');
      const cdkDialog = ref(false);
      const cdkRevealDialog = ref(false);
      const cdkForm = ref({ count: 1 });
      const createdCDKs = ref([]);
      const cdkSearch = ref('');
      const shareCodes = ref([]);
      const shareCodesTotal = ref(0);
      const shareCodePage = ref(1);
      const shareCodeItemsPerPage = ref(25);
      const shareCodeSortBy = ref([]);
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
        { title: '学号', key: 'student_id' },
        { title: '操作', key: 'actions', sortable: false },
      ];
      const userSearch = ref('');
      const userEmptyField = ref('');
      const userPage = ref(1);
      const userItemsPerPage = ref(25);
      const userTotal = ref(0);
      const userSortBy = ref([{ key: 'last_login_at', order: 'desc' }]);
      const releaseConfig = ref({ latestVersion: '', downloadUrl: '', changelog: '', updatedAt: '' });
      const releaseForm = ref({ latestVersion: '', downloadUrl: '', changelog: '' });
      const releaseTestConfig = ref({ latestVersion: '', downloadUrl: '', changelog: '', updatedAt: '' });
      const releaseTestForm = ref({ latestVersion: '', downloadUrl: '', changelog: '' });
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
      const schoolCalendarSlotsText = ref('');
      const formatSchoolCalendarSlots = (slots) => (Array.isArray(slots) ? slots : [])
        .map(slot => {
          const start = String((slot && slot.start) || '');
          const end = String((slot && slot.end) || '');
          if (!start && !end) return '';
          return start + '-' + end + (slot && slot.lab ? ' T' : '');
        })
        .filter(Boolean)
        .join('\n');
      const parseSchoolCalendarSlots = (text) => String(text || '')
        .split('\n')
        .map(line => line.trim())
        .filter(Boolean)
        .map((line, index) => {
          let body = line;
          let lab = false;
          if (/[Tt]$/.test(body)) { lab = true; body = body.slice(0, -1).trim(); }
          const parts = body.split(/[-~–—\s,，]+/).filter(Boolean);
          if (parts.length < 2) throw new Error('作息时间第 ' + (index + 1) + ' 行格式无效，应为 08:00-08:45，行尾可加 T');
          const start = parts[0];
          const end = parts[1];
          const clock = /^([01]\d|2[0-3]):[0-5]\d$/;
          if (!clock.test(start) || !clock.test(end)) throw new Error('作息时间第 ' + (index + 1) + ' 行时间格式无效');
          return { index: index + 1, start, end, lab };
        });
      const bankDialog = ref(false);
      const bankEditing = ref(false);
      const bankJSON = ref('');
      const bankCourseCodes = ref([]);
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
        { key: 'admins', label: '管理员', icon: 'mdi-account-cog-outline' },
        { key: 'risk', label: '风控', icon: 'mdi-shield-alert-outline' },
        { key: 'error-reports', label: '错误', icon: 'mdi-bug-outline' },
        { key: 'library', label: '题库', icon: 'mdi-book-open-page-variant' },
        { key: 'textbooks', label: '书单', icon: 'mdi-bookshelf' },
        { key: 'resources', label: '试卷', icon: 'mdi-file-document-multiple-outline' },
        { key: 'courses', label: '课程', icon: 'mdi-book-open-variant' },
        { key: 'share-codes', label: '分享码', icon: 'mdi-share-variant-outline' },
        { key: 'config', label: '配置', icon: 'mdi-cog-outline' },
        { key: 'audit', label: '审计', icon: 'mdi-history' }
      ];
      const title = computed(() => (nav.find(item => item.key === view.value) || nav[0]).label);
      const canAccess = (page, write) => {
        const perms = adminPerms.value || {};
        if (perms['*'] === 'rw') return true;
        const access = perms[page];
        if (!access) return false;
        return access === 'rw' || (!write && access === 'r');
      };
      const canRead = (page) => canAccess(page, false);
      const canWrite = (page) => canAccess(page, true);
      const visibleNav = computed(() => nav.filter(item => canRead(item.key)));
      const adminPageOptions = [
        { key: 'overview', label: '总览' },
        { key: 'users', label: '用户' },
        { key: 'devices', label: '设备' },
        { key: 'risk', label: '风控' },
        { key: 'error-reports', label: '错误' },
        { key: 'library', label: '题库' },
        { key: 'textbooks', label: '书单' },
        { key: 'resources', label: '试卷' },
        { key: 'courses', label: '课程' },
        { key: 'share-codes', label: '分享码' },
        { key: 'config', label: '配置' },
        { key: 'audit', label: '审计' }
      ];
      const adminPageKeys = adminPageOptions.map((page) => page.key);
      const adminPermOptions = [
        { title: '无', value: '' },
        { title: '只读', value: 'r' },
        { title: '读写', value: 'rw' }
      ];
      const adminHeaders = [
        { title: '密钥', key: 'key', sortable: false, width: '25%' },
        { title: '页面权限', key: 'permissions', sortable: false, width: '35%' },
        { title: '创建时间', key: 'created_at', sortable: false, width: '13%' },
        { title: '最近登录', key: 'last_login_at', sortable: false, width: '15%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '12%' }
      ];
      watch(mdAndUp, (desktop) => { if (!desktop) drawer.value = false; }, { immediate: true });

      // 总览显示比例（百分比，100=原始值，200=2 倍）。由后端保存在 data/.env。
      const overviewDialog = ref(false);
      const overviewScaleInput = ref(100);
      const overviewScalePercent = ref(100);
      const scaleOverviewValue = (value) => {
        const base = Number(value) || 0;
        const percent = Number(overviewScalePercent.value);
        return Math.round(base * (Number.isFinite(percent) ? percent : 100) / 100);
      };
      const scaleRows = (rows) => rows.map(row => Object.assign({}, row, { value: scaleOverviewValue(row.value) }));
      const applyOverviewScale = () => call(async () => {
        const percent = Math.max(0, Math.min(100000, Math.round(Number(overviewScaleInput.value) || 0)));
        await api('/api/v1/admin/overview-display', { method: 'PUT', body: JSON.stringify({ scalePercent: percent }) });
        overviewScalePercent.value = percent;
        overviewScaleInput.value = percent;
        overviewDialog.value = false;
        await nextTick();
        drawChart();
      });
      const resetOverviewScale = () => call(async () => {
        await api('/api/v1/admin/overview-display', { method: 'PUT', body: JSON.stringify({ scalePercent: 100 }) });
        overviewScalePercent.value = 100;
        overviewScaleInput.value = 100;
        await nextTick();
        drawChart();
      });
      const statCards = computed(() => [
        { label: 'DAU', value: scaleOverviewValue(overview.value.dau), icon: 'mdi-account-check-outline', color: 'primary' },
        { label: 'WAU', value: scaleOverviewValue(overview.value.wau), icon: 'mdi-account-clock-outline', color: 'secondary' },
        { label: 'MAU', value: scaleOverviewValue(overview.value.mau), icon: 'mdi-account-multiple-outline', color: 'info' },
        { label: '活跃设备', value: scaleOverviewValue(overview.value.active_devices), icon: 'mdi-devices', color: 'success' },
        { label: '用户总数', value: scaleOverviewValue(overview.value.total_users), icon: 'mdi-account-multiple', color: 'warning' },
        { label: '今日事件', value: scaleOverviewValue(overview.value.today_events), icon: 'mdi-pulse', color: 'error' }
      ]);
      const platformRows = computed(() => scaleRows(Object.entries(breakdown.value.platforms || {}).map(([label, value]) => ({ label, value }))));
      const versionRows = computed(() => scaleRows(Object.entries(breakdown.value.versions || {}).map(([label, value]) => ({ label, value }))));
      const distributionRows = (value) => Object.entries(value || {})
        .map(([label, value]) => ({ label, value: Number(value) || 0 }))
        .sort((a, b) => b.value - a.value || a.label.localeCompare(b.label));
      const collegeRows = computed(() => scaleRows(distributionRows(breakdown.value.colleges)));
      // "未知"（无班级数据）始终排在最后。
      const classRows = computed(() => {
        const rows = scaleRows(distributionRows(breakdown.value.classes));
        return rows.filter(row => row.label !== '未知').concat(rows.filter(row => row.label === '未知'));
      });
      const eventRows = computed(() => scaleRows(Object.entries(breakdown.value.event_hours || {})
        .map(([label, value]) => ({ label, value: Number(value) || 0 }))
        .sort((a, b) => a.label.localeCompare(b.label))));
      const cdkActivationRows = computed(() => [
        { label: '今日激活数', value: breakdown.value.cdk_activations_today || 0 },
        { label: '本周激活数', value: breakdown.value.cdk_activations_week || 0 },
        { label: '总激活数', value: breakdown.value.cdk_activations_total || 0 }
      ]);
      const featureLabels = { campus_card: '一卡通', power: '电费', exams: '考试安排', academic: '学业情况', network: '网络管理', repair: '极速报修', learning_center: '题库中心', school_calendar: '学校校历', book_list: '教材查询', teacher_evaluation: '教师评价', share_code: '分享码' };
      // 功能使用分布：仅统计用户主动使用的服务与分享码，不含启动/登录/退出等生命周期事件。
      const featureRows = computed(() => scaleRows(Object.entries(breakdown.value.feature_usage || {})
        .map(([key, value]) => ({ label: featureLabels[key] || valueOrDash(key), value: Number(value) || 0 }))
        .filter(row => row.value > 0)
        .sort((a, b) => b.value - a.value || a.label.localeCompare(b.label))));
      const userDetails = computed(() => {
        if (!selectedUser.value) return [];
        const user = selectedUser.value.user || {};
        return [
          { label: '显示名', value: valueOrDash(user.display_name) },
          { label: '学号', value: valueOrDash(selectedUser.value.student_id) },
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
        { title: '设备码', key: 'device_serial', width: '20%' }, { title: '平台', key: 'platform', width: '14%' },
        { title: '版本', key: 'app_version', width: '14%' }, { title: '安装标识', key: 'installation_id', width: '24%' },
        { title: '最近活动', key: 'last_seen_at', width: '18%' }, { title: '状态', key: 'status', width: '10%' }
      ];
      const riskHeaders = [
        { title: '类型', key: 'type', width: '16%' }, { title: '班级', key: 'class_name', width: '18%' },
        { title: '显示名', key: 'display_name', width: '16%' }, { title: '版本号', key: 'app_version', width: '12%' },
        { title: '次数', key: 'observed_count', align: 'center', width: '10%' }, { title: '时间', key: 'created_at', width: '14%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '10%' }
      ];
      const auditHeaders = [
        { title: '时间', key: 'created_at', width: '18%' }, { title: '动作', key: 'action', width: '18%' },
        { title: '操作者', key: 'actor_id', width: '16%' }, { title: '目标', key: 'target_text', width: '22%' },
        { title: '详情', key: 'detail', width: '26%' }
      ];
      const errorReportHeaders = [
        { title: '时间', key: 'occurred_at', width: '15%' },
        { title: '学号', key: 'student_id', width: '13%' },
        { title: '标题', key: 'title', width: '17%' },
        { title: '错误摘要', key: 'message', width: '22%' },
        { title: '版本', key: 'app_version', width: '9%' },
        { title: '平台', key: 'platform', width: '9%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '15%' },
      ];
      const userDeviceHeaders = [
        { title: '设备码', key: 'device_serial', width: '52%' }, { title: '平台', key: 'platform', width: '18%' },
        { title: '最近活动', key: 'last_seen_at', width: '30%' }
      ];
      const bankHeaders = [
        { title: '题库 ID', key: 'id', width: '14%' }, { title: '名称', key: 'name', width: '17%' },
        { title: '关联课程', key: 'courseCode', sortable: true, width: '11%' },
        { title: '题目数', key: 'question_count', align: 'center', width: '8%' }, { title: '状态', key: 'status', width: '8%' },
        { title: 'CDK 解锁', key: 'requiresCDK', align: 'center', width: '9%' }, { title: '更新时间', key: 'updated_at', width: '8%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '25%' }
      ];
      const cdkHeaders = [
        { title: 'CDK ID', key: 'id', width: '20%' },
        { title: '题库', key: 'question_bank_id', width: '14%' },
        { title: '状态', key: 'status', width: '13%' }, { title: '绑定学号', key: 'bound_student_id', width: '16%' },
        { title: '创建时间', key: 'created_at', width: '13%' }, { title: '使用时间', key: 'used_at', width: '13%' }, { title: '操作', key: 'actions', sortable: false, align: 'center', width: '11%' }
      ];
      const textbookHeaders = [
        { title: '教材名称', key: 'name', sortable: true, width: '20%' },
        { title: '标签', key: 'tags', width: '14%' },
        { title: '关联课程', key: 'course_code', sortable: true, width: '13%' },
        { title: '贡献者学号', key: 'contributor_student_id', width: '14%' },
        { title: '下载量', key: 'download_count', sortable: true, align: 'center', width: '8%' },
        { title: '更新时间', key: 'updated_at', width: '10%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '21%' }
      ];
      const submissionHeaders = [
        { title: '名称', key: 'name', width: '22%' },
        { title: '类型', key: 'format', align: 'center', width: '7%' },
        { title: '学号', key: 'student_id', width: '13%' },
        { title: '关联课程', key: 'course_code', width: '11%' },
        { title: '提交人', key: 'submitted_by_name', width: '12%' },
        { title: '大小', key: 'size_bytes', align: 'center', width: '7%' },
        { title: '提交时间', key: 'created_at', width: '12%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '16%' }
      ];
      const resourceHeaders = [
        { title: '课程编号', key: 'code', sortable: true, width: '22%' },
        { title: '课程名称', key: 'name', sortable: true, width: '34%' },
        { title: '试卷数', key: 'resource_count', align: 'center', width: '14%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '30%' }
      ];
      const courseResourceHeaders = [
        { title: '文件名', key: 'name', width: '26%' },
        { title: '贡献者学号', key: 'contributor_student_id', width: '15%' },
        { title: '下载量', key: 'download_count', align: 'center', width: '10%' },
        { title: '上传时间', key: 'created_at', width: '15%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '34%' }
      ];
      const archivedTextbookHeaders = [
        { title: '教材名称', key: 'name', width: '25%' },
        { title: '关联课程', key: 'course_code', width: '14%' },
        { title: '贡献者学号', key: 'contributor_student_id', width: '14%' },
        { title: '下载量', key: 'download_count', align: 'center', width: '8%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '27%' }
      ];
      const archivedResourceHeaders = [
        { title: '文件名', key: 'name', width: '26%' },
        { title: '关联课程', key: 'course_code', width: '12%' },
        { title: '类型', key: 'format', align: 'center', width: '7%' },
        { title: '贡献者学号', key: 'contributor_student_id', width: '14%' },
        { title: '下载量', key: 'download_count', align: 'center', width: '8%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '25%' }
      ];
      const archivedBankHeaders = [
        { title: '题库 ID', key: 'id', width: '15%' },
        { title: '名称', key: 'name', width: '23%' },
        { title: '关联课程', key: 'courseCode', width: '16%' },
        { title: '题目数', key: 'question_count', align: 'center', width: '8%' },
        { title: '状态', key: 'status', width: '9%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '24%' }
      ];
      const courseHeaders = [
        { title: '课程编号', key: 'code', sortable: true, width: '20%' },
        { title: '课程名称', key: 'name', sortable: true, width: '30%' },
        { title: '关联书籍', key: 'books_text', width: '33%' },
        { title: '更新时间', key: 'updated_at', width: '11%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '6%' }
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
        bankCourseCodes.value = [];
      };
      const setBankDialog = (visible) => { if (visible) bankDialog.value = true; else closeBankEditor(); };
      const clearAdminSession = () => {
        localStorage.removeItem('control_admin');
        localStorage.removeItem('control_admin_name');
        localStorage.removeItem('control_admin_perms');
        adminName.value = '';
        adminPerms.value = {};
        logged.value = false;
        drawer.value = false;
        selectedUser.value = null;
        userDialog.value = false;
        closeBankEditor();
        cdkDialog.value = false;
        cdkRevealDialog.value = false;
        createdCDKs.value = [];
        textbookDialog.value = false;
        adminDialog.value = false;
        resourceDialog.value = false;
        courseDialog.value = false;
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
      const sortQuery = (sortBy) => {
        const item = Array.isArray(sortBy) ? (sortBy[0] || {}) : {};
        return item.key ? '&sort=' + encodeURIComponent(item.key) + '&order=' + encodeURIComponent(item.order || 'asc') : '';
      };
      const login = () => call(async () => {
        const out = await api('/api/v1/admin/session', { method: 'POST', body: JSON.stringify(loginForm.value) });
        localStorage.setItem('control_admin', out.access_token);
        localStorage.setItem('control_admin_name', out.name || '');
        localStorage.setItem('control_admin_perms', out.permissions || '');
        adminName.value = out.name || '';
        adminPerms.value = parseAdminPerms(out.permissions || '');
        logged.value = true; notify('登录成功'); ensureViewAllowed(); await load();
      });
      const logout = () => call(async () => {
        await api('/api/v1/admin/session', { method: 'DELETE' });
        localStorage.removeItem('control_admin'); logged.value = false; drawer.value = false; snackbar.value.show = false;
      });
      const loadOverviewScale = async () => {
        const display = await api('/api/v1/admin/overview-display');
        const percent = Number(display && display.scalePercent);
        if (Number.isFinite(percent)) { overviewScalePercent.value = percent; overviewScaleInput.value = percent; }
      };
      const formatBytes = (value) => {
        const n = Number(value) || 0;
        if (n < 1000) return n + ' B';
        const units = ['KB', 'MB', 'GB', 'TB', 'PB'];
        let v = n / 1000, i = 0;
        while (v >= 1000 && i < units.length - 1) { v /= 1000; i += 1; }
        return v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2) + ' ' + units[i];
      };
      const formatBitrate = (value) => {
        const n = Number(value) || 0;
        if (n < 1000) return n + ' bps';
        const units = ['Kbps', 'Mbps', 'Gbps', 'Tbps'];
        let v = n / 1000, i = 0;
        while (v >= 1000 && i < units.length - 1) { v /= 1000; i += 1; }
        return v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2) + ' ' + units[i];
      };
      const formatPercent = (value) => ((Number(value) || 0) * 100).toFixed(1) + '%';
      const formatBytesIEC = (value) => {
        const n = Number(value) || 0;
        if (n < 1024) return n + ' B';
        const units = ['KB', 'MB', 'GB', 'TB'];
        let v = n / 1024, i = 0;
        while (v >= 1024 && i < units.length - 1) { v /= 1024; i += 1; }
        return v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2) + ' ' + units[i];
      };
      const cdnDomains = computed(() => (cdnStats.value && cdnStats.value.domains) || []);
      const cdnDomain = computed(() => cdnTab.value === '__host__' ? null : (cdnDomains.value.find(item => item.domain === cdnTab.value) || cdnDomains.value[0] || null));
      const cdnCards = computed(() => {
        const item = cdnDomain.value;
        if (!item) return [];
        const days = (cdnStats.value && cdnStats.value.days) || 30;
        return [
          { label: '今日流量', value: formatBytes(item.today_flux) },
          { label: '今日请求', value: Number(item.today_requests || 0).toLocaleString() },
          { label: '今日回源', value: formatBytes(item.today_origin_flux) },
          { label: '缓存命中率', value: formatPercent(item.hit_rate) },
          { label: '近 ' + days + ' 天流量', value: formatBytes(item.flux) },
          { label: '峰值带宽', value: formatBitrate(item.bandwidth_peak) }
        ];
      });
      const cdnPackage = computed(() => {
        const pkg = cdnStats.value && cdnStats.value.traffic_package;
        if (!pkg) return null;
        return {
          remaining: formatBytes(pkg.remaining),
          total: formatBytes(pkg.total),
          expire: pkg.expire_time ? pkg.expire_time.slice(0, 10) : '',
          count: pkg.count || 1
        };
      });
      const activePackage = computed(() => {
        if (cdnTab.value === '__host__') {
          const pkg = hostStats.value && hostStats.value.traffic_package;
          if (!pkg) return null;
          return {
            remaining: formatBytesIEC(pkg.remaining),
            total: formatBytesIEC(pkg.total),
            expire: pkg.expire_time ? pkg.expire_time.slice(0, 10) : '',
            count: 1
          };
        }
        if (cdnTab.value.startsWith('__bucket_')) return null;
        return cdnPackage.value;
      });
      const hostCards = computed(() => {
        const item = hostStats.value;
        if (!item) return [];
        return [
          { label: '外网出带宽', value: (Number(item.out_bandwidth) || 0).toFixed(2) + ' Mbps' },
          { label: '外网入带宽', value: (Number(item.in_bandwidth) || 0).toFixed(2) + ' Mbps' },
          { label: '带宽使用率', value: (Number(item.out_ratio) || 0).toFixed(1) + '%' },
          { label: 'CPU', value: (Number(item.cpu) || 0).toFixed(1) + '%' },
          { label: '内存', value: (Number(item.mem) || 0).toFixed(1) + '%' },
          { label: '磁盘', value: (Number(item.disk) || 0).toFixed(1) + '%' }
        ];
      });
      const activeBucketKey = computed(() => cdnTab.value.indexOf('__bucket_') === 0 ? cdnTab.value.slice('__bucket_'.length).replace(/__$/, '') : '');
      const activeBucket = computed(() => {
        if (!activeBucketKey.value) return null;
        const list = (bucketStats.value && bucketStats.value.buckets) || [];
        return list.find(item => item.key === activeBucketKey.value) || null;
      });
      const formatStorageMB = (value) => {
        const mb = Number(value) || 0;
        if (mb < 1024) return mb.toFixed(mb >= 10 ? 0 : 1) + ' MB';
        return (mb / 1024).toFixed(2) + ' GB';
      };
      const bucketCards = computed(() => {
        const item = activeBucket.value;
        if (!item) return [];
        const days = (bucketStats.value && bucketStats.value.days) || 30;
        return [
          { label: '标准存储', value: formatStorageMB(item.std_storage_mb) },
          { label: '对象数', value: Number(item.object_number || 0).toLocaleString() },
          { label: '请求数（近 ' + days + ' 天）', value: Number(item.total_requests || 0).toLocaleString() },
          { label: 'CDN 回源（近 ' + days + ' 天）', value: formatBytes(item.cdn_origin_traffic) },
          { label: '上传流量（近 ' + days + ' 天）', value: formatBytes(item.inbound_traffic) },
          { label: '外网下行（近 ' + days + ' 天）', value: formatBytes(item.internet_traffic) }
        ];
      });
      const drawCDNChart = () => {
        if (cdnChart) { cdnChart.dispose(); cdnChart = null; }
        const item = cdnDomain.value;
        if (!window.echarts || !cdnChartEl.value || !item) return;
        cdnChart = window.echarts.init(cdnChartEl.value);
        const points = item.flux_series || [];
        cdnChart.setOption({
          animationDuration: 350,
          tooltip: { trigger: 'axis', valueFormatter: (value) => formatBytes(value) },
          grid: { left: 6, right: 18, top: 20, bottom: 30, containLabel: true },
          xAxis: { type: 'category', data: points.map(point => point.day), axisLabel: { color: '#697984' }, axisLine: { lineStyle: { color: '#d8e0e4' } } },
          yAxis: { type: 'value', axisLabel: { color: '#697984', formatter: (value) => formatBytes(value) }, splitLine: { lineStyle: { color: '#edf1f3' } } },
          series: [{ name: '流量', type: 'line', smooth: true, showSymbol: false, areaStyle: { opacity: 0.12 }, data: points.map(point => point.value), itemStyle: { color: '#176b4d' }, lineStyle: { width: 3 } }]
        });
      };
      const scheduleCDNRefreshPoll = () => {
        if (cdnPollAttempts >= 3) return;
        cdnPollAttempts += 1;
        if (cdnPollTimer) clearTimeout(cdnPollTimer);
        cdnPollTimer = setTimeout(() => { cdnPollTimer = null; loadCDNStats(false, true); }, 4000);
      };
      const drawHostChart = () => {
        if (hostChart) { hostChart.dispose(); hostChart = null; }
        if (!window.echarts || !hostChartEl.value || !hostStats.value) return;
        hostChart = window.echarts.init(hostChartEl.value);
        const points = hostStats.value.out_series || [];
        hostChart.setOption({
          animationDuration: 350,
          tooltip: { trigger: 'axis', valueFormatter: (value) => (Number(value) || 0).toFixed(2) + ' Mbps' },
          grid: { left: 6, right: 18, top: 20, bottom: 30, containLabel: true },
          xAxis: { type: 'category', data: points.map(point => point.day), axisLabel: { color: '#697984' }, axisLine: { lineStyle: { color: '#d8e0e4' } } },
          yAxis: { type: 'value', axisLabel: { color: '#697984', formatter: (value) => value + ' Mbps' }, splitLine: { lineStyle: { color: '#edf1f3' } } },
          series: [{ name: '出带宽', type: 'line', smooth: true, showSymbol: false, areaStyle: { opacity: 0.12 }, data: points.map(point => point.value), itemStyle: { color: '#287ca8' }, lineStyle: { width: 3 } }]
        });
      };
      const loadHostStats = async (refresh, silent) => {
        if (!silent) { hostLoading.value = true; hostError.value = ''; }
        try {
          const out = await api('/api/v1/admin/host/stats?days=30' + (refresh ? '&refresh=1' : ''));
          hostStats.value = out;
          await nextTick();
          drawHostChart();
        } catch (e) {
          if (e && e.status === 401) { clearAdminSession(); return; }
          if (!silent) hostError.value = (e && e.message) || '主机统计加载失败';
        } finally {
          if (!silent) hostLoading.value = false;
        }
      };
      const drawBucketChart = () => {
        if (bucketChart) { bucketChart.dispose(); bucketChart = null; }
        const item = activeBucket.value;
        if (!window.echarts || !bucketChartEl.value || !item) return;
        bucketChart = window.echarts.init(bucketChartEl.value);
        const points = item.origin_series || [];
        bucketChart.setOption({
          animationDuration: 350,
          tooltip: { trigger: 'axis', valueFormatter: (value) => formatBytes(value) },
          grid: { left: 6, right: 18, top: 20, bottom: 30, containLabel: true },
          xAxis: { type: 'category', data: points.map(point => point.day), axisLabel: { color: '#697984' }, axisLine: { lineStyle: { color: '#d8e0e4' } } },
          yAxis: { type: 'value', axisLabel: { color: '#697984', formatter: (value) => formatBytes(value) }, splitLine: { lineStyle: { color: '#edf1f3' } } },
          series: [{ name: '回源流量', type: 'line', smooth: true, showSymbol: false, areaStyle: { opacity: 0.12 }, data: points.map(point => point.value), itemStyle: { color: '#805ad5' }, lineStyle: { width: 3 } }]
        });
      };
      const loadBucketStats = async (refresh, silent) => {
        if (!silent) { bucketLoading.value = true; bucketError.value = ''; }
        try {
          const out = await api('/api/v1/admin/buckets/stats?days=30' + (refresh ? '&refresh=1' : ''));
          bucketStats.value = out;
          await nextTick();
          drawBucketChart();
          if (out && out.stale) {
            if (bucketPollTimer) clearTimeout(bucketPollTimer);
            bucketPollTimer = setTimeout(() => { bucketPollTimer = null; loadBucketStats(false, true); }, 4000);
          }
        } catch (e) {
          if (e && e.status === 401) { clearAdminSession(); return; }
          if (!silent) bucketError.value = (e && e.message) || '存储桶统计加载失败';
        } finally {
          if (!silent) bucketLoading.value = false;
        }
      };
      const refreshActiveStats = () => {
        if (cdnTab.value === '__host__') return loadHostStats(true);
        if (activeBucketKey.value) return loadBucketStats(true);
        return loadCDNStats(true);
      };
      const loadCDNStats = async (refresh, silent) => {
        if (!silent) { cdnLoading.value = true; cdnError.value = ''; }
        try {
          const out = await api('/api/v1/admin/cdn/stats?days=30' + (refresh ? '&refresh=1' : ''));
          if (!out || !out.stale) cdnPollAttempts = 0;
          cdnStats.value = out;
          const domains = (out && out.domains) || [];
          if (!cdnTabInitialized && domains.length) {
            cdnTab.value = domains[0].domain;
            cdnTabInitialized = true;
          } else if (cdnTabInitialized && !cdnTab.value.startsWith('__') && !domains.some(item => item.domain === cdnTab.value)) {
            cdnTab.value = domains[0] ? domains[0].domain : '';
          }
          await nextTick();
          drawCDNChart();
          if (out && out.stale) scheduleCDNRefreshPoll();
        } catch (e) {
          if (e && e.status === 401) { clearAdminSession(); return; }
          if (!silent) cdnError.value = (e && e.message) || 'CDN 统计加载失败';
        } finally {
          if (!silent) cdnLoading.value = false;
        }
      };
      watch(cdnTab, async () => {
        await nextTick();
        if (cdnTab.value === '__host__') {
          if (!hostStats.value && !hostLoading.value) loadHostStats();
          else drawHostChart();
        } else if (activeBucketKey.value) {
          if (!bucketStats.value && !bucketLoading.value) loadBucketStats();
          else drawBucketChart();
        } else {
          drawCDNChart();
        }
      });
      // Refresh the overview statistics silently every 10 minutes.
      const refreshStatsSilently = () => {
        if (!logged.value) return;
        loadCDNStats(false, true);
        if (hostStats.value) loadHostStats(false, true);
        if (bucketStats.value) loadBucketStats(false, true);
      };
      const loadOverview = async () => {
        overview.value = await api('/api/v1/admin/metrics/overview');
        breakdown.value = await api('/api/v1/admin/metrics/breakdown');
        await loadOverviewScale();
        // 趋势按天统计，当天数据尚未固定，只展示到昨天。
        const today = new Date(Date.now() + 8 * 3600 * 1000).toISOString().slice(0, 10);
        series.value = ((await api('/api/v1/admin/metrics/series?days=30')).items || []).filter(row => row.day < today);
        await nextTick(); drawChart();
        loadCDNStats();
        if (cdnTab.value === '__host__') loadHostStats();
      };
      const loadBanks = async () => {
        const offset = (bankPage.value - 1) * bankItemsPerPage.value;
        const out = await api('/api/v1/admin/question-banks?limit=' + bankItemsPerPage.value + '&offset=' + offset + sortQuery(bankSortBy.value));
        banks.value = out.items || []; bankTotal.value = out.total || 0;
      };
      const loadArchivedBanks = async () => {
        const out = await api('/api/v1/admin/question-banks?archived=1&limit=200');
        archivedBanks.value = out.items || []; archivedBankTotal.value = out.total || 0;
      };
      const sortBanks = (sortBy) => call(async () => { bankSortBy.value = Array.isArray(sortBy) ? sortBy : []; bankPage.value = 1; await loadBanks(); });
      const loadCDKs = async () => {
        const offset = (cdkPage.value - 1) * cdkItemsPerPage.value;
        const search = (cdkSearch.value || '').trim();
        const query = search ? '&q=' + encodeURIComponent(search) : '';
        const out = await api('/api/v1/admin/library-cdks?limit=' + cdkItemsPerPage.value + '&offset=' + offset + query + sortQuery(cdkSortBy.value));
        cdks.value = out.items || []; cdkTotal.value = out.total || 0;
      };
      const sortCDKs = (sortBy) => call(async () => { cdkSortBy.value = Array.isArray(sortBy) ? sortBy : []; cdkPage.value = 1; await loadCDKs(); });
      const booksLabel = (books) => (books || [])
        .map((book) => (book.tags ? (book.name + ' / ' + book.tags) : book.name))
        .join('；');
      const loadCourses = async () => {
        const offset = (coursePage.value - 1) * courseItemsPerPage.value;
        const search = (courseSearch.value || '').trim();
        const query = search ? '&q=' + encodeURIComponent(search) : '';
        const out = await api('/api/v1/admin/courses?limit=' + courseItemsPerPage.value + '&offset=' + offset + query + sortQuery(courseSortBy.value));
        courses.value = (out.items || []).map(item => Object.assign({}, item, { books_text: booksLabel(item.books) }));
        courseTotal.value = out.total || 0;
      };
      const sortCourses = (sortBy) => call(async () => { courseSortBy.value = Array.isArray(sortBy) ? sortBy : []; coursePage.value = 1; await loadCourses(); });
      const searchCourses = () => call(async () => { coursePage.value = 1; await loadCourses(); });
      const openNewCourse = () => {
        courseEditingId.value = '';
        courseForm.value = { code: '', name: '' };
        courseDialog.value = true;
      };
      const editCourse = (row) => {
        const item = rawItem(row);
        courseEditingId.value = item.id || '';
        courseForm.value = { code: item.code || '', name: item.name || '' };
        courseDialog.value = true;
      };
      const saveCourse = () => call(async () => {
        const form = courseForm.value;
        if (!String(form.code || '').trim()) throw new Error('请填写课程编号');
        if (!String(form.name || '').trim()) throw new Error('请填写课程名称');
        const editing = !!courseEditingId.value;
        const path = '/api/v1/admin/courses' + (editing ? '/' + encodeURIComponent(courseEditingId.value) : '');
        const body = { code: form.code, name: form.name };
        await api(path, { method: editing ? 'PUT' : 'POST', body: JSON.stringify(body) });
        courseDialog.value = false;
        notify(editing ? '课程已更新' : '课程已创建');
        await loadCourses();
      });
      const deleteCourse = (row) => call(async () => {
        const item = rawItem(row);
        if (!window.confirm('确定删除课程“' + item.name + '”吗？App 重新上报后会再次收录。')) return;
        await api('/api/v1/admin/courses/' + encodeURIComponent(item.id), { method: 'DELETE' });
        notify('课程已删除');
        await loadCourses();
      });
      const loadCourseOptions = async () => {
        const out = await api('/api/v1/admin/courses?limit=200');
        courseOptions.value = (out.items || []).map((item) => ({ title: (item.code || '') + ' ' + (item.name || ''), value: item.code || '' }));
      };
      const loadTextbooks = async () => {
        const offset = (textbookPage.value - 1) * textbookItemsPerPage.value;
        const search = (textbookSearch.value || '').trim();
        const query = search ? '&q=' + encodeURIComponent(search) : '';
        const out = await api('/api/v1/admin/textbooks?limit=' + textbookItemsPerPage.value + '&offset=' + offset + query + sortQuery(textbookSortBy.value));
        textbooks.value = out.items || []; textbookTotal.value = out.total || 0;
      };
      const loadArchivedTextbooks = async () => {
        const out = await api('/api/v1/admin/textbooks?archived=1&limit=200');
        archivedTextbooks.value = out.items || []; archivedTextbookTotal.value = out.total || 0;
      };
      // ── App upload review queue ──
      const submissionBase = (kind) => '/api/v1/admin/' + kind + '-submissions';
      const loadTextbookSubmissions = async () => {
        const out = await api(submissionBase('textbook') + '?limit=200');
        textbookSubmissions.value = out.items || []; textbookSubmissionTotal.value = out.total || 0;
      };
      const loadResourceSubmissions = async () => {
        const out = await api(submissionBase('resource') + '?limit=200');
        resourceSubmissions.value = out.items || []; resourceSubmissionTotal.value = out.total || 0;
      };
      const downloadSubmission = (kind, row) => call(async () => {
        const item = rawItem(row);
        await downloadFile(submissionBase(kind) + '/' + encodeURIComponent(item.id) + '/download', documentFileName(item.name, item.format));
        notify('已开始下载');
      });
      const approveSubmission = (kind, row) => call(async () => {
        const item = rawItem(row);
        await api(submissionBase(kind) + '/' + encodeURIComponent(item.id) + '/approve', { method: 'POST' });
        notify(kind === 'textbook' ? '已通过审核，教材已加入书单' : '已通过审核，试卷已加入课程');
        if (kind === 'textbook') {
          await Promise.all([loadTextbookSubmissions(), loadTextbooks()]);
        } else {
          await Promise.all([loadResourceSubmissions(), loadCourses()]);
          if (resourceDialog.value && resourceCourse.value.code === item.course_code) await loadCourseResources();
        }
      });
      const rejectSubmission = (kind, row) => call(async () => {
        const item = rawItem(row);
        if (!window.confirm('确定驳回并删除“' + (item.name || '该资料') + '”吗？')) return;
        await api(submissionBase(kind) + '/' + encodeURIComponent(item.id) + '/reject', { method: 'POST' });
        notify('已驳回');
        if (kind === 'textbook') await loadTextbookSubmissions(); else await loadResourceSubmissions();
      });
      const sortTextbooks = (sortBy) => call(async () => { textbookSortBy.value = Array.isArray(sortBy) ? sortBy : []; textbookPage.value = 1; await loadTextbooks(); });
      const searchTextbooks = () => call(async () => { textbookPage.value = 1; await loadTextbooks(); });
      const openNewTextbook = () => call(async () => {
        textbookEditingId.value = '';
        textbookForm.value = { name: '', tags: '', courseCodes: [] };
        resetPdfState();
        await loadCourseOptions();
        textbookDialog.value = true;
      });
      const editTextbook = (row) => call(async () => {
        const item = rawItem(row);
        textbookEditingId.value = item.id || '';
        const codes = Array.isArray(item.course_codes) && item.course_codes.length ? item.course_codes : (item.course_code ? [item.course_code] : []);
        textbookForm.value = { name: item.name || '', tags: item.tags || '', courseCodes: codes };
        resetPdfState();
        await loadCourseOptions();
        textbookDialog.value = true;
        if (item.id && item.has_cover) await loadExistingCover(item.id);
      });
      const saveTextbook = () => call(async () => {
        const form = textbookForm.value;
        if (!String(form.name || '').trim()) throw new Error('请填写教材名称');
        const editing = !!textbookEditingId.value;
        const path = '/api/v1/admin/textbooks' + (editing ? '/' + encodeURIComponent(textbookEditingId.value) : '');
        const body = { name: form.name, tags: form.tags, course_codes: form.courseCodes || [] };
        const saved = await api(path, { method: editing ? 'PUT' : 'POST', body: JSON.stringify(body) });
        const id = editing ? textbookEditingId.value : (saved && saved.id);
        if (id) await uploadTextbookAssets(id);
        textbookDialog.value = false;
        notify(editing ? '教材已更新' : '教材已创建');
        await loadTextbooks();
      });
      const openContributorDialog = (kind, item) => {
        contributorKind.value = kind;
        contributorId.value = item.id || '';
        contributorName.value = item.name || '';
        contributorStudentID.value = item.contributor_student_id || '';
        contributorDialog.value = true;
      };
      const saveContributor = () => call(async () => {
        const kind = contributorKind.value;
        const id = contributorId.value;
        if (!id) return;
        const collection = kind === 'textbook' ? 'textbooks' : 'resources';
        const studentId = String(contributorStudentID.value || '').trim();
        await api('/api/v1/admin/' + collection + '/' + encodeURIComponent(id) + '/contributor', {
          method: 'POST',
          body: JSON.stringify({ student_id: studentId }),
        });
        notify(studentId ? '已绑定贡献者学号' : '已解除贡献者绑定');
        contributorDialog.value = false;
        if (kind === 'textbook') {
          await loadTextbooks();
        } else {
          await loadCourseResources();
          await loadCourses();
        }
      });
      const deleteTextbook = (row) => call(async () => {
        const item = rawItem(row);
        if (!window.confirm('确定删除教材“' + item.name + '”吗？App 重新上报后会再次收录。')) return;
        await api('/api/v1/admin/textbooks/' + encodeURIComponent(item.id), { method: 'DELETE' });
        notify('教材已删除');
        await Promise.all([loadTextbooks(), loadArchivedTextbooks()]);
      });
      const downloadTextbook = (row) => call(async () => {
        const item = rawItem(row);
        await downloadFile('/api/v1/admin/textbooks/' + encodeURIComponent(item.id) + '/download', pdfFileName(item.name));
        notify('已开始下载');
        await Promise.all([loadTextbooks(), loadArchivedTextbooks()]);
      });
      const setTextbookArchived = (row, archived) => call(async () => {
        const item = rawItem(row);
        await api('/api/v1/admin/textbooks/' + encodeURIComponent(item.id) + (archived ? '/archive' : '/restore'), { method: 'POST' });
        notify(archived ? '教材已归档' : '教材已恢复');
        await Promise.all([loadTextbooks(), loadArchivedTextbooks()]);
      });
      const loadCourseResources = async () => {
        const code = (resourceCourse.value && resourceCourse.value.code) || '';
        const out = await api('/api/v1/admin/resources?course_code=' + encodeURIComponent(code));
        courseResources.value = out.items || [];
      };
      const loadArchivedResources = async () => {
        const out = await api('/api/v1/admin/resources?archived=1&limit=200');
        archivedResources.value = out.items || []; archivedResourceTotal.value = out.total || 0;
      };
      const openResourceDialog = (row) => call(async () => {
        const item = rawItem(row);
        resourceCourse.value = { code: item.code || '', name: item.name || '' };
        resourceFiles.value = [];
        await loadCourseResources();
        resourceDialog.value = true;
      });
      const onResourceDrop = (event) => {
        const dropped = Array.from(event.dataTransfer ? event.dataTransfer.files : []);
        const pdfs = dropped.filter((file) => file.type === 'application/pdf' || /\.pdf$/i.test(file.name || ''));
        if (!pdfs.length) return;
        const current = Array.isArray(resourceFiles.value) ? resourceFiles.value.slice() : (resourceFiles.value ? [resourceFiles.value] : []);
        resourceFiles.value = current.concat(pdfs);
      };
      const uploadResources = () => call(async () => {
        const files = Array.isArray(resourceFiles.value) ? resourceFiles.value : (resourceFiles.value ? [resourceFiles.value] : []);
        if (!files.length) throw new Error('请选择至少一个 PDF 文件');
        const data = new FormData();
        data.append('course_code', (resourceCourse.value && resourceCourse.value.code) || '');
        files.forEach((file) => data.append('files', file, file.name || 'resource.pdf'));
        uploading.value = true; uploadProgress.value = 0;
        let out;
        try {
          out = await uploadWithProgress('/api/v1/admin/resources/upload', data, (loaded, total) => {
            uploadProgress.value = total > 0 ? Math.round((loaded / total) * 100) : 0;
          });
        } finally {
          uploading.value = false;
        }
        const created = out && out.items ? out.items.length : 0;
        const failed = out && out.failed ? out.failed : [];
        resourceFiles.value = [];
        await loadCourseResources();
        await loadCourses();
        if (failed.length) {
          notify('已上传 ' + created + ' 个，失败 ' + failed.length + ' 个：' + failed.map(f => f.name + '（' + f.reason + '）').join('；'), 'warning');
        } else {
          notify('已上传 ' + created + ' 个 PDF');
        }
      });
      const deleteResource = (row) => call(async () => {
        const item = rawItem(row);
        if (!window.confirm('确定删除文件“' + item.name + '”吗？')) return;
        await api('/api/v1/admin/resources/' + encodeURIComponent(item.id), { method: 'DELETE' });
        notify('文件已删除');
        await loadCourseResources();
        await Promise.all([loadCourses(), loadArchivedResources()]);
      });
      const downloadResource = (row) => call(async () => {
        const item = rawItem(row);
        await downloadFile('/api/v1/admin/resources/' + encodeURIComponent(item.id) + '/download', documentFileName(item.name, item.format));
        notify('已开始下载');
        await Promise.all([loadCourseResources(), loadArchivedResources()]);
      });
      const setResourceArchived = (row, archived) => call(async () => {
        const item = rawItem(row);
        await api('/api/v1/admin/resources/' + encodeURIComponent(item.id) + (archived ? '/archive' : '/restore'), { method: 'POST' });
        notify(archived ? '试卷已归档' : '试卷已恢复');
        await Promise.all([loadCourseResources(), loadCourses(), loadArchivedResources()]);
      });

      // ── Textbook PDF + cover (rendered in the browser with pdf.js) ──
      let pdfModule = null;
      let pdfDoc = null;
      const loadPdfModule = () => {
        if (!pdfModule) {
          pdfModule = import('/vendor/js/pdf.min.mjs').then((mod) => {
            const lib = mod.default ?? mod;
            lib.GlobalWorkerOptions.workerSrc = '/vendor/js/pdf.worker.min.mjs';
            return lib;
          });
        }
        return pdfModule;
      };
      const clearCoverPreview = () => {
        if (coverPreviewUrl.value) URL.revokeObjectURL(coverPreviewUrl.value);
        coverPreviewUrl.value = '';
        coverBlob.value = null;
      };
      const resetPdfState = () => {
        pdfDoc = null;
        pdfFile.value = null;
        pdfPage.value = 1;
        pdfPageCount.value = 0;
        existingCoverUrl.value = '';
        clearCoverPreview();
      };
      const renderPdfPage = async (page) => {
        if (!pdfDoc || !pdfCanvasEl.value) return;
        const rendered = await pdfDoc.getPage(page);
        const viewport = rendered.getViewport({ scale: 1.1 });
        const canvas = pdfCanvasEl.value;
        canvas.width = Math.floor(viewport.width);
        canvas.height = Math.floor(viewport.height);
        await rendered.render({ canvasContext: canvas.getContext('2d'), viewport }).promise;
      };
      const onPdfSelected = (files) => call(async () => {
        const file = Array.isArray(files) ? files[0] : files;
        pdfDoc = null;
        pdfPageCount.value = 0;
        clearCoverPreview();
        if (!file) { pdfFile.value = null; return; }
        pdfFile.value = file;
        const lib = await loadPdfModule();
        pdfDoc = await lib.getDocument({ data: await file.arrayBuffer() }).promise;
        pdfPageCount.value = pdfDoc.numPages;
        pdfPage.value = 1;
        await nextTick();
        await renderPdfPage(1);
      });
      const onPdfPageChange = () => call(async () => {
        if (!pdfDoc) return;
        const page = Math.min(Math.max(1, parseInt(pdfPage.value, 10) || 1), pdfDoc.numPages);
        pdfPage.value = page;
        await renderPdfPage(page);
      });
      const onTextbookDrop = (event) => {
        const dropped = Array.from(event.dataTransfer ? event.dataTransfer.files : []);
        const file = dropped.find((item) => item.type === 'application/pdf' || /\.pdf$/i.test(item.name || ''));
        if (file) onPdfSelected([file]);
      };
      const generateCover = () => call(async () => {
        if (!pdfDoc) throw new Error('请先选择 PDF');
        const page = Math.min(Math.max(1, parseInt(pdfPage.value, 10) || 1), pdfDoc.numPages);
        const rendered = await pdfDoc.getPage(page);
        const viewport = rendered.getViewport({ scale: 2 });
        const canvas = document.createElement('canvas');
        canvas.width = Math.floor(viewport.width);
        canvas.height = Math.floor(viewport.height);
        await rendered.render({ canvasContext: canvas.getContext('2d'), viewport }).promise;
        const blob = await new Promise((resolve) => canvas.toBlob(resolve, 'image/webp', 0.85));
        if (!blob) throw new Error('生成封面失败（浏览器不支持 WebP）');
        if (coverPreviewUrl.value) URL.revokeObjectURL(coverPreviewUrl.value);
        coverBlob.value = blob;
        coverPreviewUrl.value = URL.createObjectURL(blob);
        notify('封面已生成，保存后上传');
      });
      const uploadTextbookAssets = async (id) => {
        if (pdfFile.value) {
          const data = new FormData();
          data.append('file', pdfFile.value, pdfFile.value.name || 'original.pdf');
          uploading.value = true; uploadProgress.value = 0;
          try {
            await uploadWithProgress('/api/v1/admin/textbooks/' + encodeURIComponent(id) + '/pdf', data, (loaded, total) => {
              uploadProgress.value = total > 0 ? Math.round((loaded / total) * 100) : 0;
            });
          } finally {
            uploading.value = false;
          }
        }
        if (coverBlob.value) {
          await api('/api/v1/admin/textbooks/' + encodeURIComponent(id) + '/cover', {
            method: 'POST', body: coverBlob.value, headers: { 'Content-Type': 'image/webp' }
          });
        }
      };
      const loadExistingCover = async (id) => {
        try {
          const out = await api('/api/v1/admin/textbooks/' + encodeURIComponent(id) + '/resource-url?kind=cover');
          existingCoverUrl.value = out.url || '';
        } catch (_) {
          existingCoverUrl.value = '';
        }
      };
      const loadRelease = async () => {
        const out = await api('/api/v1/admin/app/release');
        releaseConfig.value = Object.assign({}, out || {}, { updatedAt: formatDateTime(out && out.updatedAt) });
        releaseForm.value = { latestVersion: out.latestVersion || '', downloadUrl: out.downloadUrl || '', changelog: out.changelog || '' };
      };
      const loadReleaseTest = async () => {
        const out = await api('/api/v1/admin/app/release-test');
        releaseTestConfig.value = Object.assign({}, out || {}, { updatedAt: formatDateTime(out && out.updatedAt) });
        releaseTestForm.value = { latestVersion: out.latestVersion || '', downloadUrl: out.downloadUrl || '', changelog: out.changelog || '' };
      };
      const loadSchoolCalendar = async () => {
        const out = await api('/api/v1/admin/school-calendar');
        schoolCalendarConfig.value = Object.assign({}, out || {}, { updatedAt: out && out.updatedAt ? out.updatedAt : '' });
        schoolCalendarDays.value = (out && out.days || []).map(day => Object.assign({}, day, { name: day.name || '', adjustment: day.adjustment || '' }));
        schoolCalendarSlotsText.value = formatSchoolCalendarSlots(out && out.slots);
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
        const out = await api('/api/v1/admin/error-reports?limit=' + errorReportsPerPage.value + '&offset=' + offset + sortQuery(errorReportSortBy.value));
        errorReports.value = out.items || [];
        errorReportsTotal.value = out.total || 0;
      };
      const sortErrorReports = (sortBy) => call(async () => { errorReportSortBy.value = Array.isArray(sortBy) ? sortBy : []; errorReportsPage.value = 1; await loadErrorReports(); });
      const searchCDKs = () => call(async () => { cdkPage.value = 1; await loadCDKs(); });
      const loadLibrary = async () => {
        const results = await Promise.all([loadBanks(), loadArchivedBanks(), loadCDKs(), api('/api/v1/admin/metrics/breakdown')]);
        breakdown.value = results[3];
      };
      const loadShareCodes = async () => {
        const offset = (shareCodePage.value - 1) * shareCodeItemsPerPage.value;
        const out = await api('/api/v1/admin/share-codes?limit=' + shareCodeItemsPerPage.value + '&offset=' + offset + sortQuery(shareCodeSortBy.value));
        shareCodes.value = (out.items || []).map(item => Object.assign({}, item, { type: String(item.code || '').slice(-1) === '1' ? 'schedule' : 'personalization' }));
        shareCodesTotal.value = out.total || 0;
      };
      const sortShareCodes = (sortBy) => call(async () => { shareCodeSortBy.value = Array.isArray(sortBy) ? sortBy : []; shareCodePage.value = 1; await loadShareCodes(); });
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
        const search = (userSearch.value || '').trim();
        const empty = userEmptyField.value;
        const sort = userSortBy.value[0] || { key: 'last_login_at', order: 'desc' };
        const offset = (userPage.value - 1) * userItemsPerPage.value;
        const query = '?limit=' + userItemsPerPage.value + '&offset=' + offset
          + '&sort=' + encodeURIComponent(sort.key || 'last_login_at') + '&order=' + encodeURIComponent(sort.order || 'desc')
          + (empty ? '&empty=' + encodeURIComponent(empty) : '')
          + (search && !empty ? '&q=' + encodeURIComponent(search) : '');
        const out = await api('/api/v1/admin/users' + query);
        users.value = out.items || [];
        userTotal.value = out.total || 0;
      };
      const searchUsers = () => call(async () => {
        const term = (userSearch.value || '').trim();
        userEmptyField.value = term === '学院未知' ? 'college' : (term === '班级未知' ? 'class' : '');
        userPage.value = 1;
        await loadUsers();
      });
      // 从总览的分布图/列表跳到用户列表，并用该值作为搜索词；
      // 点击“未知”时改为筛选对应字段为空（无数据）的用户。
      const filterUsersBy = (value, field) => {
        const term = String(value || '').trim();
        if (!term) return;
        if (term === '未知' && (field === 'college' || field === 'class')) {
          userEmptyField.value = field;
          userSearch.value = field === 'college' ? '学院未知' : '班级未知';
        } else {
          userEmptyField.value = '';
          userSearch.value = term;
        }
        userPage.value = 1;
        switchView('users');
      };
      const sortUsers = (sortBy) => call(async () => {
        userSortBy.value = Array.isArray(sortBy) && sortBy.length ? sortBy : [{ key: 'last_login_at', order: 'desc' }];
        userPage.value = 1;
        await loadUsers();
      });
      const ensureViewAllowed = () => {
        if (canRead(view.value)) return;
        const first = visibleNav.value[0];
        if (!first) return;
        view.value = first.key;
        if (window.location.pathname !== '/' + first.key) window.history.replaceState({ view: first.key }, '', '/' + first.key);
      };
      const refreshAdminIdentity = async () => {
        const out = await api('/api/v1/admin/me');
        adminName.value = out.name || '';
        adminPerms.value = parseAdminPerms(out.permissions || '');
        localStorage.setItem('control_admin_name', adminName.value);
        localStorage.setItem('control_admin_perms', out.permissions || '');
        ensureViewAllowed();
      };
      const loadAdmins = async () => {
        const out = await api('/api/v1/admin/admins');
        admins.value = out.items || [];
      };
      const resetAdminForm = (item) => {
        adminEditingId.value = item ? (item.id || '') : '';
        adminForm.value = { key: '' };
        const parsed = parseAdminPerms(item && item.permissions);
        const perms = {};
        adminPageKeys.forEach((page) => { perms[page] = parsed[page] || ''; });
        adminPermsForm.value = perms;
      };
      const openNewAdmin = () => { resetAdminForm(null); adminDialog.value = true; };
      const editAdmin = (row) => { resetAdminForm(rawItem(row)); adminDialog.value = true; };
      const randomAdminKey = () => {
        const bytes = new Uint8Array(16);
        window.crypto.getRandomValues(bytes);
        adminForm.value = Object.assign({}, adminForm.value, {
          key: Array.from(bytes).map((b) => b.toString(16).padStart(2, '0')).join('')
        });
      };
      const saveAdmin = () => call(async () => {
        const form = adminForm.value;
        const editing = !!adminEditingId.value;
        const key = String(form.key || '');
        if (key && key.length < 10) throw new Error('管理密钥至少 10 位');
        if (!editing && !key) throw new Error('请填写管理密钥');
        const parts = [];
        adminPageKeys.forEach((page) => {
          const access = adminPermsForm.value[page];
          if (access) parts.push(page + '=' + access);
        });
        if (!parts.length) throw new Error('请至少授予一个页面的读权限');
        const body = editing
          ? { key: form.key || '', permissions: parts.join(',') }
          : { key: form.key, permissions: parts.join(',') };
        const path = '/api/v1/admin/admins' + (editing ? '/' + encodeURIComponent(adminEditingId.value) : '');
        await api(path, { method: editing ? 'PUT' : 'POST', body: JSON.stringify(body) });
        adminDialog.value = false;
        notify(editing ? '管理员已更新' : '管理员已创建');
        await loadAdmins();
      });
      const deleteAdmin = (row) => call(async () => {
        const item = rawItem(row);
        if (item.super) return;
        if (!window.confirm('确定删除该管理员吗？')) return;
        await api('/api/v1/admin/admins/' + encodeURIComponent(item.id), { method: 'DELETE' });
        notify('管理员已删除');
        await loadAdmins();
      });
      const adminPermSummary = (row) => {
        if (!row) return '-';
        if (row.super) return '全部页面（读写）';
        const parsed = parseAdminPerms(row.permissions);
        const parts = [];
        adminPageOptions.forEach((page) => {
          const access = parsed[page.key];
          if (access) parts.push(page.label + (access === 'rw' ? '（读写）' : '（只读）'));
        });
        return parts.length ? parts.join('、') : '无权限';
      };
      const load = async () => {
        if (!logged.value) return;
        ensureViewAllowed();
        await call(async () => {
          if (view.value === 'overview') await loadOverview();
          else if (view.value === 'users') { await loadOverviewScale(); await loadUsers(); }
          else if (view.value === 'admins') await loadAdmins();
          else if (view.value === 'risk') risks.value = (await api('/api/v1/admin/risk-events?limit=100')).items || [];
          else if (view.value === 'error-reports') await loadErrorReports();
          else if (view.value === 'library') await loadLibrary();
          else if (view.value === 'textbooks') await Promise.all([loadTextbookSubmissions(), loadTextbooks(), loadArchivedTextbooks()]);
          else if (view.value === 'resources') await Promise.all([loadResourceSubmissions(), loadCourses(), loadArchivedResources()]);
          else if (view.value === 'courses') await loadCourses();
          else if (view.value === 'share-codes') await loadShareCodes();
          else if (view.value === 'config') await Promise.all([loadRelease(), loadReleaseTest(), loadSchoolCalendar()]);
          else if (view.value === 'audit') audit.value = ((await api('/api/v1/admin/audit?limit=100')).items || []).map(item => Object.assign({}, item, { target_text: [item.target_type, item.target_id].filter(Boolean).join(' ') }));
        });
      };
      const switchView = (name) => {
        if (!canRead(name)) {
          const first = visibleNav.value[0];
          if (!first) return;
          name = first.key;
        }
        view.value = name;
        if (window.location.pathname !== '/' + name) window.history.pushState({ view: name }, '', '/' + name);
        if (!mdAndUp.value) drawer.value = false;
        load();
      };
      const handlePopState = () => { view.value = viewForPath(window.location.pathname); ensureViewAllowed(); load(); };
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
            grid: { left: 4, right: 18, top: 34, bottom: 30, containLabel: true },
            xAxis: { type: 'category', data: series.value.map(x => x.day), axisLabel: { color: '#697984' }, axisLine: { lineStyle: { color: '#d8e0e4' } } },
            yAxis: { type: 'value', axisLabel: { color: '#697984' }, splitLine: { lineStyle: { color: '#edf1f3' } } },
            series: [
              { name: '总用户', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => scaleOverviewValue(x.total_users)), itemStyle: { color: '#176b4d' }, lineStyle: { width: 3 } },
              { name: 'DAU', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => scaleOverviewValue(x.dau)), itemStyle: { color: '#287ca8' }, lineStyle: { width: 3 } },
              { name: 'WAU', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => scaleOverviewValue(x.wau)), itemStyle: { color: '#805ad5' }, lineStyle: { width: 3 } }
            ]
          });
        }
        if (collegeChart) collegeChart.dispose();
        if (collegeChartEl.value && collegeRows.value.length) {
          collegeChart = window.echarts.init(collegeChartEl.value);
          collegeChart.setOption({
            animationDuration: 350,
            tooltip: { trigger: 'item' },
            legend: { type: 'scroll', bottom: 0, textStyle: { color: '#5e6d76' }, selected: { '未知': false } },
            series: [{ type: 'pie', cursor: 'pointer', radius: ['36%', '68%'], center: ['50%', '43%'], data: collegeRows.value.map(row => ({ name: row.label, value: row.value })), label: { formatter: '{b}' } }]
          });
          collegeChart.on('click', (params) => { if (params && params.componentType === 'series') filterUsersBy(params.name, 'college'); });
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
            series: [{ type: 'pie', radius: ['36%', '68%'], center: ['50%', '43%'], data: featureRows.value.map(row => ({ name: row.label, value: row.value })), label: { formatter: '{b}' } }]
          });
        }
      };
      const openNewBank = () => call(async () => {
        bankEditing.value = false;
        bankCourseCodes.value = [];
        bankJSON.value = JSON.stringify(emptyBankTemplate(), null, 2);
        await loadCourseOptions();
        bankDialog.value = true;
      });
      const editBank = (row) => call(async () => {
        const out = await api('/api/v1/admin/question-banks/' + encodeURIComponent(rawItem(row).id));
        bankEditing.value = true;
        bankCourseCodes.value = Array.isArray(out.questionBank.courseCodes) && out.questionBank.courseCodes.length
          ? out.questionBank.courseCodes.slice()
          : (out.questionBank.courseCode ? [out.questionBank.courseCode] : []);
        await loadCourseOptions();
        bankJSON.value = JSON.stringify({
          questionBank: {
            id: out.questionBank.id,
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
        // The update endpoint takes the bank ID from the URL and rejects
        // unknown JSON fields, so it must not be echoed in the payload.
        delete bank.id;
        delete bank.courseCode;
        bank.courseCodes = (bankCourseCodes.value || []).map((code) => String(code).trim()).filter(Boolean);
        const payload = { questionBank: bank };
        const path = '/api/v1/admin/question-banks' + (bankEditing.value ? '/' + encodeURIComponent(bankID) : '');
        const editing = bankEditing.value;
        await api(path, { method: editing ? 'PUT' : 'POST', body: JSON.stringify(payload) });
        closeBankEditor(); notify(editing ? '题库已更新' : '题库已创建'); await loadBanks();
      });
      const saveRelease = () => call(async () => {
        const latestVersion = (releaseForm.value.latestVersion || '').trim();
        const downloadUrl = (releaseForm.value.downloadUrl || '').trim();
        const changelog = releaseForm.value.changelog || '';
        if (!latestVersion) throw new Error('请填写最新版版本号');
        if (!downloadUrl) throw new Error('请填写下载 URL');
        const out = await api('/api/v1/admin/app/release', { method: 'PUT', body: JSON.stringify({ latestVersion, downloadUrl, changelog }) });
        releaseConfig.value = Object.assign({}, out || {}, { updatedAt: formatDateTime(out && out.updatedAt) });
        releaseForm.value = { latestVersion: out.latestVersion || latestVersion, downloadUrl: out.downloadUrl || downloadUrl, changelog: out.changelog || changelog };
        notify('APP 发布配置已保存');
      });
      const saveReleaseTest = () => call(async () => {
        const latestVersion = (releaseTestForm.value.latestVersion || '').trim();
        const downloadUrl = (releaseTestForm.value.downloadUrl || '').trim();
        const changelog = releaseTestForm.value.changelog || '';
        if (!latestVersion) throw new Error('请填写最新版版本号');
        if (!downloadUrl) throw new Error('请填写下载 URL');
        const out = await api('/api/v1/admin/app/release-test', { method: 'PUT', body: JSON.stringify({ latestVersion, downloadUrl, changelog }) });
        releaseTestConfig.value = Object.assign({}, out || {}, { updatedAt: formatDateTime(out && out.updatedAt) });
        releaseTestForm.value = { latestVersion: out.latestVersion || latestVersion, downloadUrl: out.downloadUrl || downloadUrl, changelog: out.changelog || changelog };
        notify('APP 测试发布配置已保存');
      });
      const saveSchoolCalendar = () => call(async () => {
        if (!schoolCalendarDays.value.length) throw new Error('请至少配置一天校历');
        const slots = parseSchoolCalendarSlots(schoolCalendarSlotsText.value);
        const out = await api('/api/v1/admin/school-calendar', { method: 'PUT', body: JSON.stringify({ days: schoolCalendarDays.value, slots }) });
        schoolCalendarConfig.value = Object.assign({}, out || {}, { updatedAt: out && out.updatedAt ? out.updatedAt : '' });
        schoolCalendarDays.value = (out.days || schoolCalendarDays.value).map(day => Object.assign({}, day, { name: day.name || '', adjustment: day.adjustment || '' }));
        schoolCalendarSlotsText.value = formatSchoolCalendarSlots((out && out.slots) || slots);
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
        notify('题库已删除'); await Promise.all([loadBanks(), loadArchivedBanks()]);
      });
      const setBankArchived = (row, archived) => call(async () => {
        const item = rawItem(row);
        await api('/api/v1/admin/question-banks/' + encodeURIComponent(item.id) + (archived ? '/archive' : '/restore'), { method: 'POST' });
        notify(archived ? '题库已归档' : '题库已恢复');
        await Promise.all([loadBanks(), loadArchivedBanks()]);
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
      const courseCodesText = (codes) => (Array.isArray(codes) ? codes.filter(Boolean).join('、') : '') || '-';
      const statusColor = (status) => ({ active: 'success', draft: 'info', disabled: 'warning', used: 'info' }[status] || 'secondary');
      const statusLabel = (status) => ({ active: '启用', draft: '草稿', disabled: '禁用', used: '已兑换' }[status] || valueOrDash(status));
      const riskTypeLabel = (type) => ({ login_attempt_burst: '短时间登录过多', account_device_burst: '账号设备过多' }[type] || valueOrDash(type));
      const eventTypeLabel = (type) => ({ app_start: '启动', foreground: '前台', heartbeat: '心跳', control_login_success: '登录成功', library_open: '打开题库', logout: '退出' }[type] || valueOrDash(type));
      const actionLabel = (action) => ({ admin_login: '管理员登录', admin_logout: '管理员退出', login_attempt: '登录尝试', user_status_change: '用户状态更新', error_report_student_ignore: '忽略学号错误上报', error_report_student_allow: '允许学号错误上报', error_reports_clear: '清空错误记录', question_bank_create: '创建题库', question_bank_update: '更新题库', question_bank_status: '更新题库状态', library_cdk_create: '生成通用 CDK', library_cdk_redeem: '兑换通用 CDK', library_cdk_status: '更新通用 CDK 状态', app_release_update: '更新 APP 发布配置', app_release_test_update: '更新 APP 测试发布配置', school_calendar_update: '更新学校校历', risk_acknowledge: '标记风控记录', risk_clear: '清空风控记录', device_revoke: '封禁设备', device_restore: '恢复设备', textbook_upload: '上传教材', textbook_create: '创建教材', textbook_update: '更新教材', textbook_delete: '删除教材', textbook_archive: '归档教材', textbook_restore: '恢复教材', resource_upload: '上传试卷', resource_delete: '删除试卷', resource_archive: '归档试卷', resource_restore: '恢复试卷', question_bank_archive: '归档题库', question_bank_restore: '恢复题库', textbook_submission_create: '提交教材审核', textbook_submission_approve: '通过教材审核', textbook_submission_reject: '驳回教材审核', resource_submission_create: '提交试卷审核', resource_submission_approve: '通过试卷审核', resource_submission_reject: '驳回试卷审核', admin_create: '创建管理员', admin_update: '更新管理员', admin_delete: '删除管理员', course_upload: '上传课程', course_create: '创建课程', course_update: '更新课程', course_delete: '删除课程' }[action] || valueOrDash(action));
      const riskStatusColor = (risk) => risk.acknowledged_at ? 'success' : 'warning';
      const handleResize = () => { if (chart) chart.resize(); if (platformChart) platformChart.resize(); if (collegeChart) collegeChart.resize(); if (eventChart) eventChart.resize(); if (featureChart) featureChart.resize(); if (cdnChart) cdnChart.resize(); if (hostChart) hostChart.resize(); if (bucketChart) bucketChart.resize(); };
      onMounted(() => {
        if (window.location.pathname !== '/' + view.value) window.history.replaceState({ view: view.value }, '', '/' + view.value);
        if (logged.value) {
          call(async () => { await refreshAdminIdentity(); await load(); });
        }
        window.addEventListener('resize', handleResize);
        window.addEventListener('popstate', handlePopState);
        statsTimer = setInterval(refreshStatsSilently, 10 * 60 * 1000);
      });
      onBeforeUnmount(() => { window.removeEventListener('resize', handleResize); window.removeEventListener('popstate', handlePopState); if (chart) chart.dispose(); if (platformChart) platformChart.dispose(); if (collegeChart) collegeChart.dispose(); if (eventChart) eventChart.dispose(); if (featureChart) featureChart.dispose(); if (cdnChart) cdnChart.dispose(); if (hostChart) hostChart.dispose(); if (bucketChart) bucketChart.dispose(); if (bucketPollTimer) clearTimeout(bucketPollTimer); if (cdnPollTimer) clearTimeout(cdnPollTimer); if (statsTimer) clearInterval(statsTimer); });

      return {
        logged, drawer, mdAndUp, view, loading, error, overview, breakdown, series, users, devices, risks, audit, errorReports, errorReportsTotal, errorReportsPage, errorReportsPerPage, errorReportSortBy,
        banks, bankTotal, bankPage, bankItemsPerPage, bankSortBy, bankDialog, bankEditing, bankJSON, bankCourseCodes, courseCodesText, archivedBanks, archivedBankTotal, archivedBankHeaders, loadArchivedBanks, setBankArchived, releaseConfig, releaseForm, releaseTestConfig, releaseTestForm, schoolCalendarConfig, schoolCalendarDays, schoolCalendarSlotsText, schoolCalendarRange, schoolCalendarExceptions, schoolCalendarHeaders, schoolCalendarTableKey, schoolCalendarDayDialog, schoolCalendarRebuildDialog, schoolCalendarEditingDate, schoolCalendarEditingDateLabel, schoolCalendarEditingAdjustment, schoolCalendarEditingName,
        cdks, cdkTotal, cdkPage, cdkItemsPerPage, cdkSortBy, cdkDialog, cdkRevealDialog, cdkForm, createdCDKs, cdkSearch, shareCodes, shareCodesTotal, shareCodePage, shareCodeItemsPerPage, shareCodeSortBy, shareCodeDialog, shareCodeJSON, shareCodeKind, createdShareCode, shareCodeDetail, shareCodeDetailDialog, shareCodeDetailJSON, userSearch, userPage, userItemsPerPage, userTotal, userSortBy,
        textbooks, textbookTotal, textbookPage, textbookItemsPerPage, textbookSearch, textbookSortBy, textbookDialog, textbookEditingId, textbookForm, textbookHeaders, archivedTextbooks, archivedTextbookTotal, archivedTextbookHeaders, loadArchivedTextbooks, setTextbookArchived, courseOptions,
        textbookSubmissions, textbookSubmissionTotal, resourceSubmissions, resourceSubmissionTotal, submissionHeaders, loadTextbookSubmissions, loadResourceSubmissions, downloadSubmission, approveSubmission, rejectSubmission,
        resourceDialog, resourceCourse, courseResources, contributorDialog, contributorKind, contributorId, contributorName, contributorStudentID, resourceFiles, uploading, uploadProgress, resourceHeaders, courseResourceHeaders, archivedResources, archivedResourceTotal, archivedResourceHeaders, loadArchivedResources, setResourceArchived,
        pdfCanvasEl, pdfFile, pdfPage, pdfPageCount, coverPreviewUrl, existingCoverUrl,
        courses, courseTotal, coursePage, courseItemsPerPage, courseSearch, courseSortBy, courseDialog, courseEditingId, courseForm, courseHeaders,
        shareCodeHeaders,
        selectedUser, userDialog, loginForm, userStatus, snackbar, overviewDialog, overviewScaleInput, scaleOverviewValue, applyOverviewScale, resetOverviewScale, chartEl, platformChartEl, collegeChartEl, eventChartEl, featureChartEl, cdnStats, cdnTab, cdnDomains, cdnDomain, cdnChartEl, cdnCards, cdnPackage, cdnLoading, cdnError, loadCDNStats, formatBytes, formatBitrate, formatPercent, hostStats, hostLoading, hostError, hostChartEl, hostCards, loadHostStats, refreshActiveStats, activePackage, formatBytesIEC, bucketStats, bucketLoading, bucketError, bucketChartEl, bucketCards, activeBucket, activeBucketKey, loadBucketStats, formatStorageMB, nav, visibleNav, title, statCards, adminName, canRead, canWrite,
        platformRows, versionRows, collegeRows, classRows, eventRows, cdkActivationRows, featureRows, userDetails, userHeaders, deviceHeaders, riskHeaders, auditHeaders, errorReportHeaders, userDeviceHeaders,
        bankHeaders, cdkHeaders, riskOptions, bankStatusOptions, bankNewOptions, bankCDKOptions, questionTypeOptions, bankPageCount, login, logout, switchView, load, loadBanks, loadCDKs, searchCDKs, loadShareCodes, loadRelease, loadReleaseTest, loadSchoolCalendar, openSchoolCalendarRebuild, confirmSchoolCalendarRebuild, loadErrorReports, sortErrorReports, sortBanks, sortCDKs, sortShareCodes, saveRelease, saveReleaseTest, saveSchoolCalendar, openNewSchoolCalendarDay, openSchoolCalendarDay, deleteSchoolCalendarException, saveSchoolCalendarDay, loadUsers, searchUsers, filterUsersBy, sortUsers,
        showUser, closeUser, setStatus, disableUser, setRiskDeviceStatus, clearRiskEvents, acknowledge, openNewBank, closeBankEditor, setBankDialog, editBank, saveBank, setBankStatus, removeBank, setBankArchived, openNewCDK, createCDK, copyCDK, setCDKStatus, openShareCodeDialog, createShareCode, deleteShareCode, openShareCodeDetail, setErrorReportStudentIgnored, clearErrorReports, rawItem, valueOrDash, formatDateTime, statusColor, statusLabel, riskTypeLabel, eventTypeLabel, actionLabel, riskStatusColor,
        loadTextbooks, searchTextbooks, sortTextbooks, openNewTextbook, editTextbook, saveTextbook, deleteTextbook, openContributorDialog, saveContributor, onPdfSelected, onPdfPageChange, generateCover, downloadTextbook, setTextbookArchived,
        openResourceDialog, loadCourseResources, onResourceDrop, uploadResources, deleteResource, downloadResource, onTextbookDrop,
        loadCourses, searchCourses, sortCourses, openNewCourse, editCourse, saveCourse, deleteCourse, booksLabel,
        admins, adminDialog, adminEditingId, adminForm, adminPermsForm, adminPageOptions, adminPermOptions, adminHeaders, loadAdmins, openNewAdmin, editAdmin, saveAdmin, deleteAdmin, adminPermSummary, randomAdminKey
      };
    },
    template,
  }).use(vuetify).mount('#app');
})();
