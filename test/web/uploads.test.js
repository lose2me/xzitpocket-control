import { afterEach, expect, it, vi } from 'vitest';
import { consoleHarness, settle } from './harness.js';

const pdf = vi.hoisted(()=>({getDocument:vi.fn(),getPage:vi.fn(),render:vi.fn()}));
vi.mock('../../web/vendor/js/pdf.min.mjs',()=>({default:undefined,GlobalWorkerOptions:{},getDocument:pdf.getDocument}));
let h;
afterEach(()=>{h?.close();vi.restoreAllMocks();});
const row={id:'r/1',name:'试卷.docx',code:'C1',course_codes:['C1'],format:'docx'};
const response=({url,method})=>{
  if(url.endsWith('/me'))return {body:{permissions:'*=rw'}};
  if(url.includes('/resource-url'))return {body:{url:'https://example.org/cover'}};
  if(method==='POST')return {body:row};
  return {body:{items:[row],total:1}};
};
const file=(name='paper.pdf',type='application/pdf')=>Object.assign(new File(['pdf'],name,{type}),{arrayBuffer:async()=>new ArrayBuffer(4)});
async function setup(){
  h=await consoleHarness({path:'/resources',respond:response});
  URL.createObjectURL=vi.fn(()=> 'blob:test');
  URL.revokeObjectURL=vi.fn();
  vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(()=>{});
  vi.spyOn(HTMLCanvasElement.prototype,'getContext').mockReturnValue({});
  vi.spyOn(HTMLCanvasElement.prototype,'toBlob').mockImplementation(function(callback){callback(new Blob(['cover'],{type:'image/webp'}));});
  pdf.render.mockReturnValue({promise:Promise.resolve()});
  pdf.getPage.mockResolvedValue({getViewport:({scale})=>({width:100*scale,height:200*scale}),render:pdf.render});
  pdf.getDocument.mockReturnValue({promise:Promise.resolve({numPages:3,getPage:pdf.getPage})});
  return h.vm;
}
function xhrFake(result={status:200,body:{items:[row]}}){
  const sent=[];
  class XHR {
    upload={};headers={};
    open(method,url){this.method=method;this.url=url;}
    setRequestHeader(key,value){this.headers[key]=value;}
    send(body){
      sent.push(this);this.body=body;
      queueMicrotask(()=>{
        this.upload.onprogress?.({lengthComputable:false});
        this.upload.onprogress?.({lengthComputable:true,loaded:0,total:0});
        this.upload.onprogress?.({lengthComputable:true,loaded:5,total:10});
        if(result.network){this.onerror();return;}
        this.status=result.status;this.responseText=result.raw??JSON.stringify(result.body);this.onload();
      });
    }
  }
  vi.stubGlobal('XMLHttpRequest',XHR);
  return sent;
}

it('filters dropped documents, uploads PDF batches with real progress and partial failures',async()=>{
  const vm=await setup();
  await vm.openResourceDialog({raw:row});
  expect(vm.resourceCourse.code).toBe('C1');
  vm.onResourceDrop({});
  vm.onResourceDrop({dataTransfer:{files:[file('bad.txt','text/plain')]}});
  expect(vm.resourceFiles).toEqual([]);
  await vm.uploadResources();
  expect(vm.error).toContain('至少');
  vm.onResourceDrop({dataTransfer:{files:[file(),file('upper.PDF','')]}});
  document.cookie='control_csrf=token';
  const sent=xhrFake({status:200,body:{items:[row],failed:[{name:'broken.pdf',reason:'损坏'}]}});
  await vm.uploadResources();
  expect(sent[0].headers).toMatchObject({Authorization:'Bearer test-token','X-CSRF-Token':'token'});
  expect(sent[0].body.getAll('files')).toHaveLength(2);
  expect(vm.uploadProgress).toBe(50);
  expect(vm.uploading).toBe(false);
  expect(vm.snackbar.color).toBe('warning');
  expect(vm.resourceFiles).toEqual([]);
  vm.resourceFiles=file();
  xhrFake();
  await vm.uploadResources();
  expect(vm.snackbar.text).toContain('1 个 PDF');
  vm.resourceFiles=file();
  vm.onResourceDrop({dataTransfer:{files:[file()]}});
  expect(vm.resourceFiles).toHaveLength(2);
});

