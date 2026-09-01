(function () {
  const { createApp, ref, computed, onMounted, nextTick } = Vue;

  async function api(path, options) {
    options = options || {};
    const csrfMatch = document.cookie.match(/(?:^|; )control_csrf=([^;]+)/);
    const headers = Object.assign({ 'Content-Type': 'application/json' }, options.headers || {});
    const storedToken = localStorage.getItem('control_admin');
    if (storedToken && !headers.Authorization) headers.Authorization = 'Bearer ' + storedToken;
    if (csrfMatch && options.method && options.method !== 'GET') headers['X-CSRF-Token'] = decodeURIComponent(csrfMatch[1]);
    const response = await fetch(path, Object.assign({ credentials: 'same-origin' }, options, { headers }));
    let body = {};
    try { body = await response.json(); } catch (_) {}
    if (!response.ok) throw new Error((body.error && body.error.message) || '请求失败');
    return body;
  }

  createApp({
    setup() {
      const logged = ref(!!localStorage.getItem('control_admin'));
      const view = ref('overview');
      const loading = ref(false);
      const error = ref('');
      const admin = ref(JSON.parse(localStorage.getItem('control_admin_info') || 'null'));
      const overview = ref({});
      const breakdown = ref({ platforms: {}, versions: {} });
      const series = ref([]);
      const users = ref([]);
      const devices = ref([]);
      const risks = ref([]);
      const audit = ref([]);
      const clients = ref([]);
      const providers = ref([]);
      const services = ref([]);
      const selectedUser = ref(null);
      const clientForm = ref({ client_id: '', client_name: '', client_secret: '', redirect_uris_text: '', scopes_text: 'openid profile', status: 'active' });
      const providerForm = ref({ id: '', name: '', authorization_url: '', token_url: '', userinfo_url: '', client_id: '', client_secret: '', scopes_text: 'openid profile', status: 'active' });
      const serviceForm = ref({ audience: '', secret: '', scopes_text: '', status: 'active' });
      const loginForm = ref({ username: 'admin', password: '' });
      const userStatus = ref('');
      const riskFilter = ref('');
      const chartEl = ref(null);
      let chart = null;
      const nav = [['overview', '活跃总览'], ['users', '用户'], ['devices', '设备'], ['risk', '风控'], ['oauth', 'OAuth 配置'], ['audit', '审计']];
      const title = computed(() => (nav.find(x => x[0] === view.value) || nav[0])[1]);

      const call = async (fn) => {
        loading.value = true; error.value = '';
        try { await fn(); } catch (e) { error.value = e.message; } finally { loading.value = false; }
      };
      const login = () => call(async () => {
        const out = await api('/api/v1/admin/session', { method: 'POST', body: JSON.stringify(loginForm.value) });
        localStorage.setItem('control_admin', out.access_token);
        localStorage.setItem('control_admin_info', JSON.stringify(out.admin));
        admin.value = out.admin; logged.value = true; await load();
      });
      const logout = () => call(async () => {
        await api('/api/v1/admin/session', { method: 'DELETE' });
        localStorage.removeItem('control_admin'); localStorage.removeItem('control_admin_info'); logged.value = false;
      });
      const load = async () => {
        if (!logged.value) return;
        await call(async () => {
          if (view.value === 'overview') {
            overview.value = await api('/api/v1/admin/metrics/overview');
            breakdown.value = await api('/api/v1/admin/metrics/breakdown');
            series.value = (await api('/api/v1/admin/metrics/series?days=30')).items || [];
            await nextTick(); drawChart();
          } else if (view.value === 'users') users.value = (await api('/api/v1/admin/users?limit=100')).items || [];
          else if (view.value === 'devices') devices.value = (await api('/api/v1/admin/devices?limit=100')).items || [];
          else if (view.value === 'risk') risks.value = (await api('/api/v1/admin/risk-events?limit=100' + (riskFilter.value ? '&acknowledged=' + riskFilter.value : ''))).items || [];
          else if (view.value === 'oauth') {
            clients.value = (await api('/api/v1/admin/oauth-clients')).items || [];
            providers.value = (await api('/api/v1/admin/oauth-providers')).items || [];
            services.value = (await api('/api/v1/admin/service-clients')).items || [];
          } else if (view.value === 'audit') audit.value = (await api('/api/v1/admin/audit?limit=100')).items || [];
        });
      };
      const switchView = (name) => { view.value = name; load(); };
      const drawChart = () => {
        if (!chartEl.value || !window.echarts) return;
        if (!chart) chart = echarts.init(chartEl.value);
        chart.setOption({ tooltip: { trigger: 'axis' }, legend: { data: ['用户', '设备', '事件'] }, grid: { left: 40, right: 20, top: 30, bottom: 28 }, xAxis: { type: 'category', data: series.value.map(x => x.day) }, yAxis: { type: 'value' }, series: [
          { name: '用户', type: 'line', smooth: true, data: series.value.map(x => x.users), itemStyle: { color: '#17724f' } },
          { name: '设备', type: 'line', smooth: true, data: series.value.map(x => x.devices), itemStyle: { color: '#287ca8' } },
          { name: '事件', type: 'line', smooth: true, data: series.value.map(x => x.events), itemStyle: { color: '#b77b25' } }
        ] });
      };
      const showUser = (user) => call(async () => { selectedUser.value = await api('/api/v1/admin/users/' + encodeURIComponent(user.id)); });
      const setStatus = (user) => call(async () => { await api('/api/v1/admin/users/' + encodeURIComponent(user.id) + '/status', { method: 'PATCH', body: JSON.stringify({ status: userStatus.value || user.status }) }); selectedUser.value = null; await load(); });
      const acknowledge = (risk) => call(async () => { await api('/api/v1/admin/risk-events/' + encodeURIComponent(risk.id), { method: 'PATCH', body: '{}' }); await load(); });
      const editClient = (client) => { clientForm.value = { client_id: client.client_id, client_name: client.client_name, client_secret: '', redirect_uris_text: (client.redirect_uris || []).join('\n'), scopes_text: (client.scopes || []).join(' '), status: client.status }; };
      const saveClient = () => call(async () => { const f = clientForm.value; await api('/api/v1/admin/oauth-clients/' + encodeURIComponent(f.client_id), { method: 'PUT', body: JSON.stringify({ client_id: f.client_id, client_name: f.client_name, client_secret: f.client_secret, redirect_uris: f.redirect_uris_text.split(/\s*\n\s*/).filter(Boolean), scopes: f.scopes_text.split(/\s+/).filter(Boolean), status: f.status }) }); clientForm.value = { client_id: '', client_name: '', client_secret: '', redirect_uris_text: '', scopes_text: 'openid profile', status: 'active' }; await load(); });
      const editProvider = (provider) => { providerForm.value = { id: provider.id, name: provider.name, authorization_url: provider.authorization_url, token_url: provider.token_url, userinfo_url: provider.userinfo_url || '', client_id: provider.client_id, client_secret: '', scopes_text: (provider.scopes || []).join(' '), status: provider.status }; };
      const saveProvider = () => call(async () => { const f = providerForm.value; await api('/api/v1/admin/oauth-providers/' + encodeURIComponent(f.id), { method: 'PUT', body: JSON.stringify({ id: f.id, name: f.name, authorization_url: f.authorization_url, token_url: f.token_url, userinfo_url: f.userinfo_url, client_id: f.client_id, client_secret: f.client_secret, scopes: f.scopes_text.split(/\s+/).filter(Boolean), status: f.status }) }); providerForm.value = { id: '', name: '', authorization_url: '', token_url: '', userinfo_url: '', client_id: '', client_secret: '', scopes_text: 'openid profile', status: 'active' }; await load(); });
      const editService = (service) => { serviceForm.value = { audience: service.audience, secret: '', scopes_text: (service.scopes || []).join(' '), status: service.status }; };
      const saveService = () => call(async () => { const f = serviceForm.value; await api('/api/v1/admin/service-clients/' + encodeURIComponent(f.audience), { method: 'PUT', body: JSON.stringify({ audience: f.audience, secret: f.secret, scopes: f.scopes_text.split(/\s+/).filter(Boolean), status: f.status }) }); serviceForm.value = { audience: '', secret: '', scopes_text: '', status: 'active' }; await load(); });
      onMounted(() => { if (logged.value) load(); window.addEventListener('resize', () => chart && chart.resize()); });
      return { logged, view, loading, error, admin, overview, breakdown, series, users, devices, risks, audit, clients, providers, services, selectedUser, clientForm, providerForm, serviceForm, loginForm, userStatus, riskFilter, chartEl, title, nav, login, logout, switchView, load, showUser, setStatus, acknowledge, editClient, saveClient, editProvider, saveProvider, editService, saveService };
    },
    template: `
      <div v-if="!logged" class="login"><div class="login-box"><h1>xzitpocket control</h1><p>管理中心</p><div v-if="error" class="error">{{error}}</div><form class="form" @submit.prevent="login"><label>用户名<input v-model="loginForm.username" autocomplete="username"></label><label>密码<input v-model="loginForm.password" type="password" autocomplete="current-password"></label><button class="btn primary" :disabled="loading">登录</button></form></div></div>
      <div v-else class="shell"><aside class="sidebar"><div class="brand">xzitpocket<small>control center</small></div><nav class="nav"><button v-for="item in nav" :key="item[0]" :class="{active:view===item[0]}" @click="switchView(item[0])">{{item[1]}}</button></nav><button class="logout" @click="logout">退出</button></aside><main class="main"><header class="topbar"><h1>{{title}}</h1><span class="admin-chip">{{admin && admin.username}} · {{admin && admin.role}}</span></header><section class="content"><div v-if="error" class="error" style="margin-bottom:14px">{{error}}</div>
        <div v-if="view==='overview'"><div class="grid stats"><div class="stat"><label>DAU</label><strong>{{overview.dau||0}}</strong></div><div class="stat"><label>WAU</label><strong>{{overview.wau||0}}</strong></div><div class="stat"><label>MAU</label><strong>{{overview.mau||0}}</strong></div><div class="stat"><label>活跃设备</label><strong>{{overview.active_devices||0}}</strong></div><div class="stat"><label>用户总数</label><strong>{{overview.total_users||0}}</strong></div><div class="stat"><label>今日事件</label><strong>{{overview.today_events||0}}</strong></div></div><div class="panel"><h2>近 30 天趋势</h2><div ref="chartEl" class="chart"></div></div><div class="grid three"><div class="panel"><h2>平台分布</h2><div v-for="(value,key) in breakdown.platforms" :key="key" class="breakdown-row"><span>{{key}}</span><b>{{value}}</b></div><div v-if="!Object.keys(breakdown.platforms||{}).length" class="empty">暂无数据</div></div><div class="panel"><h2>版本分布</h2><div v-for="(value,key) in breakdown.versions" :key="key" class="breakdown-row"><span>{{key}}</span><b>{{value}}</b></div><div v-if="!Object.keys(breakdown.versions||{}).length" class="empty">暂无数据</div></div><div class="panel"><h2>付费入口</h2><div class="detail"><div><span>打开次数</span>{{breakdown.paid_entries||0}}</div><div><span>去重用户</span>{{breakdown.paid_users||0}}</div><div><span>匿名事件</span>{{breakdown.anonymous_events||0}}</div></div></div></div></div>
        <div v-else-if="view==='users'"><div class="panel"><div class="toolbar"><button class="btn" @click="load">刷新</button></div><div class="table-wrap"><table><thead><tr><th>ID</th><th>显示名</th><th>状态</th><th>设备数</th><th>最近登录</th><th></th></tr></thead><tbody><tr v-for="u in users" :key="u.id"><td>{{u.id}}</td><td>{{u.display_name||'-'}}</td><td><span class="badge" :class="{off:u.status!=='active'}">{{u.status}}</span></td><td>{{u.device_count}}</td><td>{{u.last_login_at}}</td><td><button class="btn" @click="showUser(u)">详情</button></td></tr></tbody></table><div v-if="!users.length" class="empty">暂无数据</div></div></div></div>
        <div v-else-if="view==='devices'"><div class="panel"><div class="toolbar"><button class="btn" @click="load">刷新</button></div><div class="table-wrap"><table><thead><tr><th>设备码</th><th>平台</th><th>版本</th><th>安装 ID</th><th>最近活动</th><th>状态</th></tr></thead><tbody><tr v-for="d in devices" :key="d.id"><td>{{d.device_serial}}</td><td>{{d.platform}}</td><td>{{d.app_version}}</td><td>{{d.installation_id}}</td><td>{{d.last_seen_at}}</td><td><span class="badge" :class="{off:d.revoked_at}">{{d.revoked_at?'已撤销':'正常'}}</span></td></tr></tbody></table><div v-if="!devices.length" class="empty">暂无数据</div></div></div></div>
        <div v-else-if="view==='risk'"><div class="panel"><div class="toolbar"><select v-model="riskFilter" @change="load"><option value="">全部</option><option value="false">未查看</option><option value="true">已查看</option></select><button class="btn" @click="load">刷新</button></div><div class="table-wrap"><table><thead><tr><th>类型</th><th>用户</th><th>设备</th><th>次数</th><th>时间</th><th>状态</th><th></th></tr></thead><tbody><tr v-for="r in risks" :key="r.id"><td>{{r.type}}</td><td>{{r.user_id||'-'}}</td><td>{{r.device_id||'-'}}</td><td>{{r.observed_count}}</td><td>{{r.created_at}}</td><td>{{r.acknowledged_at?'已查看':'未查看'}}</td><td><button v-if="!r.acknowledged_at" class="btn" @click="acknowledge(r)">标记</button></td></tr></tbody></table><div v-if="!risks.length" class="empty">暂无数据</div></div></div></div>
        <div v-else-if="view==='oauth'"><div class="panel"><div class="panel-head"><h2>OAuth 客户端</h2><button class="btn" @click="clientForm={client_id:'',client_name:'',client_secret:'',redirect_uris_text:'',scopes_text:'openid profile',status:'active'}">新建</button></div><div class="form inline-form"><input v-model="clientForm.client_id" placeholder="client_id"><input v-model="clientForm.client_name" placeholder="名称"><input v-model="clientForm.client_secret" type="password" placeholder="新 secret（可留空）"><input v-model="clientForm.redirect_uris_text" placeholder="回调地址，每行一个"><input v-model="clientForm.scopes_text" placeholder="scope，以空格分隔"><select v-model="clientForm.status"><option value="active">active</option><option value="disabled">disabled</option></select><button class="btn primary" @click="saveClient">保存客户端</button></div><div class="table-wrap"><table><thead><tr><th>ID</th><th>名称</th><th>回调地址</th><th>Scope</th><th>状态</th><th></th></tr></thead><tbody><tr v-for="c in clients" :key="c.client_id"><td>{{c.client_id}}</td><td>{{c.client_name}}</td><td>{{(c.redirect_uris||[]).join(', ')}}</td><td>{{(c.scopes||[]).join(' ')}}</td><td>{{c.status}}</td><td><button class="btn" @click="editClient(c)">编辑</button></td></tr></tbody></table><div v-if="!clients.length" class="empty">暂无配置</div></div></div>
          <div class="panel"><div class="panel-head"><h2>外部 Provider</h2><button class="btn" @click="providerForm={id:'',name:'',authorization_url:'',token_url:'',userinfo_url:'',client_id:'',client_secret:'',scopes_text:'openid profile',status:'active'}">新建</button></div><div class="form inline-form"><input v-model="providerForm.id" placeholder="provider id"><input v-model="providerForm.name" placeholder="名称"><input v-model="providerForm.authorization_url" placeholder="authorization URL"><input v-model="providerForm.token_url" placeholder="token URL"><input v-model="providerForm.userinfo_url" placeholder="userinfo URL"><input v-model="providerForm.client_id" placeholder="client id"><input v-model="providerForm.client_secret" type="password" placeholder="新 secret（可留空）"><input v-model="providerForm.scopes_text" placeholder="scope，以空格分隔"><select v-model="providerForm.status"><option value="active">active</option><option value="disabled">disabled</option></select><button class="btn primary" @click="saveProvider">保存 Provider</button></div><div class="table-wrap"><table><thead><tr><th>ID</th><th>名称</th><th>Client ID</th><th>Scope</th><th>状态</th><th></th></tr></thead><tbody><tr v-for="p in providers" :key="p.id"><td>{{p.id}}</td><td>{{p.name}}</td><td>{{p.client_id}}</td><td>{{(p.scopes||[]).join(' ')}}</td><td>{{p.status}}</td><td><button class="btn" @click="editProvider(p)">编辑</button></td></tr></tbody></table><div v-if="!providers.length" class="empty">暂无配置</div></div></div>
          <div class="panel"><div class="panel-head"><h2>付费服务</h2><button class="btn" @click="serviceForm={audience:'',secret:'',scopes_text:'',status:'active'}">新建</button></div><div class="form inline-form"><input v-model="serviceForm.audience" placeholder="audience"><input v-model="serviceForm.secret" type="password" placeholder="服务端 secret（可留空）"><input v-model="serviceForm.scopes_text" placeholder="scope，以空格分隔"><select v-model="serviceForm.status"><option value="active">active</option><option value="disabled">disabled</option></select><button class="btn primary" @click="saveService">保存服务</button></div><div class="table-wrap"><table><thead><tr><th>Audience</th><th>Scope</th><th>状态</th><th></th></tr></thead><tbody><tr v-for="service in services" :key="service.audience"><td>{{service.audience}}</td><td>{{(service.scopes||[]).join(' ')}}</td><td>{{service.status}}</td><td><button class="btn" @click="editService(service)">编辑</button></td></tr></tbody></table></div></div></div>
        <div v-else-if="view==='audit'"><div class="panel"><div class="toolbar"><button class="btn" @click="load">刷新</button></div><div class="table-wrap"><table><thead><tr><th>时间</th><th>动作</th><th>操作者</th><th>目标</th><th>详情</th></tr></thead><tbody><tr v-for="a in audit" :key="a.id"><td>{{a.created_at}}</td><td>{{a.action}}</td><td>{{a.actor_id||'-'}}</td><td>{{a.target_type}} {{a.target_id}}</td><td class="muted">{{a.detail}}</td></tr></tbody></table><div v-if="!audit.length" class="empty">暂无数据</div></div></div></div>
      </section></main></div>
      <div v-if="selectedUser" class="modal-backdrop" @click.self="selectedUser=null"><div class="modal"><div class="modal-head"><h2>用户详情</h2><button class="close" @click="selectedUser=null">×</button></div><div class="detail" style="margin-top:16px"><div><span>ID</span>{{selectedUser.user.id}}</div><div><span>显示名</span>{{selectedUser.user.display_name||'-'}}</div><div><span>学号</span>{{selectedUser.student_id||'-'}}</div><div><span>伪名</span>{{selectedUser.user.student_alias||'-'}}</div><div><span>状态</span>{{selectedUser.user.status}}</div><div><span>最近登录</span>{{selectedUser.user.last_login_at}}</div></div><h3 style="font-size:14px;margin:20px 0 8px">设备</h3><div class="table-wrap"><table><thead><tr><th>设备码</th><th>平台</th><th>最近活动</th></tr></thead><tbody><tr v-for="d in selectedUser.devices" :key="d.id"><td>{{d.device_serial}}</td><td>{{d.platform}}</td><td>{{d.last_seen_at}}</td></tr></tbody></table></div><div class="toolbar" style="margin-top:18px"><select v-model="userStatus"><option value="">选择状态</option><option value="active">active</option><option value="disabled">disabled</option><option value="deleted">deleted</option></select><button class="btn primary" @click="setStatus(selectedUser.user)">更新状态</button></div></div></div>
    `
  }).mount('#app');
})();
