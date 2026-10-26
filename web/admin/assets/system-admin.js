/* System tools, ticket 08. No remote assets or framework runtime. */
(function () {
    'use strict';
    const root = document.getElementById('systemRoot');
    if (!root) return;
    const strings = JSON.parse(document.getElementById('systemI18n').textContent);
    const t = key => strings['sys_' + key] || key;
    const esc = value => String(value == null ? '' : value).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
    const $ = (selector, ctx = root) => ctx.querySelector(selector);
    const all = (selector, ctx = root) => Array.from(ctx.querySelectorAll(selector));
    const text = (value, fallback = '—') => esc(value == null || value === '' ? fallback : value);
    const date = value => value ? new Date(value).toLocaleString(document.documentElement.lang) : t('never');
    const bytes = value => new Intl.NumberFormat(document.documentElement.lang, {maximumFractionDigits:1}).format(Number(value || 0) / 1024) + ' KiB';
    const badge = (value, kind = '') => '<span class="badge ' + (kind ? 'badge-' + kind : '') + '">' + esc(value) + '</span>';
    const button = (label, action = '', id = '', primary = false) => '<button type="button" class="btn ' + (primary ? 'btn-primary' : 'btn-secondary') + ' btn-sm"' + (action ? ' data-action="' + action + '"' : '') + (id ? ' data-id="' + esc(id) + '"' : '') + '>' + esc(t(label)) + '</button>';
    const link = (label, url) => '<a class="btn btn-secondary btn-sm" href="' + esc(url) + '">' + esc(t(label)) + '</a>';
    const empty = () => '<p class="sys-empty">' + esc(t('empty')) + '</p>';
    const code = value => '<pre class="sys-code">' + esc(typeof value === 'string' ? value : JSON.stringify(value, null, 2)) + '</pre>';
    const stat = (label, value) => '<div class="sys-stat"><span>' + esc(t(label)) + '</span><strong>' + text(value) + '</strong></div>';
    let refresh = async () => {};
    let dirty = false;
    let live = null;
    let epoch = 0;
    let downloadURL = null;
    let fieldCounter = 0;
    function status(container, message = '', kind = 'info') {
        let el = container.querySelector(':scope > .sys-status');
        if (!el) { el = document.createElement('div'); el.className = 'sys-status'; container.prepend(el); }
        el.hidden = !message; el.dataset.kind = kind; el.setAttribute('role', kind === 'error' ? 'alert' : 'status'); el.textContent = message;
    }
    function errorText(err) { return err.message || t('failed'); }
    function operationStatus(container, result, completed = 'operationDone') {
        const ids = result ? [result.job_id, result.queue_job_id, ...(result.job_ids || [])].filter(Boolean) : [];
        const queued = result && (result.state === 'queued' || ids.length > 0);
        status(container, result&&result.state==='partial' ? t('jobsPartiallyScheduled') + (ids.length ? ' ' + ids.join(', ') : '') : queued ? t('jobScheduled') + (ids.length ? ' ' + ids.join(', ') : '') : result&&result.state==='rejected' ? t('fetchRejected') : t(completed), result&&['partial','rejected'].includes(result.state) ? 'error' : queued ? 'info' : 'success');
        if (queued) {
            const el = container.querySelector(':scope > .sys-status');
            el.append(document.createTextNode(' '));
            const jobs = document.createElement('a'); jobs.href = '/admin/settings/infra?lang='+encodeURIComponent(document.documentElement.lang)+'#sys-jobs'; jobs.textContent = t('viewJobs'); el.append(jobs);
        }
    }
    async function api(method, url, body, timeout = 30000) {
        const ctl = new AbortController();
        const timer = setTimeout(() => ctl.abort(), timeout);
        const headers = {Accept:'application/json'};
        if (method !== 'GET') headers['X-Jelly-CSRF'] = '1';
        if (body != null && !(body instanceof FormData)) headers['Content-Type'] = 'application/json';
        try {
            const response = await fetch(url, {method, credentials:'same-origin', headers, signal:ctl.signal, body:body == null ? undefined : (body instanceof FormData ? body : JSON.stringify(body))});
            const ct = response.headers.get('content-type') || '';
            const data = response.status === 204 ? null : (ct.includes('json') ? await response.json() : await response.text());
            if (!response.ok) { const err = new Error((data && (data.error || data.message)) || t('failed')); err.status = response.status; err.data = data; throw err; }
            return data;
        } catch (err) {
            if (err.name === 'AbortError') throw new Error(t('timeout'));
            if (err instanceof TypeError) throw new Error(t('offline'));
            throw err;
        } finally { clearTimeout(timer); }
    }
    async function busy(el, fn, container = root) {
        if (el.disabled || el.getAttribute('aria-busy') === 'true') return;
        el.disabled = true; el.setAttribute('aria-busy', 'true');
        try { return await fn(); } catch (err) { status(container, errorText(err), 'error'); }
        finally { el.disabled = false; el.removeAttribute('aria-busy'); }
    }
    function row(spec, value) {
        const id = spec.id || ('sys-' + spec.key + '-' + (++fieldCounter));
        const attrs = ' id="' + esc(id) + '" name="' + esc(spec.key) + '"' + (spec.required ? ' required' : '') + (spec.min != null ? ' min="' + spec.min + '"' : '') + (spec.max != null ? ' max="' + spec.max + '"' : '') + (spec.maxlength ? ' maxlength="' + spec.maxlength + '"' : '');
        value = value == null ? (spec.default == null ? '' : spec.default) : value;
        let input;
        if (spec.type === 'select') input = '<select' + attrs + '>' + spec.options.map(option => { const pair = Array.isArray(option) ? option : [option, option]; return '<option value="' + esc(pair[0]) + '"' + (String(value) === String(pair[0]) ? ' selected' : '') + '>' + esc(t(pair[1])) + '</option>'; }).join('') + '</select>';
        else if (spec.type === 'multiselect') input = '<select multiple' + attrs + '>' + spec.options.map(pair => '<option value="' + esc(pair[0]) + '"' + ((value || []).includes(pair[0]) ? ' selected' : '') + '>' + esc(t(pair[1])) + '</option>').join('') + '</select>';
        else if (spec.type === 'bool') input = '<input type="checkbox"' + attrs + ((value === true || value === 'true') ? ' checked' : '') + '>';
        else if (spec.type === 'textarea' || spec.type === 'json') input = '<textarea' + attrs + ' rows="' + (spec.rows || 4) + '"' + (spec.type === 'json' ? ' class="sys-mono" spellcheck="false"' : '') + '>' + esc(typeof value === 'object' ? JSON.stringify(value, null, 2) : value) + '</textarea>';
        else input = '<input type="' + (spec.type === 'secret' ? 'password' : (spec.type || 'text')) + '"' + attrs + ' value="' + (spec.type === 'file' || spec.type === 'secret' ? '' : esc(value)) + '"' + (spec.type === 'secret' ? ' autocomplete="new-password" placeholder="' + esc(value || t('unset')) + '"' : '') + (spec.accept ? ' accept="' + esc(spec.accept) + '"' : '') + '>';
        if (spec.type === 'secret') input += '<small class="sys-hint">' + esc(t('keepSecret')) + '</small>' + (spec.clear === false ? '' : '<label class="sys-check"><input type="checkbox" data-clear-secret="' + esc(spec.key) + '">' + esc(t('clearSecret')) + '</label>');
        return '<div class="sys-row"><label for="' + esc(id) + '">' + esc(t(spec.label || spec.key)) + (spec.required ? ' *' : '') + (spec.hint ? '<small class="sys-hint">' + esc(t(spec.hint)) + '</small>' : '') + '</label><div class="sys-control">' + input + '</div></div>';
    }
    function fields(specs, values = {}) { return specs.map(spec => row(spec, values[spec.key])).join(''); }
    function values(form, specs, changesOnly = false, original = {}) {
        const result = {};
        for (const spec of specs) {
            const el = form.elements[spec.key];
            let value = spec.type === 'bool' ? el.checked : el.value;
            if (spec.type === 'secret') {
                if (form.querySelector('[data-clear-secret="' + CSS.escape(spec.key) + '"]:checked')) value = '';
                else if (value === '') continue;
            }
            if (spec.type === 'multiselect') value = Array.from(el.selectedOptions, option => option.value);
            if (spec.type === 'number' && value !== '') value = Number(value);
            if (spec.type === 'json') { try { value = JSON.parse(value || '{}'); } catch (_) { el.focus(); throw new Error(t('invalidJSON')); } }
            if (spec.type === 'file') value = el.files[0];
            if (!changesOnly || JSON.stringify(value) !== JSON.stringify(original[spec.key])) result[spec.key] = value;
        }
        return result;
    }
    function card(title, body, hint = '', id = '') { return '<section class="sys-card"' + (id ? ' id="' + id + '"' : '') + '><h2>' + esc(t(title)) + '</h2>' + (hint ? '<p class="sys-subtitle">' + esc(t(hint)) + '</p>' : '') + body + '</section>'; }
    function formCard(title, specs, initial, submit, hint = '', extra = '', label = 'save') {
        const container = document.createElement('section'); container.className = 'sys-card';
        container.innerHTML = '<h2>' + esc(t(title)) + '</h2>' + (hint ? '<p class="sys-subtitle">' + esc(t(hint)) + '</p>' : '') + '<form>' + fields(specs, initial) + '<div class="sys-actions"><button type="submit" class="btn btn-primary btn-sm">' + esc(t(label)) + '</button>' + extra + '</div></form>';
        const form = $('form', container); form.dataset.formKey = title;
        form.addEventListener('input', () => { dirty = true; form.dataset.dirty = 'true'; });
        form.addEventListener('submit', event => {
            event.preventDefault(); if (!form.reportValidity()) return;
            busy($('[type=submit]', form), async () => {
                form.dataset.saving = 'true';
                try { await submit(values(form, specs, false, initial), form, container); delete form.dataset.dirty; }
                finally { delete form.dataset.saving; dirty = !!$('form[data-dirty]'); }
            }, container);
        });
        root.append(container); return {container, form};
    }
    function table(headers, rows, compact=false) { return !rows.length ? empty() : '<div class="sys-table"><table'+(compact?' style="min-width:0;width:100%"':'')+'><thead><tr>' + headers.map(h => '<th scope="col">' + esc(t(h)) + '</th>').join('') + '</tr></thead><tbody>' + rows.map(r => '<tr>' + r.map(c => '<td>' + c + '</td>').join('') + '</tr>').join('') + '</tbody></table></div>'; }
    function toolbar(body = '') { return '<div class="sys-toolbar">' + body + '<div class="sys-actions">' + button('refresh', 'refresh') + '</div></div>'; }
    function dataMap(rows) { if (Array.isArray(rows)) return Object.fromEntries(rows.map(item => [item.key, item.value])); return rows && rows.settings ? rows.settings : rows || {}; }
    async function getOptions() { return dataMap(await api('GET', '/api/admin/settings')); }
    function optionSpec(key, label, type = 'text', extra = {}) { return Object.assign({key, label, type}, extra); }
    async function saveSettings(data) {
        const kv = {}; Object.entries(data).forEach(([k,v]) => { kv[k] = typeof v === 'object' ? JSON.stringify(v) : String(v); });
        await api('PUT', '/api/admin/settings', kv);
    }
    async function settingForm(title, specs, opts, hint = '') {
        const initial = {}; specs.forEach(s => { let v = opts[s.key] == null ? s.default : opts[s.key]; if (s.type === 'bool') v = v === true || v === 'true'; if (s.type === 'number') v = Number(v || s.default || 0); if (s.type === 'json') {try {v = JSON.parse(v || (s.default || '{}'));} catch (_) {v = s.default || {};}} initial[s.key] = v; });
        return formCard(title, specs, initial, async (data, form, container) => {
            const changed = {}; Object.entries(data).forEach(([k,v]) => { if (JSON.stringify(v) !== JSON.stringify(initial[k])) changed[k] = v; });
            if (!Object.keys(changed).length) {status(container, t('noChanges')); return;}
            await saveSettings(changed);
            if(Object.hasOwn(changed,'site_language')){dirty=false;window.location.reload();return;}
            try { await refresh(); status(root, t('saved'), 'success'); } catch (err) {status(container, t('partialSaved') + ' ' + errorText(err), 'error');}
        }, hint);
    }
    async function modal(title, specs = [], initial = {}, hint = '', label = 'confirm', extra = '', validate = null, configure = null) {
        const dialog = document.createElement('dialog'); dialog.className = 'sys-dialog';
        dialog.innerHTML = '<form><h2>' + esc(t(title)) + '</h2>' + (hint ? '<p>' + esc(t(hint)) + '</p>' : '') + extra + fields(specs, initial) + '<div class="sys-actions"><button type="submit" class="btn btn-primary">' + esc(t(label)) + '</button><button type="button" class="btn btn-secondary" data-cancel>' + esc(t('cancel')) + '</button></div></form>';
        document.body.append(dialog); const form = $('form', dialog); const previous = document.activeElement;
        if (configure) configure(form);
        return new Promise(resolve => {
            const finish = data => { dialog.close(); dialog.remove(); if (previous && previous.isConnected) previous.focus(); resolve(data); };
            $('[data-cancel]', dialog).onclick = () => finish(null);
            dialog.addEventListener('cancel', event => {event.preventDefault(); finish(null);});
            form.onsubmit = event => { event.preventDefault(); if (!form.reportValidity()) return; try { const data = values(form, specs); if (validate) validate(data); finish(data); } catch (err) {status(form, errorText(err), 'error');} };
            dialog.showModal();
        });
    }
    const bool = (key,label,def = false) => optionSpec(key,label,'bool',{default:def});
    const num = (key,label,def = 10,min = 1,max = 100) => optionSpec(key,label,'number',{default:def,min,max});
    const select = (key,label,options,def = '') => optionSpec(key,label,'select',{options,default:def});
    async function showSettings(page) {
        const options = await getOptions(); root.innerHTML = toolbar();
        if (page === 'site') {
            await settingForm('site',[optionSpec('site_title','siteTitle','text',{required:true,default:'Jelly CMS'}),optionSpec('site_description','siteDescription','textarea'),select('site_language','language',[['zh-TW','繁體中文'],['en','English']],'zh-TW'),optionSpec('site_timezone','timezone','text',{required:true,default:'UTC'})],options);
        } else if (page === 'routes') {
            await settingForm('routes',[optionSpec('permalink_post','permalink','text',{required:true,default:'/post/{slug}',hint:'permalinkHint'}),optionSpec('permalink_category_prefix','categoryPrefix','text',{default:'/c'}),optionSpec('permalink_tag_prefix','tagPrefix','text',{default:'/t'}),optionSpec('permalink_author_prefix','authorPrefix','text',{default:'/author'}),optionSpec('permalink_archive_prefix','archivePrefix','text',{default:'/archive'}),select('slug_strategy','slugStrategy',[['title','titleSlug'],['timestamp','timestamp'],['random','random']],'title'),select('home_show','homeShow',[['latest','latestPosts'],['page','staticPage']],'latest'),num('home_page_id','homePageID',0,0,Number.MAX_SAFE_INTEGER),num('per_page_home','perPageHome'),num('per_page_taxonomy','perPageTaxonomy',12),num('per_page_author','perPageAuthor',12),num('per_page_search','perPageSearch'),num('excerpt_auto_length','excerptLength',110,0,10000),select('feed_mode','feedMode',[['summary','summary'],['full','full'],['off','off']],'summary'),num('feed_items','feedItems',20)],options);
            const legacy = document.createElement('div'); legacy.innerHTML = card('legacyPatterns',code(JSON.parse(options.permalink_legacy || '[]'))); root.append(legacy);
            await showMaintenance(); await showInjection();
        } else if (page === 'crawlers') await showCrawlers(options);
        else if (page === 'comments') {
            await settingForm('commentModeration', [
                optionSpec('comments_enabled','commentsEnabled','bool',{default:true,hint:'commentsEnabledHint'}),
                optionSpec('comments_registered_only','commentsRegisteredOnly','bool',{default:false,hint:'commentsRegisteredOnlyHint'}),
                optionSpec('comments_require_approval','commentsRequireApproval','bool',{default:true,hint:'commentsRequireApprovalHint'})
            ], options, 'commentHint');
            root.insertAdjacentHTML('beforeend','<div class="sys-actions">' + link('openComments','/admin/comments') + link('openPosts','/admin/posts') + '</div>');
        }
        else if (page === 'mail') await showMail(options);
        else if (page === 'infra') await showInfra();
        else if (page === 'update') await showUpdate();
    }
    async function showMaintenance() {
        const data = await api('GET','/api/admin/maintenance');
        const f = formCard('maintenance',[bool('enabled','enabled'),optionSpec('message','maintenanceMessage','textarea',{maxlength:280}),num('retry_after','retryAfter',600,1,86400)],data,async data => {await api('PUT','/api/admin/maintenance',data);await refresh();status(root,t('saved'),'success');},'maintenanceHint');
        f.container.id = 'maintenance'; if (data.enabled) f.container.insertAdjacentHTML('beforeend','<small class="sys-hint">' + esc(t('elapsedMinutes')) + ': ' + text(data.elapsed_minutes) + '</small>');
    }
    async function showInjection() {
        const data = await api('GET','/api/admin/settings/injection');
        const specs = [optionSpec('head_all','headAll','textarea',{rows:5}),optionSpec('head_content','headContent','textarea',{rows:5}),optionSpec('footer','footer','textarea',{rows:5}),bool('on_commerce','injectCommerce'),optionSpec('current_password','currentPassword','password',{required:true})];
        const f = formCard('injection',specs,data,async (data,form,container) => {await api('PUT','/api/admin/settings/injection',data);form.elements.current_password.value='';await refresh();status(root,t('saved'),'success');},'injectionHint'); f.container.id='injection';
    }
    async function showCrawlers(options) {
        const types = ['home','post','page','category','tag','author','archive','product','search','commerce','account'];
        const typeNames = {home:'homeShow',post:'posts',page:'pages',category:'category',tag:'tags',author:'author',archive:'archive',product:'products',search:'search',commerce:'commerce',account:'account'};
        await settingForm('crawlers',[bool('seo_index_site','indexSite',true),bool('seo_images_index','imagesIndex',true),select('seo_snippet_mode','snippetMode',[['default','default'],['short','short'],['noarchive','noArchive']],'default'),optionSpec('seo_title_format','titleFormat','text',{default:'{title} - {site}'}),optionSpec('seo_default_og_image','ogImage','url'),bool('seo_indexnow_enabled','indexNow'),optionSpec('seo_indexnow_key','indexNowKey','secret'),select('ai_policy_preset','aiPreset',[['block_training_allow_search','blockTraining'],['block_all','blockAll'],['allow_all','allowAll']],'block_training_allow_search'),optionSpec('webmcp_enabled','webmcpEnabled','bool',{id:'webmcpEnabled',default:true,hint:'webmcpEnabledHint'}),bool('ai_block_server_side','serverBlock'),bool('tdm_reservation','tdm'),optionSpec('tdm_policy_url','tdmPolicyURL','url'),bool('llms_txt_enabled','llmsEnabled'),optionSpec('llms_txt_intro','llmsIntro','textarea')],options);
        const currentTypes = JSON.parse(options.seo_index_types || '{}'); const typeValues = {}; types.forEach(key=>{typeValues[key]=currentTypes[key] !== false;});
        formCard('indexTypes',types.map(key => bool(key,typeNames[key])),typeValues,async data=>{await saveSettings({seo_index_types:Object.assign({},currentTypes,data)});await refresh();status(root,t('saved'),'success');});
        const bots = ['GPTBot','OAI-SearchBot','ChatGPT-User','ClaudeBot','Claude-SearchBot','Claude-User','Google-Extended','Applebot-Extended','CCBot','Bytespider','PerplexityBot','Perplexity-User'];
        const currentBots = JSON.parse(options.ai_bots || '{}');
        formCard('bots',bots.map(bot=>select(bot,bot,[['','inherit'],['allow','allow'],['block','block']])),currentBots,async data=>{const next=Object.assign({},currentBots);Object.entries(data).forEach(([k,v])=>{if(v)next[k]=v;else delete next[k];});await saveSettings({ai_bots:next});await refresh();status(root,t('saved'),'success');});
        const rules = JSON.parse(options.ai_custom_rules || '[]');
        const ruleSection = document.createElement('section'); ruleSection.className='sys-card';
        ruleSection.innerHTML='<h2>'+esc(t('customRules'))+'</h2>'+table(['userAgent','actions','path'],rules.map((rule,i)=>[text(rule.user_agent),badge(rule.action),text(rule.path)+button('delete','remove-ai-rule',i)]))+'<div>'+button('add','add-ai-rule')+'</div>';
        ruleSection.addEventListener('click',event=>{const b=event.target.closest('[data-action]');if(!b)return;event.stopPropagation();busy(b,async()=>{if(b.dataset.action==='add-ai-rule'){const data=await modal('customRules',[optionSpec('user_agent','userAgent','text',{required:true}),select('action','actions',[['allow','allow'],['disallow','block']],'disallow'),optionSpec('path','path','text',{required:true,default:'/'})]);if(!data)return;rules.push(data);}else rules.splice(Number(b.dataset.id),1);await saveSettings({ai_custom_rules:rules});await refresh();status(root,t('saved'),'success');},ruleSection);});
        root.append(ruleSection);
        const [robots,stats,overrides] = await Promise.all([api('GET','/api/admin/seo/robots-preview'),api('GET','/api/admin/seo/crawler-stats?days=7'),api('GET','/api/admin/seo/overrides')]);
        root.insertAdjacentHTML('beforeend',card('robotsPreview',code(robots.robots))+card('crawlerStats',table(['bot','actions','count'],(stats.items || stats || []).map(item=>[text(item.bot),text(item.action),text(item.count)])))+card('overrides',table(['kind','name','status'],(overrides.items || overrides || []).map(item=>[text(item.kind),text(item.title),text(item.seo_index)+' / '+text(item.seo_ai)]))));
    }
    async function showMail(options) {
        const data=dataMap(await api('GET','/api/admin/mail/settings'));
        const current=Object.assign({},options,data);
        const specs=[bool('mail_enabled','mailEnabled'),optionSpec('mail_host','mailHost'),num('mail_port','mailPort',587,1,65535),optionSpec('mail_user','mailUser'),optionSpec('mail_password_enc','mailPassword','secret'),optionSpec('mail_from','mailFrom','email'),select('mail_tls','mailTLS',[['starttls','STARTTLS'],['tls','TLS'],['none','off']],'starttls')];
        const initial={};specs.forEach(s=>{initial[s.key]=current[s.key]==null?s.default:current[s.key];if(s.type==='bool')initial[s.key]=initial[s.key]===true||initial[s.key]==='true';if(s.type==='number')initial[s.key]=Number(initial[s.key]||s.default);});
        formCard('mail',specs,initial,async data=>{await api('PUT','/api/admin/mail/settings',data);await refresh();status(root,t('saved'),'success');});
        formCard('sendTest',[optionSpec('to','testRecipient','email',{required:true})],{},async(data,form,container)=>{const result=await api('POST','/api/admin/mail/test',data);operationStatus(container,result);},'', '', 'sendTest');
        root.insertAdjacentHTML('beforeend','<div class="sys-actions">'+link('notifyLink','/admin/notifications')+link('notificationPreferences','/admin/account?tab=notifications')+'</div>');
    }
    async function showInfra() {
        const data=await api('GET','/api/admin/infra');const cache=data.cache||{},queue=data.queue||{},stats=cache.stats||{};
        root.insertAdjacentHTML('beforeend','<div class="sys-stats">'+stat('entries',stats.entries)+stat('hits',stats.hits)+stat('misses',stats.misses)+stat('workers',queue.workers)+'</div>');
        const cacheSpecs=[select('driver','driver',[['memory','Memory'],['redis','Redis / Valkey']],cache.driver),optionSpec('url','connectionURL','secret',{hint:cache.url_source==='env'?'readOnly':'keepSecret'}),num('ttl_page','ttlPage',300,1,86400),bool('object_enabled','objectCache',true)];
        const cf=formCard('cache',cacheSpecs,cache,async(data,form,container)=>{await api('PUT','/api/admin/infra/cache',data);await refresh();status(root,t('saved'),'success');},'',button('testConnection','test-cache')+button('flush','flush-cache'));
        cf.container.insertAdjacentHTML('beforeend','<p class="sys-subtitle">'+esc(t(cache.connected?'connected':'disconnected'))+' · '+text(cache.latency_ms)+' ms'+(cache.url_source==='env'?' · '+esc(t('readOnly')):'')+'</p>');
        const qf=formCard('queue',[optionSpec('url','connectionURL','secret',{hint:queue.url_source==='env'?'readOnly':'keepSecret'}),num('workers','workers',queue.workers||2,1,64)],queue,async data=>{await api('PUT','/api/admin/infra/queue',data);await refresh();status(root,t('saved'),'success');},'',button('testConnection','test-queue')+button('switchBackend','switch-queue')+button(queue.paused?'resume':'pause',queue.paused?'resume-queue':'pause-queue'));
        qf.container.insertAdjacentHTML('afterbegin','<p>'+badge(queue.driver)+' '+badge(t(queue.paused?'paused':queue.connected?'connected':'disconnected'),queue.connected?'success':'warning')+'</p>');
        root.insertAdjacentHTML('beforeend',table(['queue','queued','running','failedState','done'],(queue.queues||[]).map(item=>[text(item.queue),text(item.queued),text(item.running),text(item.failed),text(item.done)])));
        const jobSection=document.createElement('section');jobSection.className='sys-card';jobSection.id='sys-jobs';
        jobSection.innerHTML='<h2>'+esc(t('job'))+'</h2><form class="sys-toolbar" id="sys-job-filter">'+fields([select('queue','queue',[['','all'],...['mail','media','webhook','import','schedule']]),select('state','status',[['','all'],['queued','queued'],['running','running'],['failed','failedState'],['done','done']])])+'<button class="btn btn-secondary btn-sm">'+esc(t('filter'))+'</button>'+button('retryFailed','retry-jobs')+'</form><div id="sys-job-list"></div>';
        root.append(jobSection);
        const loadJobs=async()=>{const form=$('form',jobSection);const q=new URLSearchParams(new FormData(form));const jobs=await api('GET','/api/admin/jobs?'+q);$('#sys-job-list',jobSection).innerHTML=table(['jobID','queue','kind','status','attempts','lastError','actions'],(jobs.items||[]).map(item=>['<input readonly aria-label="'+esc(t('jobID'))+'" title="'+esc(item.id)+'" value="'+esc(item.id)+'" style="width:10ch;max-width:10ch;font:inherit">',text(item.queue),text(item.kind),badge(t(item.state)),text(item.attempts)+' / '+text(item.max_attempts),text(item.last_error),'<div class="sys-actions">'+(item.state==='failed'?button('retry','retry-job',item.id):'')+(item.state!=='running'?button('delete','delete-job',item.id):'')+'</div>']));};
        $('form',jobSection).onsubmit=event=>{event.preventDefault();busy($('[type=submit]',jobSection)||$('form button',jobSection),loadJobs,jobSection);};
        jobSection.addEventListener('click',event=>{const b=event.target.closest('[data-action]');if(!b)return;event.stopPropagation();busy(b,async()=>{
            let result;
            if(b.dataset.action==='retry-jobs')result=await api('POST','/api/admin/jobs/retry-failed?queue='+encodeURIComponent($('[name=queue]',jobSection).value),{});
            else if(b.dataset.action==='retry-job'){result=await api('POST','/api/admin/jobs/'+encodeURIComponent(b.dataset.id)+'/retry',{});result.job_id=result.id;}
            else if(b.dataset.action==='delete-job'){if(!await modal('confirmDelete'))return;await api('DELETE','/api/admin/jobs/'+encodeURIComponent(b.dataset.id));}
            await loadJobs();
            if(result&&result.retried!=null)status(jobSection,t('jobsRetried')+' '+result.retried);
            else operationStatus(jobSection,result);
        },jobSection);});
        await loadJobs();
        if(window.location.hash==='#sys-jobs')jobSection.scrollIntoView({block:'start'});
        root.dataset.queueDriver=queue.driver;
    }
    async function showUpdate() {
        const data=await api('GET','/api/admin/update');root.dataset.latest=data.latest||'';
        root.insertAdjacentHTML('beforeend','<div class="sys-stats">'+stat('current',data.current)+stat('latest',data.latest||t('noRelease'))+stat('runtime',data.runtime.kind)+stat('checkedAt',data.checked_at?date(data.checked_at):t('never'))+'</div>');
        const state=data.state||{};
        let body='<div class="sys-actions">'+button('checkUpdate','check-update')+(data.can_update&&data.update_available?button('applyUpdate','apply-update','',true):'')+(data.can_update&&state.previous?button('rollback','rollback-update'):'')+(['applying','checking','downloading'].includes(state.status)?button('cancelUpdate','cancel-update'):'')+'</div><p>'+badge(t(state.status||'status'))+'</p>';
        if(state.last_error)body+='<p class="sys-status" data-kind="error">'+esc(state.last_error)+'</p>';
        if(!data.can_update&&data.commands&&data.commands.length)body+='<p class="sys-subtitle">'+esc(t('runtimeHint'))+'</p>'+data.commands.map(command=>code(command)).join('')+button('copy','copy-commands');
        root.insertAdjacentHTML('beforeend',card('update',body));
        formCard('settings',[select('channel','channel',[['stable','stable'],['preview','preview']],'stable'),bool('auto_check','autoCheck',true),select('on_new','onNew',[['notify','notify'],['off','off']],'notify'),optionSpec('auto_time','autoTime','time',{default:'03:00'}),num('keep_versions','keepVersions',2,1,10)],data,async data=>{await api('PUT','/api/admin/update/settings',data);await refresh();status(root,t('saved'),'success');});
        root.dataset.commands=JSON.stringify(data.commands||[]);
    }
    async function showBackup() {
        const [list,options]=await Promise.all([api('GET','/api/admin/backups'),getOptions()]);
        root.innerHTML=toolbar('<div class="sys-actions">'+link('import','/admin/tools/import')+'</div>');
        const spec=[bool('db','database',true),bool('attachments','attachments',true),bool('settings','settings',true),optionSpec('note','note','text',{maxlength:280}),optionSpec('passphrase','passphrase','password',{hint:'passphraseHint'})];
        formCard('backupNow',spec,{db:true,attachments:true,settings:true},async(data,form,container)=>{const body={content:['db','attachments','settings'].filter(k=>data[k]),note:data.note,passphrase:data.passphrase};if(!body.content.length)throw new Error(t('required'));const result=await api('POST','/api/admin/backups',body,600000);form.elements.passphrase.value='';await refresh();operationStatus(root,result);},'','', 'backupNow');
        root.insertAdjacentHTML('beforeend',card('backup',table(['date','kind','contents','status','size','actions'],(list.items||[]).map(item=>[text(date(item.created_at)),text(item.kind),text((item.contents||[]).map(k=>t(k==='db'?'database':k)).join(', ')),badge(t(item.state),item.state==='done'?'success':item.state==='failed'?'danger':''),text(bytes(item.size_bytes)),'<div class="sys-actions">'+(item.state==='done'?link('download','/api/admin/backups/'+item.id+'/download')+button('restore','restore-backup',item.id):item.state==='failed'?button('retry','retry-backup',item.id):'')+(['done','failed'].includes(item.state)?button('delete','delete-backup',item.id):'')+'</div>'+(item.last_error?'<small class="sys-hint">'+esc(item.last_error)+'</small>':'')]))));
        formCard('schedule',[bool('enabled','enabled'),optionSpec('time','scheduleTime','time',{required:true,default:'03:00'}),num('keep_daily','keepDaily',14,1,365)],{enabled:options.backup_enabled==='true',time:options.backup_time||'03:00',keep_daily:Number(options.backup_keep_daily||14)},async data=>{await api('PUT','/api/admin/backups/schedule',data);await refresh();status(root,t('saved'),'success');});
        const policies=await api('GET','/api/admin/media/policies');
        await settingForm('retention',[num('backup_keep_weekly_months','keepWeeklyMonths',3,0,120),num('backup_local_keep','localKeep',3,0,365),select('backup_policy_id','backupDestination',[['0','localDefault'],...(policies.items||policies||[]).filter(policy=>policy.purpose==='backup').map(policy=>[String(policy.id),policy.name])],'0')],options);
        formCard('export',[select('format','exportFormat',[['markdown_zip','formatMarkdown'],['wxr','formatWXR'],['json','formatJSON']],'markdown_zip')],{format:'markdown_zip'},async(data,form,container)=>{const result=await api('POST','/api/admin/export',data,600000);status(container,t('operationDone'),'success');let output=$('[data-export-download]',container);if(!output){output=document.createElement('div');output.dataset.exportDownload='';container.append(output);}output.innerHTML=link('download','/api/admin/exports/'+encodeURIComponent(result.id)+'/download');},'','','export');
    }
    let selectedImport=null, importPoll=null, importTimer=null, pageActive=true;
    function stopImportTimer() { clearTimeout(importTimer); importTimer=null; }
    function scheduleImportPoll(id) {
        stopImportTimer();
        if(!pageActive)return;
        if(!importPoll||String(importPoll.id)!==String(id))importPoll={id,deadline:Date.now()+120000};
        if(Date.now()>=importPoll.deadline){status($('#sys-import-detail')||root,t('importPollStopped'));return;}
        const token=epoch;
        importTimer=setTimeout(()=>{
            importTimer=null;
            if(!pageActive||token!==epoch||root.dataset.page!=='import'||String(selectedImport)!==String(id))return;
            if(dirty){scheduleImportPoll(id);return;}
            refresh(true).catch(()=>{if(pageActive&&token<epoch&&root.dataset.page==='import'&&String(selectedImport)===String(id))scheduleImportPoll(id);});
        },1500);
    }
    async function showImport(polling=false) {
        const token=epoch;const list=await api('GET','/api/admin/import');if(!pageActive||token!==epoch)return;if(polling&&dirty){scheduleImportPoll(selectedImport);return;}root.innerHTML=toolbar('<div class="sys-actions">'+link('backup','/admin/tools/backup')+'</div>');
        const f=formCard('import',[optionSpec('file','uploadWXR','file',{required:true,accept:'.xml,.xml.gz,.zip'})],{},async(data,form,container)=>{if(data.file.size>512*1024*1024)throw new Error(t('wxrHint'));const body=new FormData();body.append('file',data.file);const job=await api('POST','/api/admin/import/wxr',body,600000);selectedImport=job.id;await refresh();},'wxrHint','','upload');
        root.insertAdjacentHTML('beforeend',table(['name','status','date','actions'],(list.items||[]).map(item=>[text(item.source_name),badge(t(importPoll&&String(importPoll.id)===String(item.id)&&!['running','done','failed','cancelled'].includes(item.state)?'queued':item.state)),text(date(item.created_at)),button('details','select-import',item.id)])));
        if(!selectedImport&&list.items&&list.items.length)selectedImport=list.items[0].id;
        if(selectedImport)await showImportDetail(selectedImport);
    }
    async function showImportDetail(id) {
        const token=epoch;const job=await api('GET','/api/admin/import/'+encodeURIComponent(id));
        if(!pageActive||token!==epoch||String(selectedImport)!==String(id))return;
        const terminal=['done','failed','cancelled'].includes(job.state);
        if(terminal)importPoll=null;
        const running=job.state==='queued'||job.state==='running'||(importPoll&&String(importPoll.id)===String(id));
        const section=document.createElement('section');section.className='sys-card';section.id='sys-import-detail';
        section.innerHTML='<h2>'+text(job.source_name)+'</h2><div class="sys-steps">'+['uploaded','mapped','dry_run','done'].map(st=>'<span'+(job.state===st?' class="active"':'')+'>'+esc(t(st))+'</span>').join('')+'</div><div class="sys-stats">'+stat('posts',job.posts)+stat('pages',job.pages)+stat('attachments',job.attachments)+stat('comments',job.comments)+'</div>';
        if(job.last_error)section.insertAdjacentHTML('beforeend','<div class="sys-status" data-kind="error">'+esc(job.last_error)+'</div>');
        root.append(section);
        if(!running&&!['done','cancelled'].includes(job.state)){
            const specs=[bool('post','posts',true),bool('page','pages',true),bool('attachment','attachments',true),bool('comment','comments',true),bool('fetch_attachments','fetchAttachments',true)];
            const initial={fetch_attachments:job.mapping.fetch_attachments};['post','page','attachment','comment'].forEach(k=>{initial[k]=(job.mapping.types||[]).includes(k);});
            (job.author_list||[]).forEach((author,i)=>{specs.push(select('author_'+i,author.display_name||author.login,[['author','author'],['editor','editor'],['subscriber','subscriber']],'author'));initial['author_'+i]=(job.mapping.author_roles||{})[author.login]||'author';});
            const f=formCard('authorMapping',specs,initial,async data=>{const body={types:['post','page','attachment','comment'].filter(k=>data[k]),fetch_attachments:data.fetch_attachments,author_roles:{}};(job.author_list||[]).forEach((author,i)=>{body.author_roles[author.login]=data['author_'+i];});await api('PUT','/api/admin/import/'+id+'/mapping',body);await refresh();status(root,t('saved'),'success');});section.append(f.container);
        }
        section.insertAdjacentHTML('beforeend','<div class="sys-actions">'+(!running&&!['done','cancelled'].includes(job.state)?button('dryRun','dry-import',id):'')+(!running&&job.state==='dry_run'?button('startImport','run-import',id,true):'')+(!['done','cancelled'].includes(job.state)?button('cancelImport','cancel-import',id):'')+'</div>');
        if(!running&&job.state==='dry_run')section.insertAdjacentHTML('beforeend','<p class="sys-subtitle">'+esc(t('readyToImport'))+'</p>');
        ['conflicts','blocked','failed'].forEach(kind=>{if((job[kind]||[]).length)section.insertAdjacentHTML('beforeend',card(kind==='failed'?'failedState':kind,table(['kind','name','description'],job[kind].map(item=>[text(item.kind),text(item.item),text(item.detail)]))));});
        if(job.state==='done'){
            const types=[['posts','posts'],['pages','pages'],['attachments','attachments'],['comments','comments'],['authors','authors']];
            section.insertAdjacentHTML('beforeend',card('importReport',table(['kind','created'],types.map(([key,label])=>[esc(t(label)),text((job.created||{})[key]||0)]),true)));
            const reasons={already:'skipAlready',filtered:'skipFiltered',spam_or_unapproved_type:'skipCommentType',orphan:'skipOrphan',attachment_not_fetched:'skipAttachmentDisabled',attachment_no_url:'skipAttachmentURL'};
            section.insertAdjacentHTML('beforeend',card('skipped',table(['reason','count'],Object.entries(job.skipped||{}).map(([key,count])=>[text(reasons[key]?t(reasons[key]):key),text(count)]),true)));
        }
        if(running){status(section,t('importInProgress'));scheduleImportPoll(id);}
    }
    let logRows=[],logBefore=null,logFilters={source:'',level:'',q:'',since:''};
    async function showLogs() {
        root.innerHTML=toolbar('<div class="sys-actions"><label class="sys-check"><input type="checkbox" id="sys-live">'+esc(t('live'))+'</label>'+link('download24h','/api/admin/logs/export?hours=24')+'</div>')+'<div class="sys-stats" id="sys-log-stats"></div><form id="sys-log-filter" class="sys-toolbar"><div class="sys-field"><label for="sys-log-source">'+esc(t('source'))+'</label><select id="sys-log-source" name="source">'+[['','all'],...['app','http','payment','plugin','audit','cron','job','crawler']].map(v=>{const a=Array.isArray(v)?v:[v,v];return '<option value="'+a[0]+'">'+esc(t(a[1]))+'</option>';}).join('')+'</select></div><div class="sys-field"><label for="sys-log-level">'+esc(t('level'))+'</label><select id="sys-log-level" name="level"><option value="">'+esc(t('all'))+'</option>'+['debug','info','warn','error'].map((k,i)=>'<option value="'+i+'">'+esc(t(k))+'</option>').join('')+'</select></div><div class="sys-field"><label for="sys-log-q">'+esc(t('search'))+'</label><input type="search" id="sys-log-q" name="q"></div><div class="sys-field"><label for="sys-log-since">'+esc(t('since'))+'</label><input type="datetime-local" id="sys-log-since" name="since"></div><button class="btn btn-secondary btn-sm" type="submit">'+esc(t('filter'))+'</button></form><div id="sys-log-rows"></div><div>'+button('more','more-logs')+'</div>';
        const form=$('#sys-log-filter');Object.entries(logFilters).forEach(([k,v])=>{if(form.elements[k])form.elements[k].value=v;});
        form.onsubmit=event=>{event.preventDefault();logFilters=Object.fromEntries(new FormData(form));busy($('[type=submit]',form),async()=>{await loadLogs(false);if($('#sys-live').checked)startLive();});};
        $('#sys-live').onchange=()=>{if($('#sys-live').checked)startLive();else stopLive();};
        await loadLogs(false);
        const summary=await api('GET','/api/admin/logs/summary');await settingForm('retention',[num('logs_retention_days','retentionDays',30,1,3650),num('logs_error_retention_days','errorRetentionDays',90,1,3650),select('logs_http_level','httpLogLevel',[['debug','debug'],['info','info'],['warn','warn'],['error','error']],'error')],await getOptions());$('#sys-log-stats').innerHTML=stat('last24h',summary.total)+stat('error',summary.by_level.error)+stat('warn',summary.by_level.warn)+stat('info',summary.by_level.info);
    }
    function logQuery() {const data=Object.assign({},logFilters);if(data.since)data.since=new Date(data.since).toISOString();Object.keys(data).forEach(k=>{if(!data[k])delete data[k];});return new URLSearchParams(data);}
    async function loadLogs(more) {
        const q=logQuery();if(more&&logBefore)q.set('before_id',logBefore);
        const data=await api('GET','/api/admin/logs?'+q);logRows=more?logRows.concat(data.items||[]):data.items||[];logBefore=data.next_before_id;renderLogs();
        const b=$('[data-action=more-logs]');if(b)b.hidden=!logBefore;
    }
    function renderLogs() {
        const list=$('#sys-log-rows');if(!list)return;
        list.innerHTML=table(['date','level','source','description','requestID'],logRows.slice(0,1000).map(item=>[text(date(item.at)),badge(t(['debug','info','warn','error'][item.level]),['','info','warning','danger'][item.level]),text(item.source),'<span class="sys-mono">'+text(item.message)+'</span>'+((item.fields&&Object.keys(item.fields).length)?'<details class="sys-detail"><summary>'+esc(t('details'))+'</summary>'+code(item.fields)+'</details>':''),item.request_id?'<button type="button" class="btn btn-text btn-sm sys-mono" data-action="filter-request" data-id="'+esc(item.request_id)+'">'+esc(item.request_id)+'</button>':'—']));
    }
    function stopLive(){if(live)live.close();live=null;}
    function startLive(){stopLive();live=new EventSource('/api/admin/logs/stream?'+logQuery());live.addEventListener('log',event=>{try{const item=JSON.parse(event.data);if(!logRows.some(row=>row.id===item.id)){logRows.unshift(item);logRows=logRows.slice(0,1000);renderLogs();}}catch(_){status(root,t('failed'),'error');}});live.onerror=()=>status(root,t('liveDisconnected'),'error');live.onopen=()=>status(root,'');}
    let redirectPage=1,redirectFilters={kind:'',source:'',q:''},redirects=[],redirectTab='rules';
    async function showRedirects() {
        root.innerHTML=toolbar('<div class="sys-actions">'+button('add','new-redirect','',true)+button('importCSV','import-redirects')+link('exportCSV','/api/admin/redirects/export.csv')+'</div>')+'<div class="sys-tabs" role="tablist">'+[['rules','redirectRules'],['404','notFound'],['seo','seo']].map(([key,label])=>'<button type="button" role="tab" data-action="redirect-tab" data-id="'+key+'" aria-selected="'+(redirectTab===key)+'">'+esc(t(label))+'</button>').join('')+'</div><div id="sys-redirect-body"></div>';
        if(redirectTab==='seo'){const opts=await getOptions();const holder=$('#sys-redirect-body');holder.innerHTML='<p class="sys-subtitle">'+esc(t('crawlers'))+'</p>'+link('crawlers','/admin/settings/crawlers');await settingForm('seo',[optionSpec('seo_title_format','titleFormat','text',{default:'{title} - {site}'}),optionSpec('seo_default_og_image','ogImage','url')],opts);return;}
        if(redirectTab==='404'){await show404();return;}
        const container=$('#sys-redirect-body');const data=await api('GET','/api/admin/redirects?'+new URLSearchParams(Object.assign({page:redirectPage,per_page:25},redirectFilters)));redirects=data.items||[];
        container.innerHTML='<form id="sys-redirect-filter" class="sys-toolbar">'+fields([select('kind','kind',[['','all'],['301','301'],['302','302'],['410','410']]),select('source','source',[['','all'],['manual','manual'],['auto','auto'],['import','import']]),optionSpec('q','search','search')],redirectFilters)+'<button type="submit" class="btn btn-secondary btn-sm">'+esc(t('filter'))+'</button></form>'+table(['fromPath','toURL','kind','hits','expiresAt','actions'],redirects.map(item=>['<span class="sys-mono">'+text(item.from_path)+'</span>','<span class="sys-mono">'+text(item.to_url)+'</span>',badge(item.kind),text(item.hits),text(item.expires_at?date(item.expires_at):'—'),'<div class="sys-actions">'+button('edit','edit-redirect',item.id)+button('delete','delete-redirect',item.id)+'</div>']))+'<div class="sys-pagination">'+button('previous','prev-redirect')+'<span>'+esc(t('total'))+': '+text(data.total)+'</span>'+button('next','next-redirect')+'</div>';
        $('[data-action=prev-redirect]',container).disabled=redirectPage<=1;$('[data-action=next-redirect]',container).disabled=redirectPage*25>=data.total;
        $('#sys-redirect-filter').onsubmit=event=>{event.preventDefault();redirectFilters=Object.fromEntries(new FormData(event.target));redirectPage=1;busy($('[type=submit]',event.target),refresh);};
        formCard('testPath',[optionSpec('path','path','text',{required:true,default:'/'})],{},async(data,form,container)=>{const result=await api('POST','/api/admin/redirects/test',data);let output=$('[data-test-output]',container);if(!output){output=document.createElement('div');output.dataset.testOutput='';container.append(output);}output.innerHTML=code(result);},'','','test');
    }
    async function show404() {
        const data=await api('GET','/api/admin/not-found?days=30');const holder=$('#sys-redirect-body');holder.innerHTML='<div class="sys-actions">'+button('clear','clear-404')+'</div>'+table(['path','count','suggestion','status','actions'],(data.items||[]).map(item=>[text(item.path),text(item.count),text(item.suggestion),item.ignored?badge(t('ignored')):'—','<div class="sys-actions">'+(!item.ignored?button('ignore','ignore-404',item.id):'')+button('add','redirect-404',encodeURIComponent(JSON.stringify({from_path:item.path,to_url:typeof item.suggestion==='string'?item.suggestion:'',kind:301})))+'</div>']));
    }
    async function editRedirect(item={}) {
        const data=await modal(item.id?'edit':'add',[optionSpec('from_path','fromPath','text',{required:true,default:'/'}),optionSpec('to_url','toURL'),select('kind','kind',[['301','301'],['302','302'],['410','410']],'301'),bool('is_rule','wildcard'),bool('keep_query','keepQuery'),optionSpec('expires_at','expiresAt','datetime-local')],Object.assign({},item,{expires_at:item.expires_at?new Date(new Date(item.expires_at).getTime()-new Date(item.expires_at).getTimezoneOffset()*60000).toISOString().slice(0,16):''}));
        if(!data)return;data.kind=Number(data.kind);data.expires_at=data.expires_at?new Date(data.expires_at).toISOString():null;if(data.kind===410)data.to_url='';await api(item.id?'PUT':'POST','/api/admin/redirects'+(item.id?'/'+item.id:''),data);await refresh();status(root,t('saved'),'success');
    }
    let storeItems=[],storePage=1,storeFilters={q:'',kind:'',category:'',installed:'',updates:''};
    async function showStore() {
        const data=await api('GET','/api/admin/store?'+new URLSearchParams(Object.assign({page:storePage},storeFilters)));storeItems=data.items||[];
        root.innerHTML=toolbar('<div class="sys-actions">'+button('refreshCatalog','refresh-store')+button('updateAll','update-all')+button('upload','upload-plugin')+button('installURL','install-url')+'</div>')+'<form id="sys-store-filter" class="sys-toolbar">'+fields([optionSpec('q','search','search'),select('kind','kind',[['','all'],['plugin','plugin'],['theme','themes']]),optionSpec('category','category'),select('installed','installed',[['','all'],['true','yes'],['false','no']]),select('updates','updates',[['','all'],['true','yes']])],storeFilters)+'<button class="btn btn-secondary btn-sm" type="submit">'+esc(t('filter'))+'</button></form><div class="sys-grid three">'+(storeItems.length?storeItems.map(item=>'<article class="sys-app"><h3>'+text(item.name)+'</h3><p>'+text(item.description)+'</p><div class="sys-actions">'+badge(item.version)+badge(t(item.official?'official':'unsigned'),item.official?'success':'warning')+'</div><small class="sys-subtitle">'+text(item.category)+' / '+text(item.kind)+'</small><div class="sys-actions">'+(item.installed?link('details','/admin/plugins/'+encodeURIComponent(item.id)):'')+((!item.installed||item.update_available)?button(item.installed?'updates':'install','install-store',item.id,true):badge(t('installed'),'success'))+'</div></article>').join(''):empty())+'</div><div class="sys-pagination">'+button('previous','prev-store')+'<span>'+esc(t('total'))+': '+text(data.total)+'</span>'+button('next','next-store')+'</div>';
        $('[data-action=prev-store]').disabled=storePage<=1;$('[data-action=next-store]').disabled=storePage*data.per_page>=data.total;
        $('#sys-store-filter').onsubmit=event=>{event.preventDefault();storeFilters=Object.fromEntries(new FormData(event.target));storePage=1;busy($('[type=submit]',event.target),refresh);};
    }
    async function permissionHash(names) {const raw=Array.from(new Set(names)).sort().join('\n');const digest=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(raw));return Array.from(new Uint8Array(digest),v=>v.toString(16).padStart(2,'0')).join('');}
    async function installStore(id) {
        const item=storeItems.find(item=>item.id===id);if(!item)return;
        const data=await modal('install',[bool('accept','acceptPermissions')],{},'permissionHint','install','<p><b>'+text(item.name)+' '+text(item.version)+'</b></p><p>'+esc(t(item.official?'official':'unsigned'))+'</p><ul>'+(item.permissions||[]).map(name=>'<li class="sys-mono">'+esc(name)+'</li>').join('')+'</ul>',value=>{if(!value.accept)throw new Error(t('acceptPermissions'));});if(!data)return;
        await api('POST','/api/admin/store/install',{id:item.id,version:item.version,accept_permissions:await permissionHash(item.permissions||[])},600000);await refresh();status(root,t('operationDone'),'success');
    }
    let currentPlugin=null;
    async function showPlugin() {
        const id=root.dataset.pluginId;
        const [plugin,settings,logs,extensions]=await Promise.all([api('GET','/api/admin/plugins/'+encodeURIComponent(id)),api('GET','/api/admin/plugins/'+encodeURIComponent(id)+'/settings'),api('GET','/api/admin/plugins/'+encodeURIComponent(id)+'/logs'),api('GET','/api/admin/extensions')]);currentPlugin=plugin;
        root.innerHTML=toolbar('<div class="sys-actions">'+link('store','/admin/store')+button(plugin.enabled?'disable':'enable',plugin.enabled?'disable-plugin':'enable-plugin')+button('reload','reload-plugin')+button('test','test-plugin')+button('uninstall','uninstall-plugin')+'</div>')+card('pluginDetail','<h3>'+text(plugin.name)+'</h3><p>'+text(plugin.description)+'</p><div class="sys-actions">'+badge(plugin.version)+badge(plugin.enabled?t('enabled'):t('disabled'),plugin.enabled?'success':'')+badge(plugin.loaded?t('running'):t('off'))+badge(t(plugin.signature==='official'?'official':'unsigned'))+'</div><dl class="sys-detail"><dt>'+esc(t('author'))+'</dt><dd>'+text(plugin.author)+'</dd><dt>'+esc(t('source'))+'</dt><dd>'+text(plugin.source)+'</dd></dl>');
        const specs=(settings.settings||[]).map(spec=>optionSpec(spec.key,spec.label||spec.key,({int:'number',bool:'bool',secret:'secret',string:'text'})[spec.type],{min:spec.min,max:spec.max,default:spec.default}));
        const initial=Object.assign({},settings.values);specs.forEach(spec=>{if(spec.type==='secret')initial[spec.key]=(initial[spec.key]||{}).masked||'';});
        if(specs.length)formCard('settings',specs,initial,async data=>{await api('PUT','/api/admin/plugins/'+encodeURIComponent(id)+'/settings',{values:data});await refresh();status(root,t('saved'),'success');},'',button('resetSettings','reset-plugin'));
        root.insertAdjacentHTML('beforeend',card('permissions',table(['name','status'],(plugin.permissions||[]).map(permission=>[text(permission.name),badge(t(permission.enforced?'enforced':'declared'),permission.enforced?'success':'warning')]))));
        if((plugin.actions||[]).length)root.insertAdjacentHTML('beforeend',card('actions','<div class="sys-actions">'+plugin.actions.map(action=>'<button type="button" class="btn btn-secondary" data-action="plugin-action" data-id="'+esc(action.id)+'">'+text(action.label||action.id)+'</button>').join('')+'</div>'));
        const extensionContainer=document.createElement('section');extensionContainer.className='sys-card';extensionContainer.innerHTML='<h2>'+esc(t('extensions'))+'</h2>';root.append(extensionContainer);
        for(const extension of extensions){if(!extension.shipped){extensionContainer.insertAdjacentHTML('beforeend','<p class="sys-subtitle">'+text(extension.point)+' · '+esc(t('declared'))+'</p>');continue;}const multiple=extension.point==='media.storage';const choices=(extension.providers||[]).map(id=>[id,id==='builtin'?'builtIn':id]);const initial={provider:multiple?(extension.active||[]):((extension.active||[])[0]||extension.resolved||'')};const spec=multiple?optionSpec('provider','provider','multiselect',{options:choices}):select('provider','provider',[['','inherit'],...choices]);const f=formCard(extension.point,[spec],initial,async data=>{await api('PUT','/api/admin/extensions/'+encodeURIComponent(extension.point),{active:multiple?data.provider:(data.provider?[data.provider]:[])});await refresh();status(root,t('saved'),'success');});extensionContainer.append(f.container);}
        root.insertAdjacentHTML('beforeend',card('logs',table(['date','level','description','requestID'],(logs||[]).map(log=>[text(date(log.at)),badge(t(['debug','info','warn','error'][log.level])),text(log.message),text(log.request_id)]))));
    }
    let mediaTab='policies',mediaPolicies=[],mediaGroups=[],mediaFiles=[],mediaFilters={q:'',group_id:'',policy_id:''},mediaPage=1,storageProviders=[];
    async function showMedia() {
        const [policies,groups,options,extensions]=await Promise.all([api('GET','/api/admin/media/policies'),api('GET','/api/admin/media/groups'),getOptions(),api('GET','/api/admin/extensions')]);storageProviders=((extensions.find(item=>item.point==='media.storage')||{}).providers||[]).filter(id=>id!=='builtin');mediaPolicies=policies.items||policies||[];mediaGroups=groups.items||groups||[];
        root.innerHTML=toolbar('<div class="sys-actions">'+link('files','/admin/media')+button('add','new-policy')+button('add','new-group')+'</div>')+'<div class="sys-tabs" role="tablist">'+['policies','groups','files','settings'].map(tab=>'<button role="tab" type="button" data-action="media-tab" data-id="'+tab+'" aria-selected="'+(tab===mediaTab)+'">'+esc(t(tab))+'</button>').join('')+'</div><div id="sys-media-body"></div>';
        const holder=$('#sys-media-body');
        if(mediaTab==='policies')holder.innerHTML=table(['name','driver','purpose','fileCount','status','actions'],mediaPolicies.map(policy=>[text(policy.name),text(policy.driver),text(t(policy.purpose==='media'?'mediaPurpose':'backupPurpose')),text(policy.file_count),policy.is_default?badge(t('isDefault'),'success'):'—','<div class="sys-actions">'+button('edit','edit-policy',policy.id)+button('test','test-policy',policy.id)+(!policy.is_default?button('makeDefault','default-policy',policy.id):'')+button('move','move-policy',policy.id)+button('delete','delete-policy',policy.id)+'</div>']));
        else if(mediaTab==='groups')holder.innerHTML=table(['name','description','fileCount','actions'],mediaGroups.map(group=>[text(group.name),text(group.description),text(group.file_count),'<div class="sys-actions">'+button('edit','edit-group',group.id)+button('delete','delete-group',group.id)+'</div>']));
        else if(mediaTab==='settings'){holder.innerHTML='<p class="sys-subtitle">'+esc(t('webpProcessorHint'))+'</p>'+link('store','/admin/store');await settingForm('settings',[num('media_max_bytes','maxBytes',20971520,1,67108864),bool('media_strip_exif','stripEXIF',true),optionSpec('media_allowed_types','allowedTypes','json',{default:'[]'}),optionSpec('media_thumb_sizes','thumbnailSizes','json',{default:'[]'})],options);}
        else await showMediaFiles(holder);
    }
    async function editPolicy(policy={}) {
        const config=policy.config||{};
        const drivers=policy.id?[[policy.driver,policy.driver==='local'?'Local':policy.driver]]:[['local','Local'],['s3','S3'],...Array.from(new Set(storageProviders.map(id=>'plugin:'+id))).map(id=>[id,id])];
        const credentials=['access_key','secret_key','session_token'];
        const s3Fields=['endpoint','bucket','region','prefix',...credentials,'clear_credentials'];
        const specs=[optionSpec('name','name','text',{required:true,maxlength:64}),select('driver','driver',drivers,'local'),select('purpose','purpose',policy.id?[[policy.purpose,policy.purpose==='media'?'mediaPurpose':'backupPurpose']]:[['media','mediaPurpose'],['backup','backupPurpose']],'media'),optionSpec('endpoint','endpoint','url'),optionSpec('bucket','bucket'),optionSpec('region','region'),optionSpec('prefix','prefix'),optionSpec('access_key','s3AccessKey','secret',{clear:false}),optionSpec('secret_key','s3SecretKey','secret',{clear:false}),optionSpec('session_token','s3SessionToken','secret',{clear:false}),bool('clear_credentials','clearS3Credentials')];
        const initial=Object.assign({},policy,config);
        credentials.forEach(key=>{initial[key]=policy.has_secret?'••••••••':'';});
        const data=await modal(policy.id?'edit':'add',specs,initial,'s3CredentialsHint','confirm','',data=>{
            if(data.driver==='s3'&&!data.clear_credentials&&!policy.has_secret&&credentials.some(key=>data[key])&&(!data.access_key||!data.secret_key))throw new Error(t('s3CredentialsPair'));
        },form=>{
            const update=()=>{
                const s3=form.elements.driver.value==='s3';
                s3Fields.forEach(key=>{form.elements[key].closest('.sys-row').hidden=!s3;form.elements[key].disabled=!s3;});
                form.elements.clear_credentials.closest('.sys-row').hidden=!s3||!policy.has_secret;
                form.elements.endpoint.required=s3;form.elements.bucket.required=s3;
                credentials.forEach(key=>{form.elements[key].disabled=!s3||form.elements.clear_credentials.checked;});
            };
            form.elements.driver.addEventListener('change',update);
            form.elements.clear_credentials.addEventListener('change',update);update();
        });if(!data)return;
        const body={name:data.name,driver:data.driver,purpose:data.purpose,config:{}};
        if(data.driver==='s3'){
            body.config={endpoint:data.endpoint,bucket:data.bucket,region:data.region,prefix:data.prefix};
            if(/^https?:\/\//i.test(data.endpoint))body.config.secure=/^https:/i.test(data.endpoint);
            if(data.clear_credentials)body.clear_secret=true;
            else {const supplied={};credentials.forEach(key=>{if(data[key])supplied[key]=data[key];});if(Object.keys(supplied).length)body.secret=JSON.stringify(supplied);}
        }
        await api(policy.id?'PUT':'POST','/api/admin/media/policies'+(policy.id?'/'+policy.id:''),body);await refresh();status(root,t('saved'),'success');
    }
    async function showMediaFiles(holder) {
        const data=await api('GET','/api/admin/media/files?'+new URLSearchParams(Object.assign({page:mediaPage,per_page:25},mediaFilters)));mediaFiles=data.items||[];
        holder.innerHTML='<div class="sys-actions">'+button('upload','upload-media')+button('fetchURL','fetch-media')+'</div><form id="sys-media-filter" class="sys-toolbar">'+fields([optionSpec('q','search','search'),select('policy_id','policies',[['','all'],...mediaPolicies.map(p=>[p.id,p.name])]),select('group_id','groups',[['','all'],['none','ungrouped'],...mediaGroups.map(g=>[g.id,g.name])])],mediaFilters)+'<button class="btn btn-secondary btn-sm" type="submit">'+esc(t('filter'))+'</button></form>'+table(['filename','kind','size','alt','actions'],mediaFiles.map(file=>[text(file.filename),text(file.mime),text(bytes(file.size)),text(file.alt),'<div class="sys-actions">'+button('edit','edit-media',file.id)+button('copyMarkdown','copy-media',file.id)+button('copyHTML','copy-media-html',file.id)+button('replace','replace-media',file.id)+button('delete','delete-media',file.id)+'</div>']))+'<div class="sys-pagination">'+button('previous','prev-media')+'<span>'+esc(t('total'))+': '+text(data.total)+'</span>'+button('next','next-media')+'</div>';
        $('[data-action=prev-media]',holder).disabled=mediaPage<=1;$('[data-action=next-media]',holder).disabled=mediaPage*25>=data.total;
        $('#sys-media-filter').onsubmit=event=>{event.preventDefault();mediaFilters=Object.fromEntries(new FormData(event.target));mediaPage=1;busy($('[type=submit]',event.target),refresh);};
    }
    async function installExternal(source) {
        const specs=source==='url'?[optionSpec('url','packageURL','url',{required:true}),optionSpec('sha256','sha256')]:[optionSpec('file','uploadFile','file',{required:true,accept:'.zip'})];
        const supplied=await modal(source==='url'?'installURL':'upload',specs,{},'permissionHint','dryRun');if(!supplied)return;
        const endpoint=source==='url'?'/api/admin/plugins/install-url':'/api/admin/plugins/upload';
        let previewSHA='';
        const bodyFor=hash=>{if(source==='url')return Object.assign({},supplied,hash?{accept_permissions:hash,sha256:previewSHA}:{});const form=new FormData();form.append('file',supplied.file);if(hash){form.append('accept_permissions',hash);form.append('sha256',previewSHA);}return form;};
        const preview=await api('POST',endpoint+'?dry=1',bodyFor(),600000);
        previewSHA=preview.sha256;
        if(!previewSHA)throw new Error(t('previewChanged'));
        const manifest=preview.manifest||preview;
        const permissions=(preview.permissions||manifest.permissions||[]).map(p=>typeof p==='string'?p:p.name);
        const accepted=await modal('permissions',[bool('accept','acceptPermissions')],{},'permissionHint','install','<p><b>'+text(manifest.name||manifest.id||preview.id)+' '+text(manifest.version||preview.version)+'</b></p><p>'+esc(t('unsigned'))+'</p><ul>'+permissions.map(name=>'<li class="sys-mono">'+esc(name)+'</li>').join('')+'</ul>',data=>{if(!data.accept)throw new Error(t('acceptPermissions'));});
        if(!accepted)return;
        const result=await api('POST',endpoint,bodyFor(preview.permissions_hash||await permissionHash(permissions)),600000);
        await refresh();status(root,t('operationDone'),'success');
        if(result.id||result.plugin_id)root.insertAdjacentHTML('afterbegin','<div class="sys-actions">'+link('pluginDetail','/admin/plugins/'+encodeURIComponent(result.id||result.plugin_id))+'</div>');
    }
    async function handleAction(action,id,el) {
        if(action==='refresh'){if(dirty&&!await modal('confirm',[],{},'leaveUnsaved'))return;all('form[data-dirty]').forEach(form=>delete form.dataset.dirty);await refresh();return;}
        if(action==='test-cache'){const form=el.closest('form');const data=values(form,[select('driver','driver',[]),optionSpec('url','connectionURL','secret')]);const result=await api('POST','/api/admin/infra/cache/test',data);status(el.closest('.sys-card'),result.ok?t('connectionOK'):(result.error||t('connectionFailed')),result.ok?'success':'error');return;}
        if(action==='flush-cache'){const data=await modal('flush',[select('scope','scope',[['all','all'],['page','pageCache'],['object','objects']],'all')],{},'','flush');if(!data)return;await api('POST','/api/admin/infra/cache/flush',data);await refresh();status(root,t('operationDone'),'success');return;}
        if(action==='test-queue'){const form=el.closest('form');const data=values(form,[optionSpec('url','connectionURL','secret')]);data.driver=root.dataset.queueDriver;const result=await api('POST','/api/admin/infra/queue/test',data);status(el.closest('.sys-card'),result.ok?t('connectionOK'):(result.error||t('connectionFailed')),result.ok?'success':'error');return;}
        if(action==='switch-queue'){const data=await modal('switchBackend',[select('to','driver',[['postgres','PostgreSQL'],['redis','Redis Streams'],['nats','NATS JetStream']]),optionSpec('url','connectionURL','secret')],{},'','switchBackend');if(!data)return;await api('POST','/api/admin/infra/queue/switch',data,600000);await refresh();status(root,t('operationDone'),'success');return;}
        if(action==='pause-queue'||action==='resume-queue'){await api('POST','/api/admin/infra/queue/'+(action==='pause-queue'?'pause':'resume'),{});await refresh();return;}
        if(action==='check-update'){const result=await api('POST','/api/admin/update/check',{},120000);await refresh();operationStatus(root,result);return;}
        if(action==='apply-update'){if(!await modal('applyUpdate',[],{},'updateConfirm','applyUpdate'))return;const result=await api('POST','/api/admin/update/apply',{version:root.dataset.latest},600000);await refresh();operationStatus(root,result);return;}
        if(action==='rollback-update'){if(!await modal('rollback',[],{},'updateConfirm','rollback'))return;const result=await api('POST','/api/admin/update/rollback',{},600000);await refresh();operationStatus(root,result);return;}
        if(action==='cancel-update'){await api('POST','/api/admin/update/cancel',{});await refresh();return;}
        if(action==='copy-commands'){await navigator.clipboard.writeText(JSON.parse(root.dataset.commands).join('\n'));status(root,t('copied'),'success');return;}
        if(action==='restore-backup'){const data=await modal('restore',[optionSpec('confirm','restoreConfirm','text',{required:true}),optionSpec('passphrase','passphrase','password')],{},'restoreHint','restore','',value=>{if(value.confirm!=='RESTORE')throw new Error(t('restoreConfirm'));});if(!data)return;await api('POST','/api/admin/backups/'+encodeURIComponent(id)+'/restore',data,600000);await refresh();status(root,t('restoreDone'),'success');return;}
        if(action==='retry-backup'){const data=await modal('retry',[optionSpec('passphrase','passphrase','password')]);if(!data)return;const result=await api('POST','/api/admin/backups/'+encodeURIComponent(id)+'/retry',data,600000);await refresh();operationStatus(root,result);return;}
        if(action==='delete-backup'){if(!await modal('confirmDelete'))return;await api('DELETE','/api/admin/backups/'+encodeURIComponent(id));await refresh();return;}
        if(action==='select-import'){stopImportTimer();importPoll=null;selectedImport=id;await refresh();return;}
        if(['dry-import','run-import','cancel-import'].includes(action)){if(action==='run-import'&&!await modal('startImport',[],{},'readyToImport','startImport'))return;const suffix={'dry-import':'dry-run','run-import':'run','cancel-import':'cancel'}[action];const result=await api('POST','/api/admin/import/'+encodeURIComponent(id)+'/'+suffix,{},600000);selectedImport=id;if(action==='run-import'&&result.state!=='done')importPoll={id,deadline:Date.now()+120000};if(action==='cancel-import')importPoll=null;await refresh();if(result.state==='queued')operationStatus(root,result);return;}
        if(action==='more-logs'){await loadLogs(true);return;}
        if(action==='filter-request'){logFilters.q=id;await refresh();return;}
        if(action==='redirect-tab'){redirectTab=id;await refresh();return;}
        if(action==='new-redirect'){await editRedirect();return;}
        if(action==='edit-redirect'){await editRedirect(redirects.find(item=>String(item.id)===id));return;}
        if(action==='delete-redirect'){if(!await modal('confirmDelete'))return;await api('DELETE','/api/admin/redirects/'+encodeURIComponent(id));await refresh();return;}
        if(action==='prev-redirect'||action==='next-redirect'){redirectPage+=action==='next-redirect'?1:-1;await refresh();return;}
        if(action==='clear-404'){if(!await modal('clear404'))return;await api('DELETE','/api/admin/not-found');await refresh();return;}
        if(action==='ignore-404'){await api('POST','/api/admin/not-found/'+encodeURIComponent(id)+'/ignore',{});await refresh();return;}
        if(action==='redirect-404'){await editRedirect(JSON.parse(decodeURIComponent(id)));return;}
        if(action==='import-redirects'){
            const data=await modal('importCSV',[optionSpec('file','uploadFile','file',{required:true,accept:'.csv,text/csv'})],{},'csvHint','dryRun');if(!data)return;
            const body=new FormData();body.append('file',data.file);const result=await api('POST','/api/admin/redirects/import?dry=1',body);const check=await modal('importConfirm',[],{},'csvHint','importConfirm',code(result));if(!check)return;await api('POST','/api/admin/redirects/import',body);await refresh();return;
        }
        if(action==='refresh-store'){const result=await api('POST','/api/admin/store/refresh',{},120000);await refresh();operationStatus(root,result);return;}
        if(action==='install-store'){await installStore(id);return;}
        if(action==='prev-store'||action==='next-store'){storePage+=action==='next-store'?1:-1;await refresh();return;}
        if(action==='update-all'){if(!await modal('updateAll'))return;const result=await api('POST','/api/admin/store/update',{all:true},600000);await refresh();if(result.failed&&result.failed.length){root.insertAdjacentHTML('afterbegin',card('failedState',table(['name','error'],result.failed.map(row=>[text(row.id),text(row.error)]))));status(root,t('permissionsChanged'),'error');}else status(root,t('operationDone'),'success');return;}
        if(action==='install-url'){await installExternal('url');return;}
        if(action==='upload-plugin'){await installExternal('upload');return;}
        if(action==='enable-plugin'||action==='disable-plugin'||action==='reload-plugin'){const suffix={'enable-plugin':'enable','disable-plugin':'disable','reload-plugin':'reload'}[action];await api('POST','/api/admin/plugins/'+encodeURIComponent(root.dataset.pluginId)+'/'+suffix,{},120000);await refresh();return;}
        if(action==='test-plugin'){const result=await api('POST','/api/admin/plugins/'+encodeURIComponent(root.dataset.pluginId)+'/test',{});root.insertAdjacentHTML('beforeend',card('test',table(['name','status','description'],(result.checks||[]).map(row=>[text(row.name),badge(t(row.ok?'yes':'no'),row.ok?'success':'danger'),text(row.detail)]))));return;}
        if(action==='reset-plugin'){if(!await modal('resetSettings'))return;await api('POST','/api/admin/plugins/'+encodeURIComponent(root.dataset.pluginId)+'/settings/reset',{});await refresh();return;}
        if(action==='plugin-action'){const result=await api('POST','/api/admin/plugins/'+encodeURIComponent(root.dataset.pluginId)+'/actions/'+encodeURIComponent(id),{},120000);if(!result.dispatched)throw new Error(result.reason||t('failed'));status(root,t('operationDone'),'success');return;}
        if(action==='uninstall-plugin'){const data=await modal('uninstall',[bool('keep','keepSettings',true)],{keep:true},'','uninstall');if(!data)return;await api('DELETE','/api/admin/plugins/'+encodeURIComponent(root.dataset.pluginId)+'?keep_settings='+data.keep);dirty=false;window.location.assign('/admin/plugins');return;}
        if(action==='media-tab'){mediaTab=id;mediaPage=1;await refresh();return;}
        if(action==='new-policy'){await editPolicy();return;}
        if(action==='edit-policy'){await editPolicy(mediaPolicies.find(p=>String(p.id)===id));return;}
        if(action==='test-policy'){const result=await api('POST','/api/admin/media/policies/'+encodeURIComponent(id)+'/test',{});status(root,result.ok?t('connectionOK'):(result.error||t('connectionFailed')),result.ok?'success':'error');return;}
        if(action==='default-policy'){await api('POST','/api/admin/media/policies/'+encodeURIComponent(id)+'/default',{});await refresh();return;}
        if(action==='move-policy'){const source=mediaPolicies.find(p=>String(p.id)===id);const data=await modal('move',[select('to_policy_id','moveTo',mediaPolicies.filter(p=>p.id!==source.id&&p.purpose===source.purpose).map(p=>[p.id,p.name]))],{},'','move');if(!data)return;data.to_policy_id=Number(data.to_policy_id);const result=await api('POST','/api/admin/media/policies/'+id+'/move',data,600000);await refresh();operationStatus(root,result);return;}
        if(action==='delete-policy'){if(!await modal('confirmDelete'))return;await api('DELETE','/api/admin/media/policies/'+id);await refresh();return;}
        if(action==='new-group'||action==='edit-group'){const group=action==='edit-group'?mediaGroups.find(g=>String(g.id)===id):{};const data=await modal(action==='edit-group'?'edit':'add',[optionSpec('name','name','text',{required:true,maxlength:128}),optionSpec('description','description','textarea',{maxlength:500})],group);if(!data)return;await api(group.id?'PUT':'POST','/api/admin/media/groups'+(group.id?'/'+group.id:''),data);await refresh();return;}
        if(action==='delete-group'){const data=await modal('confirmDelete',[select('files','groupDeleteMode',[['ungroup','ungroup'],['trash','trashFiles']],'ungroup')]);if(!data)return;await api('DELETE','/api/admin/media/groups/'+id+'?files='+data.files);await refresh();return;}
        if(action==='prev-media'||action==='next-media'){mediaPage+=action==='next-media'?1:-1;await refresh();return;}
        if(action==='upload-media'||action==='replace-media'){const specs=[optionSpec('file','uploadFile','file',{required:true})];if(action==='upload-media')specs.push(select('policy_id','policies',mediaPolicies.filter(p=>p.purpose==='media').map(p=>[p.id,p.name])),select('group_id','groups',[['','ungrouped'],...mediaGroups.map(g=>[g.id,g.name])]));const data=await modal(action==='upload-media'?'upload':'replace',specs,{},'',action==='upload-media'?'upload':'replace');if(!data)return;const body=new FormData();Object.entries(data).forEach(([k,v])=>{if(v!==''&&v!=null)body.append(k,v);});await api('POST','/api/admin/media/files'+(action==='replace-media'?'/'+id+'/replace':''),body,600000);await refresh();return;}
        if(action==='fetch-media'){const data=await modal('fetchURL',[optionSpec('urls','urls','textarea',{required:true})]);if(!data)return;let result;try{result=await api('POST','/api/admin/media/fetch',{urls:data.urls.split('\n').map(s=>s.trim()).filter(Boolean)},600000);}catch(err){if(!err.data||!Array.isArray(err.data.results))throw err;result=err.data;}await refresh();root.insertAdjacentHTML('beforeend',card('fetchURL',table(['packageURL','status','jobID','error'],(result.results||[]).map(row=>[text(row.url),text(t(row.status)),text(row.job_id),text(row.error)]))));operationStatus(root,result);return;}
        if(action==='edit-media'){const file=mediaFiles.find(f=>String(f.id)===id);const data=await modal('edit',[optionSpec('alt','alt','textarea',{maxlength:500}),select('group_id','groups',[['','ungrouped'],...mediaGroups.map(g=>[g.id,g.name])])],file);if(!data)return;data.group_id=data.group_id?Number(data.group_id):null;await api('PATCH','/api/admin/media/files/'+id,data);await refresh();return;}
        if(action==='delete-media'){if(!await modal('confirmDelete'))return;await api('DELETE','/api/admin/media/files/'+id);await refresh();return;}
        if(action==='copy-media'||action==='copy-media-html'){const file=mediaFiles.find(f=>String(f.id)===id);if(!file.url)throw new Error(t('unset'));const image=(file.mime||'').startsWith('image/');const label=file.alt||file.filename;const snippet=action==='copy-media-html'?(image?'<img src="'+esc(file.url)+'" alt="'+esc(label)+'">':'<a href="'+esc(file.url)+'">'+esc(label)+'</a>'):(image?'!':'')+'['+label.replace(/[\[\]]/g,'')+']('+file.url.replace(/[()]/g,encodeURIComponent)+')';await navigator.clipboard.writeText(snippet);status(root,t('copied'),'success');return;}
    }
    root.addEventListener('click',event=>{const el=event.target.closest('[data-action]');if(!el||!root.contains(el))return;event.preventDefault();busy(el,()=>handleAction(el.dataset.action,el.dataset.id,el));});
    root.addEventListener('input',event=>{if(event.target.closest('form[data-form-key]'))dirty=true;});
    window.addEventListener('beforeunload',event=>{if(dirty){event.preventDefault();event.returnValue='';}});
    window.addEventListener('pagehide',()=>{pageActive=false;++epoch;stopImportTimer();importPoll=null;stopLive();if(downloadURL)URL.revokeObjectURL(downloadURL);});
    refresh=async(polling=false)=>{
        const preserved=all('form[data-dirty]:not([data-saving])').map(form=>({key:form.dataset.formKey,fields:Array.from(form.elements).filter(el=>el.name&&el.type!=='file').map(el=>({name:el.name,type:el.type,value:el.value,checked:el.checked,selected:el.multiple?Array.from(el.selectedOptions,option=>option.value):null}))}));
        const token=++epoch;stopImportTimer();stopLive();root.setAttribute('aria-busy','true');
        try{
            const page=root.dataset.page;
            if(page.startsWith('settings-'))await showSettings(page.slice(9));
            else if(page==='backup')await showBackup();else if(page==='import')await showImport(polling);else if(page==='logs')await showLogs();else if(page==='redirects')await showRedirects();else if(page==='store')await showStore();else if(page==='plugin')await showPlugin();else if(page==='media')await showMedia();
            for(const saved of preserved){const form=all('form[data-form-key]').find(form=>form.dataset.formKey===saved.key);if(!form)continue;for(const field of saved.fields){const el=form.elements[field.name];if(!el)continue;if(field.type==='checkbox')el.checked=field.checked;else if(field.selected)Array.from(el.options).forEach(option=>{option.selected=field.selected.includes(option.value);});else el.value=field.value;}form.dataset.dirty='true';}
            dirty=!!$('form[data-dirty]');
            if(token===epoch)root.setAttribute('aria-busy','false');
        }catch(err){if(token===epoch){root.setAttribute('aria-busy','false');status(root,t('loadFailed')+': '+errorText(err),'error');if(!$('[data-action=refresh]'))root.insertAdjacentHTML('beforeend',button('retry','refresh'));}throw err;}
    };
    window.addEventListener('pageshow',event=>{pageActive=true;if(event.persisted)refresh().catch(()=>{});});
    refresh().catch(()=>{});
}());
