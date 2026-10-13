const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const root = path.resolve(__dirname, '../../../..');
const read = (p) => fs.readFileSync(path.join(root, p), 'utf8');

test('utility navigation retains useful resources, not affiliate or standalone Pro sales tabs', () => {
  const html = read('web/html/navigation.html');
  assert.doesNotMatch(html, /aff\.php|affiliation_code|#register\/|推广赞助|批量折扣|Buy_ShouQuan_Bot/);
  assert.deepEqual([...html.matchAll(/<a-tab-pane key="(\d+)"/g)].map(m => m[1]), ['1', '5']);
  for (const marker of ['有用技巧', '其他资源', 'https://github.com/2dust/v2rayN', 'https://xtls.github.io/', 'https://t.me/XUI_CN', '项目声明与注意']) {
    assert.ok(html.includes(marker), marker);
  }
  assert.match(html, /\['1', '5'\]\.includes\(tab\)/);
});

test('real management menu output keeps every operation and project credits without affiliate block', () => {
  const script = read('x-ui.sh');
  const start = script.indexOf('\nshow_menu() {') + 1;
  const end = script.indexOf('\n}\n', start);
  assert.ok(start >= 0 && end > start);
  const fn = script.slice(start, end + 3);
  // Execute only the actual menu function; never source host detection/install code.
  const run = spawnSync('bash', ['-c', 'show_status() { :; }\n' + fn + '\nshow_menu'], { input: '0\n', encoding: 'utf8', timeout: 5000 });
  assert.equal(run.status, 0, run.stderr);
  assert.doesNotMatch(run.stdout, /aff\.php|affiliation_code|#register\/|赞 助 推 广 区/);
  for (let i = 0; i <= 25; i++) assert.match(run.stdout, new RegExp('(?:^|\\n)  ' + i + '\\.'), 'operation ' + i);
  assert.ok(run.stdout.includes('https://github.com/xeefei/x-panel'));
  assert.ok(run.stdout.includes('https://t.me/XUI_CN'));
});

test('real paid-feature restrictions, homepage no-popup contract and credits stay intact', () => {
  for (const file of ['web/html/index.html', 'web/html/inbounds.html', 'web/html/servers.html']) {
    const html = read(file);
    assert.ok(html.includes('付费Pro版'), file);
    assert.ok(html.includes('https://t.me/Buy_ShouQuan_Bot'), file);
  }
  assert.doesNotMatch(read('web/html/index.html'), /welcomeModal|welcomeShown|jumpToProFeatures/);
  assert.ok(read('LICENSE').includes('GNU GENERAL PUBLIC LICENSE'));
  assert.ok(read('docs/credits.md').includes('xeefei'));
});

test('installer completion text has no affiliate advertising without changing edition selection', () => {
  const script = read('install.sh');
  assert.doesNotMatch(script, /aff\.php|affiliation_code|#register\/|赞 助 推 广 区/);
  for (const marker of ['install_paid_version()', 'install_free_version()', 'main_menu()', 'install_paid_version', 'install_free_version', 'https://auth.x-panel.vip/install_pro.php', 'https://github.com/xeefei/x-panel']) {
    assert.ok(script.includes(marker), marker);
  }
});
