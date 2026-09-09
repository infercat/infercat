import css from './styles.css?inline';
import { createConsole } from './app';
import type { RemoteState } from './remote';
const fonts = import.meta.glob<string>('./public/fonts/*.woff2', { eager:true,query:'?url',import:'default' });
// Shadow DOM scopes every selector; distinct font names keep both subsets usable on the same origin.
export function mountConsole(host:HTMLElement,secret:string,request:typeof fetch,remote:RemoteState) {
 const shadow=host.shadowRoot||host.attachShadow({mode:'open'}),style=document.createElement('style'),root=document.createElement('div');
 let sheet=css.replace(/url\((['"]?)\/fonts\/([^)'"\s]+)\1\)/g,(_,quote,file)=>`url("${fonts['./public/fonts/'+file]}")`);
 for(const name of ['Archivo','IBM Plex Mono','Noto Sans SC'])sheet=sheet.replaceAll(name,'Infercat Console '+name);
 const faces:FontFace[]=[];
 sheet=sheet.replace(/@font-face\s*\{([^}]+)\}/g,(_,rule:string)=>{
  const family=/font-family:\s*([^;]+)/.exec(rule)?.[1]?.trim().replace(/^['"]|['"]$/g,''),source=/src:([^;]+)/.exec(rule)?.[1],weight=/font-weight:([^;]+)/.exec(rule)?.[1];
  if(family&&source){const face=new FontFace(family,source,{weight:weight?.trim()||'400'});faces.push(face);document.fonts.add(face);void face.load().catch(()=>{});}return '';
 });
 style.textContent=sheet.replaceAll(':root',':host').replace(/html\s*,\s*body\s*\{/g,':host {').replace(/\bbody\s*\{/g,':host {')+'\n:host { display:block; } .who strong { display:inline-flex;gap:4px;min-width:0; } .who .host-name { overflow:hidden;text-overflow:ellipsis;min-width:0; } .who .rem { flex:none; } .sec-head .count { min-width:0;max-width:100%;overflow-wrap:anywhere; }';
 shadow.replaceChildren(style,root);const oldTitle=document.title,oldLang=document.documentElement.lang;
 const app=createConsole(root,secret,request,Date.now,undefined,remote);
 return {refresh:app.refresh,stop(){app.stop();shadow.replaceChildren();for(const face of faces)document.fonts.delete(face);document.title=oldTitle;document.documentElement.lang=oldLang;}};
}
