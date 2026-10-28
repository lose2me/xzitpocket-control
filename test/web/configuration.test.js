import { afterEach, expect, it } from 'vitest';
import { consoleHarness, settle } from './harness.js';

let h;
afterEach(() => h?.close());
const calendar = {
  days: [{ date: '2026-10-01', name: '国庆', adjustment: '2026-10-02' }, { date: '2026-10-02' }],
  slots: [{ start: '08:00', end: '08:45' }, { start: '09:00', end: '09:45', lab: true }, {}],
  colleges: [{ college: '计算机', exceptions: [{ date: '2026-10-02', name: '院庆' }] }],
  collegeOptions: ['机械'],
};
const groups = [
  { key: 'general', label: '常规', fields: [{ key: 'CONTROL_OVERVIEW_SCALE_PERCENT', kind: 'number', value: '100' }, {key:'TITLE',kind:'string',value:'标题'}] },
  { key: 'ai', label: 'AI', fields: [
    {key:'ENABLED',kind:'bool',value:'true'}, {key:'TOKEN',kind:'secret',set:true},
    {key:'PROMPT',kind:'text',value:'提示词'}, {key:'SECOND',kind:'text'},
  ] },
];
const response = ({url,method,body}) => {
  if (url.endsWith('/me')) return {body:{permissions:'*=rw'}};
  if (url.includes('/school-calendar')) return {body:calendar};
  if (url.endsWith('/settings')) return {body:{groups}};
  return {body:method === 'PUT' ? {...JSON.parse(body),updatedAt:'2026-10-28T00:00:00Z'} : {items:[]}};
};

it('validates administrator keys and module grants, creates and updates independent permissions', async () => {
  h = await consoleHarness({path:'/admins',respond:response});
  h.vm.openNewAdmin();
  for (const [key,error] of [['','填写'],['short','至少']]) {
    h.vm.adminForm.key=key;
    await h.vm.saveAdmin();
    expect(h.vm.error).toContain(error);
  }
  h.vm.randomAdminKey();
  expect(h.vm.adminForm.key).toMatch(/^[a-f0-9]{32}$/);
  await h.vm.saveAdmin();
  expect(h.vm.error).toContain('权限');
  h.vm.adminPermsForm['school-calendar']='r';
  h.vm.adminPermsForm['settings-ai']='rw';
  await h.vm.saveAdmin();
  const post=h.requests.find(r=>r.method==='POST');
  expect(JSON.parse(post.body).permissions).toBe('school-calendar=r,settings-ai=rw');
  h.vm.editAdmin({raw:{id:'a/1',permissions:'school-calendar=rw,settings-ai=r,invalid'}});
  expect(h.vm.adminPermsForm['school-calendar']).toBe('rw');
  await h.vm.saveAdmin();
  expect(h.requests.find(r=>r.method==='PUT').url).toContain('a%2F1');
  h.vm.editAdmin({});
  expect(h.vm.adminEditingId).toBe('');
});

it('loads settings and saves only editable fields in the selected group, preserving hidden secrets', async () => {
  h = await consoleHarness({path:'/web-config',respond:response});
  expect(h.vm.settingTabLabel).toBe('常规');
  expect(h.vm.aiPromptSettingFields.map(f=>f.key)).toEqual(['PROMPT','SECOND']);
  expect(h.vm.aiPromptSettingField.key).toBe('PROMPT');
  h.vm.aiPromptSettingTab='SECOND';
  expect(h.vm.aiPromptSettingField.key).toBe('SECOND');
  expect(h.vm.canWriteSettingGroup('ai')).toBe(true);
  expect(h.vm.canWriteSettingGroup('unknown')).toBe(false);
  expect(h.vm.settingGroupIcon('ai')).toContain('file-search');
  expect(h.vm.settingGroupIcon('unknown')).toContain('cog');
  h.vm.settingTab='ai';
  h.vm.settingsForm.PROMPT.value='  new prompt  ';
  h.vm.settingsForm.SECOND.value=null;
  await h.vm.saveSettings();
  expect(JSON.parse(h.requests.findLast(r=>r.method==='PUT').body)).toEqual({values:{ENABLED:'true',PROMPT:'new prompt',SECOND:''}});
  h.vm.settingsForm.TOKEN.value=' secret ';
  h.vm.settingsForm.ENABLED.value=false;
  await h.vm.saveSettings();
  expect(JSON.parse(h.requests.findLast(r=>r.method==='PUT').body).values).toMatchObject({TOKEN:'secret',ENABLED:'false'});
  h.respond(({url})=>({body:url.endsWith('/me')?{permissions:'settings-ai=r'}:{groups}}));
  await h.vm.load(); // reload configuration, then refresh identity by remounting separately below
  h.respond(()=>({body:{}}));
  await h.vm.loadSettings();
  expect(h.vm.settingTabLabel).toBe('运行配置');
  expect(h.vm.aiPromptSettingField).toBeNull();
});

it('does not serialize fields from modules with only read permission', async () => {
  h=await consoleHarness({path:'/web-config',permissions:'settings-ai=r',respond:({url})=>({body:url.endsWith('/me')?{permissions:'settings-ai=r'}:{groups}})});
  h.vm.settingTab='ai';
  expect(h.vm.canWriteSettingGroup('ai')).toBe(false);
  await h.vm.saveSettings();
  expect(JSON.parse(h.requests.findLast(r=>r.method==='PUT').body)).toEqual({values:{}});
});

