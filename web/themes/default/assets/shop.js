/*
 * Jelly 預設主題：商店、結帳與會員中心的互動。
 * 不依賴外部套件；所有請求同源、帶登入 Cookie，寫入請求加 X-Jelly-CSRF: 1。
 * 伺服器資料一律以 textContent 寫入畫面，不組 HTML 字串。
 */
(function () {
    'use strict';

    var root = document.querySelector('.shop[data-page]');
    if (!root) return;
    root.classList.add('js');

    var EN = (root.getAttribute('data-lang') || '').toLowerCase() === 'en';
    var CURRENCY = root.getAttribute('data-currency') || 'TWD';
    var TIMEOUT_MS = 15000;

    /* ---------------- 文案 ---------------- */
    var T = {
        added: ['已加入購物車', 'Added to your cart'],
        viewCart: ['看購物車', 'View cart'],
        addFailed: ['沒有加入購物車，請再試一次。', 'Could not add to your cart. Please try again.'],
        offline: ['目前離線，請連線後重試。', 'You are offline. Reconnect and try again.'],
        timeout: ['連線逾時，請重試。', 'The request timed out. Please try again.'],
        network: ['連線失敗，請重試。', 'Could not connect. Please try again.'],
        server: ['伺服器暫時無法完成，請稍後再試。', 'The server could not complete this. Please try again later.'],
        session: ['登入已逾期，請重新登入。', 'Your session expired. Please sign in again.'],
        forbidden: ['這個操作被拒絕，請重新整理頁面後再試。', 'This action was refused. Refresh the page and try again.'],
        notFound: ['找不到資料，可能已被移除。', 'Not found. It may have been removed.'],
        rate: ['操作太頻繁，請稍候再試。', 'Too many attempts. Please wait a moment and try again.'],
        stock: ['庫存不足，購物車已更新，請確認後再送出。', 'Some items are out of stock. Your cart was updated; please check it again.'],
        cartChanged: ['購物車內容剛剛變動，已重新計價，請確認後再送出。', 'Your cart just changed and was re-priced. Please check it and submit again.'],
        couponInvalid: ['折扣碼無效或不符合使用條件。', 'This code is not valid or does not apply to your cart.'],
        couponApplied: ['已套用折扣碼', 'Code applied'],
        couponRemoved: ['已移除折扣碼', 'Code removed'],
        retry: ['重試', 'Retry'],
        cartUnknown: ['目前無法確認購物車狀態。請重試讀取後再繼續操作。', 'The cart state could not be confirmed. Retry loading it before continuing.'],
        remove: ['移除', 'Remove'],
        minus: ['減一', 'Decrease'],
        plus: ['加一', 'Increase'],
        qty: ['數量', 'Quantity'],
        each: ['單價', 'Each'],
        inStock: ['有貨', 'In stock'],
        left: ['剩 {n}', '{n} left'],
        soldOut: ['售完', 'Sold out'],
        addToCart: ['加入購物車', 'Add to cart'],
        chooseVariant: ['請先選擇規格', 'Choose an option first'],
        issue_qty_reduced: ['庫存不足，數量已調整為 {n}。', 'Not enough stock. Quantity changed to {n}.'],
        issue_sold_out: ['已售完，不計入總額。', 'Sold out and not counted in the total.'],
        issue_price_changed: ['價格已更新。', 'The price has changed.'],
        issue_unavailable: ['商品已下架，不計入總額。', 'No longer available and not counted in the total.'],
        issue_other: ['這個項目需要確認。', 'Please review this item.'],
        cartEmpty: ['購物車是空的', 'Your cart is empty'],
        cartEmptyHint: ['去商店挑幾樣喜歡的吧。', 'Find something you like in the shop.'],
        goShop: ['去逛商店', 'Go to the shop'],
        loadFailed: ['無法載入，請重試。', 'Could not load. Please try again.'],
        subtotal: ['小計', 'Subtotal'],
        discount: ['折扣', 'Discount'],
        shipping: ['運費', 'Shipping'],
        roundingAdjustment: ['尾數調整', 'Rounding adjustment'],
        shippingEst: ['運費（預估）', 'Shipping (estimate)'],
        gapToFree: ['再買 {m} 免運', 'Spend {m} more for free shipping'],
        free: ['免運', 'Free'],
        days: ['約 {n} 天', 'About {n} days'],
        later: ['稍後開放', 'Coming soon'],
        cvs: ['超商取貨', 'Convenience store pickup'],
        payTotal: ['前往付款 {m}', 'Continue to payment {m}'],
        placeOrder: ['送出訂單 {m}', 'Place order {m}'],
        placeNoPayment: ['確認訂單（無需付款）', 'Place order (no payment required)'],
        noPaymentRequired: ['這筆訂單無需付款。', 'No payment is required for this order.'],
        submitting: ['送出中…', 'Submitting…'],
        processing: ['處理中…', 'Working…'],
        required: ['請填寫這個欄位。', 'Please fill in this field.'],
        badEmail: ['請輸入有效的 Email。', 'Enter a valid email address.'],
        badPhone: ['請輸入有效的電話號碼。', 'Enter a valid phone number.'],
        badCarrier: ['手機條碼為斜線開頭加 7 碼大寫英數字，例如 /ABC1234。', 'A mobile barcode is a slash and 7 uppercase letters or digits, such as /ABC1234.'],
        badTaxID: ['統一編號格式或檢查碼不正確。', 'This tax ID is not valid.'],
        badDonation: ['捐贈碼為 3 到 7 位數字。', 'A donation code is 3 to 7 digits.'],
        termsRequired: ['請先同意購物須知與退換貨政策。', 'Please accept the terms and return policy.'],
        fixFields: ['有欄位需要修正，請檢查標示的地方。', 'Some fields need attention. Please check the highlighted ones.'],
        noShipping: ['目前沒有可用的運送方式，請聯絡我們。', 'No shipping method is available. Please contact us.'],
        noShippingRequired: ['這筆訂單不需要配送。', 'Shipping is not needed for this order.'],
        noPayment: ['目前沒有可用的付款方式，請聯絡我們。', 'No payment method is available. Please contact us.'],
        pickShipping: ['請選擇運送方式。', 'Choose a shipping method.'],
        pickPayment: ['請選擇付款方式。', 'Choose a payment method.'],
        pm_credit_card: ['信用卡', 'Credit card'],
        pm_atm: ['ATM 轉帳', 'ATM transfer'],
        pm_cvs: ['超商代碼', 'Convenience store code'],
        pm_bank_transfer: ['銀行轉帳', 'Bank transfer'],
        pd_credit_card: ['前往付款頁刷卡', 'Pay by card on the payment page'],
        pd_atm: ['取得虛擬帳號後匯款', 'Get a virtual account number and transfer'],
        pd_cvs: ['取得繳費代碼到超商繳費', 'Get a payment code and pay at a store'],
        pd_bank_transfer: ['下單後依轉帳說明匯款，人工確認', 'Transfer using the instructions; confirmed manually'],
        pv_sandbox: ['測試金流，不會實際扣款', 'Test gateway, no real charge'],
        pv_ecpay: ['綠界 ECPay', 'ECPay'],
        pv_newebpay: ['藍新 NewebPay', 'NewebPay'],
        orderFailed: ['沒有建立訂單，請再試一次。', 'The order was not created. Please try again.'],
        resent: ['已提出重寄，請稍後查看信箱。', 'A resend was requested. Check your inbox shortly.'],
        resendFailed: ['沒有重寄成功，請稍後再試。', 'Could not resend. Please try again later.'],
        statusChanged: ['訂單狀態已更新。', 'The order status was updated.'],
        lookupSent: ['如果訂單編號和 Email 相符，系統會寄出 15 分鐘有效的查詢連結到 {e}。沒收到請看垃圾信匣。', 'If the order number and email match, we will email a lookup link to {e}. It is valid for 15 minutes. Check your spam folder if it does not arrive.'],
        sandboxDone: ['已送出模擬結果，正在回到訂單…', 'Result sent. Returning to your order…'],
        cancelAsk: ['確定取消這筆訂單？已付款的訂單會送出退款申請，等商家確認後安排退款。', 'Cancel this order? For paid orders, this sends a refund request for the merchant to review.'],
        cancelYes: ['確定取消', 'Yes, cancel'],
        keep: ['先不要', 'Keep it'],
        cancelled: ['訂單已取消。', 'The order was cancelled.'],
        refundRequested: ['已提出退款申請，商家確認後會為你安排退款。', 'Refund request sent. The merchant will review it and arrange the refund.'],
        cancelConflict: ['這筆訂單已出貨或狀態已變更，無法取消。', 'This order has shipped or changed and can no longer be cancelled.'],
        reorderSome: ['有 {n} 項商品目前無法購買，其餘已加入購物車。', '{n} items are unavailable; the rest were added to your cart.'],
        reorderAll: ['已把商品加入購物車。', 'The items were added to your cart.'],
        saved: ['已儲存。', 'Saved.'],
        deleted: ['已刪除。', 'Deleted.'],
        deleteAsk: ['確定刪除？', 'Delete this?'],
        deleteYes: ['刪除', 'Delete'],
        edit: ['編輯', 'Edit'],
        setDefault: ['設為預設', 'Make default'],
        isDefault: ['預設', 'Default'],
        noAddresses: ['還沒有儲存的地址。', 'No saved addresses yet.'],
        noProfiles: ['還沒有儲存的發票設定。', 'No saved invoice settings yet.'],
        inv_none: ['個人（Email 收據）', 'Personal (email receipt)'],
        inv_personal_carrier: ['手機條碼載具', 'Mobile barcode carrier'],
        inv_tax_id: ['公司統編', 'Company tax ID'],
        inv_donation: ['捐贈', 'Donation'],
        current: ['目前這台', 'This device'],
        newDevice: ['新裝置', 'New device'],
        revoke: ['登出這台', 'Sign out'],
        revoked: ['已登出該裝置。', 'That device was signed out.'],
        revokedOthers: ['已登出其他裝置。', 'Other devices were signed out.'],
        lastSeen: ['最後使用 {t}', 'Last active {t}'],
        noSessions: ['沒有其他登入中的裝置。', 'No other signed-in devices.'],
        twofaOn: ['已啟用', 'On'],
        twofaOff: ['未啟用', 'Off'],
        recoveryLeft: ['剩 {n} 組備用碼', '{n} recovery codes left'],
        twofaEnabled: ['兩步驟驗證已啟用。請把備用碼存在安全的地方，它們只會顯示這一次。', 'Two-step verification is on. Store these recovery codes safely; they are shown only once.'],
        twofaDisabled: ['兩步驟驗證已停用。', 'Two-step verification is off.'],
        badCode: ['驗證碼不正確或已過期。', 'The code is incorrect or expired.'],
        emailSent: ['已送出。請到新信箱打開確認連結，完成後才會更換。', 'Sent. Open the confirmation link in the new mailbox to finish the change.'],
        emailLink: ['尚未設定寄信，請直接開啟這個確認連結：', 'Email is not configured yet. Open this confirmation link directly:'],
        pendingEmail: ['等待確認：{e}', 'Waiting for confirmation: {e}'],
        passwordChanged: ['密碼已更新，其他裝置已登出。', 'Password updated. Other devices were signed out.'],
        passwordShort: ['新密碼至少 8 個字元。', 'Use at least 8 characters.'],
        wrongPassword: ['目前密碼不正確。', 'The current password is incorrect.'],
        emailTaken: ['這個 Email 已被使用。', 'This email is already in use.'],
        noNotifications: ['目前沒有通知。', 'No notifications.'],
        loadMore: ['載入更多', 'Load more'],
        markRead: ['標為已讀', 'Mark read'],
        allRead: ['已全部標為已讀。', 'All marked as read.'],
        k_order_new: ['收到新訂單', 'A new order was placed'],
        k_order_payment_failed: ['訂單付款失敗', 'An order payment failed'],
        k_security_new_device: ['有新裝置登入', 'Sign-in from a new device'],
        k_security_password_changed: ['密碼已變更', 'Your password was changed'],
        k_comment_reply: ['留言收到回覆', 'Your comment received a reply'],
        k_other: ['通知', 'Notification']
    };
    function t(key, vars) {
        var pair = T[key];
        var text = pair ? pair[EN ? 1 : 0] : key;
        if (vars) Object.keys(vars).forEach(function (k) { text = text.split('{' + k + '}').join(String(vars[k])); });
        return text;
    }

    /* ---------------- 共用工具 ---------------- */
    function money(minor, currency) {
        var cur = String(currency || CURRENCY || 'TWD').trim().toUpperCase();
        var prefix = cur === '' || cur === 'TWD' ? 'NT$' : cur === 'USD' ? 'US$' : cur + ' ';
        var n = Number(minor);
        if (!isFinite(n)) n = 0;
        n = Math.round(n);
        var neg = n < 0;
        var mag = Math.abs(n);
        var whole = Math.floor(mag / 100);
        var frac = mag % 100;
        var out = (neg ? '-' : '') + prefix + String(whole).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
        if (frac) out += '.' + (frac < 10 ? '0' : '') + frac;
        return out;
    }

    function roundWholeMinor(minor) {
        var n = Number(minor);
        return isFinite(n) ? Math.round(n / 100) * 100 : 0;
    }

    function appendRoundingRow(grid, adjustment, currency) {
        var value = Number(adjustment) || 0;
        if (!value) return;
        grid.appendChild(el('span', { text: t('roundingAdjustment') }));
        grid.appendChild(el('b', { text: (value > 0 ? '+' : '') + money(value, currency) }));
    }

    function el(tag, attrs, kids) {
        var node = document.createElement(tag);
        if (attrs) Object.keys(attrs).forEach(function (k) {
            var v = attrs[k];
            if (v === null || v === undefined || v === false) return;
            if (k === 'text') node.textContent = String(v);
            else if (k === 'class') node.className = v;
            else if (k.slice(0, 2) === 'on' && typeof v === 'function') node.addEventListener(k.slice(2), v);
            else node.setAttribute(k, v === true ? '' : String(v));
        });
        (kids || []).forEach(function (kid) {
            if (kid === null || kid === undefined || kid === false) return;
            node.appendChild(typeof kid === 'string' ? document.createTextNode(kid) : kid);
        });
        return node;
    }
    function $(sel, scope) { return (scope || root).querySelector(sel); }
    function $all(sel, scope) { return Array.prototype.slice.call((scope || root).querySelectorAll(sel)); }
    function clear(node) { while (node && node.firstChild) node.removeChild(node.firstChild); }
    function listOf(data) {
        if (Array.isArray(data)) return data;
        if (data && Array.isArray(data.items)) return data.items;
        return [];
    }
    function fmtTime(value) {
        if (!value) return '';
        var d = new Date(value);
        if (isNaN(d.getTime())) return '';
        try {
            return d.toLocaleString(EN ? 'en' : 'zh-TW', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
        } catch (_) { return d.toISOString().slice(0, 16).replace('T', ' '); }
    }
    // Only same-origin paths may be followed or linked from server data.
    function safePath(value, prefixes) {
        if (typeof value !== 'string' || !value) return null;
        try {
            var url = new URL(value, location.origin);
            if (url.origin !== location.origin) return null;
            if (prefixes && !prefixes.some(function (p) { return url.pathname.indexOf(p) === 0; })) return null;
            return url.pathname + url.search + url.hash;
        } catch (_) { return null; }
    }
    function randomKey() {
        var bytes = new Uint8Array(16);
        (window.crypto || window.msCrypto).getRandomValues(bytes);
        return Array.prototype.map.call(bytes, function (b) { return ('0' + b.toString(16)).slice(-2); }).join('');
    }

    // api resolves with {ok, status, data}; it rejects only when no response arrived
    // (err.kind is offline, timeout or network).
    function api(method, url, body) {
        method = (method || 'GET').toUpperCase();
        var headers = { 'Accept': 'application/json' };
        var opts = { method: method, headers: headers, credentials: 'same-origin', cache: 'no-store', redirect: 'follow' };
        if (body !== undefined) {
            headers['Content-Type'] = 'application/json';
            opts.body = JSON.stringify(body);
        }
        if (method !== 'GET' && method !== 'HEAD') headers['X-Jelly-CSRF'] = '1';
        var controller = typeof AbortController === 'function' ? new AbortController() : null;
        var timer = 0;
        if (controller) {
            opts.signal = controller.signal;
            timer = setTimeout(function () { controller.abort(); }, TIMEOUT_MS);
        }
        return fetch(url, opts).then(function (response) {
            clearTimeout(timer);
            return response.text().then(function (text) {
                var data = null;
                if (text) { try { data = JSON.parse(text); } catch (_) { data = null; } }
                return { ok: response.ok, status: response.status, data: data };
            });
        }, function (error) {
            clearTimeout(timer);
            var err = new Error('request failed');
            err.kind = error && error.name === 'AbortError' ? 'timeout' : (navigator.onLine === false ? 'offline' : 'network');
            throw err;
        });
    }
    function errorCode(res) {
        var d = res && res.data;
        return d && typeof d.code === 'string' ? d.code : '';
    }
    // messageFor turns a failed response or a rejected request into display text.
    function messageFor(resOrErr, fallback) {
        if (!resOrErr) return t(fallback || 'server');
        if (resOrErr.kind) return t(resOrErr.kind);
        var s = resOrErr.status;
        var code = errorCode(resOrErr);
        if (code === 'stock') return t('stock');
        if (code === 'cart_changed') return t('cartChanged');
        if (s === 401) return t('session');
        if (s === 403) return t('forbidden');
        if (s === 404) return t('notFound');
        if (s === 422) return t('couponInvalid');
        if (s === 429) return t('rate');
        if (s >= 500) return t('server');
        return t(fallback || 'server');
    }

    var toastTimer = 0;
    function toast(message, type, link) {
        var node = document.getElementById('shop-toast');
        if (!node) {
            node = el('div', { id: 'shop-toast', class: 'shop-toast', role: 'status', 'aria-live': 'polite' });
            document.body.appendChild(node);
        }
        clear(node);
        node.className = 'shop-toast show' + (type === 'error' ? ' error' : '');
        node.appendChild(el('span', { text: message }));
        if (link) node.appendChild(el('a', { href: link.href, text: link.text }));
        clearTimeout(toastTimer);
        toastTimer = setTimeout(function () { node.className = node.className.replace(' show', ''); }, 4500);
    }

    // notice renders a status line in a container: kind is ok, warn, bad or info.
    function notice(container, kind, message, retry) {
        if (!container) return;
        clear(container);
        if (!message) { container.hidden = true; return; }
        container.hidden = false;
        var box = el('div', { class: 'notice ' + (kind || ''), role: kind === 'bad' ? 'alert' : 'status' }, [el('div', { text: message })]);
        if (retry) box.appendChild(el('button', { class: 'btn t sm', type: 'button', text: t('retry'), onclick: retry }));
        container.appendChild(box);
    }

    function busy(button, on, label) {
        if (!button) return;
        if (on) {
            if (!button.hasAttribute('data-label')) button.setAttribute('data-label', button.textContent);
            button.disabled = true;
            button.setAttribute('aria-busy', 'true');
            if (label) button.textContent = label;
        } else {
            button.disabled = false;
            button.removeAttribute('aria-busy');
            if (button.hasAttribute('data-label')) {
                button.textContent = button.getAttribute('data-label');
                button.removeAttribute('data-label');
            }
        }
    }

    function thumb(src, alt, cls) {
        var box = el('span', { class: 'thumb' + (cls ? ' ' + cls : ''), 'aria-hidden': alt ? null : 'true' });
        var path = typeof src === 'string' && src ? src : '';
        if (path && (path.charAt(0) === '/' || /^https?:\/\//i.test(path))) {
            box.appendChild(el('img', { src: path, alt: alt || '', loading: 'lazy' }));
        } else {
            box.textContent = (alt || '').trim().charAt(0);
        }
        return box;
    }

    function qtyInput(value, max, onCommit, label, canCommit) {
        var wrap = el('div', { class: 'qty-box', role: 'group', 'aria-label': label || t('qty') });
        var input = el('input', { type: 'number', inputmode: 'numeric', min: '1', max: String(Math.max(1, max || 99)), value: String(value), 'aria-label': t('qty') });
        function commit(next) {
            if (canCommit && !canCommit()) return;
            var n = parseInt(next, 10);
            var top = Math.max(1, Math.min(99, max || 99));
            if (!isFinite(n) || n < 1) n = 1;
            if (n > top) n = top;
            if (n === value) { input.value = String(value); return; }
            var result = onCommit(n);
            if (!result || typeof result.then !== 'function') input.value = String(n);
        }
        var minus = el('button', { class: 'btn sm i', type: 'button', 'aria-label': t('minus'), text: '−', onclick: function () { commit((parseInt(input.value, 10) || value) - 1); } });
        var plus = el('button', { class: 'btn sm i', type: 'button', 'aria-label': t('plus'), text: '+', onclick: function () { commit((parseInt(input.value, 10) || value) + 1); } });
        if (value <= 1) minus.disabled = true;
        if (max && value >= Math.min(99, max)) plus.disabled = true;
        input.addEventListener('change', function () { commit(input.value); });
        input.addEventListener('keydown', function (e) { if (e.key === 'Enter') { e.preventDefault(); commit(input.value); } });
        wrap.appendChild(minus); wrap.appendChild(input); wrap.appendChild(plus);
        return wrap;
    }

    function addToCart(variantID, qty, button) {
        busy(button, true);
        return api('POST', '/api/cart/items', { variant_id: Number(variantID), qty: Number(qty) || 1 }).then(function (res) {
            busy(button, false);
            if (res.ok) {
                toast(t('added'), 'ok', { href: '/cart', text: t('viewCart') });
                return true;
            }
            toast(messageFor(res, 'addFailed'), 'error');
            return false;
        }, function (err) {
            busy(button, false);
            toast(messageFor(err), 'error');
            return false;
        });
    }

    function confirmInline(container, question, yesLabel, onYes) {
        clear(container);
        container.hidden = false;
        var yes = el('button', { class: 'btn sm danger t', type: 'button', text: yesLabel });
        var no = el('button', { class: 'btn sm x', type: 'button', text: t('keep') });
        var box = el('div', { class: 'confirm', role: 'group' }, [el('span', { text: question }), yes, no]);
        container.appendChild(box);
        no.addEventListener('click', function () { clear(container); container.hidden = true; });
        yes.addEventListener('click', function () { onYes(yes); });
        yes.focus();
    }

    function printButtons() {
        $all('[data-print]').forEach(function (btn) {
            btn.addEventListener('click', function () { window.print(); });
        });
    }

    function paymentLabel(method) { return T['pm_' + method] ? t('pm_' + method) : method; }
    function providerLabel(provider) { return T['pv_' + provider] ? t('pv_' + provider) : ''; }

    /* ---------------- Taiwan invoice checks (server validates again) ---------------- */
    function validTaxID(id) {
        if (!/^\d{8}$/.test(id)) return false;
        var w = [1, 2, 1, 2, 1, 2, 4, 1];
        var sum = 0;
        for (var i = 0; i < 8; i++) {
            var p = Number(id.charAt(i)) * w[i];
            sum += Math.floor(p / 10) + (p % 10);
        }
        return sum % 5 === 0 || (id.charAt(6) === '7' && (sum + 1) % 5 === 0);
    }
    function invoiceErrors(inv) {
        var errs = {};
        if (inv.kind === 'personal_carrier' && !/^\/[0-9A-Z.+-]{7}$/.test(inv.carrier_code || '')) errs.carrier_code = t('badCarrier');
        if (inv.kind === 'tax_id') {
            if (!validTaxID(inv.tax_id || '')) errs.tax_id = t('badTaxID');
            if (!(inv.company_name || '').trim()) errs.company_name = t('required');
        }
        if (inv.kind === 'donation' && !/^\d{3,7}$/.test(inv.donation_code || '')) errs.donation_code = t('badDonation');
        return errs;
    }

    // showFieldErrors marks inputs named in errs (keys are input data-field names) and returns the first one.
    function showFieldErrors(form, errs) {
        var first = null;
        $all('[data-field]', form).forEach(function (input) {
            var key = input.getAttribute('data-field');
            var holder = form.querySelector('[data-err="' + key + '"]');
            if (errs[key]) {
                input.setAttribute('aria-invalid', 'true');
                if (holder) { holder.textContent = errs[key]; holder.hidden = false; }
                if (!first && !input.closest('[hidden]')) first = input;
            } else {
                input.removeAttribute('aria-invalid');
                if (holder) { holder.textContent = ''; holder.hidden = true; }
            }
        });
        return first;
    }
    // serverFieldErrors maps a 400 body ({fields:{...}} or {field:"x"}) to messages keyed by our field names.
    function serverFieldErrors(data, aliases) {
        var out = {};
        if (!data) return out;
        var fields = data.fields && typeof data.fields === 'object' ? data.fields : {};
        if (typeof data.field === 'string') fields[data.field] = data.error || '';
        Object.keys(fields).forEach(function (key) {
            var name = key.split('.').pop();
            if (aliases && aliases[key]) name = aliases[key];
            out[name] = typeof fields[key] === 'string' && fields[key] ? fields[key] : t('required');
        });
        return out;
    }

    /* ======================================================================
       商店列表與分類頁
       ====================================================================== */
    function initListing() {
        var form = $('form[data-filters]');
        if (form) {
            // form.elements also holds controls outside the form tag that use form="…".
            var controls = Array.prototype.slice.call(form.elements).filter(function (c) { return c.name; });
            controls.forEach(function (input) {
                input.addEventListener('change', function () {
                    // Do not send empty values so the address stays short.
                    controls.forEach(function (field) {
                        if ((field.type === 'radio' || field.type === 'checkbox') ? !field.checked : !field.value) field.disabled = true;
                    });
                    form.submit();
                });
            });
        }
        $all('[data-add-variant]').forEach(function (btn) {
            btn.addEventListener('click', function () { addToCart(btn.getAttribute('data-add-variant'), 1, btn); });
        });
    }

    /* ======================================================================
       商品頁
       ====================================================================== */
    function initProduct() {
        var form = $('form[data-product]');
        if (!form) return;
        var radios = $all('input[name="variant"]', form);
        var priceNode = $('[data-show-price]');
        var compareNode = $('[data-show-compare]');
        var stockNode = $('[data-show-stock]');
        var addBtn = $('[data-add]', form);
        var qty = $('input[name="qty"]', form);
        var threshold = parseInt(form.getAttribute('data-low') || '0', 10) || 0;
        var msg = $('[data-buy-msg]');

        function current() {
            if (radios.length === 0) return null;
            var picked = radios.filter(function (r) { return r.checked; })[0];
            return picked || null;
        }
        function update() {
            var r = current();
            // Without options the server already rendered the single variant's state.
            if (!r) { if (radios.length) addBtn.disabled = true; return; }
            var stock = parseInt(r.getAttribute('data-stock'), 10) || 0;
            var price = Number(r.getAttribute('data-price'));
            var compare = Number(r.getAttribute('data-compare') || 0);
            if (priceNode) priceNode.textContent = money(price);
            if (compareNode) {
                compareNode.hidden = !(compare > price);
                compareNode.textContent = compare > price ? money(compare) : '';
            }
            if (stockNode) {
                stockNode.className = 'chip dot ' + (stock <= 0 ? 'bad' : (threshold > 0 && stock <= threshold ? 'warn' : 'ok'));
                stockNode.textContent = stock <= 0 ? t('soldOut') : (threshold > 0 && stock <= threshold ? t('left', { n: stock }) : t('inStock'));
            }
            addBtn.disabled = stock <= 0;
            if (qty) {
                qty.max = String(Math.max(1, Math.min(99, stock)));
                if (Number(qty.value) > Number(qty.max)) qty.value = qty.max;
            }
        }
        radios.forEach(function (r) { r.addEventListener('change', update); });
        $all('[data-step]', form).forEach(function (b) {
            b.addEventListener('click', function () {
                var n = (parseInt(qty.value, 10) || 1) + Number(b.getAttribute('data-step'));
                var top = parseInt(qty.max, 10) || 99;
                qty.value = String(Math.max(1, Math.min(top, n)));
            });
        });
        form.addEventListener('submit', function (e) {
            e.preventDefault();
            var r = current();
            var id = r ? r.value : form.getAttribute('data-variant');
            if (!id) { notice(msg, 'warn', t('chooseVariant')); return; }
            notice(msg, '', '');
            addToCart(id, Math.max(1, parseInt(qty.value, 10) || 1), addBtn).then(update);
        });
        update();

        var main = $('[data-main-image]');
        $all('[data-thumb]').forEach(function (b) {
            b.addEventListener('click', function () {
                if (!main) return;
                var img = main.querySelector('img');
                var pick = b.querySelector('img');
                // The thumbnail src was sanitized by the template; reuse it.
                if (img && pick) img.src = pick.getAttribute('src');
                $all('[data-thumb]').forEach(function (o) { o.setAttribute('aria-pressed', o === b ? 'true' : 'false'); });
            });
        });
        $all('[data-add-variant]').forEach(function (btn) {
            btn.addEventListener('click', function (e) { e.preventDefault(); addToCart(btn.getAttribute('data-add-variant'), 1, btn); });
        });
    }

    /* ======================================================================
       購物車
       ====================================================================== */
    function issueText(issue, item) {
        var key = 'issue_' + (issue && issue.code);
        if (!T[key]) key = 'issue_other';
        return t(key, { n: item ? item.qty : '' });
    }

    function initCart() {
        var listBox = $('[data-cart-items]');
        var sumBox = $('[data-cart-summary]');
        var status = $('[data-cart-status]');
        var couponForm = $('form[data-coupon]');
        var couponMsg = $('[data-coupon-msg]');
        var couponOn = $('[data-coupon-on]');
        var checkoutBtn = $('[data-checkout]');
        var pending = false;
        var unknownState = false;
        var cartCanCheckout = false;

        var carry = null;
        try { carry = sessionStorage.getItem('jelly-shop-cart-note'); sessionStorage.removeItem('jelly-shop-cart-note'); } catch (_) {}
        if (carry) notice(status, 'warn', carry);

        function locked() { return pending || unknownState; }
        function setLockedControls() {
            var disabled = locked();
            listBox.setAttribute('aria-busy', pending ? 'true' : 'false');
            listBox.querySelectorAll('button, input').forEach(function (control) {
                if (disabled) {
                    if (!control.hasAttribute('data-cart-was-disabled')) control.setAttribute('data-cart-was-disabled', control.disabled ? 'true' : 'false');
                    control.disabled = true;
                } else if (control.hasAttribute('data-cart-was-disabled')) {
                    control.disabled = control.getAttribute('data-cart-was-disabled') === 'true';
                    control.removeAttribute('data-cart-was-disabled');
                }
            });
            couponForm.querySelectorAll('button, input').forEach(function (control) {
                if (disabled) {
                    if (!control.hasAttribute('data-cart-was-disabled')) control.setAttribute('data-cart-was-disabled', control.disabled ? 'true' : 'false');
                    control.disabled = true;
                } else if (control.hasAttribute('data-cart-was-disabled')) {
                    control.disabled = control.getAttribute('data-cart-was-disabled') === 'true';
                    control.removeAttribute('data-cart-was-disabled');
                }
            });
            couponOn.querySelectorAll('button').forEach(function (control) {
                if (disabled) {
                    if (!control.hasAttribute('data-cart-was-disabled')) control.setAttribute('data-cart-was-disabled', control.disabled ? 'true' : 'false');
                    control.disabled = true;
                } else if (control.hasAttribute('data-cart-was-disabled')) {
                    control.disabled = control.getAttribute('data-cart-was-disabled') === 'true';
                    control.removeAttribute('data-cart-was-disabled');
                }
            });
            checkoutBtn.setAttribute('aria-disabled', disabled || !cartCanCheckout ? 'true' : 'false');
            if (disabled || !cartCanCheckout) checkoutBtn.removeAttribute('href');
            else checkoutBtn.setAttribute('href', '/checkout');
        }

        function render(cart) {
            var items = Array.isArray(cart.items) ? cart.items : [];
            var issues = Array.isArray(cart.issues) ? cart.issues : [];
            var cur = cart.currency || CURRENCY;
            clear(listBox);
            if (!items.length) {
                $('[data-cart-head]').hidden = true;
                sumBox.hidden = true;
                listBox.appendChild(el('div', { class: 'empty' }, [
                    el('b', { text: t('cartEmpty') }), el('p', { class: 'muted', text: t('cartEmptyHint') }),
                    el('a', { class: 'btn p', href: '/shop', text: t('goShop') })
                ]));
                cartCanCheckout = false;
                setLockedControls();
                return;
            }
            $('[data-cart-head]').hidden = false;
            sumBox.hidden = false;
            items.forEach(function (item) {
                var mine = issues.filter(function (i) { return Number(i.variant_id) === Number(item.variant_id); });
                var row = el('div', { class: 'cart-row' + (item.available === false ? ' off' : '') });
                row.appendChild(thumb(item.image, item.title));
                var info = el('div', { class: 't' }, [
                    el('b', { text: item.title || '' }),
                    el('div', { class: 'sub', text: [item.variant_name, t('each') + ' ' + money(item.unit_price, cur)].filter(Boolean).join(' · ') })
                ]);
                mine.forEach(function (i) { info.appendChild(el('div', { class: 'issue', text: issueText(i, item) })); });
                row.appendChild(info);
                var q = el('div', { class: 'q' });
                if (item.available !== false && (item.max_qty === undefined || item.max_qty > 0)) {
                    q.appendChild(qtyInput(Number(item.qty) || 1, Number(item.max_qty) || 99, function (n) { return change('PATCH', '/api/cart/items/' + encodeURIComponent(item.variant_id), { qty: n }); }, (item.title || '') + ' ' + t('qty'), function () { return !locked(); }));
                }
                row.appendChild(q);
                row.appendChild(el('span', { class: 'line mono', text: money(item.line_total, cur) }));
                row.appendChild(el('button', { class: 'btn x sm i rm', type: 'button', 'aria-label': t('remove') + ' ' + (item.title || ''), title: t('remove'), text: '×', onclick: function () { change('DELETE', '/api/cart/items/' + encodeURIComponent(item.variant_id)); } }));
                listBox.appendChild(row);
            });
            var grid = $('[data-sum-grid]');
            clear(grid);
            grid.appendChild(el('span', { text: t('subtotal') })); grid.appendChild(el('b', { text: money(cart.subtotal, cur) }));
            if (Number(cart.discount) > 0) { grid.appendChild(el('span', { text: t('discount') })); grid.appendChild(el('b', { text: '-' + money(cart.discount, cur) })); }
            grid.appendChild(el('span', { text: t('shipping') })); grid.appendChild(el('b', { text: Number(cart.shipping) > 0 ? money(cart.shipping, cur) : t('free') }));
            appendRoundingRow(grid, cart.rounding_adjustment, cur);
            if (Number(cart.shipping_gap_to_free) > 0) grid.appendChild(el('span', { class: 'gap', text: t('gapToFree', { m: money(cart.shipping_gap_to_free, cur) }) }));
            $('[data-total]').textContent = money(cart.total, cur);
            var coupon = cart.coupon && (cart.coupon.code || typeof cart.coupon === 'string') ? cart.coupon : null;
            couponForm.hidden = !!coupon;
            couponOn.hidden = !coupon;
            if (coupon) {
                $('[data-coupon-code]', couponOn).textContent = typeof coupon === 'string' ? coupon : [coupon.code, coupon.label].filter(Boolean).join(' · ');
            }
            cartCanCheckout = items.some(function (i) { return i.available !== false; });
            setLockedControls();
        }

        function readCart() {
            return api('GET', '/api/cart').then(function (res) {
                if (!res.ok || !res.data || !Array.isArray(res.data.items)) throw new Error('cart read failed');
                return res.data;
            });
        }

        function unknown() {
            pending = false;
            unknownState = true;
            setLockedControls();
            notice(status, 'bad', t('cartUnknown'), retryRead);
        }

        function retryRead() {
            if (pending) return;
            pending = true;
            setLockedControls();
            readCart().then(function (cart) {
                pending = false;
                unknownState = false;
                notice(status, '', '');
                carry = null;
                render(cart);
            }, unknown);
        }

        function load() {
            $('[data-cart-loading]').hidden = true;
            pending = true;
            setLockedControls();
            return readCart().then(function (cart) {
                pending = false;
                unknownState = false;
                if (carry) carry = null; else notice(status, '', '');
                render(cart);
            }, unknown);
        }

        function change(method, url, body, onFailure, onSuccess) {
            if (locked()) return Promise.resolve(false);
            pending = true;
            setLockedControls();
            return api(method, url, body).then(function (res) {
                var succeeded = res.ok;
                var hasCart = res.data && Array.isArray(res.data.items);
                if (succeeded && hasCart) {
                    render(res.data);
                    pending = false;
                    setLockedControls();
                    notice(status, '', '');
                    if (onSuccess) onSuccess();
                    return true;
                }
                return readCart().then(function (cart) {
                    render(cart);
                    pending = false;
                    setLockedControls();
                    if (!succeeded && onFailure) onFailure(res);
                    else if (!succeeded) notice(status, 'bad', messageFor(res), retryRead);
                    else {
                        notice(status, '', '');
                        if (onSuccess) onSuccess();
                    }
                    return succeeded;
                }, function () {
                    if (!succeeded && onFailure) onFailure(res);
                    unknown();
                    return false;
                });
            }, function (err) {
                return readCart().then(function (cart) {
                    render(cart);
                    pending = false;
                    setLockedControls();
                    if (onFailure) onFailure(err);
                    else notice(status, 'bad', messageFor(err), retryRead);
                    return false;
                }, function () {
                    if (onFailure) onFailure(err);
                    unknown();
                    return false;
                });
            });
        }

        couponForm.addEventListener('submit', function (e) {
            e.preventDefault();
            var input = couponForm.querySelector('input[name="code"]');
            var code = input.value.trim();
            if (!code || locked()) { if (!code) input.focus(); return; }
            notice(couponMsg, '', '');
            change('POST', '/api/cart/coupon', { code: code }, function (resOrErr) {
                notice(couponMsg, 'bad', messageFor(resOrErr, 'couponInvalid'));
                if (resOrErr && resOrErr.status === 422) { input.setAttribute('aria-invalid', 'true'); input.focus(); }
            }, function () { input.value = ''; toast(t('couponApplied')); });
        });
        couponForm.querySelector('input').addEventListener('input', function (e) { e.target.removeAttribute('aria-invalid'); });
        $('[data-coupon-remove]').addEventListener('click', function () {
            if (locked()) return;
            change('DELETE', '/api/cart/coupon', undefined, function (resOrErr) { notice(couponMsg, 'bad', messageFor(resOrErr)); }, function () { toast(t('couponRemoved')); });
        });
        checkoutBtn.addEventListener('click', function (e) { if (locked() || !cartCanCheckout || checkoutBtn.getAttribute('aria-disabled') === 'true') e.preventDefault(); });
        load();
    }

    /* ======================================================================
       結帳
       ====================================================================== */
    function initCheckout() {
        var form = $('form[data-checkout-form]');
        var status = $('[data-co-status]');
        var shipBox = $('[data-ship-options]');
        var payBox = $('[data-pay-options]');
        var addrBox = $('[data-address]');
        var submit = $('[data-submit]');
        var invKind = form.querySelector('[name="invoice_kind"]');
        var sum = { cart: null, methods: [], payments: [] };
        var KEY_STORE = 'jelly-shop-checkout-key';
        var sending = false;
        var prefillStopped = false;

        function clearKey() {
            try { sessionStorage.removeItem(KEY_STORE); } catch (_) {}
            form.removeAttribute('data-key');
        }
        function idemKey(reset) {
            var key = '';
            try {
                if (reset) sessionStorage.removeItem(KEY_STORE);
                key = sessionStorage.getItem(KEY_STORE) || '';
                if (!key) { key = randomKey(); sessionStorage.setItem(KEY_STORE, key); }
            } catch (_) {
                if (reset || !form.getAttribute('data-key')) form.setAttribute('data-key', randomKey());
                key = form.getAttribute('data-key');
            }
            return key;
        }

        function selectedMethod() {
            var r = shipBox.querySelector('input[name="shipping_method"]:checked');
            if (!r) return null;
            return sum.methods.filter(function (m) { return String(m.id) === r.value; })[0] || null;
        }
        function requiresShipping() {
            // The server always sends this boolean. Treat a missing field as unknown/required.
            return !sum.cart || sum.cart.requires_shipping !== false;
        }
        function estShipping() {
            var cart = sum.cart;
            var m = selectedMethod();
            if (!cart) return 0;
            if (cart.requires_shipping === false) return 0;
            var subtotal = Number(cart.subtotal) || 0;
            var selectedFreeOver = m ? (Number(m.free_over) || Number(m.FreeOver) || 0) : 0;
            var globalFreeOver = Number(cart.free_shipping_over) || 0;
            if (cart.coupon && cart.coupon.free_shipping === true) return 0;
            if ((globalFreeOver > 0 && subtotal >= globalFreeOver) ||
                (selectedFreeOver > 0 && subtotal >= selectedFreeOver)) return 0;
            if (!m) return 0;
            return Number(m.fee) || 0;
        }
        function exactTotal() {
            var cart = sum.cart;
            if (!cart) return 0;
            return Math.max(0, (Number(cart.subtotal) || 0) - (Number(cart.discount) || 0) + estShipping());
        }
        function isZeroOrder() {
            return !!sum.cart && roundWholeMinor(exactTotal()) === 0;
        }
        function renderSummary() {
            var cart = sum.cart;
            if (!cart) return;
            var cur = cart.currency || CURRENCY;
            var box = $('[data-sum-items]');
            clear(box);
            (cart.items || []).filter(function (i) { return i.available !== false; }).forEach(function (item) {
                box.appendChild(el('div', null, [
                    thumb(item.image, item.title),
                    el('span', null, [el('b', { text: item.title || '' }), el('div', { class: 'small muted', text: [item.variant_name, '× ' + item.qty].filter(Boolean).join(' · ') })]),
                    el('b', { text: money(item.line_total, cur) })
                ]));
            });
            var grid = $('[data-sum-grid]');
            clear(grid);
            var ship = estShipping();
            var exact = exactTotal();
            var total = roundWholeMinor(exact);
            var adjustment = total - exact;
            grid.appendChild(el('span', { text: t('subtotal') })); grid.appendChild(el('b', { text: money(cart.subtotal, cur) }));
            if (Number(cart.discount) > 0) {
                var code = cart.coupon && cart.coupon.code ? ' (' + cart.coupon.code + ')' : '';
                grid.appendChild(el('span', { text: t('discount') + code })); grid.appendChild(el('b', { text: '-' + money(cart.discount, cur) }));
            }
            var m = selectedMethod();
            grid.appendChild(el('span', { text: m && m.label ? t('shippingEst') + ' · ' + m.label : t('shippingEst') }));
            grid.appendChild(el('b', { text: ship > 0 ? money(ship, cur) : t('free') }));
            appendRoundingRow(grid, adjustment, cur);
            $('[data-total]').textContent = money(total, cur);
            var pay = payBox.querySelector('input[name="payment_method"]:checked');
            submit.textContent = t(pay && pay.getAttribute('data-kind') === 'none' ? 'placeNoPayment' : pay && pay.getAttribute('data-kind') === 'offline' ? 'placeOrder' : 'payTotal', { m: money(total, cur) });
        }

        function renderPaymentOptions() {
            clear(payBox);
            var zeroOrder = isZeroOrder();
            if (zeroOrder) {
                payBox.appendChild(el('input', { type: 'radio', name: 'payment_method', value: 'none', 'data-kind': 'none', checked: true, hidden: true }));
                payBox.appendChild(el('div', { class: 'notice', text: t('noPaymentRequired') }));
            } else if (!sum.payments.length) payBox.appendChild(el('div', { class: 'notice warn', text: t('noPayment') }));
            (zeroOrder ? [] : sum.payments).forEach(function (p, i) {
                var method = p.method || p.id;
                var kind = p.kind || (method === 'bank_transfer' ? 'offline' : 'redirect');
                var input = el('input', { type: 'radio', name: 'payment_method', value: method, 'data-kind': kind, 'data-field': 'payment' });
                if (i === 0) input.checked = true;
                var sub = [T['pd_' + method] ? t('pd_' + method) : '', providerLabel(p.provider)].filter(Boolean).join(' · ');
                payBox.appendChild(el('label', { class: 'opt' }, [input, el('span', { class: 'rad' }), el('span', null, [el('b', { text: p.label || paymentLabel(method) }), el('span', { class: 'sub', text: sub })])]));
            });
            $all('input[name="payment_method"]', payBox).forEach(function (r) { r.addEventListener('change', renderSummary); });
        }
        function renderMethods() {
            clear(shipBox);
            var methods = sum.methods;
            if (sum.cart && sum.cart.requires_shipping === false) {
                shipBox.appendChild(el('div', { class: 'notice info', text: t('noShippingRequired') }));
                toggleAddress();
                renderPaymentOptions();
                renderSummary();
                return;
            }
            if (!methods.length) { shipBox.appendChild(el('div', { class: 'notice warn', text: t('noShipping') })); }
            methods.forEach(function (m, i) {
                var fee = Number(m.fee) > 0 ? money(m.fee) : t('free');
                var sub = [fee, Number(m.eta_days) > 0 ? t('days', { n: m.eta_days }) : '', m.note || ''].filter(Boolean).join(' · ');
                var input = el('input', { type: 'radio', name: 'shipping_method', value: String(m.id), 'data-field': 'method' });
                if (i === 0) input.checked = true;
                shipBox.appendChild(el('label', { class: 'opt' }, [input, el('span', { class: 'rad' }), el('span', null, [el('b', { text: m.label || String(m.id) }), el('span', { class: 'sub', text: sub })])]));
            });
            if (!methods.some(function (m) { return m.id === 'cvs'; })) {
                shipBox.appendChild(el('div', { class: 'opt off', 'aria-disabled': 'true' }, [el('span', { class: 'rad' }), el('span', null, [el('b', { text: t('cvs') }), el('span', { class: 'sub' }, [el('span', { class: 'chip', text: t('later') })])])]));
            }
            $all('input[name="shipping_method"]', shipBox).forEach(function (r) { r.addEventListener('change', function () { toggleAddress(); renderPaymentOptions(); renderSummary(); }); });
            toggleAddress();
            renderPaymentOptions();
        }
        function toggleAddress() {
            var m = selectedMethod();
            var need = requiresShipping() && (!m || m.requires_address !== false);
            addrBox.hidden = !need;
            $all('input', addrBox).forEach(function (i) { i.required = need && i.hasAttribute('data-required'); });
        }
        function toggleInvoice() {
            var kind = invKind.value;
            $all('[data-inv]', form).forEach(function (box) { box.hidden = box.getAttribute('data-inv') !== kind; });
        }
        invKind.addEventListener('change', toggleInvoice);
        toggleInvoice();

        function prefillMember() {
            var addressTouched = Object.create(null);
            var invoiceTouched = false;
            ['name', 'phone', 'postcode', 'city', 'address'].forEach(function (name) {
                var input = form.querySelector('[name="' + name + '"]');
                if (!input) return;
                ['input', 'change'].forEach(function (eventName) {
                    input.addEventListener(eventName, function () { addressTouched[name] = true; });
                });
            });
            ['invoice_kind', 'carrier_code', 'tax_id', 'company_name', 'donation_code'].forEach(function (name) {
                var input = form.querySelector('[name="' + name + '"]');
                if (!input) return;
                ['input', 'change'].forEach(function (eventName) {
                    input.addEventListener(eventName, function () { invoiceTouched = true; });
                });
            });
            api('GET', '/api/account/addresses').then(function (res) {
                if (prefillStopped || !res.ok) return;
                var list = listOf(res.data);
                var a = list.filter(function (x) { return x.is_default; })[0] || list[0];
                if (!a) return;
                [['name', a.recipient], ['postcode', a.postcode], ['city', a.city], ['address', a.address], ['phone', a.phone]].forEach(function (p) {
                    var input = form.querySelector('[name="' + p[0] + '"]');
                    if (!prefillStopped && !addressTouched[p[0]] && input && !input.value && p[1]) input.value = p[1];
                });
            }, function () {});
            api('GET', '/api/account/invoice-profiles').then(function (res) {
                if (prefillStopped || invoiceTouched || !res.ok) return;
                var list = listOf(res.data);
                var p = list.filter(function (x) { return x.is_default; })[0] || list[0];
                if (!p || !p.kind) return;
                invKind.value = p.kind;
                [['carrier_code', p.carrier_code], ['tax_id', p.tax_id], ['company_name', p.company_name], ['donation_code', p.donation_code]].forEach(function (f) {
                    var input = form.querySelector('[name="' + f[0] + '"]');
                    if (!prefillStopped && !invoiceTouched && input && !input.value && f[1]) input.value = f[1];
                });
                if (!prefillStopped && !invoiceTouched) toggleInvoice();
            }, function () {});
        }

        function loadAll() {
            notice(status, '', '');
            $('[data-co-loading]').hidden = false;
            form.hidden = true;
            return Promise.all([api('GET', '/api/cart'), api('GET', '/api/shop/shipping-methods'), api('GET', '/api/shop/payment-methods')]).then(function (r) {
                $('[data-co-loading]').hidden = true;
                var bad = r.filter(function (x) { return !x.ok; })[0];
                if (bad) { notice(status, 'bad', messageFor(bad, 'loadFailed'), loadAll); return; }
                sum.cart = r[0].data || {};
                if (!(sum.cart.items || []).some(function (i) { return i.available !== false; })) { location.replace('/cart'); return; }
                sum.methods = listOf(r[1].data).filter(function (m) { return m && m.enabled !== false; });
                sum.payments = listOf(r[2].data).filter(function (p) { return p && p.enabled !== false; });
                form.hidden = false;
                renderMethods();
                renderSummary();
            }, function (err) {
                $('[data-co-loading]').hidden = true;
                notice(status, 'bad', messageFor(err), loadAll);
            });
        }
        function reloadCart() {
            return api('GET', '/api/cart').then(function (res) {
                if (res.ok && res.data) { sum.cart = res.data; renderSummary(); }
            }, function () {});
        }

        function collect() {
            var v = function (name) { var i = form.querySelector('[name="' + name + '"]'); return i ? i.value.trim() : ''; };
            var kind = invKind.value;
            var invoice = { kind: kind };
            if (kind === 'personal_carrier') invoice.carrier_code = v('carrier_code').toUpperCase();
            if (kind === 'tax_id') { invoice.tax_id = v('tax_id'); invoice.company_name = v('company_name'); }
            if (kind === 'donation') invoice.donation_code = v('donation_code');
            var shippingRequired = requiresShipping();
            var m = shippingRequired ? selectedMethod() : null;
            var pay = payBox.querySelector('input[name="payment_method"]:checked');
            var shipping = { method: m ? String(m.id) : '', name: shippingRequired ? v('name') : '' };
            if (shippingRequired && (!m || m.requires_address !== false)) { shipping.postcode = v('postcode'); shipping.city = v('city'); shipping.address = v('address'); }
            var create = form.querySelector('[name="create_account"]');
            return {
                email: v('email'), phone: v('phone'),
                create_account: !!(create && create.checked && !create.disabled),
                shipping: shipping,
                payment: { method: pay ? pay.value : '' },
                invoice: invoice,
                coupon_code: sum.cart && sum.cart.coupon && sum.cart.coupon.code ? sum.cart.coupon.code : '',
                accept_terms: !!form.querySelector('[name="accept_terms"]').checked,
                idempotency_key: idemKey(false),
                website: v('website')
            };
        }
        function validate(body) {
            var errs = invoiceErrors(body.invoice);
            var emailInput = form.querySelector('[name="email"]');
            if (!body.email) errs.email = t('required');
            else if (!emailInput.checkValidity()) errs.email = t('badEmail');
            var digits = body.phone.replace(/[\s()+-]/g, '');
            if (!body.phone) errs.phone = t('required');
            else if (!/^\d{8,15}$/.test(digits)) errs.phone = t('badPhone');
            if (requiresShipping() && !body.shipping.name) errs.name = t('required');
            if (requiresShipping() && !body.shipping.method) errs.method = t('pickShipping');
            if (!body.payment.method) errs.payment = t('pickPayment');
            if (requiresShipping() && !addrBox.hidden) ['postcode', 'city', 'address'].forEach(function (k) { if (!body.shipping[k]) errs[k] = t('required'); });
            if (!body.accept_terms) errs.accept_terms = t('termsRequired');
            return errs;
        }
        var ALIASES = { 'shipping.method': 'method', 'payment.method': 'payment', 'shipping.name': 'name', 'invoice.kind': 'invoice_kind' };

        form.addEventListener('submit', function (e) {
            e.preventDefault();
            if (sending) return;
            var body = collect();
            var errs = validate(body);
            var first = showFieldErrors(form, errs);
            if (first) { notice(status, 'bad', t('fixFields')); first.focus(); return; }
            sending = true;
            prefillStopped = true;
            busy(submit, true, t('submitting'));
            notice(status, '', '');
            api('POST', '/api/checkout', body).then(function (res) {
                if (res.status === 201 || (res.ok && res.data && res.data.order)) {
                    clearKey();
                    var d = res.data || {};
                    var target = safePath(d.redirect_url);
                    if (!target && d.order && d.order.number) {
                        target = '/checkout/done/' + encodeURIComponent(d.order.number) + (d.access_token ? '?t=' + encodeURIComponent(d.access_token) : '');
                    }
                    location.assign(target || '/cart');
                    return;
                }
                sending = false;
                busy(submit, false);
                renderSummary();
                if (res.status === 400) {
                    var f = showFieldErrors(form, serverFieldErrors(res.data, ALIASES));
                    notice(status, 'bad', t('fixFields'));
                    if (f) f.focus();
                    return;
                }
                if (res.status === 409 || res.status === 422) {
                    idemKey(true);
                    notice(status, 'warn', messageFor(res));
                    reloadCart();
                    return;
                }
                notice(status, 'bad', messageFor(res, 'orderFailed'));
            }, function (err) {
                // No response: keep the same idempotency key so a retry cannot create a second order.
                sending = false;
                busy(submit, false);
                renderSummary();
                notice(status, 'bad', messageFor(err));
            });
        });
        $all('[data-field]', form).forEach(function (input) {
            input.addEventListener('input', function () { input.removeAttribute('aria-invalid'); });
        });

        if (root.getAttribute('data-member') === '1') prefillMember();
        loadAll();
    }

    /* ======================================================================
       付款結果
       ====================================================================== */
    function initDone() {
        var number = root.getAttribute('data-number');
        var access = root.getAttribute('data-access') || '';
        var state = root.getAttribute('data-status');
        var payStatus = root.getAttribute('data-payment-status');
        var status = $('[data-done-status]');
        printButtons();
        if (!number) return;
        var q = access ? '?t=' + encodeURIComponent(access) : '';

        // A redirect payment may confirm a little later than the return; poll a short while.
        if (state === 'pending' && payStatus !== 'failed' && root.getAttribute('data-kind') === 'redirect') {
            var tries = 0;
            var poll = function () {
                tries++;
                api('GET', '/api/orders/' + encodeURIComponent(number) + '/status' + q).then(function (res) {
                    var d = res.ok && res.data ? res.data : null;
                    if (d && ((d.status && d.status !== state) || (d.payment_status && d.payment_status !== payStatus))) {
                        toast(t('statusChanged'));
                        location.reload();
                        return;
                    }
                    if (tries < 20) setTimeout(poll, 3000);
                }, function () { if (tries < 20) setTimeout(poll, 5000); });
            };
            setTimeout(poll, 2000);
        }

        var retryForm = $('form[data-retry]');
        if (retryForm) {
            var box = $('[data-retry-methods]', retryForm);
            var current = root.getAttribute('data-method');
            var loadMethods = function () {
                clear(box);
                box.appendChild(el('span', { class: 'sk' }));
                api('GET', '/api/shop/payment-methods').then(function (res) {
                    clear(box);
                    if (!res.ok) { notice(status, 'bad', messageFor(res, 'loadFailed'), loadMethods); return; }
                    listOf(res.data).filter(function (p) { return p && p.enabled !== false; }).forEach(function (p) {
                        var method = p.method || p.id;
                        var input = el('input', { type: 'radio', name: 'method', value: method });
                        if (method === current) input.checked = true;
                        box.appendChild(el('label', { class: 'opt' }, [input, el('span', { class: 'rad' }), el('span', null, [el('b', { text: p.label || paymentLabel(method) }), el('span', { class: 'sub', text: providerLabel(p.provider) })])]));
                    });
                    if (!box.querySelector('input:checked')) { var f = box.querySelector('input'); if (f) f.checked = true; }
                }, function (err) { clear(box); notice(status, 'bad', messageFor(err), loadMethods); });
            };
            loadMethods();
            $all('[data-retry-open]').forEach(function (b) {
                b.addEventListener('click', function () {
                    retryForm.hidden = false;
                    var same = b.getAttribute('data-retry-open') === 'same';
                    if (same) { retryForm.requestSubmit ? retryForm.requestSubmit() : retryForm.dispatchEvent(new Event('submit', { cancelable: true })); }
                    else { var first = retryForm.querySelector('input'); if (first) first.focus(); }
                });
            });
            retryForm.addEventListener('submit', function (e) {
                e.preventDefault();
                var picked = retryForm.querySelector('input[name="method"]:checked');
                var method = picked ? picked.value : current;
                var btn = retryForm.querySelector('button[type="submit"]');
                busy(btn, true, t('processing'));
                api('POST', '/api/checkout/retry', { number: number, access: access, method: method }).then(function (res) {
                    if (res.ok) {
                        var target = safePath(res.data && res.data.redirect_url);
                        if (target) location.assign(target); else location.reload();
                        return;
                    }
                    busy(btn, false);
                    notice(status, 'bad', messageFor(res, 'orderFailed'));
                }, function (err) { busy(btn, false); notice(status, 'bad', messageFor(err)); });
            });
        }

        var resend = $('[data-resend]');
        if (resend) resend.addEventListener('click', function () {
            busy(resend, true);
            api('POST', '/api/orders/' + encodeURIComponent(number) + '/resend', { email: root.getAttribute('data-email') || '', access: access }).then(function (res) {
                busy(resend, false);
                if (res.ok) notice(status, 'ok', t('resent'));
                else notice(status, 'bad', messageFor(res, 'resendFailed'));
            }, function (err) { busy(resend, false); notice(status, 'bad', messageFor(err)); });
        });
    }

    /* ======================================================================
       沙盒付款
       ====================================================================== */
    function initSandbox() {
        var trade = root.getAttribute('data-trade');
        var status = $('[data-sandbox-status]');
        var buttons = $all('[data-sandbox]');
        buttons.forEach(function (btn) {
            btn.addEventListener('click', function () {
                buttons.forEach(function (b) { b.disabled = true; });
                busy(btn, true, t('processing'));
                api('POST', '/api/checkout/sandbox/' + encodeURIComponent(trade), { status: btn.getAttribute('data-sandbox') }).then(function (res) {
                    if (res.ok) {
                        notice(status, 'ok', t('sandboxDone'));
                        var target = safePath(res.data && res.data.redirect_url);
                        location.assign(target || '/');
                        return;
                    }
                    buttons.forEach(function (b) { b.disabled = false; }); busy(btn, false);
                    notice(status, 'bad', messageFor(res));
                }, function (err) {
                    buttons.forEach(function (b) { b.disabled = false; }); busy(btn, false);
                    notice(status, 'bad', messageFor(err));
                });
            });
        });
    }

    /* ======================================================================
       訂單查詢
       ====================================================================== */
    function maskEmail(email) {
        var at = email.indexOf('@');
        if (at < 1) return email;
        return email.slice(0, Math.min(2, at)) + '•••' + email.slice(at);
    }
    function initLookup() {
        var form = $('form[data-lookup]');
        var status = $('[data-lookup-status]');
        var sent = $('[data-lookup-sent]');
        form.addEventListener('submit', function (e) {
            e.preventDefault();
            var number = form.querySelector('[name="number"]').value.trim().toUpperCase();
            var emailInput = form.querySelector('[name="email"]');
            var email = emailInput.value.trim();
            var errs = {};
            if (!number) errs.number = t('required');
            if (!email) errs.email = t('required'); else if (!emailInput.checkValidity()) errs.email = t('badEmail');
            var first = showFieldErrors(form, errs);
            if (first) { first.focus(); return; }
            var btn = form.querySelector('button[type="submit"]');
            busy(btn, true, t('submitting'));
            notice(status, '', '');
            api('POST', '/api/orders/lookup', { number: number, email: email, website: form.querySelector('[name="website"]').value }).then(function (res) {
                busy(btn, false);
                if (res.ok) {
                    sent.hidden = false;
                    $('[data-sent-text]', sent).textContent = t('lookupSent', { e: maskEmail(email) });
                    sent.focus();
                    return;
                }
                notice(status, 'bad', messageFor(res));
            }, function (err) { busy(btn, false); notice(status, 'bad', messageFor(err)); });
        });
    }

    /* ======================================================================
       會員：訂單詳情
       ====================================================================== */
    function initAccountOrder() {
        printButtons();
        var number = root.getAttribute('data-number');
        var status = $('[data-order-status]');
        if (!number) return;
        var cancelBtn = $('[data-cancel]');
        var confirmBox = $('[data-cancel-confirm]');
        if (cancelBtn) cancelBtn.addEventListener('click', function () {
            confirmInline(confirmBox, t('cancelAsk'), t('cancelYes'), function (yes) {
                busy(yes, true);
                api('POST', '/api/account/orders/' + encodeURIComponent(number) + '/cancel').then(function (res) {
                    if (res.ok) {
                        toast(res.data && res.data.status === 'refund_requested' ? t('refundRequested') : t('cancelled'));
                        location.reload();
                        return;
                    }
                    busy(yes, false);
                    notice(status, 'bad', res.status === 409 ? t('cancelConflict') : messageFor(res));
                }, function (err) { busy(yes, false); notice(status, 'bad', messageFor(err)); });
            });
        });
        var reorder = $('[data-reorder]');
        if (reorder) reorder.addEventListener('click', function () {
            busy(reorder, true);
            api('POST', '/api/account/orders/' + encodeURIComponent(number) + '/reorder').then(function (res) {
                if (res.ok) {
                    var gone = res.data && Array.isArray(res.data.unavailable) ? res.data.unavailable.length : 0;
                    try { sessionStorage.setItem('jelly-shop-cart-note', gone ? t('reorderSome', { n: gone }) : t('reorderAll')); } catch (_) {}
                    location.assign('/cart');
                    return;
                }
                busy(reorder, false);
                notice(status, 'bad', messageFor(res));
            }, function (err) { busy(reorder, false); notice(status, 'bad', messageFor(err)); });
        });
    }

    function initAccountLogout() {
        document.querySelectorAll('form[action="/auth/logout"]').forEach(function (form) {
            var button = form.querySelector('button[type="submit"]');
            var status = document.createElement('div');
            status.hidden = true;
            form.parentNode.insertBefore(status, form.nextSibling);
            var pending = false;
            function submitLogout() {
                if (pending) return;
                pending = true;
                notice(status, '', '');
                busy(button, true, t('processing'));
                api('POST', form.action).then(function (res) {
                    if (res.ok) {
                        var lang = (root.getAttribute('data-lang') || '').toLowerCase();
                        var destination = '/login';
                        if (lang === 'en') destination += '?lang=en';
                        else if (lang === 'zh-tw') destination += '?lang=zh-TW';
                        window.location.assign(destination);
                        return;
                    }
                    pending = false;
                    busy(button, false);
                    notice(status, 'bad', messageFor(res), submitLogout);
                }, function (err) {
                    pending = false;
                    busy(button, false);
                    notice(status, 'bad', messageFor(err), submitLogout);
                });
            }
            form.addEventListener('submit', function (event) {
                event.preventDefault();
                submitLogout();
            });
        });
    }

    /* ======================================================================
       會員：地址與發票設定（同一套清單＋表單）
       ====================================================================== */
    function initCrud(opts) {
        var listBox = $('[data-list]');
        var status = $('[data-list-status]');
        var form = $('form[data-editor]');
        var formTitle = $('[data-editor-title]');
        var addBtn = $('[data-new]');
        var editing = null;

        function openForm(item) {
            editing = item || null;
            form.reset();
            showFieldErrors(form, {});
            opts.fill(form, item || {});
            formTitle.textContent = formTitle.getAttribute(item ? 'data-edit' : 'data-new');
            form.hidden = false;
            var first = form.querySelector('input:not([type=hidden]), select');
            if (first) first.focus();
        }
        function closeForm() { form.hidden = true; editing = null; addBtn.focus(); }

        function render(items) {
            clear(listBox);
            if (!items.length) { listBox.appendChild(el('div', { class: 'empty' }, [el('b', { text: t(opts.emptyKey) })])); return; }
            items.forEach(function (item) {
                var acts = el('div', { class: 'acts' });
                var confirmBox = el('div', { hidden: true });
                acts.appendChild(el('button', { class: 'btn t sm', type: 'button', text: t('edit'), onclick: function () { openForm(item); } }));
                if (!item.is_default) acts.appendChild(el('button', { class: 'btn t sm', type: 'button', text: t('setDefault'), onclick: function (e) { save(item, Object.assign(opts.payload(item), { is_default: true }), e.currentTarget); } }));
                acts.appendChild(el('button', { class: 'btn x sm danger', type: 'button', text: t('deleteYes'), onclick: function () {
                    confirmInline(confirmBox, t('deleteAsk'), t('deleteYes'), function (yes) {
                        busy(yes, true);
                        api('DELETE', opts.url + '/' + encodeURIComponent(item.id)).then(function (res) {
                            if (res.ok) { toast(t('deleted')); load(); return; }
                            busy(yes, false); notice(status, 'bad', messageFor(res));
                        }, function (err) { busy(yes, false); notice(status, 'bad', messageFor(err)); });
                    });
                } }));
                var head = el('div', { style: 'display:flex;gap:.5rem;align-items:center;flex-wrap:wrap' }, [el('b', { text: opts.title(item) })]);
                if (item.is_default) head.appendChild(el('span', { class: 'chip ok', text: t('isDefault') }));
                listBox.appendChild(el('div', { class: 'box' }, [head, el('div', { class: 'small muted', style: 'white-space:pre-line', text: opts.detail(item) }), acts, confirmBox]));
            });
        }
        function load() {
            listBox.setAttribute('aria-busy', 'true');
            notice(status, '', '');
            return api('GET', opts.url).then(function (res) {
                listBox.removeAttribute('aria-busy');
                if (!res.ok) { clear(listBox); notice(status, 'bad', messageFor(res, 'loadFailed'), load); return; }
                render(listOf(res.data));
            }, function (err) { listBox.removeAttribute('aria-busy'); clear(listBox); notice(status, 'bad', messageFor(err), load); });
        }
        function save(item, body, button) {
            busy(button, true);
            var req = item ? api('PUT', opts.url + '/' + encodeURIComponent(item.id), body) : api('POST', opts.url, body);
            return req.then(function (res) {
                busy(button, false);
                if (res.ok) { toast(t('saved')); if (!form.hidden && button && form.contains(button)) closeForm(); load(); return; }
                if (res.status === 400) {
                    var f = showFieldErrors(form, serverFieldErrors(res.data));
                    if (f && !form.hidden) { f.focus(); return; }
                }
                notice(status, 'bad', messageFor(res));
            }, function (err) { busy(button, false); notice(status, 'bad', messageFor(err)); });
        }
        addBtn.addEventListener('click', function () { openForm(null); });
        $('[data-editor-cancel]').addEventListener('click', closeForm);
        form.addEventListener('submit', function (e) {
            e.preventDefault();
            var body = opts.read(form);
            var first = showFieldErrors(form, opts.validate(body));
            if (first) { first.focus(); return; }
            save(editing, body, form.querySelector('button[type="submit"]'));
        });
        if (opts.onInit) opts.onInit(form);
        load();
    }
    function fieldValue(form, name) { var i = form.querySelector('[name="' + name + '"]'); return i ? (i.type === 'checkbox' ? i.checked : i.value.trim()) : ''; }
    function setField(form, name, value) { var i = form.querySelector('[name="' + name + '"]'); if (!i) return; if (i.type === 'checkbox') i.checked = !!value; else i.value = value == null ? '' : value; }

    function initAddresses() {
        var keys = ['label', 'recipient', 'phone', 'postcode', 'city', 'address', 'is_default'];
        initCrud({
            url: '/api/account/addresses', emptyKey: 'noAddresses',
            title: function (a) { return [a.label, a.recipient].filter(Boolean).join(' · '); },
            detail: function (a) { return [a.phone, [a.postcode, a.city, a.address].filter(Boolean).join(' ')].filter(Boolean).join('\n'); },
            payload: function (a) { var o = {}; keys.forEach(function (k) { o[k] = a[k]; }); return o; },
            fill: function (form, a) { keys.forEach(function (k) { setField(form, k, a[k]); }); },
            read: function (form) { var o = {}; keys.forEach(function (k) { o[k] = fieldValue(form, k); }); return o; },
            validate: function (o) {
                var e = {};
                ['recipient', 'phone', 'city', 'address'].forEach(function (k) { if (!o[k]) e[k] = t('required'); });
                if (o.phone && !/^\d{8,15}$/.test(o.phone.replace(/[\s()+-]/g, ''))) e.phone = t('badPhone');
                return e;
            }
        });
    }
    function initInvoices() {
        var keys = ['kind', 'carrier_code', 'tax_id', 'company_name', 'donation_code', 'is_default'];
        initCrud({
            url: '/api/account/invoice-profiles', emptyKey: 'noProfiles',
            title: function (p) { return T['inv_' + p.kind] ? t('inv_' + p.kind) : p.kind; },
            detail: function (p) { return [p.carrier_code, [p.tax_id, p.company_name].filter(Boolean).join(' '), p.donation_code].filter(Boolean).join('\n'); },
            payload: function (p) { var o = {}; keys.forEach(function (k) { o[k] = p[k]; }); return o; },
            fill: function (form, p) { keys.forEach(function (k) { setField(form, k, p[k]); }); if (!p.kind) setField(form, 'kind', 'personal_carrier'); toggle(form); },
            read: function (form) {
                var o = { kind: fieldValue(form, 'kind'), is_default: fieldValue(form, 'is_default') };
                if (o.kind === 'personal_carrier') o.carrier_code = String(fieldValue(form, 'carrier_code')).toUpperCase();
                if (o.kind === 'tax_id') { o.tax_id = fieldValue(form, 'tax_id'); o.company_name = fieldValue(form, 'company_name'); }
                if (o.kind === 'donation') o.donation_code = fieldValue(form, 'donation_code');
                return o;
            },
            validate: invoiceErrors,
            onInit: function (form) { form.querySelector('[name="kind"]').addEventListener('change', function () { toggle(form); }); }
        });
        function toggle(form) {
            var kind = fieldValue(form, 'kind');
            $all('[data-inv]', form).forEach(function (box) { box.hidden = box.getAttribute('data-inv') !== kind; });
        }
    }

    /* ======================================================================
       會員：帳號與密碼（06 的 /api/account/*）
       ====================================================================== */
    function initSecurity() {
        var status = $('[data-sec-status]');
        var profileForm = $('form[data-profile]');
        var emailForm = $('form[data-email]');
        var pwForm = $('form[data-password]');
        var tfaBox = $('[data-2fa]');
        var sessBox = $('[data-sessions]');

        function fail(target) { return function (resOrErr) { notice(target || status, 'bad', messageFor(resOrErr)); }; }

        function loadProfile() {
            return api('GET', '/api/account/profile').then(function (res) {
                if (!res.ok || !res.data) { notice(status, 'bad', messageFor(res, 'loadFailed'), loadProfile); return; }
                var p = res.data;
                setField(profileForm, 'display_name', p.display_name || '');
                setField(profileForm, 'locale', p.locale || (EN ? 'en' : 'zh-TW'));
                $('[data-current-email]').textContent = p.email || '';
                var pend = $('[data-pending-email]');
                pend.hidden = !p.pending_email;
                pend.textContent = p.pending_email ? t('pendingEmail', { e: p.pending_email }) : '';
            }, function (err) { notice(status, 'bad', messageFor(err), loadProfile); });
        }
        profileForm.addEventListener('submit', function (e) {
            e.preventDefault();
            var btn = profileForm.querySelector('button[type="submit"]');
            var msg = $('[data-msg]', profileForm);
            var name = fieldValue(profileForm, 'display_name');
            var first = showFieldErrors(profileForm, name ? {} : { display_name: t('required') });
            if (first) { first.focus(); return; }
            busy(btn, true);
            api('PATCH', '/api/account/profile', { display_name: name, locale: fieldValue(profileForm, 'locale') }).then(function (res) {
                busy(btn, false);
                if (res.ok) {
                    var lang = fieldValue(profileForm, 'locale');
                    if ((lang === 'en') !== EN) { location.assign(location.pathname + '?lang=' + encodeURIComponent(lang)); return; }
                    notice(msg, 'ok', t('saved'));
                    return;
                }
                if (res.status === 400) showFieldErrors(profileForm, serverFieldErrors(res.data));
                notice(msg, 'bad', messageFor(res));
            }, function (err) { busy(btn, false); notice(msg, 'bad', messageFor(err)); });
        });

        emailForm.addEventListener('submit', function (e) {
            e.preventDefault();
            var btn = emailForm.querySelector('button[type="submit"]');
            var msg = $('[data-msg]', emailForm);
            var input = emailForm.querySelector('[name="new_email"]');
            var errs = {};
            if (!input.value.trim()) errs.new_email = t('required'); else if (!input.checkValidity()) errs.new_email = t('badEmail');
            if (!fieldValue(emailForm, 'current_password')) errs.current_password = t('required');
            var first = showFieldErrors(emailForm, errs);
            if (first) { first.focus(); return; }
            busy(btn, true);
            api('POST', '/api/account/email', { new_email: input.value.trim(), current_password: emailForm.querySelector('[name="current_password"]').value }).then(function (res) {
                busy(btn, false);
                if (res.ok) {
                    emailForm.reset();
                    clear(msg); msg.hidden = false;
                    var box = el('div', { class: 'notice ok', role: 'status' }, [el('div', { text: t('emailSent') })]);
                    var link = safePath(res.data && res.data.confirm_url, ['/account/email/confirm']);
                    if (link) box.firstChild.appendChild(el('div', null, [t('emailLink') + ' ', el('a', { href: link, text: link })]));
                    msg.appendChild(box);
                    loadProfile();
                    return;
                }
                if (res.status === 409) { showFieldErrors(emailForm, { new_email: t('emailTaken') }); notice(msg, 'bad', t('emailTaken')); return; }
                if (res.status === 400 || res.status === 401 || res.status === 403) {
                    var mapped = serverFieldErrors(res.data);
                    if (!Object.keys(mapped).length && res.status !== 400) mapped = { current_password: t('wrongPassword') };
                    showFieldErrors(emailForm, mapped);
                }
                notice(msg, 'bad', res.status === 400 || res.status === 401 ? t('wrongPassword') : messageFor(res));
            }, function (err) { busy(btn, false); notice(msg, 'bad', messageFor(err)); });
        });

        pwForm.addEventListener('submit', function (e) {
            e.preventDefault();
            var btn = pwForm.querySelector('button[type="submit"]');
            var msg = $('[data-msg]', pwForm);
            var next = pwForm.querySelector('[name="new_password"]').value;
            var current = pwForm.querySelector('[name="current_password"]').value;
            var first = showFieldErrors(pwForm, next.length >= 8 ? {} : { new_password: t('passwordShort') });
            if (first) { first.focus(); return; }
            var body = { new_password: next };
            if (current) body.current_password = current;
            busy(btn, true);
            api('POST', '/api/account/password', body).then(function (res) {
                busy(btn, false);
                if (res.ok) { pwForm.reset(); notice(msg, 'ok', t('passwordChanged')); loadSessions(); return; }
                if (res.status === 400 || res.status === 401) { showFieldErrors(pwForm, { current_password: t('wrongPassword') }); notice(msg, 'bad', t('wrongPassword')); return; }
                notice(msg, 'bad', messageFor(res));
            }, function (err) { busy(btn, false); notice(msg, 'bad', messageFor(err)); });
        });

        function render2FA(s) {
            clear(tfaBox);
            var msg = el('div', { hidden: true });
            var chip = el('span', { class: 'chip dot ' + (s.enabled ? 'ok' : ''), text: s.enabled ? t('twofaOn') : t('twofaOff') });
            var head = el('div', { style: 'display:flex;gap:.75rem;align-items:center;flex-wrap:wrap' }, [chip]);
            if (s.enabled && s.recovery_remaining !== undefined) head.appendChild(el('span', { class: 'small muted', text: t('recoveryLeft', { n: s.recovery_remaining }) }));
            tfaBox.appendChild(head);
            if (!s.enabled) {
                var start = el('button', { class: 'btn t sm', type: 'button', text: tfaBox.getAttribute('data-enable-label') });
                tfaBox.appendChild(el('div', { class: 'acts' }, [start]));
                start.addEventListener('click', function () {
                    busy(start, true);
                    api('POST', '/api/account/2fa/enroll').then(function (res) {
                        busy(start, false);
                        if (!res.ok || !res.data) { notice(msg, 'bad', messageFor(res)); return; }
                        enrollStep(res.data, msg);
                    }, function (err) { busy(start, false); notice(msg, 'bad', messageFor(err)); });
                });
            } else {
                var disableForm = el('form', { class: 'grid2', novalidate: true });
                disableForm.appendChild(el('div', null, [el('label', { class: 'flbl', for: 'tfa-pw', text: tfaBox.getAttribute('data-password-label') }), el('input', { class: 'field', id: 'tfa-pw', type: 'password', name: 'current_password', autocomplete: 'current-password', required: true })]));
                disableForm.appendChild(el('div', null, [el('label', { class: 'flbl', for: 'tfa-dc', text: tfaBox.getAttribute('data-code-label') }), el('input', { class: 'field', id: 'tfa-dc', type: 'text', name: 'code', inputmode: 'numeric', autocomplete: 'one-time-code', required: true })]));
                var off = el('button', { class: 'btn t sm danger', type: 'submit', text: tfaBox.getAttribute('data-disable-label') });
                disableForm.appendChild(el('div', { class: 'span2' }, [off]));
                tfaBox.appendChild(disableForm);
                disableForm.addEventListener('submit', function (e) {
                    e.preventDefault();
                    var code = disableForm.code.value.trim();
                    var body = { current_password: disableForm.current_password.value };
                    if (/^\d{6}$/.test(code)) body.code = code; else body.recovery_code = code;
                    busy(off, true);
                    api('POST', '/api/account/2fa/disable', body).then(function (res) {
                        busy(off, false);
                        if (res.ok) { toast(t('twofaDisabled')); load2FA(); return; }
                        notice(msg, 'bad', res.status === 400 || res.status === 401 ? t('badCode') : messageFor(res));
                    }, function (err) { busy(off, false); notice(msg, 'bad', messageFor(err)); });
                });
            }
            tfaBox.appendChild(msg);
        }
        function enrollStep(data, msg) {
            var box = el('div', { class: 'box soft' });
            if (typeof data.qr_svg === 'string' && data.qr_svg.indexOf('<svg') !== -1) {
                // The SVG is drawn by the server; as an <img> source it cannot run scripts.
                box.appendChild(el('img', { class: 'qr', alt: tfaBox.getAttribute('data-qr-label'), src: 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(data.qr_svg) }));
            }
            box.appendChild(el('div', { class: 'small' }, [tfaBox.getAttribute('data-secret-label') + ' ', el('code', { text: data.secret || '' })]));
            var form = el('form', { class: 'grid2', novalidate: true });
            form.appendChild(el('div', null, [el('label', { class: 'flbl', for: 'tfa-code', text: tfaBox.getAttribute('data-code-label') }), el('input', { class: 'field', id: 'tfa-code', name: 'code', inputmode: 'numeric', autocomplete: 'one-time-code', maxlength: '6', required: true })]));
            var ok = el('button', { class: 'btn p sm', type: 'submit', text: tfaBox.getAttribute('data-verify-label') });
            form.appendChild(el('div', { style: 'align-self:end' }, [ok]));
            box.appendChild(form);
            tfaBox.insertBefore(box, msg);
            form.code.focus();
            form.addEventListener('submit', function (e) {
                e.preventDefault();
                var code = form.code.value.trim();
                if (!/^\d{6}$/.test(code)) { notice(msg, 'bad', t('badCode')); form.code.focus(); return; }
                busy(ok, true);
                api('POST', '/api/account/2fa/verify', { code: code }).then(function (res) {
                    busy(ok, false);
                    if (!res.ok) { notice(msg, 'bad', res.status === 400 || res.status === 401 ? t('badCode') : messageFor(res)); return; }
                    var codes = res.data && Array.isArray(res.data.recovery_codes) ? res.data.recovery_codes : [];
                    load2FA().then(function () {
                        var list = el('div', { class: 'codes', 'aria-label': 'recovery codes' });
                        codes.forEach(function (c) { list.appendChild(el('span', { text: String(c) })); });
                        tfaBox.appendChild(el('div', { class: 'notice ok', role: 'status' }, [el('div', null, [el('div', { text: t('twofaEnabled') }), list])]));
                    });
                }, function (err) { busy(ok, false); notice(msg, 'bad', messageFor(err)); });
            });
        }
        function load2FA() {
            return api('GET', '/api/account/2fa').then(function (res) {
                if (!res.ok || !res.data) { clear(tfaBox); notice(tfaBox, 'bad', messageFor(res, 'loadFailed'), load2FA); return; }
                render2FA(res.data);
            }, function (err) { clear(tfaBox); notice(tfaBox, 'bad', messageFor(err), load2FA); });
        }

        function loadSessions() {
            var list = $('[data-session-list]', sessBox);
            var msg = $('[data-msg]', sessBox);
            return api('GET', '/api/account/sessions').then(function (res) {
                if (!res.ok) { notice(msg, 'bad', messageFor(res, 'loadFailed'), loadSessions); return; }
                clear(list);
                var items = listOf(res.data);
                items.forEach(function (s) {
                    var row = el('div');
                    var text = el('div', null, [
                        el('b', { text: s.device_label || '—' }),
                        el('div', { class: 'small muted', text: [s.ip_masked, s.last_seen_at ? t('lastSeen', { t: fmtTime(s.last_seen_at) }) : ''].filter(Boolean).join(' · ') })
                    ]);
                    row.appendChild(text);
                    if (s.current) row.appendChild(el('span', { class: 'chip ok', text: t('current') }));
                    else {
                        if (s.new_device) row.appendChild(el('span', { class: 'chip warn', text: t('newDevice') }));
                        row.appendChild(el('button', { class: 'btn t sm', type: 'button', text: t('revoke'), onclick: function (e) {
                            var b = e.currentTarget; busy(b, true);
                            api('DELETE', '/api/account/sessions/' + encodeURIComponent(s.id)).then(function (r) {
                                busy(b, false);
                                if (r.ok) { toast(t('revoked')); loadSessions(); } else notice(msg, 'bad', messageFor(r));
                            }, function (err) { busy(b, false); notice(msg, 'bad', messageFor(err)); });
                        } }));
                    }
                    list.appendChild(row);
                });
                if (items.length <= 1) list.appendChild(el('p', { class: 'small muted', text: t('noSessions') }));
            }, function (err) { notice(msg, 'bad', messageFor(err), loadSessions); });
        }
        $('[data-revoke-others]', sessBox).addEventListener('click', function (e) {
            var b = e.currentTarget; busy(b, true);
            api('POST', '/api/account/sessions/revoke-others').then(function (r) {
                busy(b, false);
                if (r.ok) { toast(t('revokedOthers')); loadSessions(); } else notice($('[data-msg]', sessBox), 'bad', messageFor(r));
            }, function (err) { busy(b, false); notice($('[data-msg]', sessBox), 'bad', messageFor(err)); });
        });

        loadProfile();
        load2FA();
        loadSessions();
    }

    /* ======================================================================
       會員：通知
       ====================================================================== */
    function initNotifications() {
        var list = $('[data-n-list]');
        var status = $('[data-n-status]');
        var more = $('[data-n-more]');
        var readAll = $('[data-n-readall]');
        var before = '';
        function titleFor(item) {
            var key = 'k_' + String(item.kind || '').replace(/\./g, '_');
            var base = T[key] ? t(key) : t('k_other');
            var params = item.params && typeof item.params === 'object' ? item.params : {};
            var subject = params.number || params.title || params.name || params.device_label;
            return base + (subject ? ': ' + subject : '');
        }
        function row(item) {
            var box = el('div', { class: item.read_at ? '' : 'unread' });
            var body = el('div', null, [el('b', { text: titleFor(item) }), el('div', { class: 'small muted', text: fmtTime(item.created_at) })]);
            (Array.isArray(item.actions) ? item.actions : []).forEach(function (a) {
                var href = safePath(a && a.url, ['/account', '/orders', '/checkout', '/shop']);
                if (href) body.appendChild(el('a', { class: 'small', href: href, text: a.label_text || href }));
            });
            box.appendChild(body);
            if (!item.read_at) box.appendChild(el('button', { class: 'btn t sm', type: 'button', text: t('markRead'), onclick: function (e) {
                var b = e.currentTarget; busy(b, true);
                api('POST', '/api/account/notifications/' + encodeURIComponent(item.id) + '/read').then(function (r) {
                    if (r.ok) { box.className = ''; b.remove(); } else { busy(b, false); notice(status, 'bad', messageFor(r)); }
                }, function (err) { busy(b, false); notice(status, 'bad', messageFor(err)); });
            } }));
            return box;
        }
        function load(append) {
            more.hidden = true;
            var url = '/api/account/notifications?limit=20' + (append && before ? '&before=' + encodeURIComponent(before) : '');
            list.setAttribute('aria-busy', 'true');
            return api('GET', url).then(function (res) {
                list.removeAttribute('aria-busy');
                if (!res.ok || !res.data) { notice(status, 'bad', messageFor(res, 'loadFailed'), function () { load(append); }); return; }
                notice(status, '', '');
                if (!append) clear(list);
                var items = listOf(res.data);
                items.forEach(function (item) { list.appendChild(row(item)); });
                if (!append && !items.length) list.appendChild(el('div', { class: 'empty' }, [el('b', { text: t('noNotifications') })]));
                before = items.length ? String(items[items.length - 1].id) : before;
                more.hidden = items.length < 20;
                readAll.hidden = !(res.data.unread_count > 0);
            }, function (err) { list.removeAttribute('aria-busy'); notice(status, 'bad', messageFor(err), function () { load(append); }); });
        }
        more.addEventListener('click', function () { load(true); });
        readAll.addEventListener('click', function () {
            busy(readAll, true);
            api('POST', '/api/account/notifications/read-all').then(function (r) {
                busy(readAll, false);
                if (r.ok) { toast(t('allRead')); load(false); } else notice(status, 'bad', messageFor(r));
            }, function (err) { busy(readAll, false); notice(status, 'bad', messageFor(err)); });
        });
        load(false);
    }

    var pages = {
        list: initListing, product: initProduct, cart: initCart, checkout: initCheckout,
        done: initDone, sandbox: initSandbox, lookup: initLookup, view: printButtons,
        'account-orders': function () {}, 'account-order': initAccountOrder,
        addresses: initAddresses, invoices: initInvoices, security: initSecurity, notifications: initNotifications
    };
    initAccountLogout();
    var init = pages[root.getAttribute('data-page')];
    if (init) init();

    window.JellyShop = { money: money, api: api, addToCart: addToCart };
})();
