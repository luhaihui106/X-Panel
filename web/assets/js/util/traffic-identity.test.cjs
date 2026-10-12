const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const context = { Vue: {prototype: {$message: {error: msg => context.errors.push(msg), success: () => assert.fail('error displayed as success')}}}, errors: [] };
vm.createContext(context);
vm.runInContext(fs.readFileSync(__dirname + '/index.js', 'utf8') + '\nthis.HttpUtil=HttpUtil;', context);
(async () => {
  const message = 'client identity "retired" is reserved for unsettled core counters; choose a new identity';
  context.axios = {post: async () => ({data: {success: false, msg: message}})};
  let closed = false; const loading = [];
  const modal = {loading: value => loading.push(value), close: () => {closed = true;}};
  const result = await context.HttpUtil.postWithModal('/updateClient/live-id', {}, modal);
  assert.equal(result.success, false);
  assert.deepEqual(context.errors, [message]);
  assert.deepEqual(loading, [true, false]);
  assert.equal(closed, false, 'keep editor and unsaved input open on rejection');
  console.log('identity error toast, loading recovery, editor retention: PASS');
})().catch(error => {console.error(error); process.exitCode = 1;});
