// Browser tests for the mor panel.
//
// Everything here goes through a real browser against a running mor, because
// that is the only place the panel's failures show up: none of them break the
// Go build, and several of them broke nothing visible until somebody clicked
// the wrong thing.
//
// Each test gets a fresh page and cleans up the keys it made.

const { chromium, devices } = require('playwright');

const URL = process.env.MOR_URL || 'https://127.0.0.1:9090';
const PASSWORD = process.env.MOR_PASSWORD || '';

const tests = [];
const test = (name, fn, mobile = false) => tests.push({ name, fn, mobile });

/* ---------- helpers ---------- */

async function login(page) {
  await page.goto(URL, { waitUntil: 'networkidle', timeout: 20000 });
  await page.click('body');
  await page.keyboard.type(PASSWORD);
  await page.keyboard.press('Enter');
  await page.waitForSelector('.sq', { timeout: 15000 });
  await page.waitForTimeout(800);
}

// Keys are made through the API the panel itself uses, so a test that only
// checks display does not depend on the create form working.
async function makeKey(page, name, protocols = ['hy2', 'ss']) {
  return page.evaluate(async ([n, p]) => {
    const u = await api('/users', { method: 'POST', body: JSON.stringify({ name: n, protocols: p }) });
    await loadUsers();
    return u.id;
  }, [name, protocols]);
}

async function dropKeys(page, prefix) {
  await page.evaluate(async (pre) => {
    for (const u of [...users]) {
      if (u.name.startsWith(pre)) await api('/users/' + u.id, { method: 'DELETE' });
    }
    await loadUsers();
  }, prefix);
}

// waitFor polls instead of sleeping: creating a key takes a moment on a real
// server and much longer on a laptop with no engines installed, and a fixed
// pause is either too short there or wasted time here.
async function waitFor(page, fn, what, timeout = 20000, done = Boolean) {
  const until = Date.now() + timeout;
  for (;;) {
    const got = await page.evaluate(fn);
    if (done(got)) return got;
    if (Date.now() > until) throw new Error(`${what}: получено ${JSON.stringify(got)}`);
    await page.waitForTimeout(200);
  }
}

const keyGone = (page, name) =>
  waitFor(page, () => !document.querySelector('#createModal.show'), `окно не закрылось: ${name}`);

const eq = (got, want, what) => {
  if (got !== want) throw new Error(`${what}: получено ${JSON.stringify(got)}, ждали ${JSON.stringify(want)}`);
};
const ok = (cond, what) => {
  if (!cond) throw new Error(what);
};

/* ---------- tests ---------- */

test('вход и шесть плиток', async (page) => {
  await login(page);
  eq(await page.locator('.sq').count(), 6, 'плиток');
});

test('каждая плитка открывает свой раздел', async (page) => {
  await login(page);
  for (const pane of ['keys', 'status', 'system', 'traffic', 'actions', 'settings']) {
    await page.click(`[data-pane="${pane}"]`);
    await page.waitForTimeout(500);
    ok(await page.locator(`#pane-${pane}`).isVisible(), `раздел ${pane} не открылся`);
  }
});

test('создание ключа даёт рабочую ссылку', async (page) => {
  await login(page);
  await page.click('.btn-plus');
  await page.waitForTimeout(400);
  await page.fill('#createName', 'тест-создание');
  await page.click('#createProtos .pchip-all');
  await page.click('[data-act="doCreate"]');
  await keyGone(page, 'тест-создание');

  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(600);
  const names = await page.$$eval('.uh-name', (e) => e.map((x) => x.textContent));
  ok(names.includes('тест-создание'), 'ключ не появился в списке');

  const link = await page.locator('.uh.open .linkrow-text').textContent();
  ok(link && link.length > 20, 'ссылка пустая');
  await dropKeys(page, 'тест-');
});

test('создание без протокола не проходит молча', async (page) => {
  await login(page);
  await page.click('.btn-plus');
  await page.waitForTimeout(400);
  await page.fill('#createName', 'тест-без-протокола');
  await page.click('[data-act="doCreate"]');
  await page.waitForTimeout(800);
  ok(await page.locator('#createModal.show').count() === 1, 'окно закрылось без выбранного протокола');
  const toast = (await page.locator('#toast').textContent()).trim();
  ok(toast.length > 0, 'подсказка не показана');
  await page.click('[data-act="cancelCreate"]');
});