it.each([['saveRelease','releaseForm','releaseConfig'],['saveReleaseTest','releaseTestForm','releaseTestConfig']])('validates and saves release configuration through %s',async(action,form,config)=>{
  h=await consoleHarness({path:'/app-config',respond:response});
  await h.vm[action]();
  expect(h.vm.error).toContain('版本号');
  h.vm[form].latestVersion=' 2.0.0 ';
  await h.vm[action]();
  expect(h.vm.error).toContain('URL');
  h.vm[form].downloadUrl=' https://example.org/app.apk ';
  h.vm[form].changelog='更新';
  await h.vm[action]();
  expect(h.vm[config].latestVersion).toBe('2.0.0');
  expect(h.vm[config].updatedAt).toBe('2026-10-28 08:00:00');
  h.respond(()=>({body:{}}));
  await h.vm[action]();
  expect(h.vm[form].latestVersion).toBe('2.0.0');
});

it('loads college exceptions and defaults, serializes slots, and validates malformed times',async()=>{
  h=await consoleHarness({path:'/app-config',respond:response});
  expect(h.vm.schoolCalendarRange).toBe('20261001-20261002');
  expect(h.vm.schoolCalendarSlotsText).toBe('08:00-08:45\n09:00-09:45 T');
  expect(h.vm.schoolCalendarScopeItems.map(i=>i.value)).toEqual(['','机械','计算机']);
  expect(h.vm.schoolCalendarScopeItemProps({raw:{hasConfig:true}})).toHaveProperty('prependIcon');
  expect(h.vm.schoolCalendarScopeItemProps(null)).toEqual({});
  h.vm.selectSchoolCalendarScope('计算机');
  expect(JSON.parse(h.vm.schoolCalendarExceptionsJson)[0].name).toBe('院庆');
  h.vm.selectSchoolCalendarScope('机械');
  expect(h.vm.schoolCalendarExceptionsJson).toBe('[]');
  h.vm.selectSchoolCalendarScope(null);
  expect(JSON.parse(h.vm.schoolCalendarExceptionsJson)).toHaveLength(1);
  for(const text of ['bad','25:00-09:00','08:00-99:00']){
    h.vm.schoolCalendarSlotsText=text;
    await h.vm.saveSchoolCalendar();
    expect(h.vm.error).toContain('格式无效');
  }
  h.vm.schoolCalendarSlotsText='08:00 ~ 08:45 t\n\n10:00,10:45';
  await h.vm.saveSchoolCalendar();
  expect(JSON.parse(h.requests.findLast(r=>r.method==='PUT').body).slots).toEqual([
    {index:1,start:'08:00',end:'08:45',lab:true},{index:2,start:'10:00',end:'10:45',lab:false},
  ]);
  h.vm.schoolCalendarDays=[];
  await h.vm.saveSchoolCalendar();
  expect(h.vm.error).toContain('至少');
});

it('validates exception JSON and supports deleting or cancelling college overrides',async()=>{
  h=await consoleHarness({path:'/app-config',respond:response});
  for(const text of ['bad','{}']) {
    h.vm.schoolCalendarExceptionsJson=text;
    await h.vm.saveSchoolCalendarExceptions();
    expect(h.vm.error).toContain('JSON');
  }
  for(const scope of ['', '计算机']) {
    h.vm.selectSchoolCalendarScope(scope);
    await h.vm.saveSchoolCalendarExceptions();
    expect(JSON.parse(h.requests.findLast(r=>r.method==='PUT').body).college).toBe(scope);
  }
  window.confirm.mockReturnValueOnce(false);
  h.clearRequests();
  await h.vm.clearSchoolCalendarCollege();
  expect(h.requests).toHaveLength(0);
  await h.vm.clearSchoolCalendarCollege();
  expect(h.requests[0].method).toBe('DELETE');
  expect(h.vm.schoolCalendarScope).toBe('');
  h.clearRequests();
  await h.vm.clearSchoolCalendarCollege();
  expect(h.requests).toHaveLength(0);
  h.vm.schoolCalendarScope='disappeared';
  h.respond(()=>({body:{}}));
  await h.vm.loadSchoolCalendar();
  expect(h.vm.schoolCalendarScope).toBe('');
  expect(h.vm.schoolCalendarRange).toBe('');
});

it('rejects invalid ranges and rebuilds a leap-day calendar inclusively',async()=>{
  h=await consoleHarness({path:'/app-config',respond:response});
  for(const [range,error] of [['bad','格式'],['20260230-20260301','无效'],['20261002-20261001','晚于'],['20200101-20261001','1000']]){
    h.vm.schoolCalendarRange=range;
    h.vm.openSchoolCalendarRebuild();
    h.vm.confirmSchoolCalendarRebuild();
    await settle();
    expect(h.vm.error).toContain(error);
  }
  h.vm.schoolCalendarRange='20240228-20240301';
  h.vm.openSchoolCalendarRebuild();
  h.vm.confirmSchoolCalendarRebuild();
  await settle();
  const saved=JSON.parse(h.requests.findLast(r=>r.method==='PUT').body);
  expect(saved.days.map(d=>d.date)).toEqual(['2024-02-28','2024-02-29','2024-03-01']);
});
