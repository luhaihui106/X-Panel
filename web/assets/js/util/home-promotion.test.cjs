// Regression guard for the downstream homepage-only promotion removal.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const html = fs.readFileSync(path.join(__dirname, '../../../html/index.html'), 'utf8');

test('homepage has no automatic Pro welcome promotion or dedicated session state', () => {
    for (const marker of ['welcome-modal', 'welcomeModal', 'welcomeShown', 'closeWelcomeModal', 'jumpToProFeatures']) {
        assert.equal(html.includes(marker), false, `obsolete homepage promotion marker: ${marker}`);
    }
});

test('real status polling, navigation component and user-requested feature restrictions remain', () => {
    for (const marker of ['component/aSidebar', "'/panel/api/server/status'", 'await this.getStatus()', 'PromiseUtil.sleep(2000)', 'performRemoteBackup()', 'performRemoteRestore()', '功能受限提示', '免费版不能用', 'backupModal.show()']) {
        assert.equal(html.includes(marker), true, `preserved homepage behavior: ${marker}`);
    }
});
