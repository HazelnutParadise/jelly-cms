/*
 * Jelly CMS 商店後台共用工具（商品、訂單、折扣碼、商店設定與商品分類）。
 * 金額一律以「分」往返 API；顯示與輸入換算只在這裡做。
 */
(function (root) {
    'use strict';

    var lang = (document.documentElement.lang || '').toLowerCase().indexOf('en') === 0 ? 'en' : 'zh';

    var I18N = {
        zh: {
            loading: '載入中…', retry: '再試一次', cancel: '取消', close: '關閉', save: '儲存', saving: '儲存中…',
            saved: '已儲存', saveFailed: '儲存失敗', edit: '編輯', view: '查看', delete: '刪除', duplicate: '複製一份',
            deleted: '已刪除', copied: '已複製', copy: '複製', yes: '是', no: '否', none: '無', all: '全部',
            offline: '目前似乎離線，請確認網路連線後再試一次。', timeout: '等待時間過長，請再試一次。',
            requestFailed: '目前無法完成操作，請再試一次。', loadFailedTitle: '載入失敗', loadFailedText: '資料沒有載入成功，你的輸入都還在。',
            notReady: '後端尚未提供這個功能（%s）。', forbidden: '你沒有權限執行這個操作。', notFound: '找不到這筆資料。',
            prev: '上一頁', next: '下一頁', pageInfo: '%s–%s，共 %s %s', pageOf: '第 %s / %s 頁', unitItems: '件', unitOrders: '筆',
            unsaved: '%s 項未儲存', leaveUnsaved: '還有未儲存的變更，確定要離開嗎？', revert: '還原', invalidAmount: '請輸入不小於 0 的金額，最多兩位小數。',
            invalidInt: '請輸入不小於 0 的整數。', required: '必填', search: '搜尋',
            // order statuses
            st_pending: '待付款', st_paid: '已付款', st_shipped: '已出貨', st_completed: '完成', st_cancelled: '已取消',
            st_refunded: '已退款', st_expired: '已過期', st_processing: '處理中',
            // product statuses
            ps_active: '上架', ps_draft: '草稿', ps_archived: '下架', ps_out_of_stock: '缺貨', lowStock: '低庫存',
            // coupon statuses
            cs_active: '進行中', cs_scheduled: '排程', cs_ended: '已結束', cs_disabled: '已停用',
            // payment
            pv_ecpay: '綠界', pv_newebpay: '藍新', pv_sandbox: '測試金流', pv_manual_transfer: '銀行轉帳',
            pm_credit_card: '信用卡', pm_atm: 'ATM', pm_cvs: '超商代碼', pm_bank_transfer: '銀行轉帳',
            sm_home: '宅配', sm_pickup: '自取', sm_cvs: '超商取貨'
        },
        en: {
            loading: 'Loading…', retry: 'Try again', cancel: 'Cancel', close: 'Close', save: 'Save', saving: 'Saving…',
            saved: 'Saved', saveFailed: 'Could not save', edit: 'Edit', view: 'View', delete: 'Delete', duplicate: 'Duplicate',
            deleted: 'Deleted', copied: 'Copied', copy: 'Copy', yes: 'Yes', no: 'No', none: 'None', all: 'All',
            offline: 'You seem to be offline. Check the connection and try again.', timeout: 'The request took too long. Please try again.',
            requestFailed: 'Something went wrong. Please try again.', loadFailedTitle: 'Could not load', loadFailedText: 'The data did not load. Your input is kept.',
            notReady: 'The server does not offer this yet (%s).', forbidden: 'You do not have permission to do this.', notFound: 'Not found.',
            prev: 'Previous page', next: 'Next page', pageInfo: '%s–%s of %s %s', pageOf: 'Page %s of %s', unitItems: 'items', unitOrders: 'orders',
            unsaved: '%s unsaved', leaveUnsaved: 'You have unsaved changes. Leave anyway?', revert: 'Revert', invalidAmount: 'Enter an amount of 0 or more, with at most two decimals.',
            invalidInt: 'Enter a whole number of 0 or more.', required: 'Required', search: 'Search',
            st_pending: 'Awaiting payment', st_paid: 'Paid', st_shipped: 'Shipped', st_completed: 'Completed', st_cancelled: 'Cancelled',
            st_refunded: 'Refunded', st_expired: 'Expired', st_processing: 'Processing',
            ps_active: 'Active', ps_draft: 'Draft', ps_archived: 'Archived', ps_out_of_stock: 'Out of stock', lowStock: 'Low stock',
            cs_active: 'Active', cs_scheduled: 'Scheduled', cs_ended: 'Ended', cs_disabled: 'Disabled',
            pv_ecpay: 'ECPay', pv_newebpay: 'NewebPay', pv_sandbox: 'Sandbox', pv_manual_transfer: 'Bank transfer',
            pm_credit_card: 'Card', pm_atm: 'ATM', pm_cvs: 'Store code', pm_bank_transfer: 'Bank transfer',
            sm_home: 'Home delivery', sm_pickup: 'Pickup', sm_cvs: 'Store pickup'
        }
    };

    /** Adds page strings: SA.extend({zh:{...}, en:{...}}). */
    function extend(dict) {
        Object.keys(dict).forEach(function (l) { Object.assign(I18N[l] = I18N[l] || {}, dict[l]); });
    }

    /** t('key', a, b) replaces each %s in order. Missing keys fall back to the key itself. */
    function t(key) {
        var s = (I18N[lang] && I18N[lang][key]) || (I18N.zh[key]) || key;
        var args = Array.prototype.slice.call(arguments, 1), i = 0;
        return String(s).replace(/%s/g, function () { return i < args.length ? String(args[i++]) : ''; });
    }
    /** t() with a fallback when the key is not translated (e.g. an unknown status). */
    function tOr(key, fallback) {
        return (I18N[lang] && I18N[lang][key]) || I18N.zh[key] || fallback;
    }

    function esc(text) {
        return String(text == null ? '' : text).replace(/[&<>"']/g, function (c) {
            return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
        });
    }
    function svg(id) { return '<svg class="icon" aria-hidden="true"><use href="#' + id + '"/></svg>'; }

    /** Only relative paths and http(s) URLs may be used as src/href. */
    function safeUrl(u) {
        u = String(u == null ? '' : u).trim();
        if (/^\/(?!\/)/.test(u) || /^https?:\/\//i.test(u)) return u;
        return '';
    }

    /* ---------------- money (minor units) ---------------- */

    var PREFIX = { TWD: 'NT$', USD: 'US$', JPY: '¥', EUR: '€', HKD: 'HK$', CNY: 'CN¥' };
    function group(digits) { return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ','); }

    /** money(12000) → "NT$120"; money(12050) → "NT$120.50". Same rules as internal/money.Format. */
    function money(minor, currency) {
        var n = Number(minor);
        if (!isFinite(n)) n = 0;
        n = Math.round(n);
        var neg = n < 0;
        n = Math.abs(n);
        var whole = Math.floor(n / 100), frac = n % 100;
        var cur = (currency || 'TWD').toUpperCase();
        var prefix = PREFIX[cur] || (cur + ' ');
        return (neg ? '-' : '') + prefix + group(String(whole)) + (frac ? '.' + String(frac).padStart(2, '0') : '');
    }

    /** minor → input text in yuan: 12000 → "120", 12050 → "120.50"; null/undefined → "". */
    function toInput(minor) {
        if (minor == null || minor === '' || !isFinite(Number(minor))) return '';
        var n = Math.round(Number(minor));
        var whole = Math.floor(Math.abs(n) / 100), frac = Math.abs(n) % 100;
        return (n < 0 ? '-' : '') + whole + (frac ? '.' + String(frac).padStart(2, '0') : '');
    }

    /**
     * Parses a yuan amount as the server's money.FromInput does: digits, optional
     * correct thousands separators, up to two decimals, never negative.
     * Returns {ok, empty, minor, text} where text is the normalized yuan string.
     */
    function parseAmount(raw) {
        var s = String(raw == null ? '' : raw).trim();
        if (s === '') return { ok: true, empty: true, minor: 0, text: '' };
        if (!/^(\d{1,3}(,\d{3})+|\d+)(\.\d{1,2})?$/.test(s)) return { ok: false };
        var plain = s.replace(/,/g, '');
        var parts = plain.split('.');
        var whole = parts[0].replace(/^0+(?=\d)/, '');
        var frac = (parts[1] || '').padEnd(2, '0');
        var minor = Number(whole) * 100 + Number(frac || 0);
        if (!Number.isSafeInteger(minor)) return { ok: false };
        return { ok: true, empty: false, minor: minor, text: plain };
    }

    /** Non-negative whole number from an input; '' → {empty}. */
    function parseIntInput(raw) {
        var s = String(raw == null ? '' : raw).trim();
        if (s === '') return { ok: true, empty: true, value: 0 };
        if (!/^\d+$/.test(s)) return { ok: false };
        var v = Number(s);
        return Number.isSafeInteger(v) ? { ok: true, empty: false, value: v } : { ok: false };
    }

    /* ---------------- dates ---------------- */

    function pad(n) { return String(n).padStart(2, '0'); }
    function toDate(v) { if (!v) return null; var d = new Date(v); return isNaN(d.getTime()) ? null : d; }
    /** "10/21 14:02", or "2026/10/21 14:02" with year=true. */
    function date(v, year) {
        var d = toDate(v);
        if (!d) return '—';
        var s = pad(d.getMonth() + 1) + '/' + pad(d.getDate()) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
        return year ? d.getFullYear() + '/' + s : s;
    }
    function day(v) { var d = toDate(v); return d ? pad(d.getMonth() + 1) + '/' + pad(d.getDate()) : '—'; }
    /** ISO → value for <input type="datetime-local"> in local time. */
    function toLocalInput(v) {
        var d = toDate(v);
        return d ? d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + 'T' + pad(d.getHours()) + ':' + pad(d.getMinutes()) : '';
    }
    /** datetime-local value → ISO string with offset, or null when empty. */
    function fromLocalInput(s) {
        if (!s) return null;
        var d = new Date(s);
        return isNaN(d.getTime()) ? null : d.toISOString();
    }

    /* ---------------- requests ---------------- */

    var TIMEOUT_MS = 20000;

    /**
     * api(method, url, body?, {timeout}) → parsed JSON (or null).
     * Same-origin credentials; writes carry X-Jelly-CSRF: 1 and a JSON body.
     * Errors carry kind: 'offline' | 'timeout' | 'http', plus status and data for http.
     */
    function api(method, url, body, opts) {
        method = (method || 'GET').toUpperCase();
        opts = opts || {};
        var headers = { 'Accept': 'application/json' };
        if (method !== 'GET' && method !== 'HEAD') headers['X-Jelly-CSRF'] = '1';
        if (body !== undefined && body !== null) headers['Content-Type'] = 'application/json';
        if (opts.headers) Object.assign(headers, opts.headers);
        var ctrl = typeof AbortController === 'function' ? new AbortController() : null;
        var timedOut = false;
        var timer = ctrl ? setTimeout(function () { timedOut = true; ctrl.abort(); }, opts.timeout || TIMEOUT_MS) : null;
        if (opts.signal && ctrl) opts.signal.addEventListener('abort', function () { ctrl.abort(); });
        return fetch(url, {
            method: method,
            headers: headers,
            credentials: 'same-origin',
            body: body !== undefined && body !== null ? JSON.stringify(body) : undefined,
            signal: ctrl ? ctrl.signal : undefined
        }).then(function (res) {
            clearTimeout(timer);
            return res.text().then(function (text) {
                var data = null;
                if (text) { try { data = JSON.parse(text); } catch (e) { data = text; } }
                if (!res.ok) {
                    var err = new Error(errorMessage(res.status, data));
                    err.kind = 'http'; err.status = res.status; err.data = data;
                    throw err;
                }
                return data;
            });
        }, function (cause) {
            clearTimeout(timer);
            var err = new Error(timedOut ? t('timeout') : (navigator.onLine === false ? t('offline') : (cause && cause.name === 'AbortError' ? 'aborted' : t('requestFailed'))));
            err.kind = timedOut ? 'timeout' : (cause && cause.name === 'AbortError' ? 'aborted' : 'network');
            throw err;
        });
    }

    function errorMessage(status, data) {
        if (data && typeof data === 'object') {
            var m = data.error || data.message;
            if (typeof m === 'string' && m) return m;
        }
        if (status === 501) return t('notReady', status);
        if (status === 403) return t('forbidden');
        if (status === 404) return t('notFound');
        return t('requestFailed');
    }

    /** Human text for any error thrown by api(). */
    function errText(err) {
        if (!err) return t('requestFailed');
        if (err.kind === 'network') return navigator.onLine === false ? t('offline') : t('requestFailed');
        if (err.kind === 'timeout') return t('timeout');
        return err.message || t('requestFailed');
    }

    /** Field errors in a 400 body: {fields:{name:msg}} or {errors:{name:msg}}. */
    function fieldErrors(err) {
        var d = err && err.data;
        if (!d || typeof d !== 'object') return {};
        var f = d.fields || d.errors || d.field_errors;
        return f && typeof f === 'object' && !Array.isArray(f) ? f : {};
    }

    function idemKey() {
        if (root.crypto && typeof root.crypto.randomUUID === 'function') return root.crypto.randomUUID();
        var a = new Uint8Array(16);
        root.crypto.getRandomValues(a);
        return Array.prototype.map.call(a, function (b) { return b.toString(16).padStart(2, '0'); }).join('');
    }

    /* ---------------- UI helpers ---------------- */

    function toast(message, kind, extra) {
        var opts = Object.assign({ kind: kind || 'success' }, extra || {});
        if (root.Jelly && typeof root.Jelly.toast === 'function') root.Jelly.toast(message, opts);
    }

    /**
     * busy(button, fn): runs fn once at a time; the button is disabled and
     * aria-busy while it runs, so a double click never sends twice.
     */
    function busy(btn, fn) {
        if (btn && btn.dataset.busy === '1') return Promise.resolve();
        if (btn) { btn.dataset.busy = '1'; btn.disabled = true; btn.setAttribute('aria-busy', 'true'); }
        return Promise.resolve().then(fn).finally(function () {
            if (btn) { delete btn.dataset.busy; btn.disabled = false; btn.removeAttribute('aria-busy'); }
        });
    }

    /**
     * state(box, 'loading' | 'empty' | 'error', {title, text, actions:[{label, primary, onClick, href}]})
     * Same markup as the state-* components in components.html.
     */
    function state(box, kind, o) {
        o = o || {};
        box.setAttribute('aria-busy', kind === 'loading' ? 'true' : 'false');
        var html;
        if (kind === 'loading') {
            html = '<section class="state state-loading" role="status" aria-live="polite"><div class="state-loading-row"><span class="spinner" aria-hidden="true"><i></i></span><b>' + esc(o.title || t('loading')) + '</b></div>' +
                '<div class="skeleton" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i></div></section>';
        } else if (kind === 'error') {
            html = '<section class="state state-error" role="alert"><span class="stripes" aria-hidden="true"></span><h2>' + esc(o.title || t('loadFailedTitle')) + '</h2>' +
                '<p class="state-text">' + esc(o.text || t('loadFailedText')) + '</p><div class="state-actions"></div></section>';
        } else {
            html = '<section class="state state-empty"><span class="state-art" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i></span><div class="state-body"><span class="stripes" aria-hidden="true"></span><h2>' +
                esc(o.title || '') + '</h2>' + (o.text ? '<p class="state-text">' + esc(o.text) + '</p>' : '') + '<div class="state-actions"></div></div></section>';
        }
        box.innerHTML = html;
        var actions = box.querySelector('.state-actions');
        var list = o.actions || (kind === 'error' && o.retry ? [{ label: t('retry'), icon: 'i-refresh', onClick: o.retry }] : []);
        list.forEach(function (a) {
            var el = document.createElement(a.href ? 'a' : 'button');
            el.className = 'btn ' + (a.primary ? 'btn-primary' : 'btn-secondary');
            if (a.href) el.href = a.href; else el.type = 'button';
            el.innerHTML = (a.icon ? svg(a.icon) : '') + esc(a.label);
            if (a.onClick) el.addEventListener('click', a.onClick);
            actions.appendChild(el);
        });
        if (actions && !list.length) actions.remove();
    }

    /** confirm({title, body, ok, danger}) → Promise<boolean>, keyboard-trapped sheet. */
    function confirmSheet(o) {
        return new Promise(function (resolve) {
            var prev = document.activeElement;
            var ov = document.createElement('div');
            ov.className = 'modal-overlay open';
            ov.innerHTML = '<div class="modal-content" role="alertdialog" aria-modal="true" aria-labelledby="saCfT" aria-describedby="saCfB"><div class="modal-header"><h2 id="saCfT"></h2></div><p id="saCfB" class="muted"></p><div class="modal-footer"><button type="button" class="btn" data-ok></button><button type="button" class="btn btn-secondary" data-cancel></button></div></div>';
            ov.querySelector('#saCfT').textContent = o.title || '';
            ov.querySelector('#saCfB').textContent = o.body || '';
            var ok = ov.querySelector('[data-ok]');
            ok.textContent = o.ok || t('yes');
            ok.classList.add(o.danger ? 'btn-danger' : 'btn-primary');
            ov.querySelector('[data-cancel]').textContent = o.cancel || t('cancel');
            function done(v) { document.removeEventListener('keydown', onKey, true); ov.remove(); if (prev && document.contains(prev)) prev.focus(); resolve(v); }
            function onKey(e) {
                if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); done(false); }
                if (e.key === 'Tab') {
                    var els = ov.querySelectorAll('button'), first = els[0], last = els[els.length - 1];
                    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
                    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
                }
            }
            document.addEventListener('keydown', onKey, true);
            ov.addEventListener('click', function (e) { if (e.target === ov) done(false); });
            ok.addEventListener('click', function () { done(true); });
            ov.querySelector('[data-cancel]').addEventListener('click', function () { done(false); });
            document.body.appendChild(ov);
            ov.querySelector('[data-cancel]').focus();
        });
    }

    /**
     * pager(box, {page, perPage, total, unit}, go): "1–20，共 42 件" and ← 1/3 →.
     */
    function pager(box, o, go) {
        var pages = Math.max(1, Math.ceil((o.total || 0) / o.perPage));
        var from = o.total ? (o.page - 1) * o.perPage + 1 : 0;
        var to = Math.min(o.total || 0, o.page * o.perPage);
        box.innerHTML = '<span class="sa-pager-info">' + esc(t('pageInfo', from, to, o.total || 0, o.unit || '')) + '</span>' +
            '<span class="sa-pager-nav"><button type="button" class="btn btn-secondary btn-sm btn-icon" data-go="prev" aria-label="' + esc(t('prev')) + '"' + (o.page <= 1 ? ' disabled' : '') + '>' + svg('i-arrow-left') + '</button>' +
            '<b aria-label="' + esc(t('pageOf', o.page, pages)) + '">' + o.page + ' / ' + pages + '</b>' +
            '<button type="button" class="btn btn-secondary btn-sm btn-icon" data-go="next" aria-label="' + esc(t('next')) + '"' + (o.page >= pages ? ' disabled' : '') + '>' + svg('i-arrow-right') + '</button></span>';
        box.hidden = !o.total;
        box.querySelectorAll('[data-go]').forEach(function (b) {
            b.addEventListener('click', function () { go(b.dataset.go === 'prev' ? o.page - 1 : o.page + 1); });
        });
    }

    var ORDER_BADGE = { pending: 'badge-warning', paid: 'badge-info', shipped: 'badge-info', completed: 'badge-success', cancelled: 'badge-danger', refunded: 'badge-danger', expired: '' };
    var PRODUCT_BADGE = { active: 'badge-success', draft: 'badge-warning', archived: '', out_of_stock: 'badge-danger' };
    var COUPON_BADGE = { active: 'badge-success', scheduled: 'badge-info', ended: '', disabled: '' };

    /** badge('order' | 'product' | 'coupon', status) → escaped badge HTML. */
    function badge(kind, status) {
        var map = kind === 'order' ? ORDER_BADGE : kind === 'product' ? PRODUCT_BADGE : COUPON_BADGE;
        var prefix = kind === 'order' ? 'st_' : kind === 'product' ? 'ps_' : 'cs_';
        return '<span class="badge ' + (map[status] || '') + '">' + esc(tOr(prefix + status, status || '—')) + '</span>';
    }
    function providerLabel(p) { return p ? tOr('pv_' + p, p) : ''; }
    function methodLabel(m) { return m ? tOr('pm_' + m, m) : ''; }
    function shipLabel(m) { return m ? tOr('sm_' + m, m) : ''; }

    function copy(text) {
        if (!text) return;
        var done = function () { toast(t('copied')); };
        if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(text).then(done, function () { toast(t('requestFailed'), 'error'); });
        }
    }

    /** Sync a plain object into the query string without adding history entries. */
    function setQuery(params) {
        var u = new URL(location.href);
        Object.keys(params).forEach(function (k) {
            var v = params[k];
            if (v === '' || v == null || v === false) u.searchParams.delete(k); else u.searchParams.set(k, v);
        });
        history.replaceState(history.state, '', u.pathname + (u.searchParams.toString() ? '?' + u.searchParams.toString() : '') + u.hash);
    }

    /** Fills #saPrint with already-escaped HTML and opens the print dialog. */
    function printHTML(html) {
        var box = document.getElementById('saPrint');
        if (!box) {
            box = document.createElement('div');
            box.id = 'saPrint';
            box.className = 'sa-print';
            document.body.appendChild(box);
        }
        box.innerHTML = html;
        document.body.classList.add('sa-printing');
        var cleanup = function () { document.body.classList.remove('sa-printing'); root.removeEventListener('afterprint', cleanup); };
        root.addEventListener('afterprint', cleanup);
        root.print();
        setTimeout(cleanup, 1000);
    }

    /** Marks an input invalid with a message under it (aria-describedby). */
    function fieldError(input, message) {
        if (!input) return;
        var id = (input.id || input.name) + 'Err';
        var holder = input.closest('[data-field]') || input.parentElement;
        var el = document.getElementById(id);
        if (!message) {
            input.removeAttribute('aria-invalid');
            if (el) el.remove();
            return;
        }
        input.setAttribute('aria-invalid', 'true');
        if (!el) {
            el = document.createElement('small');
            el.id = id;
            el.className = 'sa-field-error';
            el.setAttribute('role', 'alert');
            holder.appendChild(el);
        }
        el.textContent = message;
        var by = (input.getAttribute('aria-describedby') || '').split(' ').filter(Boolean);
        if (by.indexOf(id) < 0) { by.push(id); input.setAttribute('aria-describedby', by.join(' ')); }
    }

    root.ShopAdmin = {
        lang: lang, isEn: lang === 'en', extend: extend, t: t, tOr: tOr, esc: esc, svg: svg, safeUrl: safeUrl,
        money: money, toInput: toInput, parseAmount: parseAmount, parseInt: parseIntInput,
        date: date, day: day, toLocalInput: toLocalInput, fromLocalInput: fromLocalInput,
        api: api, errText: errText, fieldErrors: fieldErrors, idemKey: idemKey,
        toast: toast, busy: busy, state: state, confirm: confirmSheet, pager: pager,
        badge: badge, providerLabel: providerLabel, methodLabel: methodLabel, shipLabel: shipLabel,
        copy: copy, setQuery: setQuery, printHTML: printHTML, fieldError: fieldError
    };
})(window);