test('правка лимитов сохраняется и не дёргает экран', async (page) => {
  await login(page);
  await makeKey(page, 'тест-правка');
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(600);
  await page.click('.uh-open');
  await page.waitForTimeout(900);

  const id = await page.evaluate(() => homeExpanded);
  await page.fill(`#ed-traffic-${id}`, '7gb');
  await page.fill(`#ed-time-${id}`, '5d');
  const scroller = () => page.evaluate(() => document.querySelector('.detail-body').scrollTop);
  const before = await scroller();
  await page.click('[data-act="saveKey"]');
  await page.waitForTimeout(1800);

  ok(Math.abs((await scroller()) - before) < 12, 'экран дёрнулся при сохранении');
  ok(await page.locator('.uh.open').count() === 1, 'карточка закрылась');
  eq(await page.locator(`#ed-traffic-${id}`).inputValue(), '7gb', 'лимит трафика');
  await dropKeys(page, 'тест-');
});

test('срок и лимит одним числом не спорят друг с другом', async (page) => {
  await login(page);
  await page.click('.btn-plus');
  await page.waitForTimeout(400);
  await page.fill('#createName', 'тест-22');
  await page.click('#createProtos .pchip-all');
  await page.fill('#createTime', '22');
  await page.fill('#createTraffic', '22');
  await page.click('[data-act="doCreate"]');
  const u = await waitFor(page, () => users.find((x) => x.name === 'тест-22') || null, 'ключ не создан');
  ok(u.expiresAt, 'срок не выставлен');
  ok(u.limit > 0, 'лимит не выставлен');
  await dropKeys(page, 'тест-');
});

test('неработающие ключи приглушены, рабочие нет', async (page) => {
  await login(page);
  const id = await makeKey(page, 'тест-бан');
  await makeKey(page, 'тест-живой');
  await page.evaluate((i) => api('/users/' + i + '/ban', { method: 'POST', body: JSON.stringify({ banned: true }) }), id);
  await page.evaluate(() => loadUsers());
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(900);

  const rows = await page.$$eval('.uh', (els) =>
    els.map((e) => ({ имя: e.querySelector('.uh-name').textContent, мёртвый: e.classList.contains('dead') })));
  const ban = rows.find((r) => r.имя === 'тест-бан');
  const live = rows.find((r) => r.имя === 'тест-живой');
  ok(ban && ban.мёртвый, 'забаненный не приглушён');
  ok(live && !live.мёртвый, 'живой приглушён');
  await dropKeys(page, 'тест-');
});

test('массовое удаление требует подтверждения', async (page) => {
  await login(page);
  await makeKey(page, 'тест-удалить-1');
  await makeKey(page, 'тест-удалить-2');
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(800);

  // Only the rows this test created. Picking by position once selected — and
  // deleted — a key that belonged to the person running the tests.
  for (const name of ['тест-удалить-1', 'тест-удалить-2']) {
    const row = page.locator('.uh', { has: page.locator('.uh-name', { hasText: name }) });
    await row.locator('.uh-pick').click();
  }
  await page.waitForTimeout(400);
  ok(await page.locator('#bulkBar.show').count() === 1, 'панель выбора не появилась');

  const wasCount = await page.locator('.uh').count();
  eq(await page.locator('#bulkDelBtn').textContent(), 'Удалить выбранные (2)', 'выбрано не два ключа');
  await page.click('#bulkDelBtn');
  await page.waitForTimeout(400);
  eq(await page.locator('.uh').count(), wasCount, 'удалило с первого нажатия');
  await page.click('#bulkDelBtn');
  await waitFor(page, () => document.querySelectorAll('.uh').length,
    'удалило не два ключа', 20000, (n) => n === wasCount - 2);
  await dropKeys(page, 'тест-');
});

test('счётчик устройств виден и сбрасывается', async (page) => {
  await login(page);
  const id = await makeKey(page, 'тест-устройства');
  await page.evaluate((i) => api('/users/' + i, { method: 'PATCH', body: JSON.stringify({ ipLimit: 3 }) }), id);
  await page.evaluate(() => loadUsers());

  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(600);
  const row = page.locator('.uh', { has: page.locator('.uh-name', { hasText: 'тест-устройства' }) });
  await row.locator('.uh-open').click();
  await page.waitForTimeout(800);

  const count = page.locator('.set-count');
  eq(await count.first().textContent(), '0/3', 'счётчик устройств');
  // The count is also the button that hands the slots back.
  await count.first().click();
  await page.waitForTimeout(600);
  eq(await count.first().textContent(), '0/3', 'счётчик после сброса');

  await dropKeys(page, 'тест-');
});

