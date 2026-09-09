import {afterEach,expect,it,vi} from 'vitest';
import {decodeCode,isAdminCode,takeAdminRoute} from './admin-route';
import {decodeInvite,inviteFromHash,inviteHint} from './invite';
const admin='ia1.tcHOST.'+'a'.repeat(43),friend=admin.replace('ia1.','ic1.');
afterEach(()=>vi.unstubAllGlobals());
it('recognises admin separately while preserving ic1 and old prefix rejection',()=>{
 expect(isAdminCode(admin)).toBe(true);expect(decodeCode(admin)).toEqual(decodeInvite(friend));expect(decodeCode(friend)).toEqual(decodeInvite(friend));expect(()=>decodeInvite(admin)).toThrow();expect(()=>decodeCode(admin.replace('ia1.','ia2.'))).toThrow(/newer/);expect(inviteHint(admin).state).toBe('valid');expect(inviteFromHash('#'+admin)).toBe('');
});
it('takes admin fragments into the route without storage or a secret in history',()=>{
 const replaceState=vi.fn();vi.stubGlobal('location',{hash:'#'+admin,pathname:'/',search:'?lang=zh'});vi.stubGlobal('history',{replaceState:(_a:unknown,_b:string,path:string)=>{replaceState(path);location.pathname='/console';}});vi.stubGlobal('localStorage',{setItem:()=>{throw new Error('storage write');}});
 expect(takeAdminRoute()).toEqual({console:true,code:admin});expect(replaceState).toHaveBeenCalledWith('/console?lang=zh');
});
it('a reload of /console has no recoverable bearer',()=>{vi.stubGlobal('location',{hash:'',pathname:'/console',search:''});expect(takeAdminRoute()).toEqual({console:true,code:''});});
