(function (global) {
  'use strict';

  var states = new WeakMap();
  var sequence = 0;
  var MAX_SOURCE = 50000;
  var allowedElements = new Set([
    'svg', 'g', 'defs', 'title', 'desc', 'path', 'rect', 'circle', 'ellipse',
    'line', 'polyline', 'polygon', 'text', 'tspan', 'textpath', 'marker',
    'clippath', 'mask', 'lineargradient', 'radialgradient', 'stop', 'pattern',
    'filter', 'fegaussianblur', 'feoffset', 'fecolormatrix', 'femerge', 'femergenode',
    'a', 'use'
  ]);
  var allowedAttributes = new Set((
    'xmlns xmlns:xlink version class id role focusable style viewbox preserveaspectratio width height x y x1 y1 x2 y2 cx cy r rx ry d points transform fill fill-opacity fill-rule stroke stroke-width stroke-linecap stroke-linejoin stroke-dasharray stroke-dashoffset stroke-opacity opacity color offset stop-color stop-opacity gradientunits gradienttransform markerwidth markerheight markerunits orient refx refy clip-path mask filter result in stddeviation dx dy values type operator k1 k2 k3 k4 text-anchor dominant-baseline alignment-baseline font-family font-size font-weight font-style letter-spacing word-spacing vector-effect shape-rendering paint-order href xlink:href target rel aria-label aria-hidden data-id'
  ).split(/\s+/));
  var allowedStyleProperties = new Set((
    'color fill fill-opacity fill-rule stroke stroke-width stroke-linecap stroke-linejoin stroke-dasharray stroke-dashoffset stroke-opacity opacity font font-family font-size font-size-adjust font-language-override font-kerning font-optical-sizing font-feature-settings font-variation-settings font-weight font-stretch font-style font-variant text-anchor dominant-baseline alignment-baseline letter-spacing word-spacing line-height direction text-transform rx unicode-bidi max-width min-width height width overflow white-space --pele-bg --pele-fg --pele-muted --pele-line --pele-surface --pele-surface-alt --pele-border --pele-accent --pele-critical --pele-error --pele-font --pele-font-mono --pele-title-weight --pele-heading-weight --pele-bold-weight --pele-radius --_bg --_fg --_m --_l --_s --_a --_b --_c --_r --_fm --pele-series1 --pele-series2 --pele-series3 --pele-series4 --pele-series5 --pele-series6 --pele-series7 --pele-series8 --pele-series-1 --pele-series-2 --pele-series-3 --pele-series-4 --pele-series-5 --pele-series-6 --pele-series-7 --pele-series-8'
  ).split(/\s+/));

  function inside(root, node) {
    if (root.nodeType === 9) return node.ownerDocument === root && node.isConnected;
    return root === node || root.contains(node);
  }

  function languageFor(pre) {
    for (var node = pre; node && node.nodeType === 1; node = node.parentElement) {
      if (node.hasAttribute('lang')) return node.getAttribute('lang') || '';
    }
    return pre.ownerDocument.documentElement.lang || '';
  }

  function messages(pre) {
    var zh = languageFor(pre).toLowerCase().indexOf('zh') === 0;
    return zh
      ? { prefix: '圖表無法顯示：', long: '圖表原文超過 50000 字元', unloaded: '繪圖元件沒有載入', empty: '沒有產生圖表' }
      : { prefix: 'Diagram could not be displayed: ', long: 'Diagram source exceeds 50000 characters', unloaded: 'The diagram renderer did not load', empty: 'No diagram was produced' };
  }

  function firstLine(error) {
    var message;
    try {
      message = error && typeof error.message === 'string' ? error.message : String(error);
    } catch (_) {
      message = 'Rendering failed';
    }
    return message.split(/\r?\n/, 1)[0].trim() || 'Rendering failed';
  }

  function removeResult(pre, state) {
    var next = pre.nextElementSibling;
    if (next && ((state && next === state.node) || next.matches('figure.jc-mermaid, p.jc-mermaid-error'))) next.remove();
    pre.hidden = false;
  }

  function safeHref(value) {
    var trimmed = value.trim();
    if (/[\u0000-\u0020\\]/.test(trimmed)) return null;
    if (!trimmed || trimmed.charAt(0) === '#' || trimmed.charAt(0) === '/') {
      return trimmed.indexOf('//') === 0 ? null : trimmed;
    }
    var scheme = /^([a-z][a-z0-9+.-]*):/i.exec(trimmed);
    if (!scheme) return trimmed;
    var protocol = scheme[1].toLowerCase();
    return protocol === 'http' || protocol === 'https' || protocol === 'mailto' ? trimmed : null;
  }

  function sanitizeStyle(value, doc) {
    if (/[\\\u0000-\u001f]|\/\*|\*\//.test(value) || /url|import|expression|animation|transition/i.test(value)) return null;
    var probe = doc.createElementNS('http://www.w3.org/2000/svg', 'svg');
    probe.setAttribute('style', value);
    var declarations = probe.style;
    var kept = [];
    for (var index = 0; index < declarations.length; index += 1) {
      var property = declarations[index].toLowerCase();
      var cssValue = declarations.getPropertyValue(property).trim();
      if (!allowedStyleProperties.has(property) || !cssValue || /[\\\u0000-\u001f]|url|expression|@/i.test(cssValue)) continue;
      kept.push(property + ':' + cssValue);
    }
    return kept.join(';');
  }

  function sanitize(svgText, doc) {
    var parsed = new DOMParser().parseFromString(svgText, 'image/svg+xml');
    if (parsed.querySelector('parsererror') || !parsed.documentElement || parsed.documentElement.localName.toLowerCase() !== 'svg' || parsed.documentElement.namespaceURI !== 'http://www.w3.org/2000/svg') {
      throw new Error('Invalid SVG output');
    }
    var nodes = [parsed.documentElement].concat(Array.from(parsed.documentElement.querySelectorAll('*')));
    nodes.forEach(function (node) {
      if (node.namespaceURI !== 'http://www.w3.org/2000/svg' || !allowedElements.has(node.localName.toLowerCase())) {
        node.remove();
        return;
      }
      var tag = node.localName.toLowerCase();
      Array.from(node.attributes).forEach(function (attr) {
        var name = attr.name.toLowerCase();
        var value = attr.value;
        if (!allowedAttributes.has(name) || name.indexOf('on') === 0) {
          node.removeAttributeNode(attr);
          return;
        }
        if (name === 'style') {
          var cleanStyle = sanitizeStyle(value, doc);
          if (cleanStyle) node.setAttribute('style', cleanStyle);
          else node.removeAttribute(attr.name);
          return;
        }
        if (/[\\\u0000-\u001f]|\/\*|\*\//.test(value)) {
          node.removeAttribute(attr.name);
          return;
        }
        if (/url\s*\(/i.test(value)) {
          if (!/^url\(\s*['\"]?#[-\w:.]+['\"]?\s*\)$/i.test(value.trim())) node.removeAttribute(attr.name);
          return;
        }
        if (name === 'href' || name === 'xlink:href') {
          var safe = safeHref(value);
          if (tag === 'use') {
            if (!safe || safe.charAt(0) !== '#' || safe.length < 2) node.removeAttribute(attr.name);
            else node.setAttribute(attr.name, safe);
          } else if (tag !== 'a') {
            node.removeAttribute(attr.name);
          } else if (safe === null) {
            node.removeAttribute(attr.name);
          } else {
            node.setAttribute(attr.name, safe);
            if (/^https?:\/\//i.test(safe) && new URL(safe, doc.baseURI).origin !== doc.location.origin) {
              node.setAttribute('rel', 'noopener noreferrer');
            }
          }
        }
      });
    });
    return doc.importNode(parsed.documentElement, true);
  }

  function showError(pre, source, reason, root, token) {
    if (!inside(root, pre) || states.get(pre).token !== token) return;
    var state = states.get(pre);
    removeResult(pre, state);
    var text = messages(pre);
    var p = pre.ownerDocument.createElement('p');
    p.className = 'jc-mermaid-error';
    p.setAttribute('role', 'status');
    p.textContent = text.prefix + reason;
    pre.insertAdjacentElement('afterend', p);
    states.set(pre, { source: source, node: p, token: token, kind: 'error' });
  }

  async function renderOne(pre, root) {
    var source = pre.querySelector('code.language-mermaid').textContent;
    var previous = states.get(pre);
    if (previous && previous.source === source && previous.kind === 'success' && previous.node && previous.node.isConnected && pre.nextElementSibling === previous.node) return;
    var token = (previous ? previous.token : 0) + 1;
    states.set(pre, { source: source, token: token });
    removeResult(pre, previous);
    await (pre.ownerDocument.fonts && pre.ownerDocument.fonts.ready ? pre.ownerDocument.fonts.ready : Promise.resolve());
    if (!inside(root, pre) || states.get(pre).token !== token) return;
    var code = pre.querySelector(':scope > code.language-mermaid');
    if (!code) {
      removeResult(pre, states.get(pre));
      states.delete(pre);
      return;
    }
    if (code.textContent !== source) return renderOne(pre, root);
    var text = messages(pre);
    if (source.length > MAX_SOURCE) return showError(pre, source, text.long, root, token);
    if (!global.pele || typeof global.pele.render !== 'function') return showError(pre, source, text.unloaded, root, token);
    var doc = pre.ownerDocument;
    var figure = doc.createElement('figure');
    figure.className = 'jc-mermaid';
    figure.style.maxWidth = '100%';
    figure.style.overflowX = 'auto';
    pre.insertAdjacentElement('afterend', figure);
    try {
      var style = doc.defaultView.getComputedStyle(figure);
      var options = {
        idPrefix: 'jc-mermaid-' + (++sequence),
        fontFamily: style.fontFamily,
        maxWidth: figure.clientWidth || pre.parentElement.clientWidth || 0,
        linkSchemes: ['http', 'https', 'mailto'],
        images: false,
        linkRel: 'noopener noreferrer',
        limit: MAX_SOURCE,
        responsive: true
      };
      var result = global.pele.render(source, options);
      if (!result || typeof result.svg !== 'string' || !result.svg.trim()) throw new Error(text.empty);
      var width = Number(result.width);
      if (!Number.isFinite(width) || width <= 0 || width > 1000000) throw new Error('Invalid diagram dimensions');
      if (!inside(root, pre) || states.get(pre).token !== token) {
        figure.remove();
        return;
      }
      var svg = sanitize(result.svg, doc);
      svg.style.minWidth = Math.round(width * 0.75) + 'px';
      figure.appendChild(svg);
      pre.hidden = true;
      states.set(pre, { source: source, node: figure, token: token, kind: 'success' });
    } catch (error) {
      figure.remove();
      showError(pre, source, firstLine(error), root, token);
    }
  }

  async function render(root) {
    root = root || global.document;
    try {
      if (!root || (root.nodeType !== 1 && root.nodeType !== 9)) return;
      var blocks = [];
      if (root.nodeType === 1 && root.matches('pre') && root.querySelector('code.language-mermaid')) blocks.push(root);
      blocks.push.apply(blocks, Array.from(root.querySelectorAll('pre > code.language-mermaid')).map(function (code) { return code.parentElement; }));
      var active = new Set(blocks);
      if (root.nodeType === 1 && root.matches('pre') && !active.has(root)) {
        var rootState = states.get(root);
        if (rootState && rootState.node && root.nextElementSibling === rootState.node) {
          if (rootState.kind === 'success') root.hidden = false;
          rootState.node.remove();
          states.delete(root);
        }
      }
      Array.from(root.querySelectorAll('figure.jc-mermaid, p.jc-mermaid-error')).forEach(function (node) {
        var previous = node.previousElementSibling;
        if (!previous || !active.has(previous)) {
          var previousState = previous && states.get(previous);
          if (previousState && previousState.node === node) {
            if (previousState.kind === 'success') previous.hidden = false;
            states.delete(previous);
          }
          node.remove();
        }
      });
      await Promise.all(blocks.map(function (pre) { return renderOne(pre, root).catch(function (error) {
        try { showError(pre, pre.querySelector('code.language-mermaid').textContent, firstLine(error), root, states.get(pre).token); } catch (_) { }
      }); }));
    } catch (_) {
      // Rendering is best-effort by contract. A malformed block must not reject callers.
    }
  }

  global.JellyMermaid = { render: render };
})(window);