it.each([
  [{status:500,body:{error:{message:'容量不足',code:'quota'}}},'容量不足'],
  [{status:500,raw:'invalid'},'请求失败'],
  [{network:true},'网络错误'],
  [{status:401,body:{}},''],
])('releases upload state on XHR failure %j',async(result,message)=>{
  const vm=await setup();
  vm.resourceFiles=[file()];
  xhrFake(result);
  await vm.uploadResources();
  expect(vm.error).toBe(message);
  expect(vm.uploading).toBe(false);
  expect(vm.loading).toBe(false);
  if(result.status===401)expect(vm.logged).toBe(false);
});

it('uploads manual analyses, resets file input, rejects wrong formats and recovers after request errors',async()=>{
  const vm=await setup();
  vm.manualFileInput={click:vi.fn()};
  vm.openManualUpload(row);
  expect(vm.manualFileInput.click).toHaveBeenCalled();
  await vm.onManualFileSelected({target:{files:[],value:'previous'}});
  await vm.onManualFileSelected({target:{files:[file('x.txt','text/plain')],value:'previous'}});
  expect(vm.error).toContain('PDF');
  for(const f of [file(),file('test.PDF','')]){
    const input={files:[f],value:'previous'};
    await vm.onManualFileSelected({target:input});
    expect(input.value).toBe('');
    expect(vm.manualUploading).toBe(false);
    const request=h.requests.findLast(r=>r.url.includes('/manual-analysis'));
    expect(request.body).toBeInstanceOf(FormData);
    expect(request.headers['Content-Type']).toBeUndefined();
  }
  h.respond(()=>({status:500,body:{}}));
  await vm.onManualFileSelected({target:{files:[file()],value:''}});
  expect(vm.manualUploading).toBe(false);
  expect(vm.error).toBe('请求失败');
});

it('renders selected PDF pages, clamps page input and uploads cover and original on textbook save',async()=>{
  const vm=await setup();
  await vm.openNewTextbook();
  await vm.saveTextbook();
  expect(vm.error).toContain('名称');
  await vm.generateCover();
  expect(vm.error).toContain('选择 PDF');
  await vm.onPdfPageChange();
  vm.pdfCanvasEl=document.createElement('canvas');
  await vm.onPdfSelected([file()]);
  expect(vm.pdfPageCount).toBe(3);
  for(const [input,page] of [[99,3],[-1,1],['bad',1]]){
    vm.pdfPage=input;await vm.onPdfPageChange();
    expect(vm.pdfPage).toBe(page);
  }
  await vm.generateCover();
  expect(vm.coverPreviewUrl).toBe('blob:test');
  await vm.generateCover();
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:test');
  vm.textbookForm={name:'教材',tags:'标签',courseCodes:['C1']};
  const sent=xhrFake();
  await vm.saveTextbook();
  expect(sent[0].url).toContain('/textbooks/r%2F1/pdf');
  expect(vm.uploadProgress).toBe(50);
  const cover=h.requests.find(r=>r.url.endsWith('/cover'));
  expect(cover.headers['Content-Type']).toBe('image/webp');
  expect(cover.body).toBeInstanceOf(Blob);
  await vm.onPdfSelected(null);
  expect(vm.pdfPageCount).toBe(0);
  expect(vm.pdfFile).toBeNull();
  await vm.editTextbook({...row,has_cover:true});
  expect(vm.existingCoverUrl).toContain('example.org');
  await vm.saveTextbook();
  expect(h.requests.some(r=>r.method==='PUT'&&r.url.endsWith('r%2F1'))).toBe(true);
});

