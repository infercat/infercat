import { decodeInvite, InviteError } from './invite';
import { tr } from './i18n/text';
export const isAdminCode=(raw:string)=>raw.trim().startsWith('ia1.');
export function decodeCode(raw:string){
 const tag=raw.trim().split('.')[0]||'',version=/^ia(\d+)$/.exec(tag)?.[1];
 if(version&&Number.isSafeInteger(Number(version))&&Number(version)>1)throw new InviteError('newer_version',tr('app_this_invite_needs_a_newer_version_of_the_app'));
 return decodeInvite(isAdminCode(raw)?'ic1.'+raw.trim().slice(4):raw);
}
export function takeAdminRoute(){
 if(typeof location==='undefined')return {console:false,code:''};
 let raw='';try{raw=decodeURIComponent(location.hash.slice(1)).trim();}catch{/* no valid code */}
 const admin=/^ia\d+\./.test(raw);
 if(admin)history.replaceState(null,'','/console'+location.search);
 return {console:location.pathname==='/console',code:admin?raw:''};
}
