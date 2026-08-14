// Browser tests for the mor panel.
//
// Everything here goes through a real browser against a running mor, because
// that is the only place the panel's failures show up: none of them break the
// Go build, and several of them broke nothing visible until somebody clicked
// the wrong thing.
//
// Each test gets a fresh page and cleans up the keys it made.

const { chromium } = require('playwright');

const URL = process.env.MOR_URL || 'https://127.0.0.1:9090';
const PASSWORD = process.env.MOR_PASSWORD || '';

const tests = [];
const test = (name, fn) => tests.push({ name, fn });

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
  await page.waitForTimeout(2000);

  ok(!(await page.locator('#createModal.show').count()), 'окно не закрылось');
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
  await page.waitForTimeout(2000);

  const u = await page.evaluate(() => users.find((x) => x.name === 'тест-22'));
  ok(u, 'ключ не создан');
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

  const picks = page.locator('.uh-pick');
  const n = await picks.count();
  await picks.nth(n - 1).click();
  await picks.nth(n - 2).click();
  await page.waitForTimeout(400);
  ok(await page.locator('#bulkBar.show').count() === 1, 'панель выбора не появилась');

  const wasCount = await page.locator('.uh').count();
  await page.click('#bulkDelBtn');
  await page.waitForTimeout(400);
  eq(await page.locator('.uh').count(), wasCount, 'удалило с первого нажатия');
  await page.click('#bulkDelBtn');
  await page.waitForTimeout(2500);
  eq(await page.locator('.uh').count(), wasCount - 2, 'удалило не два ключа');
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

/* ---------- runner ---------- */

(async () => {
  if (!PASSWORD) {
    console.error('нужен MOR_PASSWORD');
    process.exit(2);
  }
  const browser = await chromium.launch({ headless: true });
  let failed = 0;

  for (const { name, fn } of tests) {
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1500, height: 950 } });
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
