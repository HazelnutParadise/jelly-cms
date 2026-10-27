/* Shared, progressive enhancements for server-rendered Jelly content. */
(function () {
    'use strict';
    var seen = new WeakSet(), sequence = 0;
    var reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
    function all(root, selector) {
        var nodes = Array.from(root.querySelectorAll(selector));
        if (root.matches && root.matches(selector)) nodes.unshift(root);
        return nodes;
    }
    function button(text, action) {
        var b = document.createElement('button'); b.type = 'button'; b.textContent = text;
        b.addEventListener('click', action); return b;
    }
    function tabs(el) {
        var panels = Array.from(el.children).filter(function (p) { return p.classList.contains('jc-tab'); });
        if (!panels.length) return;
        var list = document.createElement('div'); list.className = 'jc-tablist'; list.setAttribute('role', 'tablist');
        var id = 'jc-tabs-' + (++sequence), buttons = [];
        function select(index, focus) {
            buttons.forEach(function (b, i) { b.setAttribute('aria-selected', String(i === index)); b.tabIndex = i === index ? 0 : -1; panels[i].hidden = i !== index; });
            if (focus) buttons[index].focus();
        }
        panels.forEach(function (panel, i) {
            var b = button(panel.dataset.title || String(i + 1), function () { select(i, false); });
            b.id = id + '-tab-' + i; b.setAttribute('role', 'tab'); b.setAttribute('aria-controls', id + '-panel-' + i);
            panel.id = id + '-panel-' + i; panel.setAttribute('role', 'tabpanel'); panel.setAttribute('aria-labelledby', b.id); panel.tabIndex = 0;
            b.addEventListener('keydown', function (e) {
                var next = i;
                if (e.key === 'ArrowRight') next = (i + 1) % panels.length;
                else if (e.key === 'ArrowLeft') next = (i + panels.length - 1) % panels.length;
                else if (e.key === 'Home') next = 0;
                else if (e.key === 'End') next = panels.length - 1;
                else return;
                e.preventDefault(); select(next, true);
            });
            buttons.push(b); list.appendChild(b);
        });
        el.prepend(list); el.dataset.enhanced = ''; select(0, false);
    }
    function carousel(el) {
        var slides = Array.from(el.children); if (slides.length < 2) return;
        var controls = document.createElement('div'); controls.className = 'jc-carousel-controls';
        var en = document.documentElement.lang === 'en';
        function move(delta) {
            var index = 0, distance = Infinity;
            slides.forEach(function (slide,i) { var next = Math.abs(slide.offsetLeft - slides[0].offsetLeft - el.scrollLeft); if (next < distance) {distance = next; index = i;} });
            index = Math.max(0, Math.min(slides.length - 1, index + delta));
            el.scrollTo({left: slides[index].offsetLeft - slides[0].offsetLeft, behavior: reduced.matches ? 'instant' : 'smooth'});
        }
        controls.append(button(en ? 'Previous' : '上一張', function () { move(-1); }), button(en ? 'Next' : '下一張', function () { move(1); }));
        el.tabIndex = 0; el.setAttribute('role', 'region'); el.setAttribute('aria-label', en ? 'Image carousel' : '圖片輪播');
        el.addEventListener('keydown', function (e) { if (e.target !== el) return; if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') { e.preventDefault(); move(e.key === 'ArrowLeft' ? -1 : 1); } });
        el.after(controls);
    }
    function lightbox(el) {
        all(el, 'img').forEach(function (img) {
            if (seen.has(img)) return; seen.add(img);
            img.tabIndex = 0; img.setAttribute('role', 'button'); img.setAttribute('aria-haspopup', 'dialog');
            function open(e) {
                if (e) e.preventDefault();
                var dialog = document.createElement('dialog'); dialog.className = 'jc-lightbox'; dialog.setAttribute('aria-label', img.alt || (document.documentElement.lang === 'en' ? 'Image preview' : '圖片預覽'));
                var form = document.createElement('form'); form.method = 'dialog';
                var close = document.createElement('button'); close.textContent = document.documentElement.lang === 'en' ? 'Close' : '關閉'; form.appendChild(close);
                var copy = document.createElement('img'); copy.src = img.currentSrc || img.src; copy.alt = img.alt;
                dialog.append(form, copy); document.body.appendChild(dialog);
                dialog.addEventListener('close', function () { dialog.remove(); if (img.isConnected) img.focus(); });
                dialog.addEventListener('click', function (event) { if (event.target === dialog) dialog.close(); }); dialog.showModal();
            }
            img.addEventListener('click', open); img.addEventListener('keydown', function (e) { if (e.key === 'Enter' || e.key === ' ') open(e); });
        });
    }
    function counter(el) {
        var target = Number(el.dataset.to); if (!Number.isFinite(target) || reduced.matches) return;
        function run() {
            var start = performance.now();
            function frame(now) { if (!el.isConnected) return; var p = reduced.matches ? 1 : Math.min(1, (now - start) / 900); el.textContent = p === 1 ? el.dataset.to : String(Math.round(target * (1 - Math.pow(1 - p, 3)))); if (p < 1) requestAnimationFrame(frame); }
            requestAnimationFrame(frame);
        }
        if (!window.IntersectionObserver) return;
        var observer = new IntersectionObserver(function (entries) { if (entries[0].isIntersecting) { observer.disconnect(); run(); } }); observer.observe(el);
    }
    function compare(el) {
        var range = el.querySelector('input[type="range"]'); if (!range) return;
        function update() { var value = Math.min(100, Math.max(0, Number(range.value))); if (!Number.isFinite(value)) return; el.style.setProperty('--compare-position', value + '%'); range.setAttribute('aria-valuetext', value + '%'); }
        el.dataset.enhanced = ''; range.addEventListener('input', update); update();
    }
    var themedFrames = new WeakSet(), themeQueued = false;
    var themeColors = ['--primary','--secondary','--accent','--warning','--ink','--muted','--surface','--surface-low','--surface-mid','--outline'];
    function sendTheme(frame) {
        if (!frame.isConnected || !frame.contentWindow) return;
        var style = getComputedStyle(frame), colors = {};
        themeColors.forEach(function (name) {
            var value = style.getPropertyValue(name).trim();
            if (value && value.length <= 128 && CSS.supports('color', value)) colors[name] = value;
        });
        if (colors['--ink']) colors['--color-ink'] = colors['--ink'];
        if (colors['--surface']) colors['--color-surface'] = colors['--surface'];
        frame.contentWindow.postMessage({type: 'jelly-widget-theme', colors: colors}, '*');
    }
    function syncThemes() {
        if (themeQueued) return; themeQueued = true;
        requestAnimationFrame(function () { themeQueued = false; document.querySelectorAll('.jc-widget iframe').forEach(sendTheme); });
    }
    var scheme = window.matchMedia('(prefers-color-scheme: dark)');
    scheme.addEventListener('change', syncThemes);
    new MutationObserver(function (records) {
        if (records.some(function (record) {
            return record.target === document.documentElement && record.attributeName !== 'class' ||
                record.attributeName === 'class' && (record.target.classList.contains('jelly-editor') || (record.oldValue || '').split(/\s+/).includes('jelly-editor'));
        })) syncThemes();
    }).observe(document.documentElement, {attributes: true, subtree: true, attributeOldValue: true, attributeFilter: ['data-theme','data-ui-theme','class']});
    function init(root) {
        root = root || document;
        all(root, '.jc-widget iframe').forEach(function (frame) {
            if (!themedFrames.has(frame)) { themedFrames.add(frame); frame.addEventListener('load', function () { sendTheme(frame); }); }
            sendTheme(frame);
        });
        all(root, '.jc-math[data-tex]').forEach(function (el) {
            if (!window.katex || el.dataset.mathRendered) return;
            try { window.katex.render(el.dataset.tex, el, {displayMode: el.dataset.display === 'block', trust: false, throwOnError: false, strict: 'warn', maxExpand: 1000}); el.dataset.mathRendered = 'true'; } catch (_) { el.textContent = el.dataset.tex; }
        });
        all(root, '[data-jc]').forEach(function (el) {
            if (el.dataset.builtin === 'countdown') {
                var frame = el.querySelector('iframe'), language = document.documentElement.lang === 'en' ? 'en' : 'zh-TW';
                if (frame && frame.getAttribute('src')) {
                    try {
                        var url = new URL(frame.getAttribute('src'), document.baseURI);
                        if (url.searchParams.get('lang') !== language) { url.searchParams.set('lang', language); frame.src = url.href; }
                    } catch (_) { /* Leave malformed frame URLs unchanged. */ }
                }
            }
            if (seen.has(el)) return; seen.add(el);
            if (el.classList.contains('jc-widget')) return; // Versioned frames own their behavior.
            if (el.dataset.jc === 'tabs') tabs(el);
            if (el.dataset.jc === 'carousel') carousel(el);
            if (el.dataset.jc === 'lightbox') lightbox(el);
            if (el.dataset.jc === 'counter') counter(el);
            if (el.dataset.jc === 'compare') compare(el);
        });
    }
    window.addEventListener('message', function (event) {
        if (!event.data || event.data.type !== 'jelly-widget-height' || typeof event.data.height !== 'number' || !Number.isFinite(event.data.height) || event.data.height < 24 || event.data.height > 4096) return;
        document.querySelectorAll('.jc-widget iframe').forEach(function (frame) { if (frame.contentWindow === event.source) frame.style.height = Math.ceil(event.data.height) + 'px'; });
    });
    window.JellyComponents = {init: init};
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', function () { init(document); }); else init(document);
})();
