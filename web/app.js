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

  const injectErrorReportsPage = (template) => template.replace(
    `              <section v-else-if="view === 'config'">`,
    `              <section v-else-if="view === 'error-reports'"><v-card variant="elevated" border><v-card-title class="d-flex align-center flex-wrap ga-2"><v-icon icon="mdi-bug-outline" color="error" /><span>错误</span><v-spacer /><v-btn size="small" variant="tonal" color="error" prepend-icon="mdi-delete-sweep-outline" :loading="loading" @click="clearErrorReports">清空记录</v-btn><v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="load">刷新</v-btn></v-card-title><v-divider /><v-data-table :headers="errorReportHeaders" :items="errorReports" item-value="id" show-expand :items-per-page="errorReportsPerPage" items-per-page-text="每页条数：" page-text="{0}-{1} 共 {2}" :loading="loading" density="comfortable" hover class="admin-table" no-data-text="暂无错误"><template #item.occurred_at="{ item }"><span class="text-medium-emphasis">{{ valueOrDash(rawItem(item).occurred_at) }}</span></template><template #item.student_id="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).student_id) }}</span></template><template #item.title="{ item }"><span>{{ valueOrDash(rawItem(item).title) }}</span></template><template #item.message="{ item }"><span class="table-ellipsis" :title="valueOrDash(rawItem(item).message)">{{ valueOrDash(rawItem(item).message) }}</span></template><template #item.app_version="{ item }"><span>{{ valueOrDash(rawItem(item).app_version) }}</span></template><template #item.platform="{ item }"><span>{{ valueOrDash(rawItem(item).platform) }}</span></template><template #item.actions="{ item }"><v-btn size="small" variant="text" :color="rawItem(item).ignored ? 'success' : 'warning'" :prepend-icon="rawItem(item).ignored ? 'mdi-check-circle-outline' : 'mdi-bell-off-outline'" @click="setErrorReportStudentIgnored(rawItem(item))">{{ rawItem(item).ignored ? '允许' : '忽略' }}</v-btn></template><template #expanded-row="{ columns, item }"><tr><td :colspan="columns.length"><div class="pa-4"><div class="text-subtitle-2 font-weight-bold mb-2">{{ valueOrDash(rawItem(item).title) }}</div><pre class="json-preview error-report-detail">{{ [rawItem(item).message, rawItem(item).error, rawItem(item).stack_trace].filter(Boolean).join('\\n\\n') }}</pre><div class="text-caption text-medium-emphasis mt-2">设备：{{ valueOrDash(rawItem(item).device_id) }} · 接收时间：{{ valueOrDash(rawItem(item).received_at) }}</div></div></td></tr></template></v-data-table><div v-if="errorReportsTotal > errorReportsPerPage" class="d-flex justify-end pa-3"><v-pagination v-model="errorReportsPage" :length="Math.max(1, Math.ceil(errorReportsTotal / errorReportsPerPage))" density="comfortable" @update:model-value="loadErrorReports" /></div></v-card></section>\n              <section v-else-if="view === 'config'">`,
  );

  const injectSchoolCalendarConfig = (template) => {
    const marker = `              <section v-else-if="view === 'config'">`;
    const card = `<v-card variant="elevated" border class="config-card mb-4"><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-calendar-edit-outline" color="primary" /><span>学校校历</span><v-spacer /><v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="loadSchoolCalendar">刷新</v-btn></v-card-title><v-divider /><v-card-text><v-form @submit.prevent="saveSchoolCalendar"><v-textarea v-model="schoolCalendarForm" label="校历 JSON" placeholder="例如：days 数组 JSON" variant="outlined" rows="16" class="mono" hide-details="auto" /><div class="d-flex align-center flex-wrap ga-3 mt-4"><span v-if="schoolCalendarConfig.updatedAt" class="text-body-2 text-medium-emphasis">最近更新：{{ formatDateTime(schoolCalendarConfig.updatedAt) }}</span><v-spacer /><v-btn type="submit" color="primary" prepend-icon="mdi-content-save-outline" :loading="loading">保存校历</v-btn></div></v-form></v-card-text></v-card>`;
    return template.replace(marker, marker + card);
  };

  createApp({
    setup() {
      const { mdAndUp } = useDisplay();
      const logged = ref(!!localStorage.getItem('control_admin'));
      const drawer = ref(false);
      const view = ref('overview');
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
      const releaseConfig = ref({ latestVersion: '', downloadUrl: '', updatedAt: '' });
      const releaseForm = ref({ latestVersion: '', downloadUrl: '' });
      const schoolCalendarConfig = ref({ days: [], updatedAt: '' });
      const schoolCalendarForm = ref('');
      const bankDialog = ref(false);
      const bankEditing = ref(false);
      const bankJSON = ref('');
      const bankNewOptions = [{ title: '是', value: true }, { title: '否', value: false }];
      const bankCDKOptions = [{ title: '需要', value: true }, { title: '不需要', value: false }];
      const selectedUser = ref(null);
      const userDialog = ref(false);
      const loginForm = ref({ key: '' });
      const userStatus = ref('');
      const riskFilter = ref('');
      const snackbar = ref({ show: false, text: '', color: 'success' });
      const chartEl = ref(null);
      let chart = null;

      const nav = [
        { key: 'overview', label: '总览', icon: 'mdi-chart-line' },
        { key: 'users', label: '用户', icon: 'mdi-account-group-outline' },
        { key: 'devices', label: '设备', icon: 'mdi-cellphone-link' },
        { key: 'risk', label: '风控', icon: 'mdi-shield-alert-outline' },
        { key: 'error-reports', label: '错误', icon: 'mdi-bug-outline' },
        { key: 'library', label: '文库', icon: 'mdi-book-open-page-variant' },
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
      const cdkActivationRows = computed(() => [
        { label: '今日激活数', value: breakdown.value.cdk_activations_today || 0 },
        { label: '本周激活数', value: breakdown.value.cdk_activations_week || 0 },
        { label: '总激活数', value: breakdown.value.cdk_activations_total || 0 }
      ]);
      const userDetails = computed(() => {
        if (!selectedUser.value) return [];
        const user = selectedUser.value.user || {};
        return [
          { label: 'ID', value: valueOrDash(user.id) },
          { label: '显示名', value: valueOrDash(user.display_name) },
          { label: '学号', value: valueOrDash(selectedUser.value.student_id) },
          { label: '伪名', value: valueOrDash(user.student_alias) },
          { label: '专业', value: valueOrDash(user.major_name) },
          { label: '班级', value: valueOrDash(user.class_name) },
          { label: '状态', value: statusLabel(user.status) },
          { label: '最近连接', value: formatDateTime(user.last_login_at) }
        ];
      });
      const userHeaders = [
        { title: 'ID', key: 'id', sortable: false, width: '18%' }, { title: '显示名', key: 'display_name', width: '20%' },
        { title: '状态', key: 'status', width: '14%' }, { title: '设备数', key: 'device_count', align: 'center', width: '12%' },
        { title: '最近连接', key: 'last_login_at', width: '18%' }, { title: '操作', key: 'actions', sortable: false, align: 'center', width: '18%' }
      ];
      const deviceHeaders = [
        { title: '设备码', key: 'device_serial', sortable: false, width: '20%' }, { title: '平台', key: 'platform', width: '14%' },
        { title: '版本', key: 'app_version', width: '14%' }, { title: '安装标识', key: 'installation_id', sortable: false, width: '24%' },
        { title: '最近活动', key: 'last_seen_at', width: '18%' }, { title: '状态', key: 'status', sortable: false, width: '10%' }
      ];
      const riskHeaders = [
        { title: '类型', key: 'type', width: '18%' }, { title: '用户', key: 'user_id', sortable: false, width: '16%' },
        { title: '设备', key: 'device_id', sortable: false, width: '16%' }, { title: '次数', key: 'observed_count', align: 'center', width: '12%' },
        { title: '时间', key: 'created_at', width: '18%' }, { title: '状态', key: 'status', sortable: false, width: '12%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '8%' }
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
        { title: '题目数', key: 'question_count', align: 'center', width: '9%' }, { title: '状态', key: 'status', width: '10%' },
        { title: '新题库', key: 'new', sortable: false, align: 'center', width: '8%' }, { title: 'CDK 解锁', key: 'requiresCDK', sortable: false, align: 'center', width: '11%' }, { title: '更新时间', key: 'updated_at', width: '9%' },
        { title: '操作', key: 'actions', sortable: false, align: 'center', width: '14%' }
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
        series.value = (await api('/api/v1/admin/metrics/series?days=30')).items || [];
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
        schoolCalendarForm.value = JSON.stringify({ days: (out && out.days) || [] }, null, 2);
      };
      const loadErrorReports = async () => {
        const offset = (errorReportsPage.value - 1) * errorReportsPerPage.value;
        const out = await api('/api/v1/admin/error-reports?limit=' + errorReportsPerPage.value + '&offset=' + offset);
        errorReports.value = out.items || [];
        errorReportsTotal.value = out.total || 0;
      };
      const searchCDKs = () => call(async () => { cdkPage.value = 1; await loadCDKs(); });
      const loadLibrary = async () => { await Promise.all([loadBanks(), loadCDKs()]); };
      const load = async () => {
        if (!logged.value) return;
        await call(async () => {
          if (view.value === 'overview') await loadOverview();
          else if (view.value === 'users') users.value = (await api('/api/v1/admin/users?limit=100')).items || [];
          else if (view.value === 'devices') devices.value = (await api('/api/v1/admin/devices?limit=100')).items || [];
          else if (view.value === 'risk') risks.value = (await api('/api/v1/admin/risk-events?limit=100' + (riskFilter.value ? '&acknowledged=' + riskFilter.value : ''))).items || [];
          else if (view.value === 'error-reports') await loadErrorReports();
          else if (view.value === 'library') await loadLibrary();
          else if (view.value === 'config') await Promise.all([loadRelease(), loadSchoolCalendar()]);
          else if (view.value === 'audit') audit.value = (await api('/api/v1/admin/audit?limit=100')).items || [];
        });
      };
      const switchView = (name) => { view.value = name; if (!mdAndUp.value) drawer.value = false; load(); };
      const drawChart = () => {
        if (!chartEl.value || !window.echarts || view.value !== 'overview') return;
        if (chart) {
          let dom = null; try { dom = chart.getDom(); } catch (_) {}
          if (dom !== chartEl.value) { chart.dispose(); chart = null; }
        }
        if (!chart) chart = window.echarts.init(chartEl.value);
        chart.clear();
        chart.setOption({
          animationDuration: 350, tooltip: { trigger: 'axis' },
          legend: { data: ['总用户', 'DAU', 'WAU', '今日事件'], top: 0, textStyle: { color: '#5e6d76' } },
          grid: { left: 42, right: 18, top: 34, bottom: 30 },
          xAxis: { type: 'category', data: series.value.map(x => x.day), axisLabel: { color: '#697984' }, axisLine: { lineStyle: { color: '#d8e0e4' } } },
          yAxis: { type: 'value', axisLabel: { color: '#697984' }, splitLine: { lineStyle: { color: '#edf1f3' } } },
          series: [
            { name: '总用户', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => x.total_users), itemStyle: { color: '#176b4d' }, lineStyle: { width: 3 } },
            { name: 'DAU', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => x.dau), itemStyle: { color: '#287ca8' }, lineStyle: { width: 3 } },
            { name: 'WAU', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => x.wau), itemStyle: { color: '#805ad5' }, lineStyle: { width: 3 } },
            { name: '今日事件', type: 'line', smooth: true, showSymbol: false, data: series.value.map(x => x.today_events), itemStyle: { color: '#b7791f' }, lineStyle: { width: 3 } }
          ]
        });
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
        let parsed;
        try { parsed = JSON.parse(schoolCalendarForm.value || ''); } catch (_) { throw new Error('校历 JSON 格式无效'); }
        const days = Array.isArray(parsed) ? parsed : parsed && parsed.days;
        if (!Array.isArray(days) || !days.length) throw new Error('请至少配置一天校历');
        const out = await api('/api/v1/admin/school-calendar', { method: 'PUT', body: JSON.stringify({ days }) });
        schoolCalendarConfig.value = Object.assign({}, out || {}, { updatedAt: out && out.updatedAt ? out.updatedAt : '' });
        schoolCalendarForm.value = JSON.stringify({ days: out.days || days }, null, 2);
        notify('学校校历已保存');
      });
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
      const actionLabel = (action) => ({ admin_login: '管理员登录', admin_logout: '管理员退出', login_attempt: '登录尝试', user_status_change: '用户状态更新', error_report_student_ignore: '忽略学号错误上报', error_report_student_allow: '允许学号错误上报', error_reports_clear: '清空错误记录', question_bank_create: '创建题库', question_bank_update: '更新题库', question_bank_status: '更新题库状态', library_cdk_create: '生成通用 CDK', library_cdk_redeem: '兑换通用 CDK', library_cdk_status: '更新通用 CDK 状态', app_release_update: '更新 APP 发布配置', school_calendar_update: '更新学校校历', risk_acknowledge: '标记风控记录' }[action] || valueOrDash(action));
      const riskStatusColor = (risk) => risk.acknowledged_at ? 'success' : 'warning';
      const handleResize = () => { if (chart) chart.resize(); };
      watch(view, async (name) => { if (name === 'overview') { await nextTick(); drawChart(); } });
      onMounted(() => { if (logged.value) load(); window.addEventListener('resize', handleResize); });
      onBeforeUnmount(() => { window.removeEventListener('resize', handleResize); if (chart) chart.dispose(); });

      return {
        logged, drawer, mdAndUp, view, loading, error, overview, breakdown, series, users, devices, risks, audit, errorReports, errorReportsTotal, errorReportsPage, errorReportsPerPage,
        banks, bankTotal, bankPage, bankItemsPerPage, bankDialog, bankEditing, bankJSON, releaseConfig, releaseForm, schoolCalendarConfig, schoolCalendarForm,
        cdks, cdkTotal, cdkPage, cdkItemsPerPage, cdkDialog, cdkRevealDialog, cdkForm, createdCDKs, cdkSearch,
        selectedUser, userDialog, loginForm, userStatus, riskFilter, snackbar, chartEl, nav, title, statCards,
        platformRows, versionRows, cdkActivationRows, userDetails, userHeaders, deviceHeaders, riskHeaders, auditHeaders, errorReportHeaders, userDeviceHeaders,
        bankHeaders, cdkHeaders, riskOptions, bankStatusOptions, bankNewOptions, bankCDKOptions, questionTypeOptions, bankPageCount, login, logout, switchView, load, loadBanks, loadCDKs, searchCDKs, loadRelease, loadSchoolCalendar, loadErrorReports, saveRelease, saveSchoolCalendar,
        showUser, closeUser, setStatus, disableUser, acknowledge, openNewBank, closeBankEditor, setBankDialog, editBank, saveBank, setBankStatus, removeBank, openNewCDK, createCDK, copyCDK, setCDKStatus, setErrorReportStudentIgnored, clearErrorReports, rawItem, valueOrDash, formatDateTime, statusColor, statusLabel, riskTypeLabel, actionLabel, riskStatusColor
      };
    },
    template: injectErrorReportsPage(injectSchoolCalendarConfig(`
      <v-app>
        <v-main v-if="!logged" class="login-page">
          <v-container fluid class="login-shell pa-4">
            <v-row align="center" justify="center" class="w-100">
              <v-col cols="12" sm="8" md="5" lg="4" xl="3">
                <v-card class="login-card" rounded="md" elevation="10">
                  <v-card-item class="pa-6 pb-3">
                    <template #prepend><v-avatar color="primary" variant="tonal" size="48"><v-icon icon="mdi-shield-account-outline" /></v-avatar></template>
                    <v-card-title class="text-h5 font-weight-bold pa-0">掌上徐工控制台</v-card-title>
                  </v-card-item>
                  <v-card-text class="px-6 pb-6">
                    <v-alert v-if="error" type="error" variant="tonal" density="comfortable" class="mb-4" closable @click:close="error=''">{{ error }}</v-alert>
                    <v-form @submit.prevent="login">
                      <v-text-field v-model="loginForm.key" label="管理密钥" type="password" autocomplete="current-password" prepend-inner-icon="mdi-key-outline" variant="outlined" density="comfortable" hide-details="auto" class="mb-4" />
                      <v-btn type="submit" block size="large" color="primary" :loading="loading" prepend-icon="mdi-login">登录</v-btn>
                    </v-form>
                  </v-card-text>
                </v-card>
              </v-col>
            </v-row>
          </v-container>
        </v-main>
        <template v-else>
          <v-navigation-drawer :model-value="mdAndUp || drawer" @update:model-value="drawer = $event" :permanent="mdAndUp" :temporary="!mdAndUp" width="208" color="sidebar" theme="dark">
             <div class="drawer-brand pa-5"><div class="text-subtitle-1 font-weight-bold text-white">掌上徐工控制台</div></div>
            <v-divider class="mx-4 drawer-divider" />
            <v-list nav density="comfortable" class="px-3 py-4" bg-color="transparent">
              <v-list-item v-for="item in nav" :key="item.key" :active="view === item.key" :prepend-icon="item.icon" :title="item.label" active-color="primary" rounded="md" class="mb-1" @click="switchView(item.key)" />
            </v-list>
            <template #append><div class="pa-3"><v-divider class="mb-3 drawer-divider" /><v-btn block variant="outlined" color="white" prepend-icon="mdi-logout" @click="logout">退出</v-btn></div></template>
          </v-navigation-drawer>
          <v-main class="app-main">
            <v-app-bar flat color="surface" height="72" class="topbar">
              <v-app-bar-nav-icon v-if="!mdAndUp" aria-label="打开导航" @click="drawer = !drawer" />
               <v-toolbar-title class="text-h6 font-weight-bold">{{ title }}</v-toolbar-title><v-spacer />
              <v-tooltip text="刷新当前数据" theme="dark" location="bottom" content-class="refresh-tooltip"><template #activator="{ props }"><v-btn v-bind="props" icon variant="text" :loading="loading" aria-label="刷新当前数据" @click="load"><v-icon icon="mdi-refresh" /></v-btn></template></v-tooltip>
            </v-app-bar>
            <v-container fluid class="content pa-4 pa-md-6">
              <v-progress-linear v-if="loading" indeterminate color="primary" class="loading-bar" />
              <v-alert v-if="error" type="error" variant="tonal" density="comfortable" class="mb-4" closable @click:close="error=''">{{ error }}</v-alert>
              <section v-if="view === 'overview'">
                <v-row dense class="mb-4"><v-col v-for="stat in statCards" :key="stat.label" cols="12" sm="6" md="4" lg="2"><v-card variant="elevated" border class="stat-card h-100"><v-card-text class="d-flex align-center ga-3"><v-avatar :color="stat.color" variant="tonal" size="42"><v-icon :icon="stat.icon" /></v-avatar><div class="min-w-0"><div class="text-caption text-medium-emphasis">{{ stat.label }}</div><div class="text-h5 font-weight-bold mt-1">{{ stat.value }}</div></div></v-card-text></v-card></v-col></v-row>
                <v-card variant="elevated" border class="mb-4"><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-chart-timeline-variant" color="primary" /><span>近 30 天趋势</span><v-spacer /><v-chip size="small" variant="tonal" color="primary">{{ series.length }} 天</v-chip></v-card-title><v-divider /><v-card-text><div ref="chartEl" class="chart" aria-label="近 30 天总用户、DAU、WAU 和今日事件趋势图" /></v-card-text></v-card>
                <v-row dense><v-col cols="12" md="4"><v-card variant="elevated" border class="h-100"><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-monitor-dashboard" color="secondary" /><span>平台分布</span></v-card-title><v-divider /><v-list v-if="platformRows.length" density="compact" lines="one" class="py-2"><v-list-item v-for="row in platformRows" :key="row.label" :title="row.label"><template #append><v-chip size="small" color="secondary" variant="tonal">{{ row.value }}</v-chip></template></v-list-item></v-list><div v-else class="empty-state">暂无数据</div></v-card></v-col><v-col cols="12" md="4"><v-card variant="elevated" border class="h-100"><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-tag-multiple-outline" color="info" /><span>版本分布</span></v-card-title><v-divider /><v-list v-if="versionRows.length" density="compact" lines="one" class="py-2"><v-list-item v-for="row in versionRows" :key="row.label" :title="row.label"><template #append><v-chip size="small" color="info" variant="tonal">{{ row.value }}</v-chip></template></v-list-item></v-list><div v-else class="empty-state">暂无数据</div></v-card></v-col><v-col cols="12" md="4"><v-card variant="elevated" border class="h-100"><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-key-chain" color="warning" /><span>文库CDK</span></v-card-title><v-divider /><v-list density="compact" lines="one" class="py-2"><v-list-item v-for="row in cdkActivationRows" :key="row.label" :title="row.label"><template #append><span class="font-weight-bold">{{ row.value }}</span></template></v-list-item></v-list></v-card></v-col></v-row>
              </section>
              <section v-else-if="view === 'users'"><v-card variant="elevated" border><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-account-group-outline" color="primary" /><span>用户列表</span><v-spacer /><v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="load">刷新</v-btn></v-card-title><v-divider /><v-data-table :headers="userHeaders" :items="users" item-value="id" :items-per-page="25" items-per-page-text="每页条数：" page-text="{0}-{1} 共 {2}" :loading="loading" density="comfortable" hover class="admin-table" no-data-text="暂无数据"><template #item.id="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).id) }}</span></template><template #item.display_name="{ item }">{{ valueOrDash(rawItem(item).display_name) }}</template><template #item.status="{ item }"><v-chip size="small" :color="statusColor(rawItem(item).status)" variant="tonal">{{ statusLabel(rawItem(item).status) }}</v-chip></template><template #item.last_login_at="{ item }"><span class="text-medium-emphasis">{{ valueOrDash(rawItem(item).last_login_at) }}</span></template><template #item.actions="{ item }"><div class="table-actions"><v-btn size="small" variant="text" color="primary" prepend-icon="mdi-eye-outline" @click="showUser(rawItem(item))">详情</v-btn><v-btn size="small" variant="text" :color="rawItem(item).status === 'disabled' ? 'success' : 'error'" :prepend-icon="rawItem(item).status === 'disabled' ? 'mdi-account-check-outline' : 'mdi-account-cancel-outline'" @click="disableUser(rawItem(item))">{{ rawItem(item).status === 'disabled' ? '启用' : '停用' }}</v-btn></div></template></v-data-table></v-card></section>
              <section v-else-if="view === 'devices'"><v-card variant="elevated" border><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-cellphone-link" color="primary" /><span>设备列表</span><v-spacer /><v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="load">刷新</v-btn></v-card-title><v-divider /><v-data-table :headers="deviceHeaders" :items="devices" item-value="id" :items-per-page="25" items-per-page-text="每页条数：" page-text="{0}-{1} 共 {2}" :loading="loading" density="comfortable" hover class="admin-table" no-data-text="暂无数据"><template #item.device_serial="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).device_serial) }}</span></template><template #item.installation_id="{ item }"><span class="mono table-ellipsis" :title="valueOrDash(rawItem(item).installation_id)">{{ valueOrDash(rawItem(item).installation_id) }}</span></template><template #item.last_seen_at="{ item }"><span class="text-medium-emphasis">{{ valueOrDash(rawItem(item).last_seen_at) }}</span></template><template #item.status="{ item }"><v-chip size="small" :color="rawItem(item).revoked_at ? 'error' : 'success'" variant="tonal">{{ rawItem(item).revoked_at ? '已撤销' : '正常' }}</v-chip></template></v-data-table></v-card></section>
              <section v-else-if="view === 'risk'"><v-card variant="elevated" border><v-card-title class="d-flex align-center flex-wrap ga-2"><v-icon icon="mdi-shield-alert-outline" color="warning" /><span>风控记录</span><v-spacer /><v-select v-model="riskFilter" :items="riskOptions" item-title="title" item-value="value" label="查看状态" variant="outlined" density="compact" hide-details style="max-width: 170px" @update:model-value="load" /><v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="load">刷新</v-btn></v-card-title><v-divider /><v-data-table :headers="riskHeaders" :items="risks" item-value="id" :items-per-page="25" items-per-page-text="每页条数：" page-text="{0}-{1} 共 {2}" :loading="loading" density="comfortable" hover class="admin-table" no-data-text="暂无数据"><template #item.type="{ item }"><span>{{ riskTypeLabel(rawItem(item).type) }}</span></template><template #item.user_id="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).user_id) }}</span></template><template #item.device_id="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).device_id) }}</span></template><template #item.created_at="{ item }"><span class="text-medium-emphasis">{{ valueOrDash(rawItem(item).created_at) }}</span></template><template #item.status="{ item }"><v-chip size="small" :color="riskStatusColor(rawItem(item))" variant="tonal">{{ rawItem(item).acknowledged_at ? '已查看' : '未查看' }}</v-chip></template><template #item.actions="{ item }"><v-btn v-if="!rawItem(item).acknowledged_at" size="small" variant="text" color="primary" prepend-icon="mdi-check" @click="acknowledge(rawItem(item))">标记</v-btn></template></v-data-table></v-card></section>
              <section v-else-if="view === 'library'"><v-card variant="elevated" border class="mb-4"><v-card-title class="d-flex align-center ga-2 toolbar-wrap library-toolbar"><v-icon icon="mdi-book-open-page-variant" color="primary" /><span>文库题库</span><v-spacer /><v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-plus" @click="openNewBank">新建题库</v-btn><v-btn size="small" variant="tonal" color="secondary" prepend-icon="mdi-key-plus" @click="openNewCDK">生成 CDK</v-btn><v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="load">刷新</v-btn></v-card-title><v-divider /><v-data-table :headers="bankHeaders" :items="banks" item-value="id" :items-per-page="bankItemsPerPage" items-per-page-text="每页条数：" page-text="{0}-{1} 共 {2}" :loading="loading" density="comfortable" hover hide-default-footer class="admin-table" no-data-text="暂无题库"><template #item.orderId="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).orderId) }}</span></template><template #item.id="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).id) }}</span></template><template #item.question_count="{ item }"><span class="font-weight-bold">{{ rawItem(item).question_count || 0 }}</span></template><template #item.status="{ item }"><v-chip size="small" :color="statusColor(rawItem(item).status)" variant="tonal">{{ statusLabel(rawItem(item).status) }}</v-chip></template><template #item.new="{ item }"><v-chip size="small" :color="rawItem(item).new ? 'primary' : 'default'" variant="tonal">{{ rawItem(item).new ? '是' : '否' }}</v-chip></template><template #item.requiresCDK="{ item }"><v-chip size="small" :color="rawItem(item).requiresCDK ? 'secondary' : 'default'" variant="tonal">{{ rawItem(item).requiresCDK ? '需要' : '不需要' }}</v-chip></template><template #item.updated_at="{ item }"><span class="text-medium-emphasis">{{ valueOrDash(rawItem(item).updated_at) }}</span></template><template #item.actions="{ item }"><div class="table-actions"><v-btn size="small" variant="text" color="primary" prepend-icon="mdi-pencil-outline" @click="editBank(rawItem(item))">编辑</v-btn><v-btn size="small" variant="text" :color="rawItem(item).status === 'disabled' ? 'success' : 'error'" :prepend-icon="rawItem(item).status === 'disabled' ? 'mdi-play-circle-outline' : 'mdi-stop-circle-outline'" @click="setBankStatus(rawItem(item))">{{ rawItem(item).status === 'disabled' ? '启用' : '停用' }}</v-btn><v-btn size="small" variant="text" color="error" prepend-icon="mdi-delete-outline" @click="removeBank(rawItem(item))">删除</v-btn></div></template></v-data-table><div v-if="bankTotal > bankItemsPerPage" class="d-flex justify-end pa-3"><v-pagination v-model="bankPage" :length="bankPageCount" density="comfortable" @update:model-value="loadBanks" /></div></v-card><v-card variant="elevated" border class="mb-4"><v-card-title class="d-flex align-center ga-2 cdk-toolbar"><v-icon icon="mdi-key-chain" color="secondary" /><span>文库 CDK</span><v-spacer /><v-text-field v-model="cdkSearch" label="搜索 CDK、题库或学号" prepend-inner-icon="mdi-magnify" variant="outlined" density="compact" hide-details clearable class="cdk-search" @keyup.enter="searchCDKs" @click:clear="searchCDKs" /><v-btn size="small" variant="text" color="primary" prepend-icon="mdi-magnify" :loading="loading" @click="searchCDKs">搜索</v-btn><v-btn size="small" variant="text" color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="loadCDKs">刷新</v-btn></v-card-title><v-divider /><v-data-table :headers="cdkHeaders" :items="cdks" item-value="id" :items-per-page="cdkItemsPerPage" items-per-page-text="每页条数：" page-text="{0}-{1} 共 {2}" :loading="loading" density="comfortable" hover hide-default-footer class="admin-table" no-data-text="暂无 CDK"><template #item.id="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).id) }}</span></template><template #item.status="{ item }"><v-chip size="small" :color="statusColor(rawItem(item).status)" variant="tonal">{{ statusLabel(rawItem(item).status) }}</v-chip></template><template #item.bound_student_id="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).bound_student_id) }}</span></template><template #item.created_at="{ item }"><span class="text-medium-emphasis">{{ valueOrDash(rawItem(item).created_at) }}</span></template><template #item.used_at="{ item }"><span class="text-medium-emphasis">{{ valueOrDash(rawItem(item).used_at) }}</span></template><template #item.actions="{ item }"><v-btn size="small" variant="text" :color="rawItem(item).status === 'disabled' ? 'success' : 'warning'" :prepend-icon="rawItem(item).status === 'disabled' ? 'mdi-check-circle-outline' : 'mdi-cancel'" @click="setCDKStatus(rawItem(item))">{{ rawItem(item).status === 'disabled' ? '启用' : '禁用' }}</v-btn></template></v-data-table><div v-if="cdkTotal > cdkItemsPerPage" class="d-flex justify-end pa-3"><v-pagination v-model="cdkPage" :length="Math.max(1, Math.ceil(cdkTotal / cdkItemsPerPage))" density="comfortable" @update:model-value="loadCDKs" /></div></v-card></section>
              <section v-else-if="view === 'config'"><v-card variant="elevated" border class="config-card"><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-cog-outline" color="primary" /><span>配置</span><v-spacer /><v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="load">刷新</v-btn></v-card-title><v-divider /><v-card-text><div class="text-subtitle-1 font-weight-bold mb-4">APP 发布配置</div><v-form @submit.prevent="saveRelease"><v-row dense align="center"><v-col cols="12" md="4"><v-text-field v-model="releaseForm.latestVersion" label="最新版版本号" placeholder="例如 2.0.4" variant="outlined" density="comfortable" hide-details="auto" /></v-col><v-col cols="12" md="8"><v-text-field v-model="releaseForm.downloadUrl" label="下载 URL" placeholder="https://..." type="url" variant="outlined" density="comfortable" hide-details="auto" /></v-col></v-row><div class="d-flex align-center flex-wrap ga-3 mt-5"><span v-if="releaseConfig.updatedAt" class="text-body-2 text-medium-emphasis">最近更新：{{ releaseConfig.updatedAt }}</span><v-spacer /><v-btn type="submit" color="primary" prepend-icon="mdi-content-save-outline" :loading="loading">保存配置</v-btn></div></v-form></v-card-text></v-card></section>
              <section v-else-if="view === 'audit'"><v-card variant="elevated" border><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-history" color="primary" /><span>审计日志</span><v-spacer /><v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-refresh" :loading="loading" @click="load">刷新</v-btn></v-card-title><v-divider /><v-data-table :headers="auditHeaders" :items="audit" item-value="id" :items-per-page="25" items-per-page-text="每页条数：" page-text="{0}-{1} 共 {2}" :loading="loading" density="comfortable" hover class="admin-table" no-data-text="暂无数据"><template #item.created_at="{ item }"><span class="text-medium-emphasis">{{ valueOrDash(rawItem(item).created_at) }}</span></template><template #item.action="{ item }"><span>{{ actionLabel(rawItem(item).action) }}</span></template><template #item.actor_id="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).actor_id) }}</span></template><template #item.target="{ item }"><span>{{ valueOrDash(rawItem(item).target_type) }} {{ valueOrDash(rawItem(item).target_id) }}</span></template><template #item.detail="{ item }"><span class="table-ellipsis audit-detail" :title="valueOrDash(rawItem(item).detail)">{{ valueOrDash(rawItem(item).detail) }}</span></template></v-data-table></v-card></section>
            </v-container>
          </v-main>
        </template>

        <v-dialog v-model="userDialog" max-width="760" scrollable><v-card v-if="selectedUser"><v-card-title class="d-flex align-center ga-2"><v-icon icon="mdi-account-details-outline" color="primary" /><span>用户详情</span><v-spacer /><v-btn icon variant="text" aria-label="关闭" @click="closeUser"><v-icon icon="mdi-close" /></v-btn></v-card-title><v-divider /><v-card-text><v-row dense><v-col v-for="detail in userDetails" :key="detail.label" cols="12" sm="6"><v-sheet color="surface-variant" rounded="md" class="detail-item pa-3"><div class="text-caption text-medium-emphasis">{{ detail.label }}</div><div class="text-body-2 mt-1 text-break">{{ detail.value }}</div></v-sheet></v-col></v-row><div class="text-subtitle-2 font-weight-bold mt-6 mb-2">设备</div><v-data-table :headers="userDeviceHeaders" :items="selectedUser.devices || []" item-value="id" density="compact" hide-default-footer class="admin-table" no-data-text="暂无设备"><template #item.device_serial="{ item }"><span class="mono">{{ valueOrDash(rawItem(item).device_serial) }}</span></template><template #item.last_seen_at="{ item }"><span class="text-medium-emphasis">{{ valueOrDash(rawItem(item).last_seen_at) }}</span></template></v-data-table><v-row dense align="center" class="mt-4"><v-col cols="12" sm="6"><v-select v-model="userStatus" :items="[{title:'选择状态',value:''},{title:'启用',value:'active'},{title:'停用',value:'disabled'}]" item-title="title" item-value="value" label="更新状态" variant="outlined" density="comfortable" hide-details /></v-col><v-col cols="12" sm="auto"><v-btn color="primary" prepend-icon="mdi-content-save-outline" :loading="loading" @click="setStatus(selectedUser.user)">更新状态</v-btn></v-col></v-row></v-card-text></v-card></v-dialog>

        <v-dialog :model-value="bankDialog" @update:model-value="setBankDialog" max-width="1100" scrollable>
          <v-card v-if="bankDialog">
            <v-card-title class="d-flex align-center ga-2">
              <v-icon icon="mdi-book-edit-outline" color="primary" />
              <span>{{ bankEditing ? '编辑题库' : '新建题库' }}</span><v-spacer />
              <v-btn icon variant="text" aria-label="关闭" @click="closeBankEditor"><v-icon icon="mdi-close" /></v-btn>
            </v-card-title>
            <v-divider />
            <v-card-text>
              <v-form @submit.prevent="saveBank">
                <v-textarea v-model="bankJSON" label="题库 JSON" variant="outlined" rows="20" class="mono" hide-details="auto" />
                <div class="d-flex flex-wrap justify-end ga-2 mt-4">
                  <v-btn variant="text" @click="closeBankEditor">取消</v-btn>
                  <v-btn type="submit" color="primary" prepend-icon="mdi-content-save-outline" :loading="loading">保存题库</v-btn>
                </div>
              </v-form>
            </v-card-text>
          </v-card>
        </v-dialog>

        <v-dialog v-model="cdkDialog" max-width="520"><v-card><v-card-title class="d-flex align-center ga-2 flex-wrap"><v-icon icon="mdi-key-plus" color="secondary" /><span>生成通用 CDK</span><v-spacer /><v-btn icon variant="text" aria-label="关闭" @click="cdkDialog=false"><v-icon icon="mdi-close" /></v-btn></v-card-title><v-divider /><v-card-text><v-form @submit.prevent="createCDK"><v-alert type="info" variant="tonal" density="compact" class="mb-3">兑换时选择一个需要 CDK 的文库题库；每个 CDK 只能绑定一个题库。</v-alert><v-text-field v-model.number="cdkForm.count" label="生成数量" type="number" min="1" max="500" hint="一次最多生成 500 个，批量结果可复制" persistent-hint variant="outlined" density="comfortable" /><div class="d-flex justify-end ga-2 mt-4"><v-btn variant="text" @click="cdkDialog=false">取消</v-btn><v-btn type="submit" color="primary" prepend-icon="mdi-key-plus" :loading="loading">生成 CDK</v-btn></div></v-form></v-card-text></v-card></v-dialog>

        <v-dialog v-model="cdkRevealDialog" max-width="720"><v-card v-if="createdCDKs.length"><v-card-title class="d-flex align-center"><v-icon icon="mdi-key-check" color="success" class="mr-2" /><span>通用 CDK 已创建（{{ createdCDKs.length }} 个）</span><v-spacer /><v-btn icon variant="text" aria-label="关闭" @click="cdkRevealDialog=false"><v-icon icon="mdi-close" /></v-btn></v-card-title><v-divider /><v-card-text><v-alert type="warning" variant="tonal" density="compact" class="mb-4">CDK 只显示这一次，请立即复制并妥善保存。</v-alert><v-textarea :model-value="createdCDKs.map(item => item.code).join('\\n')" label="CDK 列表" readonly variant="outlined" rows="8" class="mono cdk-reveal" /></v-card-text><v-card-actions><v-spacer /><v-btn color="primary" prepend-icon="mdi-content-copy" @click="copyCDK">复制全部 CDK</v-btn><v-btn variant="text" @click="cdkRevealDialog=false">完成</v-btn></v-card-actions></v-card></v-dialog>

        <v-snackbar v-model="snackbar.show" :color="snackbar.color" location="bottom end" timeout="3500">{{ snackbar.text }}<template #actions><v-btn variant="text" @click="snackbar.show = false">关闭</v-btn></template></v-snackbar>
      </v-app>
    `))
  }).use(vuetify).mount('#app');
})();
