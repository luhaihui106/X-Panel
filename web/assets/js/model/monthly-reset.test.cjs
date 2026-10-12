const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const ctx = vm.createContext({RandomUtil:{randomUUID:()=> 'id',randomLowerAndNum:()=> 'email',randomSeq:()=> 'password',randomShadowsocksPassword:()=> 'password'}});
vm.runInContext(fs.readFileSync(__dirname+'/inbound.js','utf8'),ctx);
for (const name of ['VmessSettings.VMESS','VLESSSettings.VLESS','TrojanSettings.Trojan','ShadowsocksSettings.Shadowsocks']) {
 const output=vm.runInContext(`JSON.stringify(Inbound.${name}.fromJson({id:'id',password:'password',email:'a',monthlyResetEnabled:true,monthlyResetDay:31,reset:7}).toJson())`,ctx);
 const parsed=JSON.parse(output);
 assert.equal(parsed.monthlyResetEnabled,true,name);
 assert.equal(parsed.monthlyResetDay,31,name);
 assert.equal(parsed.reset,7,name);
 const legacy=JSON.parse(vm.runInContext(`JSON.stringify(Inbound.${name}.fromJson({}).toJson())`,ctx));
 assert.equal(legacy.monthlyResetEnabled,false,name);
}
assert.match(fs.readFileSync(__dirname+'/../../../html/form/client.html','utf8'),/client.monthlyResetEnabled/);
console.log('four client protocols round-trip and form controls: PASS');
