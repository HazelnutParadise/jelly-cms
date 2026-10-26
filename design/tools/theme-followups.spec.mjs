// Browser acceptance for docs/tickets/10-theme-followups.md.
// Start internal/server/TestFollowupBrowserFixture first; credentials stay in /tmp.
import fs from 'node:fs/promises';
import path from 'node:path';

const playwrightModule = process.env.PLAYWRIGHT_MODULE || 'playwright';
const { chromium } = await import(playwrightModule);
const evidenceDir = '/tmp/jelly29-browser';
const fixturePath = '/tmp/jelly29-fixture.json';
const tokenPath = '/tmp/jelly29-fixture-token';
const fixture = JSON.parse(await fs.readFile(fixturePath, 'utf8'));
const controlToken = (await fs.readFile(tokenPath, 'utf8')).trim();
await fs.mkdir(evidenceDir, { recursive: true });

const evidence = { fixture: fixture.fixture_label, startedAt: new Date().toISOString(), checks: [], pages: [], externalRequests: [], consoleErrors: [], expectedConsoleErrors: [], fontResponses: [] };
let maintenanceEnabled = false;
const check = (name, ok, detail = '') => evidence.checks.push({ name, ok: Boolean(ok), detail: String(detail) });
const closeTo = (value, target, tolerance) => Math.abs(value - target) <= tolerance;
const box = async (page, selector) => page.locator(selector).first().evaluate(el => {
  const r = el.getBoundingClientRect();
  return { x: r.x, y: r.y, left: r.left, top: r.top, right: r.right, bottom: r.bottom, width: r.width, height: r.height };
});
const contrastFor = async (page, selector) => page.locator(selector).evaluateAll(elements => elements.map(el => {
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = 1;
  const ctx = canvas.getContext('2d', { willReadFrequently: true });
  const rgba = value => {
    ctx.clearRect(0, 0, 1, 1);
    ctx.fillStyle = value;
    ctx.fillRect(0, 0, 1, 1);
    return [...ctx.getImageData(0, 0, 1, 1).data];
  };
  let background = [255, 255, 255, 255];
  const chain = [];
  for (let n = el; n && n.nodeType === 1; n = n.parentElement) chain.push(n);
  for (const node of chain.reverse()) {
    const layer = rgba(getComputedStyle(node).backgroundColor);
    const alpha = layer[3] / 255;
    background = [0, 1, 2].map(i => Math.round(layer[i] * alpha + background[i] * (1 - alpha))).concat(255);
  }
  const foreground = rgba(getComputedStyle(el).color);
  const fgAlpha = foreground[3] / 255;
  const composite = [0, 1, 2].map(i => Math.round(foreground[i] * fgAlpha + background[i] * (1 - fgAlpha)));
  const lum = color => color.slice(0, 3).map(v => {
    const s = v / 255;
    return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  }).reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
  const l1 = lum(composite), l2 = lum(background);
  return { ratio: (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05), color: getComputedStyle(el).color, background };
}));
const checkContrast = async (page, name, selector) => {
  const scores = await contrastFor(page, selector);
  check(`${name} (${selector})`, scores.length > 0 && scores.every(score => score.ratio !== null && score.ratio >= 4.5), JSON.stringify(scores));
};
const loadBothFonts = async (page, name) => {
  const fonts = await page.evaluate(async () => {
    const [displayFaces, bodyFaces] = await Promise.all([
      document.fonts.load('16px "Bricolage Grotesque"', 'Jelly CMS'),
      document.fonts.load('16px "Noto Sans TC"', '測試文字'),
    ]);
    return {
      display: document.fonts.check('16px "Bricolage Grotesque"', 'Jelly CMS') && displayFaces.length > 0,
      body: document.fonts.check('16px "Noto Sans TC"', '測試文字') && bodyFaces.length > 0,
      families: [...document.fonts].filter(face => face.status === 'loaded').map(face => face.family),
    };
  });
  check(name, fonts.display && fonts.body, JSON.stringify(fonts));
};
const toggleMaintenance = async enabled => {
  const response = await fetch(new URL(fixture.control_path, fixture.url), {
    method: 'POST', headers: { authorization: `Bearer ${controlToken}`, 'content-type': 'application/json' },
    body: JSON.stringify({ enabled }),
  });
  check(`fixture maintenance toggle ${enabled}`, response.status === 204, `HTTP ${response.status}`);
  if (response.status === 204) maintenanceEnabled = enabled;
};