test('работает с клавиатуры', async (page) => {
  await login(page);
  await makeKey(page, 'тест-клавиатура');
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(800);

  await page.locator('.uh-open').first().focus();
  await page.keyboard.press('Enter');
  await page.waitForTimeout(900);
  ok(await page.locator('.uh.open').count() === 1, 'ключ не открылся с клавиатуры');

  const pick = page.locator('.uh-pick').first();
  await pick.focus();
  await page.keyboard.press('Space');
  await page.waitForTimeout(400);
  eq(await pick.getAttribute('aria-checked'), 'true', 'aria-checked после пробела');
  await page.keyboard.press('Space');
  await page.waitForTimeout(300);
  await dropKeys(page, 'тест-');
});

test('ни одного нарушения политики безопасности', async (page) => {
  const viol = [];
  page.on('console', (m) => {
    if (/Content Security Policy|Refused to/i.test(m.text())) viol.push(m.text().slice(0, 120));
  });
  await login(page);
  await makeKey(page, 'тест-csp');
  for (const pane of ['keys', 'status', 'system', 'traffic', 'actions', 'settings']) {
    await page.click(`[data-pane="${pane}"]`);
    await page.waitForTimeout(500);
  }
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(400);
  await page.click('.uh-open');
  await page.waitForTimeout(900);
  await page.click('.btn-plus');
  await page.waitForTimeout(400);
  await page.click('[data-act="cancelCreate"]');
  ok(viol.length === 0, 'нарушения CSP: ' + viol.join(' | '));
  await dropKeys(page, 'тест-');
});

// A <button> with no rule of its own is drawn by the browser: grey fill, black
// text, an outset border. Checks on text and classes do not catch that, so this
// one reads the computed style.
test('ни одной кнопки со стилем по умолчанию', async (page) => {
  await login(page);
  const id = await makeKey(page, 'тест-вид');
  // With a deadline and a cap set: the countdown button exists only when there
  // is something to count down to.
  await page.evaluate((i) => api('/users/' + i, { method: 'PATCH', body: JSON.stringify({ time: '30d', traffic: '50gb' }) }), id);
  await page.evaluate(() => loadUsers());

  const scan = () => page.evaluate(() => {
    const out = [];
    for (const el of document.querySelectorAll('button')) {
      if (!el.offsetParent) continue;
      const s = getComputedStyle(el);
      const beef = [];
      if (s.borderTopStyle === 'outset' || s.borderTopStyle === 'inset') beef.push('рамка ' + s.borderTopStyle);
      if (s.backgroundColor === 'rgb(239, 239, 239)') beef.push('фон по умолчанию');
      if (beef.length) out.push((el.className || '(без класса)') + ' — ' + beef.join(', '));
    }
    return [...new Set(out)];
  });

  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(600);
  const row = page.locator('.uh', { has: page.locator('.uh-name', { hasText: 'тест-вид' }) });
  await row.locator('.uh-open').click();
  await page.waitForTimeout(900);
  eq((await scan()).join(' · '), '', 'карточка ключа');

  for (const pane of ['status', 'system', 'traffic', 'actions', 'settings']) {
    await page.click(`[data-pane="${pane}"]`);
    await page.waitForTimeout(500);
    eq((await scan()).join(' · '), '', 'раздел ' + pane);
  }
  await page.click('.btn-plus');
  await page.waitForTimeout(500);
  eq((await scan()).join(' · '), '', 'окно создания');
  await page.click('[data-act="cancelCreate"]');

  await dropKeys(page, 'тест-');
});

// The list refreshes itself every fifteen seconds and redraws the open card
// with it. Anything the person picked by hand has to survive that, or the
// panel undoes their choice while they are still looking at it.
test('выбранный протокол переживает обновление списка', async (page) => {
  await login(page);
  await makeKey(page, 'тест-выбор', ['hy2', 'reality']);
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(600);
  const row = page.locator('.uh', { has: page.locator('.uh-name', { hasText: 'тест-выбор' }) });
  await row.locator('.uh-open').click();
  await page.waitForTimeout(900);

  await row.locator('.pchip', { hasText: 'VLESS+Reality' }).click();
  await page.waitForTimeout(300);
  const было = await row.locator('.linkrow-text').textContent();
  ok(было.startsWith('vless://'), `выбрана не прямая ссылка: ${было}`);

  await page.evaluate(() => refreshAll());
  await page.waitForTimeout(800);

  eq(await row.locator('.pchip.on').textContent(), 'VLESS+Reality', 'после обновления выбран не тот протокол');
  eq(await row.locator('.linkrow-text').textContent(), было, 'после обновления сменилась ссылка');
  await dropKeys(page, 'тест-');
});