it('handles PDF parse, cover encoding, existing cover and asset upload errors',async()=>{
  const vm=await setup();
  pdf.getDocument.mockImplementationOnce(()=>({promise:Promise.reject(new Error('损坏的 PDF'))}));
  await vm.onPdfSelected(file());
  expect(vm.error).toBe('损坏的 PDF');
  await vm.onPdfSelected(file());
  HTMLCanvasElement.prototype.toBlob.mockImplementationOnce(callback=>callback(null));
  await vm.generateCover();
  expect(vm.error).toContain('WebP');
  vm.textbookForm.name='教材';
  xhrFake({network:true});
  await vm.saveTextbook();
  expect(vm.error).toBe('网络错误');
  expect(vm.uploading).toBe(false);
  h.respond(({url})=>url.includes('/resource-url')?{status:500,body:{}}:response({url}));
  await vm.editTextbook({...row,has_cover:true});
  expect(vm.existingCoverUrl).toBe('');
  vm.onTextbookDrop({});
  vm.onTextbookDrop({dataTransfer:{files:[file('ignored.txt','text/plain')]}});
  vm.onTextbookDrop({dataTransfer:{files:[file()]}});
  await settle();
  expect(vm.pdfPageCount).toBe(3);
});

it('binds and unbinds contributors in each collection and ignores absent IDs',async()=>{
  const vm=await setup();
  await vm.saveContributor();
  for(const kind of ['textbook','resource']){
    vm.openContributorDialog(kind,row);
    vm.contributorStudentID=' 20260001 ';
    await vm.saveContributor();
    expect(JSON.parse(h.requests.findLast(r=>r.url.endsWith('/contributor')).body)).toEqual({student_id:'20260001'});
    vm.contributorStudentID='';
    await vm.saveContributor();
    expect(vm.snackbar.text).toContain('解除');
  }
});

it('downloads authenticated attachments, sanitizes names, reports failures and revokes blob URLs',async()=>{
  const vm=await setup();
  for(const [action,args,filename] of [
    ['downloadTextbook',[{...row,name:'a/b'}],'a_b.pdf'],
    ['downloadTextbook',[{...row,name:'   '}],'资料.pdf'],
    ['downloadTextbook',[{...row,name:'a.PDF'}],'a.PDF'],
    ['downloadResource',[row],'试卷.docx'],
    ['downloadResource',[{...row,name:'a/b.doc',format:'doc'}],'a_b.doc'],
    ['downloadResource',[{...row,name:'',format:'other'}],'资料.pdf'],
    ['downloadAITask',[{...row,resource_name:'试卷'},'more'],'试卷-AI解析-小学生版.pdf'],
    ['downloadAITask',[{},'basic'],'试卷-AI解析-精简版.pdf'],
  ]){
    await vm[action](...args);
    const anchor=HTMLAnchorElement.prototype.click.mock.instances.at(-1);
    expect(anchor.download).toBe(filename);
    expect(h.requests.findLast(r=>r.url.includes('/download')).headers.Authorization).toBe('Bearer test-token');
  }
  await vi.advanceTimersByTimeAsync(60000);
  expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:test');
  for(const body of [{error:{message:'无文件'}},'invalid-json']){
    h.respond(()=>({status:404,body}));
    await vm.downloadResource(row);
    expect(vm.error).toBe(body==='invalid-json'?'下载失败':'无文件');
  }
});

it('reviews multi-file submissions, keeps remaining rows in sync and closes when the last file disappears',async()=>{
  const vm=await setup();
  expect(vm.submissionFileRows).toEqual([]);
  for(const kind of ['textbook','resource']){
    const item={...row,files:[{id:'f/1',name:'原卷.docx',format:'docx'}]};
    vm.openSubmissionFiles(kind,{raw:item});
    expect(vm.submissionFileRows).toHaveLength(1);
    await vm.downloadSubmissionFile(item.files[0]);
    window.confirm.mockReturnValueOnce(false);
    h.clearRequests();
    await vm.deleteSubmissionFile(item.files[0]);
    expect(h.requests).toHaveLength(0);
    await vm.deleteSubmissionFile(item.files[0]);
    expect(vm.submissionFilesDialog).toBe(true);
    h.respond(()=>({body:{items:[]}}));
    await vm.deleteSubmissionFile(item.files[0]);
    expect(vm.submissionFilesDialog).toBe(false);
    expect(vm.submissionFilesItem).toBeNull();
    await vm.rejectSubmission(kind,row);
    window.confirm.mockReturnValueOnce(false);
    h.clearRequests();
    await vm.rejectSubmission(kind,row);
    expect(h.requests).toHaveLength(0);
    h.respond(response);
  }
});