const browser = await chromium.launch({ headless: true });
try {
  for (const colorScheme of ['light', 'dark']) {
    for (const width of [1280, 390]) {
      const context = await browser.newContext({ viewport: { width, height: 960 }, colorScheme, reducedMotion: 'reduce' });
      const page = await context.newPage();
      await page.route('**/*', route => {
        const requestURL = new URL(route.request().url());
        if (requestURL.origin !== fixture.url) {
          evidence.externalRequests.push(requestURL.href);
          return route.abort();
        }
        return route.continue();
      });
      page.on('console', message => { if (message.type() === 'error') evidence.consoleErrors.push({ scheme: colorScheme, width, text: message.text() }); });
      page.on('pageerror', error => evidence.consoleErrors.push({ scheme: colorScheme, width, text: String(error) }));
      page.on('response', async response => {
        if (new URL(response.url()).pathname.includes('/fonts/')) {
          evidence.fontResponses.push({ url: response.url(), status: response.status(), cacheControl: response.headers()['cache-control'] || '' });
        }
      });

      const routes = [
        ['home', '/'], ['posts', '/posts'], ['static', fixture.static_path], ['search', `/search?q=${encodeURIComponent(fixture.search_query)}`],
        ['post-middle', fixture.post_paths[3]], ['post-latest', fixture.post_paths[6]], ['post-oldest', fixture.post_paths[0]],
      ];
      for (const [label, routePath] of routes) {
        const response = await page.goto(new URL(routePath, fixture.url).href, { waitUntil: 'networkidle' });
        check(`${label} ${width}px ${colorScheme} loads`, response?.status() === 200, `HTTP ${response?.status()}`);
        const pageRecord = { label, width, colorScheme, route: routePath, status: response?.status(), title: await page.title(), overflow: await page.evaluate(() => document.documentElement.scrollWidth > innerWidth) };
        evidence.pages.push(pageRecord);
        check(`${label} ${width}px ${colorScheme} has no horizontal overflow`, !pageRecord.overflow, JSON.stringify(pageRecord));

        await loadBothFonts(page, `B10 fonts ${label} ${width}px ${colorScheme}`);
        if (label === 'home') {
          const masthead = await box(page, '.masthead');
          const hero = await box(page, '.home-hero');
          const stats = await box(page, '.home-stats');
          const gap = hero.top - masthead.bottom;
          const statsGap = stats.top - hero.bottom;
          check(`B1 home hero/header ${width}px`, closeTo(gap, width === 1280 ? 16 : 0, width === 1280 ? 2 : 1), `gap=${gap}`);
          check(`B1 home stats/hero ${width}px`, closeTo(statsGap, width === 1280 ? 8 : 6, 2), `gap=${statsGap}`);
          if (width === 390) {
            check('B2 compact mobile hero', hero.height <= 175, `height=${hero.height}`);
            check('B2 no visible home search form on mobile', !(await page.locator('.home-hero .search-form').isVisible().catch(() => false)), 'home hero search form visibility');
            check('B2 stats are three columns', await page.locator('.home-stats > div').count() === 3, 'stats cell count');
            const title = await page.locator('.home-hero h1').evaluate(el => ({ box: el.getBoundingClientRect().height, fontSize: parseFloat(getComputedStyle(el).fontSize) }));
            check('B2 mobile hero title is one 44px-class line', title.fontSize >= 42 && title.fontSize <= 48 && title.box <= title.fontSize * 1.5, JSON.stringify(title));
            const tagline = page.locator('.home-tagline');
            if (await tagline.count()) check('B2 mobile tagline is one line', await tagline.evaluate(el => el.getBoundingClientRect().height <= parseFloat(getComputedStyle(el).lineHeight) * 1.1), 'tagline line-height');
            check('B2 hero artwork overlays rather than occupies a row', await page.locator('.hero-art').evaluate(el => getComputedStyle(el).position === 'absolute'), 'hero-art position');
          }
          for (const selector of ['.home-stats > div', '.home-stats strong', '.home-stats span', '.home-intro h2', '.home-intro p', '.button-primary']) {
            if (selector === '.button-primary' && width === 390) continue;
            const count = await page.locator(selector).count();
            check(`B4 required element ${selector} ${width}px ${colorScheme}`, count > 0, `count=${count}`);
            if (!count) continue;
            await checkContrast(page, `B4 contrast ${selector} ${width}px ${colorScheme}`, selector);
          }
          if (width === 1280) {
            const primary = page.locator('.button-primary:visible').first();
            check(`B4 visible primary button ${colorScheme}`, await primary.count() > 0, `count=${await primary.count()}`);
            if (await primary.count()) {
              await primary.hover();
              await checkContrast(page, `B4 primary button hover contrast ${width}px ${colorScheme}`, '.button-primary:visible');
            }
          } else {
            const mobileButton = page.locator('.masthead-toggle:visible').first();
            check(`B4 visible mobile button ${colorScheme}`, await mobileButton.count() > 0, `count=${await mobileButton.count()}`);
            if (await mobileButton.count()) {
              await mobileButton.hover();
              await checkContrast(page, `B4 visible mobile button hover contrast ${colorScheme}`, '.masthead-toggle:visible');
            }
          }
          const search = page.locator('.masthead .search-toggle, .masthead a[href="/search"], .masthead button[aria-label*="搜尋"]').first();
          if (await search.count()) {
            await search.click().catch(() => {});
            const searchInput = page.locator('input[name="q"]:visible').first();
            if (await searchInput.count()) {
              await searchInput.fill('jelly fixture');
              await searchInput.press('Enter');
              await page.waitForURL(/\/search\?/);
              check(`search interaction ${width}px ${colorScheme}`, new URL(page.url()).searchParams.get('q') === 'jelly fixture', page.url());
              await page.goto(new URL('/', fixture.url).href, { waitUntil: 'networkidle' });
            }
          }
        }
        if (label === 'posts' && width === 1280) {
          const head = await box(page, '.posts-head h1');
          const chips = await box(page, '.posts-head .category-chips');
          check('B5 category chips share heading row and align right', Math.abs(chips.right - (await page.evaluate(() => document.querySelector('.posts-head').getBoundingClientRect().right))) <= 1 && Math.abs(chips.bottom - head.bottom) <= 12, JSON.stringify({ head, chips }));
          const chipColors = await page.locator('.category-chips .chip').evaluateAll(chips => chips.map(chip => ({ current: chip.getAttribute('aria-current') === 'page', background: getComputedStyle(chip).backgroundColor })));
          check('B5 selected category chip has distinct dark treatment', chipColors.some(x => x.current) && chipColors.some(x => !x.current) && new Set(chipColors.map(x => x.background)).size > 1, JSON.stringify(chipColors));
          check('B5 each category dot is inside category row', await page.locator('.entry-cats .entry-flavor').count() > 0 && await page.locator('.entry:not(:has(.entry-cats .entry-flavor)) > .entry-flavor').count() === 0, 'category marker placement');
          check('B5 at least one cover or placeholder exists', await page.locator('.entry-cover, .entry-cover-placeholder').count() > 0, 'cover inventory');
          const placeholder = page.locator('.entry-cover-placeholder').first();
          check('B5 no-cover placeholder exists', await placeholder.count() > 0, `count=${await placeholder.count()}`);
          if (await placeholder.count()) {
            const placeholderBox = await box(page, '.entry-cover-placeholder');
            check('B5 placeholder is 180px by 4:3', closeTo(placeholderBox.width, 180, 2) && closeTo(placeholderBox.width / placeholderBox.height, 4 / 3, 0.02), JSON.stringify(placeholderBox));
          }
          const categoryChip = page.locator('.entry-cats a.chip:has(.entry-flavor)').first();
          if (await categoryChip.count()) {
            const markerAlignment = await categoryChip.evaluate(chip => {
              const dot = chip.querySelector('.entry-flavor').getBoundingClientRect();
              const walker = document.createTreeWalker(chip, NodeFilter.SHOW_TEXT);
              let node;
              while ((node = walker.nextNode()) && !node.textContent.trim()) {}
              const range = document.createRange();
              range.selectNodeContents(node);
              const text = range.getBoundingClientRect();
              return { dotCenter: dot.top + dot.height / 2, textCenter: text.top + text.height / 2, dotLeft: dot.left, textLeft: text.left };
            });
            check('B5 category dot shares text row', Math.abs(markerAlignment.dotCenter - markerAlignment.textCenter) <= 4 && markerAlignment.dotLeft < markerAlignment.textLeft, JSON.stringify(markerAlignment));
          }
        }
        if (label === 'posts' && width === 390) {
          const head = await box(page, '.posts-head h1');
          const chips = await box(page, '.posts-head .category-chips');
          check('B5 mobile category chips are below heading and left aligned', chips.top >= head.bottom && Math.abs(chips.left - (await box(page, '.posts-head')).left) <= 2, JSON.stringify({ head, chips }));
          const colors = await page.locator('.category-chips .chip').evaluateAll(chips => chips.map(chip => ({ current: chip.getAttribute('aria-current') === 'page', background: getComputedStyle(chip).backgroundColor })));
          check('B5 mobile selected and unselected chips differ', colors.some(x => x.current) && colors.some(x => !x.current) && new Set(colors.map(x => x.background)).size > 1, JSON.stringify(colors));
        }
        if (label === 'static' && width === 1280) {
          const aside = page.locator('.static-page-aside').first();
          const quick = page.locator('.page-quicklinks');
          check('B6 quick links panel is in aside', await quick.count() === 1 && await quick.evaluate(el => Boolean(el.closest('aside'))), 'panel/aside relationship');
          check('B6 quick links target posts and search', await quick.locator('a[href="/posts"]').count() === 1 && await quick.locator('a[href="/search"]').count() === 1, 'quick links hrefs');
          check('B6 quick links label and button icons', (await quick.innerText()).includes('快速連結') && await quick.locator('a.button svg').count() === 2, 'panel text and icon count');
          check('B6 quick links buttons fill panel width and align left', await quick.locator('a.button').evaluateAll(links => links.length === 2 && links.every(link => {
            const style = getComputedStyle(link);
            const parent = link.parentElement;
            const parentStyle = getComputedStyle(parent);
            const parentRect = parent.getBoundingClientRect();
            const borderLeft = parseFloat(parentStyle.borderLeftWidth) || 0;
            const borderRight = parseFloat(parentStyle.borderRightWidth) || 0;
            const paddingLeft = parseFloat(parentStyle.paddingLeft) || 0;
            const paddingRight = parseFloat(parentStyle.paddingRight) || 0;
            const contentWidth = parent.clientWidth - paddingLeft - paddingRight;
            const expectedLeft = parentRect.left + borderLeft + paddingLeft;
            const rect = link.getBoundingClientRect();
            return Math.abs(rect.width - contentWidth) <= 2 && Math.abs(rect.left - expectedLeft) <= 2
              && Math.abs(rect.right - (expectedLeft + contentWidth)) <= 2 && style.justifyContent === 'flex-start';
          })), 'button geometry');
          const artBox = await box(page, '.page-art');
          check('B6 page artwork is square', closeTo(artBox.width, artBox.height, 1), JSON.stringify(artBox));
          await quick.locator('a[href="/search"]').click();
          check('quick links search interaction', new URL(page.url()).pathname === '/search', page.url());
          await page.goto(new URL(routePath, fixture.url).href, { waitUntil: 'networkidle' });
          if (await aside.count()) {
            const a = await box(page, '.static-page-aside');
            const content = await box(page, '.static-page-content');
            check('B6 aside width', closeTo(a.width, 360, 8), `width=${a.width}`);
            check('B6 aside is 64px from content', closeTo(a.left - content.right, 64, 8), JSON.stringify({ content, aside: a }));
          }
        }
        if (label === 'static' && width === 390) {
          const content = await box(page, '.static-page-content');
          const aside = await box(page, '.static-page-aside');
          check('B6 mobile aside follows content', aside.top >= content.bottom && await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), JSON.stringify({ content, aside }));
          check('B6 mobile quick links remain visible below content', await page.locator('.page-quicklinks').isVisible(), 'quick links visibility');
        }
        if (label === 'search') {
          const band = await box(page, '.search-band');
          const masthead = await box(page, '.masthead');
          check(`B1 search band/header ${width}px`, closeTo(band.top - masthead.bottom, 0, 1), `gap=${band.top - masthead.bottom}`);
          if (await page.locator('.search-band .stripes').count()) {
            const stripeColor = await page.locator('.search-band .stripes').evaluate(el => {
              const image = getComputedStyle(el).backgroundImage;
              const firstColor = image.match(/color\(srgb[^)]*\)|rgba?\([^)]*\)|#[\da-f]{3,8}/i)?.[0];
              const canvas = document.createElement('canvas');
              canvas.width = canvas.height = 1;
              const ctx = canvas.getContext('2d', { willReadFrequently: true });
              const pixel = value => {
                ctx.clearRect(0, 0, 1, 1);
                ctx.fillStyle = value;
                ctx.fillRect(0, 0, 1, 1);
                return [...ctx.getImageData(0, 0, 1, 1).data];
              };
              return { image, first: firstColor ? pixel(firstColor) : null, ink: pixel(getComputedStyle(el).getPropertyValue('--dark') || getComputedStyle(document.documentElement).getPropertyValue('--dark')) };
            });
            const colorMatchesInk = stripeColor.first && stripeColor.ink && stripeColor.first.slice(0, 3).every((channel, i) => Math.abs(channel - stripeColor.ink[i]) <= 1);
            check(`B7 search stripe is dark ink ${width}px ${colorScheme}`, Boolean(colorMatchesInk), JSON.stringify(stripeColor));
          }
          for (const selector of ['.search-band .label', '.search-hit mark', '.search-hit p', '.search-hit h2']) {
            const count = await page.locator(selector).count();
            check(`B4 required element ${selector} ${width}px ${colorScheme}`, count > 0, `count=${count}`);
            if (!count) continue;
            await checkContrast(page, `B4 contrast ${selector} ${width}px ${colorScheme}`, selector);
          }
        }
        if (['posts', 'static', 'search'].includes(label)) {
          const headerSelector = label === 'search' ? '.search-band' : label === 'static' ? '.article-head, .page-head' : '.posts-head';
          const stripeCount = await page.locator(headerSelector).first().evaluate(el => {
            const inlineStripe = el.querySelectorAll('.stripes').length;
            const style = getComputedStyle(el, '::before');
            const pseudoStripe = style.content !== 'none' && style.display !== 'none' && /gradient\(/i.test(style.backgroundImage) ? 1 : 0;
            return inlineStripe + pseudoStripe;
          });
          check(`B7 one header stripe ${label} ${width}px ${colorScheme}`, stripeCount === 1, `stripe count=${stripeCount}`);
        }
        if (label.startsWith('post-')) {
          const hero = await box(page, '.post-hero');
          const masthead = await box(page, '.masthead');
          check(`B1 post hero/header ${label} ${width}px`, closeTo(hero.top - masthead.bottom, 0, 1), `gap=${hero.top - masthead.bottom}`);
          if (label === 'post-middle') {
            const tocLinks = page.locator('.toc a');
            check(`B8 table of contents exists ${width}px`, await tocLinks.count() >= 3, `count=${await tocLinks.count()}`);
            const tocDetails = page.locator('.toc-details');
            const tocSummary = tocDetails.locator('summary');
            const tocWasClosed = await tocDetails.evaluate(details => !details.open);
            if (width === 390 && tocWasClosed) {
              await tocSummary.click();
              await page.waitForFunction(() => document.querySelector('.toc-details')?.open === true);
            }
            const level2 = page.locator('.toc a[data-toc-level="2"]').first();
            const level3 = page.locator('.toc a[data-toc-level="3"]').first();
            if (await level2.count() && await level3.count()) {
              const [b2, b3] = await Promise.all([box(page, '.toc a[data-toc-level="2"]'), box(page, '.toc a[data-toc-level="3"]')]);
              const textIndent = await page.locator('.toc a[data-toc-level="2"], .toc a[data-toc-level="3"]').evaluateAll(links => links.map(link => {
                const walker = document.createTreeWalker(link, NodeFilter.SHOW_TEXT);
                let node;
                while ((node = walker.nextNode()) && !node.textContent.trim()) {}
                const range = document.createRange();
                range.selectNodeContents(node);
                return { level: link.getAttribute('data-toc-level'), left: range.getBoundingClientRect().left };
              }));
              const left2 = textIndent.find(x => x.level === '2')?.left;
              const left3 = textIndent.find(x => x.level === '3')?.left;
              check(`B8 h3 TOC text indentation ${width}px`, Number.isFinite(left2) && Number.isFinite(left3) && left3 - left2 >= 12, JSON.stringify({ b2, b3, textIndent }));
              await checkContrast(page, `B8 TOC contrast ${width}px ${colorScheme}`, '.toc a');
              await level3.click();
              check(`TOC interaction ${width}px ${colorScheme}`, page.url().includes('#details-'), page.url());
              await page.evaluate(() => {
                history.replaceState(null, '', location.pathname + location.search);
                window.scrollTo(0, 0);
              });
              if (width === 390 && tocWasClosed) {
                await tocSummary.click();
                await page.waitForFunction(() => document.querySelector('.toc-details')?.open === false);
              }
            }
          }
          if (label === 'post-middle' || label === 'post-latest' || label === 'post-oldest') {
            const prev = page.locator('.post-nav a.post-nav-prev');
            const next = page.locator('.post-nav a.post-nav-next');
            const expected = label === 'post-middle' ? [1, 1] : label === 'post-latest' ? [1, 0] : [0, 1];
            check(`B9 navigation directions ${label}`, await prev.count() === expected[0] && await next.count() === expected[1], `prev=${await prev.count()} next=${await next.count()}`);
            const directionStyle = await page.locator('.post-nav a').evaluateAll(links => links.map(link => {
              const canvas = document.createElement('canvas');
              canvas.width = canvas.height = 1;
              const ctx = canvas.getContext('2d', { willReadFrequently: true });
              ctx.fillStyle = getComputedStyle(link).backgroundColor;
              ctx.fillRect(0, 0, 1, 1);
              const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
              const luminance = [r, g, b].map(v => { const s = v / 255; return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4; }).reduce((sum, v, i) => sum + v * [0.2126, 0.7152, 0.0722][i], 0);
              return { direction: link.classList.contains('post-nav-prev') ? 'prev' : 'next', luminance, label: link.querySelector('.label')?.innerText.trim(), textAlign: getComputedStyle(link).textAlign };
            }));
            const prevStyle = directionStyle.find(x => x.direction === 'prev');
            const nextStyle = directionStyle.find(x => x.direction === 'next');
            const nextAligned = !nextStyle || (width === 390
              ? ['start', 'left'].includes(nextStyle.textAlign)
              : ['end', 'right'].includes(nextStyle.textAlign));
            const directionColors = (!prevStyle || !nextStyle || prevStyle.luminance > nextStyle.luminance)
              && (!prevStyle || prevStyle.label.includes('上一篇')) && (!nextStyle || nextStyle.label.includes('下一篇'))
              && (!prevStyle || ['start', 'left'].includes(prevStyle.textAlign))
              && nextAligned;
            check(`B9 light previous and dark next treatment ${label} ${colorScheme}`, directionColors, JSON.stringify(directionStyle));
            const navigationLink = expected[0] ? prev : next;
            const target = new URL(await navigationLink.getAttribute('href'), fixture.url);
            await navigationLink.click();
            await page.waitForURL(url => url.pathname === target.pathname);
            check(`B9 navigation link works ${label}`, new URL(page.url()).pathname === target.pathname, page.url());
            await page.goto(new URL(routePath, fixture.url).href, { waitUntil: 'networkidle' });
            if (width === 1280) {
              const nav = await box(page, '.post-nav');
              const positions = await page.locator('.post-nav a').evaluateAll(els => els.map(el => ({
                direction: el.classList.contains('post-nav-prev') ? 'prev' : 'next',
                left: el.getBoundingClientRect().left,
                right: el.getBoundingClientRect().right,
                gridColumn: getComputedStyle(el).gridColumnStart,
                textAlign: getComputedStyle(el).textAlign,
              })));
              const positioned = positions.every(link => link.direction === 'prev'
                ? link.left < nav.left + nav.width / 2 && ['start', 'left'].includes(link.textAlign)
                : link.right > nav.left + nav.width / 2 && ['end', 'right'].includes(link.textAlign) && link.gridColumn === '2');
              check(`B9 desktop navigation alignment ${label}`, positioned, JSON.stringify({ nav, positions }));
            }
            if (width === 390) {
              const nav = await box(page, '.post-nav');
              const links = await page.locator('.post-nav a').evaluateAll(els => els.map(el => ({ left: el.getBoundingClientRect().left, top: el.getBoundingClientRect().top, width: el.getBoundingClientRect().width, textAlign: getComputedStyle(el).textAlign })));
              const oneColumn = links.length < 2 || links[1].top > links[0].top;
              check(`B9 mobile navigation is one column ${label}`, oneColumn && links.every(link => Math.abs(link.left - nav.left) <= 2 && link.left + link.width <= nav.right + 1 && ['start', 'left'].includes(link.textAlign)), JSON.stringify({ nav, links }));
            }
          }
        }

        const screenshot = path.join(evidenceDir, `${label}-${width}-${colorScheme}.png`);
        await page.screenshot({ path: screenshot, fullPage: true });
        pageRecord.screenshot = screenshot;
      }

      await context.close();
    }
  }

  for (const colorScheme of ['light', 'dark']) {
    const cacheContext = await browser.newContext({ viewport: { width: 1280, height: 960 }, colorScheme, reducedMotion: 'reduce' });
    const cachePage = await cacheContext.newPage();
    cachePage.on('request', request => {
      const requestURL = new URL(request.url());
      if (requestURL.origin !== fixture.url) evidence.externalRequests.push(requestURL.href);
    });
    cachePage.on('response', response => {
      if (new URL(response.url()).pathname.includes('/fonts/')) {
        evidence.fontResponses.push({ url: response.url(), status: response.status(), cacheControl: response.headers()['cache-control'] || '' });
      }
    });
    const initial = await cachePage.goto(new URL('/', fixture.url).href, { waitUntil: 'networkidle' });
    check(`B10 cache context home loads ${colorScheme}`, initial?.status() === 200, `HTTP ${initial?.status()}`);
    await loadBothFonts(cachePage, `B10 cache context fonts ${colorScheme}`);
    await cachePage.evaluate(() => performance.clearResourceTimings());
    const reloaded = await cachePage.reload({ waitUntil: 'networkidle' });
    check(`B10 cache context reload ${colorScheme}`, reloaded?.status() === 200, `HTTP ${reloaded?.status()}`);
    const cache = await cachePage.evaluate(() => performance.getEntriesByType('resource')
      .filter(entry => entry.name.includes('/fonts/'))
      .map(entry => ({ name: entry.name, transferSize: entry.transferSize })));
    check(`B10 both font files reused from HTTP cache ${colorScheme}`,
      new Set(cache.map(entry => new URL(entry.name).pathname)).size >= 2 && cache.every(entry => entry.transferSize === 0), JSON.stringify(cache));
    await cacheContext.close();
  }

  check('B10 no external resource requests', evidence.externalRequests.length === 0, JSON.stringify(evidence.externalRequests));
  check('browser pages have no console errors', evidence.consoleErrors.length === 0, JSON.stringify(evidence.consoleErrors));
  check('B10 both local font files responded', new Set(evidence.fontResponses.map(x => new URL(x.url).pathname)).size >= 2, JSON.stringify(evidence.fontResponses));
  for (const response of evidence.fontResponses) check(`B10 font cache header ${response.url}`, response.status === 200 && /public/.test(response.cacheControl) && /max-age=2592000/.test(response.cacheControl), JSON.stringify(response));

  await toggleMaintenance(true);
  for (const colorScheme of ['light', 'dark']) {
    for (const width of [1280, 390]) {
      const maintenanceContext = await browser.newContext({ viewport: { width, height: 960 }, colorScheme, reducedMotion: 'reduce' });
      const maintenancePage = await maintenanceContext.newPage();
      const maintenanceConsoleErrors = [];
      const maintenanceURL = new URL('/', fixture.url).href;
      await maintenancePage.route('**/*', route => {
        const requestURL = new URL(route.request().url());
        if (requestURL.origin !== fixture.url) evidence.externalRequests.push(requestURL.href);
        return requestURL.origin === fixture.url ? route.continue() : route.abort();
      });
      maintenancePage.on('response', response => {
        if (new URL(response.url()).pathname.includes('/fonts/')) {
          const headers = response.headers();
          evidence.fontResponses.push({ url: response.url(), status: response.status(), cacheControl: headers['cache-control'] || '' });
        }
      });
      maintenancePage.on('console', message => {
        if (message.type() === 'error') maintenanceConsoleErrors.push({ text: message.text(), sourceURL: message.location().url || '' });
      });
      maintenancePage.on('pageerror', error => evidence.consoleErrors.push({ scheme: colorScheme, width, text: String(error) }));
      const maintenanceResponse = await maintenancePage.goto(maintenanceURL, { waitUntil: 'networkidle' });
      check(`B3 maintenance response ${width}px ${colorScheme}`, maintenanceResponse?.status() === 503, `HTTP ${maintenanceResponse?.status()}`);
      check(`B3 Retry-After header ${width}px ${colorScheme}`, maintenanceResponse?.headers()['retry-after'] === '600', `Retry-After=${maintenanceResponse?.headers()['retry-after']}`);
      const confirmed503Document = maintenanceResponse?.status() === 503 && maintenanceResponse.url() === maintenanceURL;
      const expected503Message = 'Failed to load resource: the server responded with a status of 503 (Service Unavailable)';
      let matchedExpected503 = false;
      for (const consoleError of maintenanceConsoleErrors) {
        const isExpectedDocumentFailure = confirmed503Document && !matchedExpected503
          && consoleError.text === expected503Message
          && (!consoleError.sourceURL || consoleError.sourceURL === maintenanceURL);
        if (isExpectedDocumentFailure) {
          matchedExpected503 = true;
          evidence.expectedConsoleErrors.push({ scheme: colorScheme, width, text: consoleError.text, responseURL: maintenanceResponse.url(), responseStatus: maintenanceResponse.status() });
        } else {
          evidence.consoleErrors.push({ scheme: colorScheme, width, text: consoleError.text, sourceURL: consoleError.sourceURL });
        }
      }
      await loadBothFonts(maintenancePage, `B10 maintenance fonts ${width}px ${colorScheme}`);
      const art = await box(maintenancePage, '.art');
      const elapsed = await box(maintenancePage, '.elapsed');
      const noElapsedOverlap = elapsed.right <= art.left || elapsed.left >= art.right || elapsed.bottom <= art.top || elapsed.top >= art.bottom;
      check(`B3 elapsed text does not overlap art ${width}px ${colorScheme}`, noElapsedOverlap, JSON.stringify({ art, elapsed }));
      if (width === 1280) {
        const textBoxes = await maintenancePage.locator('main :is(h1,p,button,a,.label,.elapsed)').evaluateAll(elements => elements.map(el => {
          const r = el.getBoundingClientRect();
          return { text: el.innerText.trim(), left: r.left, right: r.right, top: r.top, bottom: r.bottom };
        }).filter(x => x.text));
        const collisions = textBoxes.filter(text => !(text.right <= art.left || text.left >= art.right || text.bottom <= art.top || text.top >= art.bottom));
        check(`B3 no text overlaps art ${width}px ${colorScheme}`, collisions.length === 0, JSON.stringify({ collisions, art }));
        check(`B3 content width ${width}px ${colorScheme}`, await maintenancePage.locator('main').evaluate(el => el.getBoundingClientRect().width) <= 880, 'main width');
        check(`B3 buttons include SVG icons ${width}px ${colorScheme}`, await maintenancePage.locator('.actions .button svg').count() === 2, 'button SVG count');
        const elapsedText = await maintenancePage.locator('.elapsed').innerText();
        check(`B3 server local time ${colorScheme}`, elapsedText.includes('2025年6月3日 00:00'), elapsedText);
      }
      if (width === 390) check(`B3 no mobile horizontal overflow ${colorScheme}`, await maintenancePage.evaluate(() => document.documentElement.scrollWidth <= innerWidth), '390px overflow');
      await maintenancePage.screenshot({ path: path.join(evidenceDir, `maintenance-${width}-${colorScheme}.png`), fullPage: true });
      await maintenanceContext.close();
    }
  }
  await toggleMaintenance(false);
  check('B10 no external requests including maintenance', evidence.externalRequests.length === 0, JSON.stringify(evidence.externalRequests));
  check('no console errors across public and maintenance pages', evidence.consoleErrors.length === 0, JSON.stringify(evidence.consoleErrors));
  check('B10 both local font files responded', new Set(evidence.fontResponses.map(x => new URL(x.url).pathname)).size >= 2, JSON.stringify(evidence.fontResponses));
  for (const response of evidence.fontResponses) check(`B10 font cache header ${response.url}`, response.status === 200 && /public/.test(response.cacheControl) && /max-age=2592000/.test(response.cacheControl), JSON.stringify(response));
} catch (error) {
  evidence.fatal = String(error?.stack || error);
} finally {
  if (maintenanceEnabled) {
    try { await toggleMaintenance(false); } catch (error) { evidence.cleanupError = String(error); }
  }
  await browser.close();
  evidence.finishedAt = new Date().toISOString();
  evidence.summary = { passed: evidence.checks.filter(x => x.ok).length, failed: evidence.checks.filter(x => !x.ok).length };
  await fs.writeFile(path.join(evidenceDir, 'evidence.json'), JSON.stringify(evidence, null, 2));
}

console.log(`Browser evidence: ${path.join(evidenceDir, 'evidence.json')}`);
console.log(`Checks: ${evidence.summary.passed} passed, ${evidence.summary.failed} failed`);
if (evidence.fatal || evidence.summary.failed) process.exitCode = 1;