// The two facts at the top of a key card are a pair, side by side. An unclosed
// tag in one of them closes the grid early and drops the other onto its own
// line; the card still renders, so only the geometry shows it.
test('срок и лимит стоят рядом, а не друг под другом', async (page) => {
  await login(page);
  const id = await makeKey(page, 'тест-пара');
  await page.evaluate((i) => api('/users/' + i, { method: 'PATCH', body: JSON.stringify({ time: '30d', traffic: '50gb' }) }), id);
  await page.evaluate(() => loadUsers());

  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(600);
  const row = page.locator('.uh', { has: page.locator('.uh-name', { hasText: 'тест-пара' }) });
  await row.locator('.uh-open').click();
  await page.waitForTimeout(900);

  const facts = await page.evaluate(() => {
    const f = document.querySelector('.uh.open .kfacts');
    return [...f.children].map((c) => ({ верх: Math.round(c.getBoundingClientRect().top), текст: c.textContent.trim() }));
  });
  eq(facts.length, 2, 'фактов в шапке');
  eq(facts[0].верх, facts[1].верх, 'срок и лимит на разных строках');
  ok(/ГБ|GB/.test(facts[1].текст), `во втором факте не лимит: ${facts[1].текст}`);
  await dropKeys(page, 'тест-');
});

// The whole point of dimming a dead key is the name: white when it works,
// grey when it does not.
test('имя рабочего ключа белое, отключённого — серое', async (page) => {
  await login(page);
  const id = await makeKey(page, 'тест-бан-цвет');
  await makeKey(page, 'тест-живой-цвет');
  await page.evaluate((i) => api('/users/' + i + '/ban', { method: 'POST', body: JSON.stringify({ banned: true }) }), id);
  await page.evaluate(() => loadUsers());
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(700);

  const цвет = (имя) => page.evaluate((n) => {
    const el = [...document.querySelectorAll('.uh-name')].find((x) => x.textContent === n);
    return getComputedStyle(el).color;
  }, имя);
  const яркость = (c) => c.match(/\d+/g).slice(0, 3).reduce((a, b) => a + +b, 0) / 3;

  const живой = яркость(await цвет('тест-живой-цвет'));
  const мёртвый = яркость(await цвет('тест-бан-цвет'));
  ok(живой > 200, `рабочий ключ не белый: ${живой}`);
  ok(мёртвый < 140, `отключённый ключ не приглушён: ${мёртвый}`);
  await dropKeys(page, 'тест-');
});

// The code is the thing most people came for: it is beside the links, not
// under them, and it is there without being asked for.
test('QR стоит справа от ссылки и рисуется сразу', async (page) => {
  await login(page);
  await makeKey(page, 'тест-qr', ['hy2', 'reality']);
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(600);
  const row = page.locator('.uh', { has: page.locator('.uh-name', { hasText: 'тест-qr' }) });
  await row.locator('.uh-open').click();
  await page.waitForTimeout(1000);

  const box = await waitFor(page, () => {
    const b = document.querySelector('.uh.open .qr-b');
    const c = document.querySelector('.uh.open .sharecol');
    if (!b || !c || !b.querySelector('img')) return null;
    const rb = b.getBoundingClientRect(), rc = c.getBoundingClientRect();
    return { qrX: Math.round(rb.x), qrY: Math.round(rb.y), linkX: Math.round(rc.x), linkY: Math.round(rc.y) };
  }, 'QR не появился сам');

  ok(box.qrX > box.linkX, `QR не справа: ссылка на ${box.linkX}, QR на ${box.qrX}`);
  ok(Math.abs(box.qrY - box.linkY) < 8, `QR не на одной высоте со ссылкой: ${box.linkY} и ${box.qrY}`);

  // Switching protocol must swap the code, not leave the previous one.
  const before = await page.locator('.uh.open .qr-b img').getAttribute('src');
  await row.locator('.pchip', { hasText: 'VLESS+Reality' }).click();
  await page.waitForTimeout(500);
  const after = await page.locator('.uh.open .qr-b img').getAttribute('src');
  ok(before !== after, `QR не сменился при выборе протокола: ${after}`);

  await dropKeys(page, 'тест-');
});

test('смена языка переводит интерфейс', async (page) => {
  await login(page);
  const before = await page.locator('.btn-plus').textContent();
  await page.click('[data-act="lang"]');
  await page.waitForTimeout(700);
  const after = await page.locator('.btn-plus').textContent();
  ok(before !== after, `язык не сменился: ${before} -> ${after}`);
  await page.click('[data-act="lang"]');
  await page.waitForTimeout(500);
});

