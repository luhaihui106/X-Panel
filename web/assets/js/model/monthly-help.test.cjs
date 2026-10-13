const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
test('monthly reset help is concise and preserves timezone, short month, cycle and lifetime',()=>{
 const html=fs.readFileSync('web/html/form/client.html','utf8');
 assert.ok(html.includes('按面板时区重置；短月取月末，仅清周期流量，累计保留。'));
 assert.ok(html.includes('启用/修改日期从下一周期开始'));
});
