/* Data values are validated by the renderer and checked again before use. */
(function () {
    'use strict';
    var seen = new WeakSet(), reduced = window.matchMedia('(prefers-reduced-motion: reduce)');
    var active = new Set(), queued = false;
    function milliseconds(value, fallback, max) { if (!/^\d*(?:\.\d+)?$/.test(value || '')) return fallback; var n = Number(value); return Number.isFinite(n) ? Math.min(max, Math.max(0, n)) : fallback; }
    function reveal(el) { el.classList.remove('jc-motion-pending'); }
    var observer = window.IntersectionObserver ? new IntersectionObserver(function (entries) { entries.forEach(function (entry) { if (!entry.target.isConnected) { observer.unobserve(entry.target); return; } if (entry.isIntersecting) { reveal(entry.target); if (entry.target.hasAttribute('data-once')) observer.unobserve(entry.target); } else if (!entry.target.hasAttribute('data-once') && !reduced.matches) entry.target.classList.add('jc-motion-pending'); }); }, {threshold: .08}) : null;
    function scroll() {
        if (queued) return; queued = true;
        requestAnimationFrame(function () { queued = false; active.forEach(function (el) { if (!el.isConnected) { active.delete(el); return; } if (reduced.matches) { el.style.removeProperty('translate'); return; } var rect = el.getBoundingClientRect(); el.style.translate = '0 ' + Math.round(Math.max(-20, Math.min(20, (innerHeight / 2 - rect.top - rect.height / 2) * .035))) + 'px'; }); });
    }
    function init(root) {
        root = root || document;
        var nodes = Array.from(root.querySelectorAll('[data-reveal], [data-scroll], [data-delay], [data-dur], [data-stagger]'));
        if (root.matches && root.matches('[data-reveal], [data-scroll]')) nodes.unshift(root);
        nodes.forEach(function (el) {
            if (seen.has(el)) return; seen.add(el);
            var durations = {fast:.3,normal:.6,slow:1.2};
            var duration = Object.prototype.hasOwnProperty.call(durations,el.dataset.dur) ? durations[el.dataset.dur] : milliseconds(el.dataset.dur, .6, 5), delay = milliseconds(el.dataset.delay, 0, 5);
            el.style.transitionDuration = duration + 's'; el.style.transitionDelay = delay + 's';
            if (el.dataset.scroll === 'parallax') { active.add(el); scroll(); }
            if (!['fade-up','fade','slide-left','slide-right','zoom'].includes(el.dataset.reveal) || reduced.matches || !observer) return;
            el.classList.add('jc-motion-ready','jc-motion-pending'); observer.observe(el);
            var stagger = milliseconds(el.dataset.stagger, 0, 1000);
            if (stagger) Array.from(el.children).forEach(function (child,i) { child.classList.add('jc-motion-ready','jc-motion-pending'); if (el.hasAttribute('data-once')) child.dataset.once = 'true'; child.style.transitionDuration = duration + 's'; child.style.transitionDelay = Math.min(5, delay + i * stagger / 1000) + 's'; observer.observe(child); });
        });
    }
    reduced.addEventListener('change', function () { if (reduced.matches) document.querySelectorAll('.jc-motion-pending').forEach(reveal); scroll(); });
    window.addEventListener('scroll', scroll, {passive: true}); window.addEventListener('resize', scroll, {passive: true});
    window.JellyMotion = {init: init};
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', function () { init(document); }); else init(document);
})();