test('обе темы читаемы', async (page) => {
  await login(page);
  for (let i = 0; i < 2; i++) {
    const contrast = await page.evaluate(() => {
      const lum = (c) => {
        const [r, g, b] = c.match(/\d+/g).map((v) => {
          v = v / 255;
          return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
        });
        return 0.2126 * r + 0.7152 * g + 0.0722 * b;
      };
      const el = document.querySelector('.sq-label');
      const bg = getComputedStyle(document.body).backgroundColor;
      const fg = getComputedStyle(el).color;
      const [a, b] = [lum(fg), lum(bg)].sort((x, y) => y - x);
      return +((a + 0.05) / (b + 0.05)).toFixed(2);
    });
    // 3:1 is the floor for large text; below that a label stops being readable.
    ok(contrast >= 3, `контраст подписи плитки ${contrast}:1 — ниже 3:1`);
    await page.click('[data-act="theme"]');
    await page.waitForTimeout(600);
  }
});

test('панель по http переадресуется на https', async (page) => {
  if (!URL.startsWith('https://')) return;
  const plain = URL.replace('https://', 'http://');
  const resp = await page.goto(plain, { waitUntil: 'domcontentloaded', timeout: 20000 });
  ok(page.url().startsWith('https://'), `остались на ${page.url()}`);
  ok(resp.status() === 200, `код ${resp.status()}`);
});

test('здоровье отвечает без пароля', async (page) => {
  const resp = await page.request.get(URL + '/healthz', { ignoreHTTPSErrors: true });
  ok(resp.status() === 200 || resp.status() === 503, `код ${resp.status()}`);
  const body = await resp.json();
  eq(Object.keys(body).length, 1, 'поля в ответе');
  ok('ok' in body, 'нет поля ok');
});

// Phones are how most people will open this. Every one of these broke before
// the layout existed: the page rendered at 980px and was zoomed out to
// illegibility, and the key row ran its own columns into each other.
test('телефон: страница по ширине экрана, без горизонтальной прокрутки', async (page) => {
  await login(page);
  const m = await page.evaluate(() => ({
    экран: window.innerWidth,
    документ: document.documentElement.scrollWidth,
  }));
  ok(m.экран < 500, `тест запущен не в телефонном контексте: ${m.экран}`);
  ok(m.документ <= m.экран + 1, `документ ${m.документ} шире экрана ${m.экран}`);
}, true);

test('телефон: колонки складываются в одну', async (page) => {
  await login(page);
  const dir = await page.evaluate(() => getComputedStyle(document.querySelector('.stage')).flexDirection);
  eq(dir, 'column', 'направление раскладки');
}, true);

test('телефон: строка ключа не наезжает сама на себя', async (page) => {
  await login(page);
  await makeKey(page, 'тест-телефон');
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(900);
  const box = await page.evaluate(() => {
    const n = document.querySelector('.uh-name').getBoundingClientRect();
    const v = document.querySelector('.uh-val').getBoundingClientRect();
    return { наложение: Math.round(n.right - v.left) };
  });
  ok(box.наложение <= 0, `имя наезжает на трафик на ${box.наложение}px`);
  await dropKeys(page, 'тест-');
});

test('телефон: по кнопкам можно попасть пальцем', async (page) => {
  await login(page);
  await makeKey(page, 'тест-палец');
  await page.click('[data-pane="keys"]');
  await page.waitForTimeout(900);
  const size = await page.evaluate(() => {
    const r = document.querySelector('.uh-pick').getBoundingClientRect();
    return Math.round(Math.min(r.width, r.height));
  });
  ok(size >= 22, `отметка ${size}px — мелко для пальца`);
  await dropKeys(page, 'тест-');
});

/* ---------- runner ---------- */

(async () => {
  if (!PASSWORD) {
    console.error('нужен MOR_PASSWORD');
    process.exit(2);
  }
  const browser = await chromium.launch({ headless: true });
  let failed = 0;

  for (const { name, fn, mobile } of tests) {
    const ctx = await browser.newContext(mobile || name.startsWith('телефон')
      ? { ...devices['iPhone 13'], ignoreHTTPSErrors: true }
      : { ignoreHTTPSErrors: true, viewport: { width: 1500, height: 950 } });
    const page = await ctx.newPage();
    const errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    try {
      await fn(page);
      if (errors.length) throw new Error('ошибки JS: ' + errors.join(' | '));
      console.log(`  ok   ${name}`);
    } catch (e) {
      failed++;
      console.log(`  FAIL ${name}\n       ${e.message}`);
    }
    await ctx.close();
  }

  await browser.close();
  console.log(failed ? `\n${failed} из ${tests.length} упало` : `\nвсе ${tests.length} прошли`);
  process.exit(failed ? 1 : 0);
})();
