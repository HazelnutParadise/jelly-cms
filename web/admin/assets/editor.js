/* Jelly editor: the server is the only Markdown renderer. */
(() => {
 'use strict';
 const root = document.getElementById('jellyEditor');
 if (!root) return;
 const T = window.JellyEditorTranslations, C = window.JellyCM;
 const $ = id => document.getElementById(id), tr = key => T['ed2' + key] || key;
 const form = root.closest('form'), title = form.elements.title, excerpt = form.elements.excerpt;
 const user = window.JellyEditorUser || {}, storageKey = 'jelly.editor.' + (user.ID || user.id || user.Username || user.username || 'self');
 const read = (key, fallback) => { try { return JSON.parse(localStorage.getItem(storageKey + '.' + key)) ?? fallback; } catch (_) { return fallback; } };
 const store = (key, value) => { try { localStorage.setItem(storageKey + '.' + key, JSON.stringify(value)); } catch (_) {} };
 let programmatic = false, savedSnapshot = null, renderSequence = 0, mermaidLoadPromise = null;
 let postId = Number(location.pathname.split('/').pop()) || 0, initialized = !postId, dirty = false, generation = 0, timer, controller, lastRenderSource = '', blocks = [], selectedBlock, schema = { Blocks: [], Slash: [] }, slashItems = [], slashIndex = 0, dismissedSlash = '', exportFormat = 'jmd', autoBusy = false, lastAutosave = '', idleTimer;
 form.inert=!initialized;
 const api = window.JellyEditor = { ready:initialized, format: 'jmd', baseUpdatedAt: undefined, save: async () => false };
 const theme = new C.Compartment();
 const view = new C.EditorView({ parent: $('editorSource'), state: C.EditorState.create({doc: '', extensions: [C.lineNumbers(), C.history(), C.drawSelection(), C.dropCursor(), C.highlightActiveLine(), C.highlightActiveLineGutter(), C.indentOnInput(), C.bracketMatching(), C.closeBrackets(), C.markdown(), C.syntaxHighlighting(C.HighlightStyle.define([{tag: C.tags.heading, color:'#d44872',fontWeight:'700'}, {tag:C.tags.strong,fontWeight:'700'}, {tag:C.tags.emphasis,fontStyle:'italic'}, {tag:C.tags.link,color:'#2f6bff'}, {tag:C.tags.monospace,color:'#098774'}])), C.search(), C.highlightSelectionMatches(), C.keymap.of([...C.defaultKeymap,...C.historyKeymap,...C.searchKeymap,...C.closeBracketsKeymap,C.indentWithTab]), C.EditorView.lineWrapping, C.EditorView.contentAttributes.of({'data-testid':'editor-source','aria-label':tr('Source'),'spellcheck':'false'}), theme.of([]), C.EditorView.updateListener.of(update => {
  if (update.docChanged) { if(initialized&&!programmatic){const day=new Date().toLocaleDateString('en-CA'),progress=read('progress',{});if(progress.day!==day){progress.day=day;progress.count=0;}progress.count=(progress.count||0)+Math.max(0,Array.from(update.state.doc.toString()).length-Array.from(update.startState.doc.toString()).length);store('progress',progress);} dirty = savedSnapshot === null || fingerprint() !== savedSnapshot; dismissedSlash = '';  generation++; clearTimeout(timer); timer = setTimeout(render, 250); backup(); if(root.classList.contains('typewriter')) queueMicrotask(()=>view.dispatch({effects:C.EditorView.scrollIntoView(view.state.selection.main.head,{y:'center'})})); }
  if(update.docChanged || update.selectionSet) { cursor(); requestAnimationFrame(slash); }
 })]}) });
 api.getValue = () => view.state.doc.toString();
 api.setValue = value => { value = String(value ?? ''); programmatic=true;try{view.dispatch({changes:{from:0,to:view.state.doc.length,insert:value}});}finally{programmatic=false;} };
 function notice(message, testid) { const n=$('editorNotice'); n.hidden=!message; n.textContent=message; if(testid)n.dataset.testid=testid;else delete n.dataset.testid; }
 function fingerprint(){return JSON.stringify(api.capture?.() || {title:title.value,source:api.getValue(),excerpt:excerpt.value});}
 api.track=()=>{if(initialized&&savedSnapshot===null)savedSnapshot=fingerprint();};
 function backup() { if(!initialized)return; store('draft.'+postId,{title:title.value,source:api.getValue(),excerpt:excerpt.value,draft:api.capture?.(),at:Date.now(),base:api.baseUpdatedAt}); }
 function changed(){if(!initialized)return;dirty=savedSnapshot===null||fingerprint()!==savedSnapshot;if(dirty)backup();else store('draft.'+postId,null);cursor();}
 function setStatus(message) { $('editorSaveStatus').textContent=message; }
 api.failed = status => { dirty=true; backup(); notice(tr(status===409?'Conflict':navigator.onLine?'SaveFailed':'Offline'),status===409?'save-conflict':!navigator.onLine?'offline-status':'save-error'); };
 api.saved = (post,submitted) => { savedSnapshot=JSON.stringify(submitted);const previousId=postId;postId=post.id;if(previousId!==postId)store('draft.'+previousId,null);api.baseUpdatedAt=post.updated_at;initialized=true;dirty=fingerprint()!==savedSnapshot;if(dirty)backup();else store('draft.'+postId,null);notice('');setStatus(dirty?tr('Unsaved'):T.saved);cursor();return !dirty; };
 api.loaded = post => {
  postId=post.id;api.ready=true;form.inert=false;api.baseUpdatedAt=post.updated_at;api.format=post.format==='md'?'md':'jmd';initialized=true;dirty=false;
  if(post.published_at){const d=new Date(post.published_at);$('publishAt').value=new Date(d-d.getTimezoneOffset()*60000).toISOString().slice(0,16);}
  form.seo_title.value=post.meta?.seo_title||'';form.seo_description.value=post.meta?.seo_description||'';savedSnapshot=fingerprint();
  const local=read('draft.'+postId,null), autosave=post.autosave;
  store('draft.'+postId,local); 
  let candidate = autosave && new Date(autosave.created_at).getTime()>new Date(post.updated_at).getTime()?autosave:null;
  if(local && local.at>new Date(post.updated_at).getTime() && (!candidate || local.at>new Date(candidate.created_at).getTime())) candidate=local;
  if(candidate && (candidate.source!==api.getValue() || candidate.title!==title.value || candidate.excerpt!==excerpt.value || candidate.draft&&JSON.stringify(candidate.draft)!==savedSnapshot)) {
   $('editorRestore').showModal();$('restoreAutosave').onclick=()=>{api.setValue(candidate.source);title.value=candidate.title??title.value;excerpt.value=candidate.excerpt??excerpt.value;api.restoreDraft?.(candidate.draft);dirty=true;$('editorRestore').close();backup();};
  }
  cursor();render();
 };
 function loadEditorScript(src, ready) {
  if(ready())return Promise.resolve();
  return new Promise((resolve,reject)=>{const script=document.createElement('script');script.src=src;script.onload=()=>ready()?resolve():reject(Error('Script did not initialize'));script.onerror=()=>{script.remove();reject(Error('Script failed to load'));};document.head.append(script);});
 }
 function ensureMermaid() {
  if(window.pele&&window.JellyMermaid)return Promise.resolve();
  if(!mermaidLoadPromise)mermaidLoadPromise=loadEditorScript('/admin/assets/pele/pele.global.js',()=>!!window.pele).then(()=>loadEditorScript('/admin/assets/jelly-mermaid.js',()=>!!window.JellyMermaid)).catch(error=>{mermaidLoadPromise=null;throw error;});
  return mermaidLoadPromise;
 }
 async function render() {
  const source=api.getValue(), gen=generation, seq=++renderSequence, current=()=>seq===renderSequence&&gen===generation;
  if(!navigator.onLine){notice(tr('Offline'),'offline-status');return;}
  if(controller)controller.abort();controller=new AbortController();
  try {
   const response=await fetch('/api/admin/render',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({source,post_id:postId||undefined,mode:'fragment',sourcemap:true,expand:true}),signal:controller.signal});
   if(!current())return;
   const result=await response.json();if(!current())return;
   if(!response.ok){diagnostics(result.diagnostics||[]);notice(tr(response.status===429?'RateLimit':'RenderFailed'),'render-error');return;}
   const features=result.features||result.Features||[], hasMermaid=features.includes('mermaid');
   let mermaidError=null;
   if(hasMermaid){try{await ensureMermaid();}catch(error){mermaidError=error;}if(!current())return;}
   diagnostics(result.diagnostics||[]);
   $('editorPreview').innerHTML=result.html;
   window.JellyComponents?.init($('editorPreview'));window.JellyMotion?.init($('editorPreview'));
   if(hasMermaid&&!mermaidError){await window.JellyMermaid.render($('editorPreview'));if(!current())return;}
   blocks=(result.blocks||[]).map(b=>({kind:b.Kind??b.kind,name:b.Name??b.name,attrs:b.Attrs??b.attrs??{},start:b.StartLine??b.start_line,end:b.EndLine??b.end_line}));lastRenderSource=source;
   outline(result.toc||[],result.stats||{});if(root.dataset.view==='layout')paintLayout();
   if(mermaidError){if($('editorNotice').dataset.testid!=='save-conflict')notice(tr('RenderFailed'),'render-error');}
   else if($('editorNotice').dataset.testid!=='save-conflict')notice('');
  } catch(error){if(error.name!=='AbortError' && current())notice(tr(navigator.onLine?'RenderFailed':'Offline'),navigator.onLine?'render-error':'offline-status');}
 }
 function diagnostics(items) { const target=$('editorDiagnostics');target.replaceChildren();for(const d of items){const b=document.createElement('button');b.type='button';b.textContent=`${tr('Line')} ${d.Line??d.line}: ${d.Message??d.message}`;b.onclick=()=>goLine(d.Line??d.line);target.append(b);} }
 function outline(toc,stats){$('editorTOC').replaceChildren();for(const h of toc){const b=document.createElement('button');b.type='button';b.textContent=h.Text??h.text;b.style.paddingLeft=((h.Level??h.level)-1)*10+'px';b.onclick=()=>{const n=Array.from($('editorPreview').querySelectorAll('[id]')).find(n=>n.id===(h.ID??h.id));if(n){n.scrollIntoView({block:'center'});if(n.dataset.line)goLine(+n.dataset.line);}};$('editorTOC').append(b);} $('editorStats').replaceChildren();for(const [key,label] of [['Chars','Chars'],['Words','Words'],['Minutes','Minutes'],['Images','Images'],['Links','Links'],['Todos','Todos']]){const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=tr(label);dd.textContent=stats[key]??stats[key.toLowerCase()]??0;$('editorStats').append(dt,dd);} }
 function goLine(n){const line=view.state.doc.line(Math.max(1,Math.min(n,view.state.doc.lines)));view.dispatch({selection:{anchor:line.from},effects:C.EditorView.scrollIntoView(line.from,{y:'center'})});view.focus();}
 function cursor(){ $('editorDocumentTitle').textContent=title.value; $('editorDocumentState').textContent=dirty?tr('Unsaved'):T.saved;const sel=view.state.selection.main,line=view.state.doc.lineAt(sel.head);$('editorCursor').textContent=`${tr('Line')} ${line.number}, ${tr('Column')} ${sel.head-line.from+1}`;$('editorSelection').textContent=sel.empty?'':`${tr('Selected')} ${Array.from(view.state.sliceDoc(sel.from,sel.to)).length}`;const day=new Date().toLocaleDateString('en-CA'),progress=read('progress',{});$('dailyProgress').textContent=`${progress.day===day?progress.count||0:0} / ${$('dailyGoal').value}`;}
 async function autosave(){if(!initialized||!postId||!dirty||autoBusy)return;backup();if(!navigator.onLine){notice(tr('Offline'),'offline-status');return;}const data={title:title.value,source:api.getValue(),excerpt:excerpt.value},snapshot=JSON.stringify(data);if(snapshot===lastAutosave)return;autoBusy=true;try{const r=await fetch(`/api/admin/posts/${postId}/autosave`,{method:'POST',headers:{'Content-Type':'application/json'},body:snapshot});if(!r.ok)throw Error();lastAutosave=snapshot;setStatus(tr('Autosaved')+' '+new Date().toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'}));}catch(_){setStatus(tr('AutosaveFailed'));}finally{autoBusy=false;}}
 setInterval(autosave,60000);view.contentDOM.addEventListener('blur',autosave);window.addEventListener('blur',autosave);for(const event of ['input','change','click','mousedown'])form.addEventListener(event,()=>queueMicrotask(changed));window.addEventListener('offline',()=>notice(tr('Offline'),'offline-status'));window.addEventListener('online',()=>{render();autosave();});window.addEventListener('beforeunload',e=>{if(dirty){backup();e.preventDefault();e.returnValue='';}});
 fetch('/api/admin/render/schema').then(r=>{if(!r.ok)throw Error();return r.json();}).then(s=>{schema={Blocks:s.Blocks||s.blocks||[],Slash:s.Slash||s.slash||[]};paintPalette();slash();}).catch(()=>notice(tr('CommandsFailed'),'schema-error'));
 function slash(){const line=view.state.doc.lineAt(view.state.selection.main.head),prefix=line.text.slice(0,view.state.selection.main.head-line.from),match=/^\s*\/([^\n]*)$/.exec(prefix);const menu=$('slashMenu');if(!match || dismissedSlash===prefix){menu.hidden=true;return;}const query=match[1].toLowerCase();slashItems=schema.Slash.filter(x=>((x.Key||x.key)+' '+(x.Label||x.label)+' '+commandLabel(x)).toLowerCase().includes(query));slashIndex=Math.min(slashIndex,Math.max(0,slashItems.length-1));menu.replaceChildren();if(!slashItems.length){const p=document.createElement('p');p.dataset.testid='slash-empty';p.textContent=tr('NoCommands');menu.append(p);}else for(const [i,item]of slashItems.entries()){const b=document.createElement('button');b.type='button';b.role='option';b.setAttribute('aria-selected',i===slashIndex);b.textContent=commandLabel(item);b.onmousedown=e=>e.preventDefault();b.onclick=()=>insertCommand(item,line.from,view.state.selection.main.head);menu.append(b);}menu.hidden=false;const rect=view.coordsAtPos(view.state.selection.main.head);if(rect){menu.style.left=Math.max(8,Math.min(innerWidth-300,rect.left))+'px';menu.style.top=Math.max(8,Math.min(innerHeight-280,rect.bottom+4))+'px';}}
 function commandLabel(item){return document.documentElement.lang==='en'?(item.Key||item.key).replace(/\b\w/g,s=>s.toUpperCase()):(item.Label||item.label);}
 function insertCommand(item,from=view.state.selection.main.from,to=view.state.selection.main.to){view.dispatch({changes:{from,to,insert:item.Insert||item.insert},selection:{anchor:from+(item.Insert||item.insert).length}});$('slashMenu').hidden=true;view.focus();}
 view.contentDOM.addEventListener('keydown',e=>{if(!$('slashMenu').hidden){if(e.key==='ArrowDown'||e.key==='ArrowUp'){e.preventDefault();slashIndex=(slashIndex+(e.key==='ArrowDown'?1:-1)+slashItems.length)%Math.max(1,slashItems.length);slash();}if(e.key==='Enter'&&slashItems.length){e.preventDefault();e.stopPropagation();const line=view.state.doc.lineAt(view.state.selection.main.head);insertCommand(slashItems[slashIndex],line.from,view.state.selection.main.head);}if(e.key==='Escape'){dismissedSlash=view.state.doc.lineAt(view.state.selection.main.head).text;$('slashMenu').hidden=true;}}},true);
 view.contentDOM.addEventListener('paste',e=>{const files=Array.from(e.clipboardData?.files||[]).filter(f=>f.type.startsWith('image/'));if(!files.length)return;e.preventDefault();for(const file of files)upload(file);});
 async function upload(file){const token='jelly-upload-'+crypto.randomUUID(),placeholder=`<!-- ${token}: ${tr('Uploading')} -->`;const sel=view.state.selection.main;view.dispatch({changes:{from:sel.from,to:sel.to,insert:placeholder}});const send=async()=>{try{const data=new FormData();const extension=/\.(png|jpe?g|gif|webp|avif|svg|bmp|ico|heic|heif|tiff?)$/i.exec(file.name)?.[1].toLowerCase()||({ 'image/png':'png','image/jpeg':'jpg','image/gif':'gif','image/webp':'webp','image/avif':'avif','image/svg+xml':'svg' }[file.type]||'img');data.append('file',file,token+'.'+extension);const r=await fetch('/api/admin/media',{method:'POST',body:data});if(!r.ok)throw Error();const m=await r.json(),url=m.url||m.media?.url;if(!url || !/^(\/[^/]|https?:\/\/)/.test(url))throw Error();const source=api.getValue(),at=source.indexOf(placeholder);if(at>=0)view.dispatch({changes:{from:at,to:at+placeholder.length,insert:`![${file.name.replace(/[\]\\\n\r]/g,'')}](${url.replace(/[()\s]/g,c=>encodeURIComponent(c))})`}});notice('');}catch(_){notice(tr('UploadFailed'),'upload-error');const retry=document.createElement('button');retry.type='button';retry.textContent=T.retry;retry.onclick=send;$('editorNotice').append(retry);}};await send();}
 function setView(mode){if(mode==='split'&&matchMedia('(max-width:700px)').matches)mode='source';root.dataset.view=mode;root.querySelectorAll('button[data-view]').forEach(b=>b.setAttribute('aria-pressed',b.dataset.view===mode));$('layoutCanvas').hidden=mode!=='layout';root.querySelector('.block-inspector').hidden=mode!=='layout';$('editorPalette').hidden=mode!=='layout';if(mode==='layout'){paintLayout();render();}else{view.requestMeasure();} }
 root.querySelectorAll('button[data-view]').forEach(b=>b.onclick=()=>setView(b.dataset.view));
 function paintPalette(){const p=$('editorPalette');for(const spec of schema.Blocks){const b=document.createElement('button');b.type='button';b.textContent=spec.Name;b.onclick=()=>{const name=spec.Name,insert=spec.Kind==='container'?`\n:::${name}\n\n:::\n`:`\n::${name}{}\n`;insertCommand({Insert:insert});};p.append(b);}}
 function paintLayout(){const canvas=$('layoutCanvas');canvas.replaceChildren();if(lastRenderSource!==api.getValue()){canvas.textContent=tr('Updating');return;}for(const block of blocks){const card=document.createElement('div');card.className='layout-block';card.dataset.testid='layout-block';card.dataset.blockStart=block.start;card.draggable=true;card.tabIndex=0;const heading=document.createElement('b');heading.textContent=block.name||block.kind;const code=document.createElement('pre');code.textContent=api.getValue().split('\n').slice(block.start-1,block.end).join('\n');card.append(heading,code);card.onclick=()=>inspect(block);card.ondragstart=e=>{selectedBlock=block;e.dataTransfer.setData('text/plain',String(block.start));e.stopPropagation();};card.ondragover=e=>{e.preventDefault();e.stopPropagation();};card.ondrop=e=>{e.preventDefault();e.stopPropagation();move(selectedBlock,block);};card.onkeydown=e=>{if(e.altKey&&(e.key==='ArrowUp'||e.key==='ArrowDown')){e.preventDefault();const parent=parentOf(block),peers=blocks.filter(b=>parentOf(b)===parent);const i=peers.indexOf(block),target=peers[i+(e.key==='ArrowUp'?-1:1)];if(target)move(block,target,e.key==='ArrowDown');}else if(e.key==='Enter')inspect(block);};canvas.append(card);} const target=selectedBlock&&blocks.find(b=>b.start===selectedBlock.start&&b.name===selectedBlock.name)||blocks.find(b=>b.name==='columns')||blocks.find(b=>schema.Blocks.some(s=>s.Name===b.name));if(target)inspect(target);}
 function parentOf(block){return blocks.filter(b=>b.start<block.start&&b.end>=block.end).sort((a,b)=>(a.end-a.start)-(b.end-b.start))[0]||null;}
 function move(block,target,after=false){if(!block||block===target||lastRenderSource!==api.getValue())return;if(parentOf(block)!==parentOf(target)||(target.start>=block.start&&target.end<=block.end)||(block.start>=target.start&&block.end<=target.end)){notice(tr('NestedMove'));return;}const lines=api.getValue().split('\n');let end=block.end;while(end<lines.length&&lines[end].trim()==='')end++;
  let at=after?target.end:target.start-1;if(after)while(at<lines.length&&lines[at].trim()==='')at++;
  const chunk=lines.splice(block.start-1,end-block.start+1);if(block.start<target.start)at-=chunk.length;
  if(at>0&&lines[at-1].trim()!==''&&chunk[0].trim()!=='')chunk.unshift('');
  if(at<lines.length&&lines[at].trim()!==''&&chunk[chunk.length-1].trim()!=='')chunk.push('');
  lines.splice(at,0,...chunk);api.setValue(lines.join('\n'));selectedBlock=null;render();}
 function inspect(block){selectedBlock=block;const fields=$('inspectorFields');fields.replaceChildren();const spec=schema.Blocks.find(s=>s.Name===block.name);if(!spec){fields.textContent=tr('SelectBlock');return;}for(const[key,values]of Object.entries(spec.Attrs||{})){const label=document.createElement('label');label.textContent=key;const input=document.createElement('input');input.name=key;input.dataset.attribute=key;if(key==='ratio')input.dataset.testid='inspector-ratio';input.value=block.attrs[key]||'';if(values?.length){const list=document.createElement('datalist');list.id='attr-'+key;for(const value of values){const o=document.createElement('option');o.value=value;list.append(o);}input.setAttribute('list',list.id);label.append(list);}input.onchange=()=>setAttribute(block,key,input.value,values);input.onkeydown=e=>{if(e.key==='Enter'){e.preventDefault();input.blur();}};label.append(input);fields.append(label);}}
 function openingAttributeRange(line, offset) {
  let start=offset;while(start<line.length&&/\s/.test(line[start]))start++;
  if(line[start]!=='{')return null;
  let quote=null;
  for(let i=start+1;i<line.length;i++){
   const c=line[i];if(c==='\\'){i++;continue;}
   if(quote){if(c===quote)quote=null;continue;}
   if(c==='"'||c==="'"){quote=c;continue;}
   if(c==='}')return {start,end:i};
  }
  return null;
 }
 function attributeToken(line,range,key) {
  let i=range.start+1;
  while(i<range.end){
   while(i<range.end&&/\s/.test(line[i]))i++;
   const start=i;while(i<range.end&&!/[\s=]/.test(line[i]))i++;
   const keyEnd=i,name=line.slice(start,keyEnd);while(i<range.end&&/\s/.test(line[i]))i++;
   if(line[i]!=='='){if(name===key)return {start,end:keyEnd,keyEnd};i=keyEnd;if(i===start)i++;continue;}
   i++;while(i<range.end&&/\s/.test(line[i]))i++;
   const valueStart=i,quote=line[i]==='"'||line[i]==="'"?line[i]:null;
   if(quote){i++;while(i<range.end){if(line[i]==='\\'){i+=2;continue;}if(line[i++]===quote)break;}}
   else while(i<range.end&&!/\s/.test(line[i]))i++;
   if(name===key)return {start,end:i,keyEnd,valueStart,valueEnd:i,quote};
  }
  return null;
 }
 function attributeValue(value,quote) {
  if(!quote&&/[\s"']/.test(value))quote='"';
  if(!quote)return value;
  return quote+value.replace(/\\/g,'\\\\').split(quote).join('\\'+quote)+quote;
 }
 function setAttribute(block,key,value,values){
  if(lastRenderSource!==api.getValue()){notice(tr('Updating'));return;}
  if(values?.length&&value&&!values.includes(value)){notice(tr('InvalidAttribute'));return;}
  if(/[\r\n{}]/.test(value)){notice(tr('InvalidAttribute'));return;}
  const lines=api.getValue().split('\n'),line=lines[block.start-1],opening=/^\s*:{2,3}\s*[\w-]+/.exec(line);
  if(!opening){notice(tr('SelectBlock'));return;}
  const range=openingAttributeRange(line,opening[0].length),token=range&&attributeToken(line,range,key);let updated=line;
  if(token){
   if(!value)updated=line.slice(0,token.start)+line.slice(token.end);
   else if(token.valueStart!==undefined)updated=line.slice(0,token.valueStart)+attributeValue(value,token.quote)+line.slice(token.valueEnd);
   else updated=line.slice(0,token.keyEnd)+'='+attributeValue(value)+line.slice(token.keyEnd);
  }else if(value){
   if(range){const at=range.end,prefix=at>range.start+1&&!/\s/.test(line[at-1])?' ':'';updated=line.slice(0,at)+prefix+key+'='+attributeValue(value)+line.slice(at);}
   else {const at=opening[0].length;updated=line.slice(0,at)+' {'+key+'='+attributeValue(value)+'}'+line.slice(at);}
  }
  lines[block.start-1]=updated;api.setValue(lines.join('\n'));render();
 }

 function focusMode(on){root.classList.toggle('focus-mode',on);root.dataset.testid=on?'focus-mode':'editor';$('focusTools').hidden=!on;document.body.classList.toggle('editor-focused',on);if(on){root.dataset.previousView=root.dataset.view;setView('source');root.classList.toggle('focus-dark',read('dark',false));$('editorCheatsheet').hidden=!read('cheatsheet',false);wake();}else{root.classList.remove('idle');$('editorCheatsheet').hidden=true;setView(root.dataset.previousView||'split');}}
 function wake(){root.classList.remove('idle');clearTimeout(idleTimer);if(root.classList.contains('focus-mode'))idleTimer=setTimeout(()=>root.classList.add('idle'),3000);}root.addEventListener('mousemove',wake);root.addEventListener('keydown',wake);root.addEventListener('focusin',wake);
 function dark(){if(root.classList.contains('focus-mode')){const on=!root.classList.contains('focus-dark');root.classList.toggle('focus-dark',on);store('dark',on);}else root.classList.toggle('editor-dark');themeUpdate();}
 function themeUpdate(){const dark=root.classList.contains('focus-mode')?root.classList.contains('focus-dark'):root.classList.contains('editor-dark')||document.documentElement.dataset.theme==='dark'||matchMedia('(prefers-color-scheme:dark)').matches;view.dispatch({effects:theme.reconfigure(C.EditorView.theme({'&':{color:'var(--ink)',backgroundColor:'transparent'},'.cm-gutters':{backgroundColor:'transparent',color:'var(--muted)',border:'none'},'.cm-cursor':{borderLeftColor:'var(--ink)'},'.cm-activeLine':{backgroundColor:'color-mix(in srgb,var(--ink) 3%,transparent)'},'.cm-activeLineGutter':{backgroundColor:'transparent'}},{dark}))});}
 const settings=form.querySelector('.props'),closeSettings=document.createElement('button');closeSettings.type='button';closeSettings.className='editor-close-settings';closeSettings.textContent=tr('Close');closeSettings.onclick=()=>settings.classList.remove('settings-open');settings.prepend(closeSettings);
 const actions={save:()=>api.save(),outline:()=>root.classList.toggle('outline-hidden'),full:()=>{root.classList.toggle('editor-full');root.querySelector('[data-action=full]').textContent=root.classList.contains('editor-full')?tr('Exit')+' '+tr('Full'):tr('Full');document.body.classList.toggle('editor-is-full',root.classList.contains('editor-full'));},focus:()=>{focusMode(!root.classList.contains('focus-mode'));themeUpdate();},export:()=>{$('editorExport').showModal();},settings:()=>{const props=form.querySelector('.props');props.classList.toggle('settings-open');props.scrollIntoView({block:'nearest'});},shortcuts:()=>$('editorShortcuts').showModal(),close:e=>e.target.closest('dialog')?.close(),typewriter:()=>{root.classList.toggle('typewriter');root.querySelector('[data-action="typewriter"]').setAttribute('aria-pressed',root.classList.contains('typewriter'));},dark,cheatsheet:()=>{const on=$('editorCheatsheet').hidden;$('editorCheatsheet').hidden=!on;store('cheatsheet',on);},desktop:()=>root.classList.remove('mobile-preview'),mobile:()=>root.classList.add('mobile-preview')};root.querySelectorAll('[data-action]').forEach(b=>b.onclick=e=>actions[b.dataset.action]?.(e));
 root.querySelectorAll('[data-formatting]').forEach(b=>b.onclick=()=>{const key=b.dataset.formatting;if(key==='bold')wrap('**','**');else if(key==='italic')wrap('*','*');else if(key==='link')wrap('[','](https://)');else if(key==='code')wrap('`','`');else if(key==='insert'){const sel=view.state.selection.main;view.dispatch({changes:{from:sel.from,to:sel.to,insert:'\n/'},selection:{anchor:sel.from+2}});view.focus();}else{const sel=view.state.selection.main,line=view.state.doc.lineAt(sel.from),prefix={heading:'## ',quote:'> ',task:'- [ ] '}[key];view.dispatch({changes:{from:line.from,insert:prefix}});view.focus();}});
 function wrap(before,after){const sel=view.state.selection.main;view.dispatch({changes:{from:sel.from,to:sel.to,insert:before+view.state.sliceDoc(sel.from,sel.to)+after},selection:{anchor:sel.from+before.length,head:sel.to+before.length}});view.focus();}
 document.addEventListener('keydown',e=>{if(e.key==='Escape'){settings.classList.remove('settings-open');if(root.classList.contains('focus-mode'))focusMode(false);else if(root.classList.contains('editor-full'))actions.full();return;}if(!(e.metaKey||e.ctrlKey))return;const key=e.key.toLowerCase();if(key==='s'){e.preventDefault();api.save();}else if(key==='e'){e.preventDefault();setView(root.dataset.view==='preview'?'source':'preview');}else if(key==='l'&&e.shiftKey){e.preventDefault();dark();}else if(key==='/'){e.preventDefault();actions.shortcuts();}else if(view.hasFocus&&['b','i','k'].includes(key)){e.preventDefault();key==='b'?wrap('**','**'):key==='i'?wrap('*','*'):wrap('[','](https://)');}},true);
 $('dailyGoal').value=read('goal',1000);$('dailyGoal').onchange=()=>{const goal=Math.max(1,Math.min(100000,Number($('dailyGoal').value)||1000));$('dailyGoal').value=goal;store('goal',goal);cursor();};
 root.querySelectorAll('[data-format]').forEach(b=>b.onclick=()=>{exportFormat=b.dataset.format;root.querySelectorAll('[data-format]').forEach(x=>x.setAttribute('aria-pressed',x===b));$('exportDownload').textContent=exportFormat==='pdf'?tr('PrintPDF'):tr('Download');});
 $('exportDownload').onclick=async()=>{const button=$('exportDownload');if(button.disabled)return;button.disabled=true;$('exportMessage').textContent='';try{if(!await api.save()){$('exportMessage').textContent=tr('ExportSaveFirst');return;}const format=exportFormat==='zip'?'html':exportFormat,query=new URLSearchParams({format});if(exportFormat==='zip')query.set('images','1');if($('exportFrontmatter').checked)query.set('frontmatter','1');let response=await fetch(`/api/admin/posts/${postId}/export?${query}`);if(format==='pdf'&&response.status===501){response=await fetch(`/api/admin/posts/${postId}/export?format=html`);if(!response.ok)throw Error();const html=await response.text();await printHTML(html);return;}if(!response.ok)throw Error();const blob=await response.blob();if(format==='pdf'&&!blob.type.includes('pdf'))throw Error();const url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;const disposition=response.headers.get('Content-Disposition')||'';link.download=(disposition.match(/filename="([^"]+)"/)||[])[1]||`${title.value||'document'}.${exportFormat==='jmd'?'jmd':exportFormat}`;link.click();setTimeout(()=>URL.revokeObjectURL(url),30000);}catch(_){$('exportMessage').textContent=tr('ExportFailed');}finally{button.disabled=false;}};
 async function printHTML(html){
  const old=$('editorPrintDocument');if(old)old.remove();
  const documentHTML=new DOMParser().parseFromString(html,'text/html'),section=document.createElement('section');section.id='editorPrintDocument';section.className='editor-print-document';
  const style=document.createElement('style');style.media='print';style.textContent=Array.from(documentHTML.querySelectorAll('style')).map(s=>s.textContent).join('\n')+'\n@page{margin:18mm}body{background:white!important;color:black!important}pre,figure,table{break-inside:avoid}';section.append(style);
  const body=document.createElement('div');body.innerHTML=documentHTML.body.innerHTML;body.querySelectorAll('script').forEach(s=>s.remove());section.append(body);document.body.append(section);window.JellyComponents?.init(body);
  if(body.querySelector('pre > code.language-mermaid')){await ensureMermaid();await window.JellyMermaid.render(body);}
  await document.fonts.ready;await Promise.all(Array.from(section.querySelectorAll('img')).map(img=>img.complete?Promise.resolve():new Promise(resolve=>{img.onload=img.onerror=resolve;})));
  await new Promise(resolve=>requestAnimationFrame(resolve));window.print();
 }

 themeUpdate();matchMedia('(prefers-color-scheme:dark)').addEventListener('change',themeUpdate);new MutationObserver(themeUpdate).observe(document.documentElement,{attributes:true,attributeFilter:['data-theme','data-ui-theme']});cursor();setView(matchMedia('(max-width:700px)').matches?'source':'split');if(!postId){const local=read('draft.0',null);if(local){$('editorRestore').showModal();$('restoreAutosave').onclick=()=>{title.value=local.title||'';excerpt.value=local.excerpt||'';api.setValue(local.source);api.restoreDraft?.(local.draft);changed();$('editorRestore').close();};}render();}
})();
